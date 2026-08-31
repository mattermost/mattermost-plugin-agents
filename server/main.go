// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/api"
	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/customprompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/indexer"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver"
	"github.com/mattermost/mattermost-plugin-agents/v2/meetings"
	"github.com/mattermost/mattermost-plugin-agents/v2/metrics"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/search"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost-plugin-agents/v2/utils"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/mattermost/mattermost/server/public/shared/httpservice"
)

func main() {
	plugin.ClientMain(&Plugin{})
}

type Plugin struct {
	plugin.MattermostPlugin

	configuration config.Container

	pluginAPI            *pluginapi.Client
	apiService           *api.API
	bots                 *bots.MMBots
	indexerService       *indexer.Indexer
	conversationsService *conversations.Conversations
	mcpClientManager     *mcp.ClientManager
	streamingService     streaming.Service
	telemetryShutdown    telemetry.ShutdownFunc
	telemetryMu          sync.Mutex
	telemetryMode        telemetry.OutputMode
	telemetryEndpoint    string
	store                *store.Store
	autoreplyService     channelAutoReplyRefresher
	configMigrated       bool
}

type pluginLogger struct {
	service *pluginapi.LogService
}

func (l *pluginLogger) Debug(message string, keyValuePairs ...any) {
	if l == nil || l.service == nil {
		return
	}
	l.service.Debug(message, keyValuePairs...)
}

func (l *pluginLogger) Info(message string, keyValuePairs ...any) {
	if l == nil || l.service == nil {
		return
	}
	l.service.Info(message, keyValuePairs...)
}

func (l *pluginLogger) Warn(message string, keyValuePairs ...any) {
	if l == nil || l.service == nil {
		return
	}
	l.service.Warn(message, keyValuePairs...)
}

func (l *pluginLogger) Error(message string, keyValuePairs ...any) {
	if l == nil || l.service == nil {
		return
	}
	l.service.Error(message, keyValuePairs...)
}

func (p *Plugin) OnActivate() error {
	pluginAPI := pluginapi.NewClient(p.API, p.Driver)
	p.pluginAPI = pluginAPI
	mmClient := mmapi.NewClient(pluginAPI)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	dbClient := mmapi.NewDBClient(pluginAPI)

	// Initialize store and run database migrations
	p.store = store.New(dbClient.DB)
	mtx, err := cluster.NewMutex(p.API, "ai_db_migrations")
	if err != nil {
		return fmt.Errorf("failed to create cluster mutex: %w", err)
	}
	mtx.Lock()
	if migrateErr := func() error {
		defer mtx.Unlock()
		return p.store.RunMigrations()
	}(); migrateErr != nil {
		return fmt.Errorf("failed to run database migrations: %w", migrateErr)
	}

	i18nBundle := i18n.Init()

	llmUpstreamHTTPClient := httpservice.MakeHTTPServicePlugin(p.API).MakeClient(true)
	llmUpstreamHTTPClient.Timeout = time.Minute * 10 // LLM requests can be slow

	// Tune connection pool for LLM streaming: shorter idle timeout to avoid
	// reusing stale connections after SSE streams close during tool calling,
	// and more idle connections per host for concurrent LLM requests.
	if mmTransport, ok := llmUpstreamHTTPClient.Transport.(*httpservice.MattermostTransport); ok {
		if transport, ok := mmTransport.Transport.(*http.Transport); ok {
			transport.IdleConnTimeout = 30 * time.Second
			transport.MaxIdleConnsPerHost = 10
		}
	}

	untrustedHTTPClient := httpservice.MakeHTTPServicePlugin(p.API).MakeClient(false)

	metricsService := metrics.NewMetrics(metrics.InstanceInfo{
		InstallationID: os.Getenv("MM_CLOUD_INSTALLATION_ID"),
		PluginVersion:  manifest.Version, // Manifest imported from manifest.go which is generated by the build process
	})

	// One-time config data migration (config.json -> DB).
	// This uses a separate cluster mutex from schema migrations above,
	// ensuring only one node performs config migration at a time.
	mtx2, err := cluster.NewMutex(p.API, "ai_config_migration")
	if err != nil {
		return fmt.Errorf("failed to create config migration mutex: %w", err)
	}
	mtx2.Lock()

	migrated, err := p.store.IsConfigMigrated()
	if err != nil {
		mtx2.Unlock()
		return fmt.Errorf("failed to check config migration status: %w", err)
	}
	if !migrated {
		// Load from config.json
		var cfgWrap configuration
		if loadErr := p.API.LoadPluginConfiguration(&cfgWrap); loadErr != nil {
			mtx2.Unlock()
			return fmt.Errorf("failed to load plugin configuration for migration: %w", loadErr)
		}

		// Apply legacy config transforms
		loadLegacyConfig := func() (config.LegacyServiceConfig, error) {
			var legacy config.LegacyServiceConfig
			if legacyErr := p.API.LoadPluginConfiguration(&legacy); legacyErr != nil {
				return legacy, fmt.Errorf("failed to load legacy config: %w", legacyErr)
			}
			return legacy, nil
		}

		finalCfg, _, migrationErr := config.RunAllLegacyMigrations(cfgWrap.Config, loadLegacyConfig)
		if migrationErr != nil {
			mtx2.Unlock()
			return fmt.Errorf("failed to run legacy config migrations: %w", migrationErr)
		}

		// Write to DB (first entry in config history)
		if saveErr := p.store.SaveConfig(finalCfg); saveErr != nil {
			mtx2.Unlock()
			return fmt.Errorf("failed to save migrated config to database: %w", saveErr)
		}

		pluginAPI.Log.Info("Config migrated from config.json to database")
	}
	mtx2.Unlock()

	// Load config from DB into memory and set migrated flag
	dbConfig, err := p.store.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to load config from database: %w", err)
	}
	if dbConfig != nil {
		p.configuration.Update(dbConfig)
	}
	p.configMigrated = true

	bots := bots.New(p.API, pluginAPI, licenseChecker, &p.configuration, p.store, llmUpstreamHTTPClient, metricsService)

	// migrateAndRefresh runs the one-time legacy bot migration, then forces
	// a bot refresh only if the migration actually created new agents.
	migrateAndRefresh := func(context string) {
		migrated, migErr := migrateLegacyConfigBotsToUserAgents(p.API, pluginAPI, p.store, &p.configuration)
		if migErr != nil {
			pluginAPI.Log.Error("failed to migrate legacy config bots to user agents", "context", context, "error", migErr)
		}
		if migrated {
			bots.ForceRefreshOnNextEnsure()
			if ensureErr := bots.EnsureBots(); ensureErr != nil {
				pluginAPI.Log.Error("failed to ensure bots after legacy bot migration", "context", context, "error", ensureErr)
			} else if pubErr := p.PublishAgentUpdate(); pubErr != nil {
				pluginAPI.Log.Error("Failed to publish agent update cluster event", "error", pubErr.Error())
			}
		}
	}

	p.configuration.RegisterUpdateListener(func() {
		if ensureErr := bots.EnsureBots(); ensureErr != nil {
			pluginAPI.Log.Error("failed to ensure bots on configuration update", "error", ensureErr)
		}
		// Drop cached service-backed LLMs whose configuration changed. Local
		// saves and HA updates both flow through config.Container.Update, so
		// this listener covers every node.
		bots.ReconcileServiceLLMs(p.configuration.GetServices())
		migrateAndRefresh("config_update")
	})

	if ensureBotsErr := bots.EnsureBots(); ensureBotsErr != nil {
		// If we fail to ensure bots, we log the error but do not return
		// as it would leave the plugin in a state where it can't be configured from the system console.
		pluginAPI.Log.Error("failed to ensure bots", "error", ensureBotsErr)
	}
	migrateAndRefresh("activation")

	autoreplyStore := autoreply.NewStore(dbClient)
	autoreplyService := autoreply.NewService(autoreplyStore, bots, mmClient, p)
	if loadCacheErr := autoreplyService.LoadCache(); loadCacheErr != nil {
		return fmt.Errorf("failed to load channel auto-reply settings: %w", loadCacheErr)
	}
	p.autoreplyService = autoreplyService

	prompts, promptManagerErr := llm.NewPrompts(prompts.PromptsFolder)
	if promptManagerErr != nil {
		pluginAPI.Log.Error("failed to initialize prompts", "error", promptManagerErr)
		return promptManagerErr
	}

	streamingService := streaming.NewMMPostStreamService(mmClient, i18nBundle)

	// Availability is the single source of truth for the embedding search.
	// Indexing stays available whenever search is initialized so a reindex can
	// always be started; query-time search is additionally gated on model
	// compatibility (wired below).
	searchAvailability := search.NewAvailability()
	var lastSearchInitError atomic.Value // stores string

	// Config getter for saving model info after reindexing
	configGetter := func() embeddings.EmbeddingSearchConfig {
		return p.configuration.EmbeddingSearchConfig()
	}

	// Getter for the last search initialization error
	getSearchInitError := func() string {
		val := lastSearchInitError.Load()
		if val == nil {
			return ""
		}
		return val.(string)
	}

	// Skip constructor CREATE while a deferred reindex owns the ANN index.
	embeddingsSearch, err := search.InitEmbeddingsSearch(
		dbClient.DB,
		llmUpstreamHTTPClient,
		p.configuration.EmbeddingSearchConfig(),
		licenseChecker,
		indexer.DeferredIndexRebuildActive(mmClient),
	)
	if err != nil {
		pluginAPI.Log.Warn("failed to initialize search infrastructure", "error", err)
		lastSearchInitError.Store(err.Error())
		// Continue without search functionality
	}

	// Create indexer service. IndexSearch stays available whenever search is
	// initialized, so a full reindex can run during model incompatibility.
	indexerService := indexer.New(searchAvailability.IndexSearch, configGetter, mmClient, bots, dbClient.DB, p.API)

	// Query-time search is only allowed when the configured embedding model is
	// compatible with the existing index.
	searchAvailability.SetQueryAllowedFunc(func() bool {
		cfg := p.configuration.EmbeddingSearchConfig()
		return indexerService.CheckModelCompatibility(indexer.ModelInfo{
			ProviderType:       cfg.GetProviderType(),
			Dimensions:         cfg.Dimensions,
			ModelName:          cfg.GetModelName(),
			HNSWM:              cfg.GetHNSWM(),
			VectorElementType:  cfg.GetVectorElementType(),
			IndexRetentionDays: utils.Ptr(cfg.GetIndexRetentionDays()),
		}).Compatible
	})

	// Mark any orphaned reindex jobs as failed (any node, staleness-based).
	// Handles wedge cases where the plugin/server crashed while a job was
	// running; works in containerized deploys where the hostname changes
	// on restart and clustered deploys where the original node may be gone.
	if orphanErr := indexerService.MarkOrphanedJobAsFailed(); orphanErr != nil {
		pluginAPI.Log.Warn("Failed to check for orphaned reindex job", "error", orphanErr)
	}

	// Store the initialized search; QuerySearch handles compatibility gating.
	searchAvailability.Set(embeddingsSearch)
	if embeddingsSearch != nil {
		lastSearchInitError.Store("") // Clear any previous error
	}

	// Reconcile leftover deferred-index state (never auto-build on activate).
	if reconcileErr := indexerService.ReconcileVectorIndexState(context.Background()); reconcileErr != nil {
		pluginAPI.Log.Warn("Failed to reconcile vector index state", "error", reconcileErr)
	}

	// Create search service. It uses QuerySearch, which returns nil when the
	// configured model and existing index are incompatible.
	searchService := search.New(
		searchAvailability.QuerySearch,
		mmClient,
		prompts,
		streamingService,
		licenseChecker,
		nil, // conversation service wired in a later step
	)

	// Register update listener for embedding search config changes
	p.configuration.RegisterUpdateListener(func() {
		newEmbeddingsSearch, initErr := search.InitEmbeddingsSearch(
			dbClient.DB,
			llmUpstreamHTTPClient,
			p.configuration.EmbeddingSearchConfig(),
			licenseChecker,
			indexer.DeferredIndexRebuildActive(mmClient),
		)
		if initErr != nil {
			pluginAPI.Log.Error("Failed to reinitialize embedding search on config change", "error", initErr)
			// Disable search on failure
			searchAvailability.Set(nil)
			lastSearchInitError.Store(initErr.Error())
			return
		}

		// Store the reinitialized search. QuerySearch blocks incompatible model
		// queries while IndexSearch allows reindexing.
		searchAvailability.Set(newEmbeddingsSearch)
		lastSearchInitError.Store("")
		pluginAPI.Log.Info("Embedding search reinitialized on config change")
	})

	webSearchService := mmtools.NewWebSearchService(func() *config.Config {
		return p.configuration.Config()
	}, &pluginLogger{service: &pluginAPI.Log}, untrustedHTTPClient)

	toolProvider := mmtools.NewMMToolProvider(
		mmClient,
		webSearchService,
	)

	// Build redirect URI
	siteURL := pluginAPI.Configuration.GetConfig().ServiceSettings.SiteURL
	if siteURL == nil || *siteURL == "" {
		return fmt.Errorf("site URL not configured")
	}
	manifestID := manifest.Id
	oauthCallbackURL := fmt.Sprintf("%s/plugins/%s/oauth/callback", *siteURL, manifestID)

	// Embedded MCP is always available after PR #617, even if older configs still
	// have the legacy toggle stored as false.
	fileContentService := files.New(mmClient)
	var embeddedMCPServer mcp.EmbeddedMCPServer
	embeddedMCPServer, err = NewEmbeddedMCPServer(pluginAPI, pluginAPI.Log, searchService, fileContentService)
	if err != nil {
		pluginAPI.Log.Error("Failed to create embedded MCP server", "error", err)
		// Continue without embedded server
	} else {
		pluginAPI.Log.Info("Embedded MCP server created successfully")
	}

	serverConfigLookup := func(serverID string) (mcp.ServerConfig, bool) {
		for _, s := range p.configuration.MCP().Servers {
			if s.Name == serverID {
				return s, true
			}
		}
		return mcp.ServerConfig{}, false
	}
	mcpClientManager := mcp.NewClientManager(p.configuration.MCP(), pluginAPI.Log, pluginAPI, mcp.NewOAuthManager(mmClient, oauthCallbackURL, untrustedHTTPClient, serverConfigLookup), embeddedMCPServer, untrustedHTTPClient, mmClient)
	p.configuration.RegisterUpdateListener(func() {
		embeddedServer, embeddedErr := NewEmbeddedMCPServer(pluginAPI, pluginAPI.Log, searchService, fileContentService)
		if embeddedErr != nil {
			pluginAPI.Log.Error("Failed to create embedded MCP server on config update", "error", embeddedErr)
		}

		mcpClientManager.ReInit(p.configuration.MCP(), embeddedServer)
	})

	contextBuilder := llmcontext.NewLLMContextBuilder(
		pluginAPI,
		toolProvider,
		mcpClientManager,
		&p.configuration,
	)
	contextBuilder.SetMCPDynamicToolTelemetry(metricsService)

	conversationsService := conversations.New(
		prompts,
		mmClient,
		streamingService,
		contextBuilder,
		bots,
		dbClient,
		licenseChecker,
		i18nBundle,
		nil, // meetingsService will be set after it's created
		&p.configuration,
	)

	convService := conversation.NewService(p.store, prompts, mmClient, bots)
	conversationsService.SetConversationService(convService)
	conversationsService.SetAutoReplySettings(autoreplyService)
	searchService.SetConversationService(convService)

	meetingsService := meetings.NewService(
		pluginAPI,
		streamingService,
		prompts,
		bots,
		i18nBundle,
		metricsService,
		contextBuilder,
		conversationsService,
	)

	// Set the meetings service on conversations to break circular dependency
	// TODO: Refactor to avoid circular dependency
	conversationsService.SetMeetingsService(meetingsService)

	// Wire per-tool policy checker for auto-approval in streaming and conversations.
	policyChecker := mcp.ToolPolicyFunc(func(serverBaseURL string, toolName string) (string, bool) {
		return mcp.LookupToolPolicy(p.configuration.MCP(), serverBaseURL, llm.BareMCPToolName(toolName))
	})
	streamingService.SetTurnStore(p.store)
	conversationsService.SetToolPolicyChecker(policyChecker)

	// Initialize embedded MCP server handlers for plugin endpoints
	var mcpHandlers *mcpserver.PluginMCPHandlers
	// Create logger adapter to route MCP handler logs through plugin logging
	mcpHandlerLogger := NewPluginAPILoggerAdapter(pluginAPI.Log)
	internalServerURL := deriveInternalServerURL(pluginAPI, *siteURL)
	handlers, err := mcpserver.NewPluginMCPHandlers(*siteURL, internalServerURL, mcpHandlerLogger, mcpClientManager, mmClient)
	if err != nil {
		pluginAPI.Log.Error("Failed to create MCP handlers", "error", err)
	} else {
		mcpHandlers = handlers
		pluginAPI.Log.Info("Embedded MCP server handlers initialized successfully")
	}

	customPromptsStore := customprompts.NewStore(dbClient)

	apiService := api.New(
		bots,
		conversationsService,
		meetingsService,
		indexerService,
		searchService,
		pluginAPI,
		metricsService,
		contextBuilder,
		&p.configuration,
		prompts,
		mmClient,
		dbClient,
		licenseChecker,
		streamingService,
		i18nBundle,
		mcpClientManager,
		mcpHandlers,
		llmUpstreamHTTPClient,
		p.store,
		p.store,
		&p.configuration,
		p,
		p,
		p,
		p,
		p.store,
		getSearchInitError,
		customPromptsStore,
		autoreplyService,
	)

	apiService.SetConversationService(convService)

	// Apply OpenTelemetry config now and re-apply on every config change so
	// admins don't need to restart the plugin to switch modes.
	p.applyTelemetryConfig()
	p.configuration.RegisterUpdateListener(p.applyTelemetryConfig)

	// Keep only what we need
	p.apiService = apiService
	p.bots = bots
	p.indexerService = indexerService
	p.conversationsService = conversationsService
	p.mcpClientManager = mcpClientManager
	p.streamingService = streamingService

	return nil
}

func (p *Plugin) OnDeactivate() error {
	// Flush pending telemetry spans
	p.telemetryMu.Lock()
	if p.telemetryShutdown != nil {
		if err := p.telemetryShutdown(context.Background()); err != nil {
			p.pluginAPI.Log.Error("Failed to shutdown OpenTelemetry", "error", err)
		}
		p.telemetryShutdown = nil
	}
	p.telemetryMu.Unlock()

	// Release Bifrost worker pools held by service-backed LLMs. OnActivate can
	// fail before bots is assigned, so guard against a nil registry.
	if p.bots != nil {
		p.bots.ShutdownServiceLLMs()
	}

	// Clean up MCP client manager if it exists
	p.mcpClientManager.Close()

	return nil
}

// applyTelemetryConfig (re)initializes the global OpenTelemetry TracerProvider
// from the current plugin configuration. It is safe to call repeatedly: when
// the mode and endpoint match what's already running, it is a no-op. When the
// settings change it shuts the previous provider down and installs a new one.
func (p *Plugin) applyTelemetryConfig() {
	cfg := p.configuration.Config()
	mode := telemetry.OutputMode(cfg.TelemetryOutput)
	if mode == "" {
		mode = telemetry.OutputModeOff
	}
	endpoint := cfg.OpenTelemetryEndpoint

	p.telemetryMu.Lock()
	defer p.telemetryMu.Unlock()

	if mode == p.telemetryMode && endpoint == p.telemetryEndpoint {
		return
	}

	if p.telemetryShutdown != nil {
		if err := p.telemetryShutdown(context.Background()); err != nil {
			p.pluginAPI.Log.Error("Failed to shutdown previous OpenTelemetry provider", "error", err)
		}
		p.telemetryShutdown = nil
	}

	shutdown, err := telemetry.Init(context.Background(), "mattermost-ai-agents", manifest.Version, mode, endpoint, &p.pluginAPI.Log)
	if err != nil {
		p.pluginAPI.Log.Error("Failed to initialize OpenTelemetry", "error", err, "mode", string(mode))
		// Leave previous mode tracked so the next change still triggers a re-init.
		return
	}
	p.telemetryShutdown = shutdown
	p.telemetryMode = mode
	p.telemetryEndpoint = endpoint
	if mode == telemetry.OutputModeOff {
		p.pluginAPI.Log.Info("OpenTelemetry tracing disabled")
	} else {
		p.pluginAPI.Log.Info("OpenTelemetry tracing initialized", "mode", string(mode))
	}
}

func (p *Plugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
	p.conversationsService.MessageHasBeenPosted(c, post)

	// Index the new message in the vector database
	if p.indexerService != nil {
		// Get channel to retrieve team ID
		channel, err := p.API.GetChannel(post.ChannelId)
		if err != nil {
			p.pluginAPI.Log.Error("Failed to get channel for post indexing", "error", err)
		} else {
			if err := p.indexerService.IndexPost(context.Background(), post, channel); err != nil {
				p.pluginAPI.Log.Error("Failed to index post in vector database", "error", err)
			}
		}
	}
}

func (p *Plugin) MessageHasBeenUpdated(c *plugin.Context, newPost, oldPost *model.Post) {
	if p.indexerService != nil {
		channel, err := p.API.GetChannel(newPost.ChannelId)
		if err != nil {
			p.pluginAPI.Log.Error("Failed to get channel for post indexing", "error", err)
		} else {
			if err := p.indexerService.IndexPost(context.Background(), newPost, channel); err != nil {
				p.pluginAPI.Log.Error("Failed to index updated post in vector database", "error", err)
			}
		}
	}
}

func (p *Plugin) MessageHasBeenDeleted(c *plugin.Context, post *model.Post) {
	if post == nil {
		return
	}
	if p.indexerService != nil {
		if err := p.indexerService.DeletePost(context.Background(), post.Id); err != nil {
			p.pluginAPI.Log.Error("Failed to delete post from vector database", "error", err)
		}
	}
	if p.conversationsService != nil {
		if err := p.conversationsService.DeleteConversationsForDeletedPost(post); err != nil {
			p.pluginAPI.Log.Error("Failed to soft-delete conversations for deleted post", "error", err, "post_id", post.Id)
		}
	}
}

func (p *Plugin) RunDataRetention(nowTime, batchSize int64) (int64, error) {
	if p.indexerService == nil {
		return 0, nil
	}

	count, err := p.indexerService.RunDataRetention(context.Background(), nowTime, batchSize)
	if err != nil {
		p.pluginAPI.Log.Error("Failed to run data retention for embeddings", "error", err)
		return 0, err
	}

	if count > 0 {
		p.pluginAPI.Log.Info("Data retention cleaned up orphaned embeddings", "deleted", count)
	}

	return count, nil
}

func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.apiService.ServeHTTP(c, w, r)
}

func (p *Plugin) ServeMetrics(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.apiService.ServeMetrics(c, w, r)
}

// EmailNotificationWillBeSent blocks redundant AI agent email notifications.
func (p *Plugin) EmailNotificationWillBeSent(emailNotification *model.EmailNotification) (*model.EmailNotificationContent, string) {
	if p.shouldBlockAgentNotification(emailNotification.SenderId, emailNotification.RootId, "", emailNotification.IsDirectMessage) {
		return nil, "notification blocked: AI agent response to user-initiated action"
	}
	return &emailNotification.EmailNotificationContent, ""
}

// NotificationWillBePushed blocks redundant AI agent push notifications.
func (p *Plugin) NotificationWillBePushed(pushNotification *model.PushNotification, userID string) (*model.PushNotification, string) {
	if pushNotification.PostId == "" {
		return pushNotification, ""
	}

	isDM := pushNotification.ChannelType == model.ChannelTypeDirect
	if p.shouldBlockAgentNotification(pushNotification.SenderId, pushNotification.RootId, pushNotification.PostType, isDM) {
		return nil, "notification blocked: AI agent response to user-initiated action"
	}
	return pushNotification, ""
}

// shouldBlockAgentNotification reports whether an AI agent notification is redundant.
func (p *Plugin) shouldBlockAgentNotification(senderID, rootID, postType string, isDM bool) bool {
	if p.bots == nil {
		return false
	}

	bot := p.bots.GetBotByID(senderID)
	if bot == nil {
		return false
	}

	if rootID != "" {
		return true
	}

	if postType == "custom_llmbot" {
		return true
	}

	if isDM {
		return true
	}

	return false
}
