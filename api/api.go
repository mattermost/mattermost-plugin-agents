// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/mattermost/mattermost-plugin-agents/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/conversations"
	"github.com/mattermost/mattermost-plugin-agents/customprompts"
	"github.com/mattermost/mattermost-plugin-agents/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/i18n"
	"github.com/mattermost/mattermost-plugin-agents/indexer"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver"
	"github.com/mattermost/mattermost-plugin-agents/meetings"
	"github.com/mattermost/mattermost-plugin-agents/metrics"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/search"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	ContextPostKey    = "post"
	ContextChannelKey = "channel"
	ContextBotKey     = "bot"
)

type Config interface {
	GetDefaultBotName() string
	MCP() mcp.Config
	AllowUnsafeLinks() bool
	EmbeddingSearchConfig() embeddings.EmbeddingSearchConfig
	EnableChannelMentionToolCalling() bool
}

type MCPClientManager interface {
	GetOAuthManager() *mcp.OAuthManager
	GetToolsCache() *mcp.ToolsCache
	GetHTTPClient() *http.Client
	ProcessOAuthCallback(ctx context.Context, loggedInUserID, state, code string) (*mcp.OAuthSession, error)
	DisconnectUserOAuth(userID, serverName string) error
	MarkOAuthNeeded(userID, serverName, authURL string) error
	GetEmbeddedServer() mcp.EmbeddedMCPServer
	EnsureMCPSessionID(userID string) (string, error)
	GetToolsForUser(userID string) ([]llm.Tool, *mcp.Errors)
	GetConfig() mcp.Config

	RegisterPluginServer(cfg mcp.PluginServerConfig)
	UnregisterPluginServer(pluginID string)
	ListPluginServers() []mcp.PluginServerConfig
	GetPluginServer(pluginID string) (mcp.PluginServerConfig, bool)
	IsPluginRegistered(pluginID string) bool

	DiscoverPluginServerTools(ctx context.Context, userID string, cfg mcp.PluginServerConfig) ([]mcp.ToolInfo, error)
}

// ConfigStore provides read/write access to the plugin configuration in the database.
type ConfigStore interface {
	GetConfig() (*config.Config, error)
	SaveConfig(cfg config.Config) error
}

// AgentStore provides CRUD access to user-created agents in the database.
type AgentStore interface {
	CreateAgent(cfg *llm.BotConfig) error
	GetAgent(id string) (*llm.BotConfig, error)
	ListAgents() ([]*llm.BotConfig, error)
	ListAgentsByCreator(creatorID string) ([]*llm.BotConfig, error)
	CountActiveAgents() (int, error)
	UpdateAgent(cfg *llm.BotConfig) error
	DeleteAgent(id string) error
}

// ConfigUpdater updates the in-memory plugin configuration.
type ConfigUpdater interface {
	Update(cfg *config.Config)
}

// ClusterNotifier broadcasts config update events to other cluster nodes.
type ClusterNotifier interface {
	PublishConfigUpdate() error
}

// ConversationStore provides read/write access to conversation and turn data.
type ConversationStore interface {
	GetConversation(id string) (*store.Conversation, error)
	GetTurnsForConversation(conversationID string) ([]store.Turn, error)
	GetTurnByPostID(postID string) (*store.Turn, error)
	UpdateTurnContent(id string, content json.RawMessage) error
	GetConversationSummariesForUser(userID string, limit, offset int) ([]store.ConversationSummary, error)
}

// ClusterAgentNotifier broadcasts agent update events to other cluster nodes.
type ClusterAgentNotifier interface {
	PublishAgentUpdate() error
}

// MCPOAuthClusterNotifier broadcasts MCP OAuth updates to other cluster nodes.
type MCPOAuthClusterNotifier interface {
	PublishMCPOAuthUpdate(userID string) error
}

// API represents the HTTP API functionality for the plugin
type API struct {
	bots                  *bots.MMBots
	conversationsService  *conversations.Conversations
	meetingsService       *meetings.Service
	indexerService        *indexer.Indexer
	searchService         *search.Search
	pluginAPI             *pluginapi.Client
	metricsService        metrics.Metrics
	metricsHandler        http.Handler
	contextBuilder        *llmcontext.Builder
	prompts               *llm.Prompts
	config                Config
	mmClient              mmapi.Client
	dbClient              *mmapi.DBClient
	licenseChecker        *enterprise.LicenseChecker
	streamingService      streaming.Service
	i18nBundle            *i18n.Bundle
	mcpClientManager      MCPClientManager
	mcpHandlers           *mcpserver.PluginMCPHandlers
	beforeHookStore       *mcp.BeforeHookStore
	llmUpstreamHTTPClient *http.Client
	configStore           ConfigStore
	agentStore            AgentStore
	configUpdater         ConfigUpdater
	clusterNotifier       ClusterNotifier
	clusterAgentNotifier  ClusterAgentNotifier
	mcpOAuthNotifier      MCPOAuthClusterNotifier
	conversationStore     ConversationStore
	convService           *conversation.Service
	getSearchInitError    func() string
	customPromptsStore    *customprompts.Store

	// externalRebuilderForTest must be nil in production; SetExternalRebuilderForTest
	// is the only supported entry point for tests.
	externalRebuilderForTest externalServerRebuilder
}

// SetExternalRebuilderForTest installs a test-only externalServerRebuilder.
func (a *API) SetExternalRebuilderForTest(rb externalServerRebuilder) {
	a.externalRebuilderForTest = rb
}

// New creates a new API instance
func New(
	bots *bots.MMBots,
	conversationsService *conversations.Conversations,
	meetingsService *meetings.Service,
	indexerService *indexer.Indexer,
	searchService *search.Search,
	pluginAPI *pluginapi.Client,
	metricsService metrics.Metrics,
	llmContextBuilder *llmcontext.Builder,
	config Config,
	prompts *llm.Prompts,
	mmClient mmapi.Client,
	dbClient *mmapi.DBClient,
	licenseChecker *enterprise.LicenseChecker,
	streamingService streaming.Service,
	i18nBundle *i18n.Bundle,
	mcpClientManager MCPClientManager,
	mcpHandlers *mcpserver.PluginMCPHandlers,
	llmUpstreamHTTPClient *http.Client,
	configStore ConfigStore,
	agentStore AgentStore,
	configUpdater ConfigUpdater,
	clusterNotifier ClusterNotifier,
	clusterAgentNotifier ClusterAgentNotifier,
	mcpOAuthNotifier MCPOAuthClusterNotifier,
	conversationStore ConversationStore,
	getSearchInitError func() string,
	customPromptsStore *customprompts.Store,
) *API {
	return &API{
		bots:                  bots,
		conversationsService:  conversationsService,
		meetingsService:       meetingsService,
		indexerService:        indexerService,
		searchService:         searchService,
		pluginAPI:             pluginAPI,
		metricsService:        metricsService,
		metricsHandler:        metrics.NewMetricsHandler(metricsService),
		contextBuilder:        llmContextBuilder,
		prompts:               prompts,
		config:                config,
		mmClient:              mmClient,
		dbClient:              dbClient,
		licenseChecker:        licenseChecker,
		streamingService:      streamingService,
		i18nBundle:            i18nBundle,
		mcpClientManager:      mcpClientManager,
		mcpHandlers:           mcpHandlers,
		beforeHookStore:       mcp.NewBeforeHookStore(&pluginAPI.KV),
		llmUpstreamHTTPClient: llmUpstreamHTTPClient,
		configStore:           configStore,
		agentStore:            agentStore,
		configUpdater:         configUpdater,
		clusterNotifier:       clusterNotifier,
		clusterAgentNotifier:  clusterAgentNotifier,
		mcpOAuthNotifier:      mcpOAuthNotifier,
		conversationStore:     conversationStore,
		getSearchInitError:    getSearchInitError,
		customPromptsStore:    customPromptsStore,
	}
}

// SetConversationService sets the conversation entity service for channel analysis.
func (a *API) SetConversationService(svc *conversation.Service) {
	a.convService = svc
}

// ServeHTTP handles HTTP requests to the plugin
func (a *API) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	router := gin.Default()
	router.Use(otelgin.Middleware("mattermost-ai-agents"))
	router.Use(a.ginlogger)
	router.Use(a.metricsMiddleware)

	// LLM Bridge API v1 routes - inter-plugin only
	llmBridgeRoute := router.Group("/bridge/v1")
	llmBridgeRoute.Use(a.interPluginAuthorizationRequired)

	// Discovery endpoints
	llmBridgeRoute.GET("/agents", a.validateUserIDQuery, a.handleGetAgents)
	llmBridgeRoute.GET("/agents/:agent/tools", a.validateAgentParam, a.validateUserIDQuery, a.handleGetAgentTools)
	llmBridgeRoute.GET("/services", a.validateUserIDQuery, a.handleGetServices)

	// Completion endpoints
	completionRoute := llmBridgeRoute.Group("/completion")
	completionRoute.POST("/agent/:agent", a.validateAgentParam, a.handleAgentCompletionStreaming)
	completionRoute.POST("/agent/:agent/nostream", a.validateAgentParam, a.handleAgentCompletionNoStream)
	completionRoute.POST("/service/:service", a.handleServiceCompletionStreaming)
	completionRoute.POST("/service/:service/nostream", a.handleServiceCompletionNoStream)

	llmBridgeRoute.POST("/mcp/register", a.handleMCPRegister)
	llmBridgeRoute.POST("/mcp/unregister", a.handleMCPUnregister)

	// MCP server endpoints - grouped under /mcp-server/
	if a.mcpHandlers != nil && a.config.MCP().EnablePluginServer {
		mcpServerGroup := router.Group("/mcp-server")

		// Store plugin.Context in gin.Context for MCP endpoints
		mcpServerGroup.Use(func(gc *gin.Context) {
			gc.Set("pluginContext", c)
			gc.Next()
		})

		mcpServerGroup.GET("/.well-known/oauth-protected-resource", func(gc *gin.Context) {
			a.mcpHandlers.OAuthMetadataHandler(gc.Writer, gc.Request)
		})

		// MCP endpoint with authentication
		mcpServerGroup.Use(a.mcpAuthMiddleware)
		mcpServerGroup.Any("/mcp", func(gc *gin.Context) {
			a.delegateToMCPHandler(gc, a.mcpHandlers.MCPHandler)
		})
	}

	router.Use(a.MattermostAuthorizationRequired)

	router.GET("/conversations/:conversationid", a.handleGetConversation)

	router.GET("/oauth/callback", a.handleOAuthCallback)
	router.GET("/ai_threads", a.handleGetAIThreads)
	router.GET("/ai_bots", a.handleGetAIBots)
	router.GET("/mcp/tools", a.handleGetUserMCPTools)
	router.GET("/mcp/oauth/:serverName/start", a.handleOAuthStart)
	router.GET("/mcp/user-preferences", a.handleGetUserPreferences)
	router.PUT("/mcp/user-preferences", a.handlePutUserPreferences)
	router.DELETE("/mcp/oauth/:serverName", a.handleDeleteUserMCPOAuth)

	// Agent routes — authenticated. Free-tier instances (no multi-LLM license)
	// can CRUD up to one self-service agent; the quota is enforced inside
	// handleCreateAgent so reads, updates, deletes, and avatar uploads remain
	// available even after a license downgrade.
	agentRouter := router.Group("/agents")
	agentRouter.POST("", a.handleCreateAgent)
	agentRouter.GET("", a.handleListAgents)
	// Register /models/fetch before /:agentid routes so "models" is never captured as :agentid.
	agentRouter.POST("/models/fetch", a.handleFetchModelsForService)
	agentRouter.GET("/:agentid", a.handleGetAgent)
	agentRouter.PUT("/:agentid", a.handleUpdateAgent)
	agentRouter.DELETE("/:agentid", a.handleDeleteAgent)
	agentRouter.POST("/:agentid/avatar", a.handleUploadAgentAvatar)

	router.GET("/services", a.handleListServices)

	// Raw search endpoint returns enriched semantic search results without LLM processing.
	// Used by the MCP server for external search callbacks.
	router.POST("/search/raw", a.handleRawSearch)

	// Custom prompts routes — available to all authenticated users
	promptsRouter := router.Group("/custom-prompts")
	promptsRouter.POST("", a.handleCreateCustomPrompt)
	promptsRouter.GET("", a.handleListCustomPrompts)
	promptsRouter.PUT("/:id", a.handleUpdateCustomPrompt)
	promptsRouter.DELETE("/:id", a.handleDeleteCustomPrompt)
	promptsRouter.GET("/pins", a.handleGetPromptPins)
	promptsRouter.PUT("/pins", a.handleSetPromptPin)
	promptsRouter.POST("/:id/render", a.handleRenderCustomPrompt)

	botRequiredRouter := router.Group("")
	botRequiredRouter.Use(a.aiBotRequired)

	postRouter := botRequiredRouter.Group("/post/:postid")
	postRouter.Use(a.postAuthorizationRequired)
	postRouter.POST("/react", a.handleReact)
	postRouter.POST("/analyze", a.handleThreadAnalysis)
	postRouter.POST("/transcribe/file/:fileid", a.handleTranscribeFile)
	postRouter.POST("/summarize_transcription", a.handleSummarizeTranscription)
	postRouter.POST("/stop", a.handleStop)
	postRouter.POST("/regenerate", a.handleRegenerate)
	postRouter.POST("/tool_call", a.handleToolCall)
	postRouter.POST("/tool_result", a.handleToolResult)
	postRouter.POST("/postback_summary", a.handlePostbackSummary)
	postRouter.POST("/loop_in_agent", a.handleLoopInAgent)

	channelRouter := botRequiredRouter.Group("/channel/:channelid")
	channelRouter.Use(a.channelAuthorizationRequired)
	channelRouter.POST("/analyze", a.channelAnalysisLicenseRequired, a.handleChannelAnalysis)
	channelRouter.POST("/interval", a.channelAnalysisLicenseRequired, a.handleInterval)

	adminRouter := router.Group("/admin")
	adminRouter.Use(a.mattermostAdminAuthorizationRequired)
	adminRouter.POST("/reindex", a.handleReindexPosts)
	adminRouter.GET("/reindex/status", a.handleGetJobStatus)
	adminRouter.POST("/reindex/cancel", a.handleCancelJob)
	adminRouter.POST("/reindex/catchup", a.handleCatchUpIndex)
	adminRouter.GET("/reindex/health-check", a.handleIndexHealthCheck)
	adminRouter.GET("/mcp/tools", a.handleGetMCPTools)
	adminRouter.GET("/mcp/vetted-tool-seed", a.handleGetVettedToolSeed)
	adminRouter.POST("/mcp/tools/cache/clear", a.handleClearMCPToolsCache)
	adminRouter.PUT("/mcp/plugin-servers/:pluginID", a.handleUpdatePluginServer)
	adminRouter.POST("/models/fetch", a.handleFetchModels)
	adminRouter.GET("/config", a.handleGetConfig)
	adminRouter.PUT("/config", a.handleSaveConfig)

	searchRouter := botRequiredRouter.Group("/search")
	// Only returns search results
	searchRouter.POST("", a.handleSearchQuery)
	// Initiates a search and responds to the user in a DM with the selected bot
	searchRouter.POST("/run", a.handleRunSearch)

	router.ServeHTTP(w, r)
}

// ServeMetrics serves the metrics endpoint
func (a *API) ServeMetrics(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	a.metricsHandler.ServeHTTP(w, r)
}

func (a *API) metricsMiddleware(c *gin.Context) {
	a.metricsService.IncrementHTTPRequests()
	now := time.Now()

	c.Next()

	elapsed := float64(time.Since(now)) / float64(time.Second)

	status := c.Writer.Status()

	if status < 200 || status > 299 {
		a.metricsService.IncrementHTTPErrors()
	}

	endpoint := c.HandlerName()
	a.metricsService.ObserveAPIEndpointDuration(endpoint, c.Request.Method, strconv.Itoa(status), elapsed)
}

func (a *API) aiBotRequired(c *gin.Context) {
	botUsername := c.Query("botUsername")
	if botUsername == "" {
		botUsername = a.config.GetDefaultBotName()
	}

	bot := a.bots.GetBotByUsernameOrFirst(botUsername)
	if bot == nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get bot: %s", botUsername))
		return
	}
	c.Set(ContextBotKey, bot)
}

func (a *API) ginlogger(c *gin.Context) {
	c.Next()

	for _, ginErr := range c.Errors {
		a.pluginAPI.Log.Error(ginErr.Error())
	}
}

func (a *API) MattermostAuthorizationRequired(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	if userID == "" {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
}

func (a *API) interPluginAuthorizationRequired(c *gin.Context) {
	pluginID := c.GetHeader("Mattermost-Plugin-ID")
	if pluginID != "" {
		return
	}
	c.AbortWithStatus(http.StatusUnauthorized)
}

// enforceEmptyBody checks if the request body is empty returning an error if not
func (a *API) enforceEmptyBody(c *gin.Context) error {
	// Check the body is empty
	if _, err := c.Request.Body.Read(make([]byte, 1)); err != io.EOF {
		return fmt.Errorf("request body must be empty")
	}
	return nil
}

// aiThreadResponse is the JSON shape for items in the GET /ai_threads response.
// This is a history DTO — only navigable, summary-level fields. No message
// preview is included because the 2.0 conversation model stores assistant
// content in typed blocks rather than a single message string.
type aiThreadResponse struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	ChannelID  *string `json:"channel_id"`
	BotID      string  `json:"bot_id"`
	RootPostID *string `json:"root_post_id"`
	TurnCount  int     `json:"turn_count"`
	UpdateAt   int64   `json:"update_at"`
}

func (a *API) handleGetAIThreads(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	summaries, err := a.conversationStore.GetConversationSummariesForUser(userID, 60, 0)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get conversation summaries: %w", err))
		return
	}

	threads := make([]aiThreadResponse, len(summaries))
	for i, s := range summaries {
		threads[i] = aiThreadResponse{
			ID:         s.ID,
			Title:      s.Title,
			ChannelID:  s.ChannelID,
			BotID:      s.BotID,
			RootPostID: s.RootPostID,
			TurnCount:  s.TurnCount,
			UpdateAt:   s.UpdatedAt,
		}
	}

	c.JSON(http.StatusOK, threads)
}

type AIBotInfo struct {
	ID                    string                 `json:"id"`
	DisplayName           string                 `json:"displayName"`
	Username              string                 `json:"username"`
	LastIconUpdate        int64                  `json:"lastIconUpdate"`
	DMChannelID           string                 `json:"dmChannelID"`
	ChannelAccessLevel    llm.ChannelAccessLevel `json:"channelAccessLevel"`
	ChannelIDs            []string               `json:"channelIDs"`
	UserAccessLevel       llm.UserAccessLevel    `json:"userAccessLevel"`
	UserIDs               []string               `json:"userIDs"`
	EnabledMCPTools       []llm.EnabledMCPTool   `json:"enabledMCPTools"`
	AutoEnableNewMCPTools bool                   `json:"autoEnableNewMCPTools"`
}

type AIBotsResponse struct {
	Bots             []AIBotInfo `json:"bots"`
	SearchEnabled    bool        `json:"searchEnabled"`
	AllowUnsafeLinks bool        `json:"allowUnsafeLinks"`
}

// getAIBotsForUser returns all AI bots available to a user
func (a *API) getAIBotsForUser(userID string) ([]AIBotInfo, error) {
	allBots := a.bots.GetAllBots()

	// Get the info from all the bots.
	// Put the default bot first.
	bots := make([]AIBotInfo, 0, len(allBots))
	defaultBotName := a.config.GetDefaultBotName()
	for _, bot := range allBots {
		// Don't return bots the user is excluded from using.
		if a.bots.CheckUsageRestrictionsForUser(bot, userID) != nil {
			continue
		}

		// Get the bot DM channel ID. To avoid creating the channel unless nessary
		/// we return "" if the channel doesn't exist.
		dmChannelID := ""
		channelName := model.GetDMNameFromIds(userID, bot.GetMMBot().UserId)
		botDMChannel, err := a.pluginAPI.Channel.GetByName("", channelName, false)
		if err == nil {
			dmChannelID = botDMChannel.Id
		}

		bots = append(bots, AIBotInfo{
			ID:                    bot.GetMMBot().UserId,
			DisplayName:           bot.GetMMBot().DisplayName,
			Username:              bot.GetMMBot().Username,
			LastIconUpdate:        bot.GetMMBot().LastIconUpdate,
			DMChannelID:           dmChannelID,
			ChannelAccessLevel:    bot.GetConfig().ChannelAccessLevel,
			ChannelIDs:            bot.GetConfig().ChannelIDs,
			UserAccessLevel:       bot.GetConfig().UserAccessLevel,
			UserIDs:               bot.GetConfig().UserIDs,
			EnabledMCPTools:       bot.GetConfig().EnabledMCPTools,
			AutoEnableNewMCPTools: bot.GetConfig().AutoEnableNewMCPTools,
		})
		if bot.GetMMBot().Username == defaultBotName {
			last := len(bots) - 1
			bots[0], bots[last] = bots[last], bots[0]
		}
	}

	return bots, nil
}

func (a *API) handleGetAIBots(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	bots, err := a.getAIBotsForUser(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Check if search is enabled
	searchEnabled := a.searchService.Enabled()

	c.JSON(http.StatusOK, AIBotsResponse{
		Bots:             bots,
		SearchEnabled:    searchEnabled,
		AllowUnsafeLinks: a.config.AllowUnsafeLinks(),
	})
}

type FetchModelsRequest struct {
	ServiceType string `json:"serviceType"`
	APIKey      string `json:"apiKey"`
	APIURL      string `json:"apiURL"`
	OrgID       string `json:"orgID"`

	// Region applies to providers that require it for model listing (Vertex AI).
	Region string `json:"region"`

	// Vertex AI credentials. VertexAuthCredentials may be empty to signal ADC.
	VertexProjectID       string `json:"vertexProjectID"`
	VertexProjectNumber   string `json:"vertexProjectNumber"`
	VertexAuthCredentials string `json:"vertexAuthCredentials"`
}

func (a *API) handleFetchModels(c *gin.Context) {
	var req FetchModelsRequest
	if err := c.BindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if req.ServiceType == "" {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("serviceType is required"))
		return
	}

	switch req.ServiceType {
	case llm.ServiceTypeOpenAICompatible:
		// openaicompatible accepts API key OR API URL (some endpoints don't require auth).
		if req.APIKey == "" && req.APIURL == "" {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("apiURL is required for openaicompatible when apiKey is not provided"))
			return
		}
	case llm.ServiceTypeVertex:
		// Vertex AI authenticates via project + region; service-account JSON is optional (ADC).
		if req.VertexProjectID == "" || req.Region == "" {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("vertexProjectID and region are required for Vertex AI"))
			return
		}
	default:
		if req.APIKey == "" {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("apiKey is required"))
			return
		}
	}

	if !bifrost.IsSupported(req.ServiceType) {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("model fetching not supported for service type: %s", req.ServiceType))
		return
	}

	models, err := bifrost.FetchModelsForService(llm.ServiceConfig{
		Type:                  req.ServiceType,
		APIKey:                req.APIKey,
		APIURL:                req.APIURL,
		OrgID:                 req.OrgID,
		Region:                req.Region,
		VertexProjectID:       req.VertexProjectID,
		VertexProjectNumber:   req.VertexProjectNumber,
		VertexAuthCredentials: req.VertexAuthCredentials,
	})
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to fetch models: %w", err))
		return
	}

	c.JSON(http.StatusOK, models)
}
