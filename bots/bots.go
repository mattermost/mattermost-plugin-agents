// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/assets"
	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/loadtest"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/subtitles"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/maximhq/bifrost/core/schemas"
)

type Config interface {
	GetBots() []llm.BotConfig
	GetServiceByID(id string) (llm.ServiceConfig, bool)
	GetDefaultBotName() string
	EnableTokenUsageLogging() bool
	EnableTokenUsageLogToPlugin() bool
	EnableTokenUsageLogToFile() bool
	GetTranscriptGenerator() string
}

// AgentStore provides read access to user-created agents from the database.
// This is a subset of the full store.AgentStore — only read methods needed here.
type AgentStore interface {
	ListAgents() ([]*llm.BotConfig, error)
}

// Transcriber interface defines the contract for transcription services
type Transcriber interface {
	Transcribe(file io.Reader) (*subtitles.Subtitles, error)
}

type MMBots struct {
	ensureBotsClusterMutex cluster.MutexPluginAPI
	pluginAPI              *pluginapi.Client
	licenseChecker         *enterprise.LicenseChecker
	config                 Config
	agentStore             AgentStore
	llmUpstreamHTTPClient  *http.Client
	tokenUsageSinks        *llm.TokenUsageSinks
	metrics                llm.MetricsObserver

	tokenSinksMu sync.Mutex
	botsLock     sync.RWMutex
	bots         []*Bot

	// serviceLLMMu guards the service LLM registry below. It is never held
	// while a model is being built.
	serviceLLMMu sync.Mutex
	// serviceLLMs holds the live service-backed models keyed by service ID.
	serviceLLMs map[string]*serviceLLMEntry
	// retiredServiceLLMs holds models that are no longer handed out but may
	// still have in-flight leases; each shuts down once its leases drain.
	retiredServiceLLMs map[*serviceLLMEntry]struct{}
	// serviceLLMBuildMu serializes concurrent first builds. It is held across a
	// build, so it must never be taken while holding serviceLLMMu.
	serviceLLMBuildMu sync.Mutex
	// baseLLMBuilderForTest replaces provider client construction so tests can
	// exercise the real wrapper chain and the registry without starting Bifrost
	// worker pools. Always nil in production;
	// SetBaseLLMBuilderForTest is the only supported entry point.
	baseLLMBuilderForTest func(svc llm.ServiceConfig, botConfig llm.BotConfig, fallbacks []llm.ServiceConfig) (llm.LanguageModel, func(), error)

	// lastEnsuredBotCfgs stores the bot configs that were last successfully ensured.
	// This is used for optimistic checking to avoid unnecessary cluster mutex acquisition.
	lastEnsuredBotCfgs []llm.BotConfig
	// lastEnsuredServiceCfgs stores the resolved service configs keyed by service ID
	// that were last successfully ensured, for optimistic change detection.
	lastEnsuredServiceCfgs map[string]llm.ServiceConfig

	// forceRefresh bypasses the optimistic config-equality check in EnsureBots.
	// Set to true by the cluster event handler or API handlers after agent CRUD.
	forceRefresh bool
}

// SetBaseLLMBuilderForTest installs a test-only provider client builder. The
// wrapper chain around it stays the production one.
func (b *MMBots) SetBaseLLMBuilderForTest(builder func(svc llm.ServiceConfig, botConfig llm.BotConfig, fallbacks []llm.ServiceConfig) (llm.LanguageModel, func(), error)) {
	b.baseLLMBuilderForTest = builder
}

func New(mutexPluginAPI cluster.MutexPluginAPI, pluginAPI *pluginapi.Client, licenseChecker *enterprise.LicenseChecker, config Config, agentStore AgentStore, llmUpstreamHTTPClient *http.Client, metrics llm.MetricsObserver) *MMBots {
	var pluginTokenLogger llm.TokenUsagePluginLogger
	if pluginAPI != nil {
		pluginTokenLogger = &pluginAPI.Log
	}

	return &MMBots{
		ensureBotsClusterMutex: mutexPluginAPI,
		pluginAPI:              pluginAPI,
		licenseChecker:         licenseChecker,
		config:                 config,
		agentStore:             agentStore,
		llmUpstreamHTTPClient:  llmUpstreamHTTPClient,
		tokenUsageSinks:        llm.NewTokenUsageSinks(pluginTokenLogger),
		metrics:                metrics,
	}
}

// snapshotBotsAndServices returns the full bot lineup (file-config bots plus
// DB-backed agents, license cap applied) and the services they reference.
// EnsureBots calls this for both the optimistic equality check and the
// rebuild, so the check can't miss a service used only by a DB agent.
func (b *MMBots) snapshotBotsAndServices() ([]llm.BotConfig, map[string]struct{}, map[string]llm.ServiceConfig, error) {
	// config.GetBots() returns the config-owned slice; clone before
	// truncating + appending so we don't overwrite it.
	botCfgs := slices.Clone(b.config.GetBots())
	if len(botCfgs) > 1 && !b.licenseChecker.IsMultiLLMLicensed() {
		b.pluginAPI.Log.Error("Only one bot allowed with current license.")
		botCfgs = botCfgs[:1]
	}

	// DB-backed user agents bypass the license multi-LLM cap — gated by
	// PermissionManageOwnAgent at the API layer instead.
	activeDBBotUsernames := make(map[string]struct{})
	if b.agentStore != nil {
		dbAgents, err := b.agentStore.ListAgents()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to list user agents: %w", err)
		}
		for _, cfg := range dbAgents {
			if cfg == nil {
				continue
			}
			activeDBBotUsernames[cfg.Name] = struct{}{}
			botCfgs = append(botCfgs, *cfg)
		}
	}

	serviceCfgs := b.resolveServiceCfgs(botCfgs)
	return botCfgs, activeDBBotUsernames, serviceCfgs, nil
}

// resolveServiceCfgs builds a map of service configs referenced by the given
// bot configs, including any services in each service's fallback chain. This
// ensures that changes to fallback services are detected for optimistic
// change-detection in EnsureBots.
func (b *MMBots) resolveServiceCfgs(botCfgs []llm.BotConfig) map[string]llm.ServiceConfig {
	result := make(map[string]llm.ServiceConfig, len(botCfgs))
	for _, botCfg := range botCfgs {
		if _, exists := result[botCfg.ServiceID]; !exists {
			if svc, ok := b.config.GetServiceByID(botCfg.ServiceID); ok {
				result[botCfg.ServiceID] = svc
			}
		}
		// Include fallback chain services so changes to them trigger re-init.
		// Best-effort: a chain-resolution error is surfaced when the bot's LLM
		// is built, so it is ignored here.
		fbChain, _ := llm.ResolveFallbackChain(botCfg.ServiceID, b.config.GetServiceByID)
		for _, fbSvc := range fbChain {
			if _, exists := result[fbSvc.ID]; !exists {
				result[fbSvc.ID] = fbSvc
			}
		}
	}
	return result
}

// ForceRefreshOnNextEnsure clears the optimistic ensure snapshot and sets forceRefresh so the
// next EnsureBots() cannot take the fast path. DB-backed agents are not part of the
// config-file bot slice used for botConfigsEqual, so we must invalidate when agents change.
func (b *MMBots) ForceRefreshOnNextEnsure() {
	b.botsLock.Lock()
	defer b.botsLock.Unlock()
	b.lastEnsuredBotCfgs = nil
	b.lastEnsuredServiceCfgs = nil
	b.forceRefresh = true
}

// botConfigsEqual compares two bot config slices for equality.
// Uses reflect.DeepEqual to compare all fields, ensuring changes to any field
// (e.g., EnabledNativeTools, CustomInstructions, access levels) are detected.
// Comparison is order-independent, matching configs by ID.
func botConfigsEqual(a, b []llm.BotConfig) bool {
	if len(a) != len(b) {
		return false
	}

	aMap := make(map[string]llm.BotConfig, len(a))
	for _, cfg := range a {
		aMap[cfg.ID] = cfg
	}

	for _, cfg := range b {
		aCfg, ok := aMap[cfg.ID]
		if !ok {
			return false
		}
		if !reflect.DeepEqual(aCfg, cfg) {
			return false
		}
	}

	return true
}

// serviceConfigsEqual compares two service config maps for equality.
func serviceConfigsEqual(a, b map[string]llm.ServiceConfig) bool {
	if len(a) != len(b) {
		return false
	}

	for id, aCfg := range a {
		bCfg, ok := b[id]
		if !ok {
			return false
		}
		if !reflect.DeepEqual(aCfg, bCfg) {
			return false
		}
	}

	return true
}

func (b *MMBots) reconcileTokenUsageSinks() {
	if b == nil || b.config == nil || b.tokenUsageSinks == nil {
		return
	}

	loggingEnabled := b.config.EnableTokenUsageLogging()
	pluginEnabled := loggingEnabled && b.config.EnableTokenUsageLogToPlugin()
	fileEnabled := loggingEnabled && b.config.EnableTokenUsageLogToFile()

	b.tokenSinksMu.Lock()
	defer b.tokenSinksMu.Unlock()

	b.tokenUsageSinks.SetLoggingEnabled(loggingEnabled)
	b.tokenUsageSinks.SetPluginEnabled(pluginEnabled)
	b.tokenUsageSinks.SetFileEnabled(fileEnabled)

	if !fileEnabled {
		b.tokenUsageSinks.SetFileLogger(nil)
		return
	}

	if b.tokenUsageSinks.FileLogger() != nil {
		return
	}

	tokenLogger, err := llm.CreateTokenLogger()
	if err != nil {
		if b.pluginAPI != nil {
			b.pluginAPI.Log.Warn("Failed to initialize token usage file logger; continuing without file sink", "error", err)
		}
		b.tokenUsageSinks.SetFileLogger(nil)
		b.tokenUsageSinks.SetFileEnabled(false)
		return
	}
	b.tokenUsageSinks.SetFileLogger(tokenLogger)
}

// snapshotForEnsure reconciles the token usage sinks, re-reads the bot and
// service configuration, and reports whether EnsureBots can skip the rebuild
// because nothing changed since the last successful ensure. Called twice per
// EnsureBots — once optimistically and once after acquiring the cluster mutex
// (deliberate double-checked locking).
func (b *MMBots) snapshotForEnsure() (botCfgs []llm.BotConfig, activeDBBotUsernames map[string]struct{}, serviceCfgs map[string]llm.ServiceConfig, unchanged bool, err error) {
	b.reconcileTokenUsageSinks()

	botCfgs, activeDBBotUsernames, serviceCfgs, err = b.snapshotBotsAndServices()
	if err != nil {
		return nil, nil, nil, false, err
	}
	b.botsLock.RLock()
	botsAlreadyInitialized := len(b.bots) > 0
	lastBotCfgs := b.lastEnsuredBotCfgs
	lastServiceCfgs := b.lastEnsuredServiceCfgs
	forceRefresh := b.forceRefresh
	b.botsLock.RUnlock()

	unchanged = botsAlreadyInitialized && !forceRefresh && botConfigsEqual(lastBotCfgs, botCfgs) && serviceConfigsEqual(lastServiceCfgs, serviceCfgs)
	return botCfgs, activeDBBotUsernames, serviceCfgs, unchanged, nil
}

func (b *MMBots) EnsureBots() error {
	if b.config == nil {
		return nil
	}

	// Optimistic check: if bot and service configuration hasn't changed since last ensure,
	// skip the expensive cluster mutex acquisition. This prevents HA timeout issues
	// when multiple nodes all try to acquire the mutex simultaneously on config changes.
	_, _, _, unchanged, err := b.snapshotForEnsure()
	if err != nil {
		return err
	}
	if unchanged {
		b.pluginAPI.Log.Debug("EnsureBots: skipping - bot/service configuration unchanged")
		return nil
	}

	mtx, err := cluster.NewMutex(b.ensureBotsClusterMutex, "ai_ensure_bots")
	if err != nil {
		return fmt.Errorf("failed to create mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	// Re-check after acquiring lock - another node may have already handled this
	currentBotCfgs, activeDBBotUsernames, currentServiceCfgs, unchanged, err := b.snapshotForEnsure()
	if err != nil {
		return err
	}
	if unchanged {
		b.pluginAPI.Log.Debug("EnsureBots: skipping after lock - bot/service configuration unchanged")
		return nil
	}

	previousMMBots, err := b.pluginAPI.Bot.List(0, 1000, pluginapi.BotOwner("mattermost-ai"), pluginapi.BotIncludeDeleted())
	if err != nil {
		return fmt.Errorf("failed to list bots: %w", err)
	}
	botCfgs := currentBotCfgs

	var bots []*Bot
	aiBotsByUsername := make(map[string]*Bot)
	for _, botCfg := range botCfgs {
		if !botCfg.IsValid() {
			b.pluginAPI.Log.Error("Configured bot is not valid", "bot_name", botCfg.Name, "bot_display_name", botCfg.DisplayName)
			continue
		}

		// Get service by ID
		service, ok := b.config.GetServiceByID(botCfg.ServiceID)
		if !ok {
			b.pluginAPI.Log.Error("Bot references non-existent service", "bot_name", botCfg.Name, "service_id", botCfg.ServiceID)
			continue
		}

		// Validate service configuration
		if !llm.IsValidService(service) {
			b.pluginAPI.Log.Error("Bot references invalid service", "bot_name", botCfg.Name, "service_id", botCfg.ServiceID, "service_type", service.Type)
			continue
		}

		if _, ok := aiBotsByUsername[botCfg.Name]; ok {
			// Duplicate bot names have to be fatal because they would cause a bot to be modified inappropreately.
			return fmt.Errorf("duplicate bot name: %s", botCfg.Name)
		}

		// Use bot's model if specified, otherwise fall back to service's default model
		if botCfg.Model != "" {
			service.DefaultModel = botCfg.Model
		}

		bot := &Bot{cfg: botCfg, service: service}
		bots = append(bots, bot)
		aiBotsByUsername[botCfg.Name] = bot
	}

	prevousMMBotsByUsername := make(map[string]*model.Bot)
	for _, bot := range previousMMBots {
		prevousMMBotsByUsername[bot.Username] = bot
	}

	// For each of the bots we found, if it's not in the configuration, delete it.
	for _, bot := range previousMMBots {
		if _, ok := aiBotsByUsername[bot.Username]; !ok {
			if _, dbActive := activeDBBotUsernames[bot.Username]; dbActive {
				b.pluginAPI.Log.Debug("EnsureBots: skipping deactivation for active DB agent not in ensure set (missing or invalid service)", "bot_name", bot.Username)
				continue
			}
			if _, err := b.pluginAPI.Bot.UpdateActive(bot.UserId, false); err != nil {
				b.pluginAPI.Log.Error("Failed to delete bot", "bot_name", bot.Username, "error", err.Error())
				continue
			}
		}
	}

	// For each bot in the configuration, try to find an existing bot matching the username.
	// If it exists, update it to match. Otherwise, create a new bot.
	for _, bot := range bots {
		description := poweredByDescription(bot.service.Type, bot.service.DefaultModel)
		if prevBot, ok := prevousMMBotsByUsername[bot.cfg.Name]; ok {
			var err error
			bot.mmBot, err = b.pluginAPI.Bot.Patch(prevBot.UserId, &model.BotPatch{
				DisplayName: &bot.cfg.DisplayName,
				Description: &description,
			})
			if err != nil {
				b.pluginAPI.Log.Error("Failed to patch bot", "bot_name", bot.cfg.Name, "error", err.Error())
				continue
			}
			if _, err := b.pluginAPI.Bot.UpdateActive(prevBot.UserId, true); err != nil {
				b.pluginAPI.Log.Error("Failed to update bot active", "bot_name", bot.cfg.Name, "error", err.Error())
				continue
			}
		} else {
			bot.mmBot = &model.Bot{
				Username:    bot.cfg.Name,
				DisplayName: bot.cfg.DisplayName,
				Description: description,
			}
			err := b.pluginAPI.Bot.Create(bot.mmBot)
			if err != nil {
				b.pluginAPI.Log.Error("Failed to ensure bot", "bot_name", bot.cfg.Name, "error", err.Error())
				continue
			}
		}

		b.ensureDefaultProfileImage(bot)

		// Resolve fallback chain for this bot's service. A misconfigured chain
		// fails bot setup so the admin finds out now, not at failover time.
		fallbackServices, err := llm.ResolveFallbackChain(bot.service.ID, b.config.GetServiceByID)
		if err != nil {
			return fmt.Errorf("failed to resolve fallback chain for bot %s: %w", bot.cfg.Name, err)
		}

		bot.llm, bot.providerServices, err = b.getLLM(bot.service, bot.cfg, fallbackServices)
		if err != nil {
			return err
		}
	}

	b.botsLock.Lock()
	b.bots = bots
	// Store deep copies of the successfully ensured configs for optimistic checking.
	// Deep copy is needed because BotConfig contains slice fields (EnabledNativeTools, etc.)
	// that would otherwise share backing arrays with the live config. The JSON-based
	// copy has non-trivial cost for large bot sets, but this path runs only when
	// optimistic checks above detect bot/service config changes.
	copiedBotCfgs, copyErr := config.DeepCopyJSON(currentBotCfgs)
	if copyErr != nil {
		b.botsLock.Unlock()
		return fmt.Errorf("failed to deep copy bot configs for change tracking: %w", copyErr)
	}
	b.lastEnsuredBotCfgs = copiedBotCfgs
	b.lastEnsuredServiceCfgs = currentServiceCfgs
	b.forceRefresh = false
	b.botsLock.Unlock()

	return nil
}

func (b *MMBots) ensureDefaultProfileImage(bot *Bot) {
	user, err := b.pluginAPI.User.Get(bot.mmBot.UserId)
	if err != nil {
		b.pluginAPI.Log.Error("Failed to get bot user for profile image check", "bot_name", bot.cfg.Name, "error", err.Error())
		return
	}

	if user.LastPictureUpdate != 0 {
		return
	}

	if err := b.pluginAPI.User.SetProfileImage(bot.mmBot.UserId, bytes.NewReader(assets.DefaultAgentProfilePicture)); err != nil {
		b.pluginAPI.Log.Error("Failed to set bot profile image", "bot_name", bot.cfg.Name, "error", err.Error())
	}
}

// builtLLM is a provider client together with the handles only the unwrapped
// client can supply. Wrappers expose only llm.LanguageModel, so provider
// capabilities and the shutdown handle must be captured before wrapping.
type builtLLM struct {
	model llm.LanguageModel
	// providerServices reports the provider-side services the client can
	// perform (see llm.ProviderServices).
	providerServices *llm.ProviderServices
	// shutdown releases the underlying Bifrost client's worker pool and queue.
	// It is a no-op for the load-test mock.
	shutdown func()
}

// getLLM builds the language model for an agent and returns it with the
// provider services resolved from the unwrapped client. The shutdown handle is
// discarded: agent LLMs are replaced wholesale by EnsureBots, and the replaced
// clients are not currently shut down.
func (b *MMBots) getLLM(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig, fallbackServices []llm.ServiceConfig) (llm.LanguageModel, *llm.ProviderServices, error) {
	built, err := b.buildLLM(serviceConfig, &botConfig, fallbackServices)
	if err != nil {
		return nil, nil, err
	}
	return built.model, built.providerServices, nil
}

// buildLLM assembles the wrapper chain shared by agent LLMs and service LLMs.
// botConfig carries the agent's provider capability settings (native tools,
// reasoning) and is nil for direct service calls, which have no agent.
func (b *MMBots) buildLLM(serviceConfig llm.ServiceConfig, botConfig *llm.BotConfig, fallbackServices []llm.ServiceConfig) (builtLLM, error) {
	var effectiveBotConfig llm.BotConfig
	if botConfig != nil {
		effectiveBotConfig = *botConfig
	}

	base, err := b.getBaseLLM(serviceConfig, effectiveBotConfig, fallbackServices)
	if err != nil {
		return builtLLM{}, err
	}

	// Truncation Support
	var result llm.LanguageModel = llm.NewLLMTruncationWrapper(base.model)

	// Token Usage Logging
	// NOTE: This wrapper converts ChatCompletionNoStream into a streaming call
	// internally, so any wrapper that needs to intercept ChatCompletionNoStream
	// must be placed outside (after) this one.
	if b.tokenUsageSinks != nil || b.metrics != nil {
		result = llm.NewTokenUsageLoggingWrapper(
			result,
			tokenUsageIdentity(serviceConfig, botConfig),
			b.tokenUsageSinks,
			b.metrics,
		)
	}

	// Structured output fallback. The decision covers the primary and every
	// fallback provider, because it is applied before Bifrost picks one.
	result = llm.NewStructuredOutputFallbackWrapper(result, llm.NewNativeStructuredOutputDecision(
		serviceConfig,
		effectiveModelFor(serviceConfig, botConfig),
		fallbackServices,
		bifrost.ResolveStructuredOutputCapability,
	))

	base.model = result
	return base, nil
}

// effectiveModelFor returns the model the primary service will actually run:
// the agent's override when it has one, otherwise the service default.
func effectiveModelFor(serviceConfig llm.ServiceConfig, botConfig *llm.BotConfig) string {
	if botConfig != nil && botConfig.Model != "" {
		return botConfig.Model
	}
	return serviceConfig.DefaultModel
}

// tokenUsageIdentity describes the spender for token usage logging. botConfig is
// nil for a direct service call, which has no agent and so logs blank agent
// dimensions. EnsureBots may already have folded an agent's model override into
// the service's DefaultModel, so the effective model is computed explicitly.
func tokenUsageIdentity(serviceConfig llm.ServiceConfig, botConfig *llm.BotConfig) llm.TokenUsageIdentity {
	identity := llm.TokenUsageIdentity{
		ServiceID:    serviceConfig.ID,
		ServiceName:  serviceConfig.Name,
		DefaultModel: effectiveModelFor(serviceConfig, botConfig),
		ServiceType:  serviceConfig.Type,
	}
	if botConfig != nil {
		identity.BotUsername = botConfig.Name
	}
	return identity
}

// getBaseLLM constructs the unwrapped provider client behind a service.
func (b *MMBots) getBaseLLM(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig, fallbackServices []llm.ServiceConfig) (builtLLM, error) {
	if b.baseLLMBuilderForTest != nil {
		model, shutdown, err := b.baseLLMBuilderForTest(serviceConfig, botConfig, fallbackServices)
		if err != nil {
			return builtLLM{}, err
		}
		return builtLLM{model: model, providerServices: &llm.ProviderServices{}, shutdown: shutdown}, nil
	}

	if serviceConfig.Type == llm.ServiceTypeLoadTestMock {
		profile, err := loadtest.ParseProfile(serviceConfig.LoadTestMockConfig)
		if err != nil {
			return builtLLM{}, fmt.Errorf("failed to parse load-test mock profile for bot %s: %w", botConfig.Name, err)
		}
		if b.pluginAPI != nil {
			// Run-audit snapshot of the active mock profile (once per LLM init; not per request).
			b.pluginAPI.Log.Info(
				"Initialized load-test mock LLM",
				"bot_name", botConfig.Name,
				"service_id", serviceConfig.ID,
				"profile_summary", profile.Summary(),
			)
		}
		// The load-test mock talks to no provider, so it has no provider-side
		// services and nothing to shut down.
		return builtLLM{model: loadtest.NewMockLLM(profile), providerServices: &llm.ProviderServices{}, shutdown: func() {}}, nil
	}

	bifrostLLM, err := bifrost.NewFromServiceConfig(serviceConfig, botConfig, fallbackServices)
	if err != nil {
		if b.pluginAPI != nil {
			b.pluginAPI.Log.Error("Unsupported service type for bot", "bot_name", botConfig.Name, "service_type", serviceConfig.Type)
		}
		return builtLLM{}, fmt.Errorf("failed to create Bifrost client for %s: %w", serviceConfig.Type, err)
	}
	return builtLLM{model: bifrostLLM, providerServices: bifrostLLM.ProviderServices(), shutdown: bifrostLLM.Shutdown}, nil
}

// TODO: This really doesn't belong here. Figure out where to put this.
func (b *MMBots) GetTranscribe() Transcriber {
	// Get the configured transcript generator bot
	bot := b.getTranscriberBot()
	if bot == nil {
		b.pluginAPI.Log.Error("No transcript generator bot found")
		return nil
	}

	service := bot.service

	// Map service type to Bifrost provider
	var provider schemas.ModelProvider
	switch service.Type {
	case llm.ServiceTypeOpenAI:
		provider = schemas.OpenAI
	case llm.ServiceTypeOpenAICompatible:
		provider = schemas.OpenAI
	case llm.ServiceTypeAzure:
		provider = schemas.Azure
	default:
		b.pluginAPI.Log.Error("Unsupported service type for transcript generator",
			"bot_name", bot.GetMMBot().Username,
			"service_type", service.Type)
		return nil
	}

	transcriptModel := "whisper-1"

	transcriber, err := bifrost.NewTranscriber(bifrost.TranscriptionConfig{
		Provider: provider,
		APIKey:   service.APIKey,
		APIURL:   service.APIURL,
		Model:    transcriptModel,
	})
	if err != nil {
		b.pluginAPI.Log.Error("Failed to create Bifrost transcriber",
			"bot_name", bot.GetMMBot().Username,
			"error", err.Error())
		return nil
	}

	return transcriber
}

// findBot returns the first bot matching pred, or nil.
func (b *MMBots) findBot(pred func(*Bot) bool) *Bot {
	b.botsLock.RLock()
	defer b.botsLock.RUnlock()
	for _, bot := range b.bots {
		if pred(bot) {
			return bot
		}
	}
	return nil
}

func (b *MMBots) getTranscriberBot() *Bot {
	return b.findBot(func(bot *Bot) bool {
		return bot.cfg.Name == b.config.GetTranscriptGenerator()
	})
}

// GetBotByUsername retrieves the bot associated with the given bot username
func (b *MMBots) GetBotByUsername(botUsername string) *Bot {
	return b.findBot(func(bot *Bot) bool {
		return bot.cfg.Name == botUsername
	})
}

// GetBotByUsernameOrFirst retrieves the bot associated with the given bot username or the first bot if not found
func (b *MMBots) GetBotByUsernameOrFirst(botUsername string) *Bot {
	bot := b.GetBotByUsername(botUsername)
	if bot != nil {
		return bot
	}

	b.botsLock.RLock()
	defer b.botsLock.RUnlock()
	if len(b.bots) > 0 {
		return b.bots[0]
	}

	return nil
}

// GetBotByID retrieves the bot associated with the given bot ID
func (b *MMBots) GetBotByID(botID string) *Bot {
	return b.findBot(func(bot *Bot) bool {
		return bot.mmBot.UserId == botID
	})
}

// GetBotConfigByID returns the bot's EnableVision and MaxFileSize. ok is
// false when botID is unknown.
func (b *MMBots) GetBotConfigByID(botID string) (bool, int64, bool) {
	bot := b.GetBotByID(botID)
	if bot == nil {
		return false, 0, false
	}
	cfg := bot.GetConfig()
	return cfg.EnableVision, cfg.MaxFileSize, true
}

// GetBotForDMChannel returns the bot for the given DM channel.
func (b *MMBots) GetBotForDMChannel(channel *model.Channel) *Bot {
	return b.findBot(func(bot *Bot) bool {
		return mmapi.IsDMWith(bot.mmBot.UserId, channel)
	})
}

// IsAnyBot returns true if the given user is an AI bot.
func (b *MMBots) IsAnyBot(userID string) bool {
	return b.GetBotByID(userID) != nil
}

// GetBotMentioned returns the bot mentioned in the text, if any.
func (b *MMBots) GetBotMentioned(text string) *Bot {
	return b.findBot(func(bot *Bot) bool {
		return userIsMentionedMarkdown(text, bot.mmBot.Username)
	})
}

// GetAllBots returns all bots
func (b *MMBots) GetAllBots() []*Bot {
	b.botsLock.RLock()
	defer b.botsLock.RUnlock()

	return b.bots
}

// SetBotsForTesting sets bots directly for testing purposes only
func (b *MMBots) SetBotsForTesting(bots []*Bot) {
	b.botsLock.Lock()
	defer b.botsLock.Unlock()
	b.bots = bots
}

// GetAllBotUserIDs returns a list of all bot user IDs
func (b *MMBots) GetAllBotUserIDs() []string {
	b.botsLock.RLock()
	defer b.botsLock.RUnlock()

	ids := make([]string, 0, len(b.bots))
	for _, bot := range b.bots {
		if bot.mmBot != nil {
			ids = append(ids, bot.mmBot.UserId)
		}
	}
	return ids
}

// poweredByDescription builds the Mattermost bot description shown in the UI.
func poweredByDescription(serviceType, modelName string) string {
	var description string
	if modelName == "" {
		description = "Powered by " + serviceType
	} else {
		description = "Powered by " + serviceType + " - " + modelName
	}
	if utf8.RuneCountInString(description) > model.BotDescriptionMaxRunes {
		return string([]rune(description)[:model.BotDescriptionMaxRunes])
	}
	return description
}
