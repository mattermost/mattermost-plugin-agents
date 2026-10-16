// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
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

// botOwnerID owns the Mattermost bot accounts the plugin creates.
const botOwnerID = "mattermost-ai"

type Config interface {
	GetBots() []llm.BotConfig
	GetServiceByID(id string) (llm.ServiceConfig, bool)
	GetServices() []llm.ServiceConfig
	GetDefaultBotName() string
	EnableTokenUsageLogging() bool
	EnableTokenUsageLogToPlugin() bool
	EnableTokenUsageLogToFile() bool
	GetTranscriptGenerator() string
	GetTranscriptBotUserID() string
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
	accessChecker          *accesscontrol.Checker
	llmUpstreamHTTPClient  *http.Client
	tokenUsageSinks        *llm.TokenUsageSinks
	metrics                llm.MetricsObserver

	tokenSinksMu sync.Mutex
	// ensureMu serializes registry rebuilds on this node, so a full rebuild
	// that read the agents before a RefreshAgent cannot swap its stale
	// registry in afterwards. EnsureBots takes it after the cluster mutex;
	// it must never be acquired while holding botsLock.
	ensureMu sync.Mutex
	botsLock sync.RWMutex
	// bots is replaced, never mutated in place: readers keep using a slice
	// and its *Bot values after releasing botsLock.
	bots []*Bot

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
	baseLLMBuilderForTest func(svc llm.ServiceConfig, fallbacks []llm.ServiceConfig) (llm.LanguageModel, func(), error)

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
func (b *MMBots) SetBaseLLMBuilderForTest(builder func(svc llm.ServiceConfig, fallbacks []llm.ServiceConfig) (llm.LanguageModel, func(), error)) {
	b.baseLLMBuilderForTest = builder
}

// New builds the bot registry. accessChecker must be non-nil; tests that want
// no-policies-anywhere pass accesscontrol.New with PassthroughClient.
func New(mutexPluginAPI cluster.MutexPluginAPI, pluginAPI *pluginapi.Client, licenseChecker *enterprise.LicenseChecker, config Config, agentStore AgentStore, accessChecker *accesscontrol.Checker, llmUpstreamHTTPClient *http.Client, metrics llm.MetricsObserver) *MMBots {
	// A nil checker would panic later inside a live permission check.
	if accessChecker == nil {
		panic("bots: New requires a non-nil access checker")
	}
	var pluginTokenLogger llm.TokenUsagePluginLogger
	if pluginAPI != nil {
		pluginTokenLogger = &pluginAPI.Log
	}

	tokenUsageSinks := llm.NewTokenUsageSinks(pluginTokenLogger)
	tokenUsageSinks.SetAccountingEnabled(func() bool {
		return licenseChecker.Allows(enterprise.CapTokenAccounting)
	})

	return &MMBots{
		ensureBotsClusterMutex: mutexPluginAPI,
		pluginAPI:              pluginAPI,
		licenseChecker:         licenseChecker,
		config:                 config,
		agentStore:             agentStore,
		accessChecker:          accessChecker,
		llmUpstreamHTTPClient:  llmUpstreamHTTPClient,
		tokenUsageSinks:        tokenUsageSinks,
		metrics:                metrics,
	}
}

// dbAgentBotSet identifies the Mattermost bot accounts that belong to active
// DB-backed agents, by bot user ID and, for agents stored without one, by username.
type dbAgentBotSet struct {
	userIDs   map[string]struct{}
	usernames map[string]struct{}
}

func newDBAgentBotSet() dbAgentBotSet {
	return dbAgentBotSet{userIDs: map[string]struct{}{}, usernames: map[string]struct{}{}}
}

func (s dbAgentBotSet) add(cfg *llm.BotConfig) {
	if cfg.BotUserID != "" {
		s.userIDs[cfg.BotUserID] = struct{}{}
		return
	}
	s.usernames[cfg.Name] = struct{}{}
}

func (s dbAgentBotSet) owns(bot *model.Bot) bool {
	if _, ok := s.userIDs[bot.UserId]; ok {
		return true
	}
	_, ok := s.usernames[bot.Username]
	return ok
}

// matchPreviousMMBots pairs each bot with its existing Mattermost bot account.
// Bots that know their bot user ID are matched by it first, so an agent whose
// username changed keeps its account (and its DMs and history) instead of being
// recreated; the rest fall back to matching by username.
func matchPreviousMMBots(bots []*Bot, previousMMBots []*model.Bot) map[*Bot]*model.Bot {
	byUserID := make(map[string]*model.Bot, len(previousMMBots))
	byUsername := make(map[string]*model.Bot, len(previousMMBots))
	for _, mmBot := range previousMMBots {
		byUserID[mmBot.UserId] = mmBot
		byUsername[mmBot.Username] = mmBot
	}

	matched := make(map[*Bot]*model.Bot, len(bots))
	claimed := make(map[string]struct{}, len(bots))
	for _, bot := range bots {
		if mmBot, ok := byUserID[bot.cfg.BotUserID]; ok && bot.cfg.BotUserID != "" {
			matched[bot] = mmBot
			claimed[mmBot.UserId] = struct{}{}
		}
	}
	for _, bot := range bots {
		if _, ok := matched[bot]; ok {
			continue
		}
		mmBot, ok := byUsername[bot.cfg.Name]
		if !ok {
			continue
		}
		if _, taken := claimed[mmBot.UserId]; taken {
			continue
		}
		matched[bot] = mmBot
		claimed[mmBot.UserId] = struct{}{}
	}
	return matched
}

// snapshotBotsAndServices returns the full bot lineup (file-config bots plus
// DB-backed agents, license cap applied) and the services they reference.
// EnsureBots calls this for both the optimistic equality check and the
// rebuild, so the check can't miss a service used only by a DB agent.
func (b *MMBots) snapshotBotsAndServices() ([]llm.BotConfig, dbAgentBotSet, map[string]llm.ServiceConfig, error) {
	var dbAgents []*llm.BotConfig
	if b.agentStore != nil {
		var err error
		dbAgents, err = b.agentStore.ListAgents()
		if err != nil {
			return nil, dbAgentBotSet{}, nil, fmt.Errorf("failed to list user agents: %w", err)
		}
	}
	dbNames := make(map[string]struct{}, len(dbAgents))
	for _, cfg := range dbAgents {
		if cfg != nil {
			dbNames[cfg.Name] = struct{}{}
		}
	}

	pool := config.AgentPool(b.config.GetBots(), dbAgents)
	botCfgs, activeDBBots := b.selectActiveBots(pool, dbNames)
	serviceCfgs := b.resolveServiceCfgs(botCfgs)
	return botCfgs, activeDBBots, serviceCfgs, nil
}

func (b *MMBots) licenseLevel() enterprise.Level {
	if b == nil || b.licenseChecker == nil {
		return enterprise.LevelUnlicensed
	}
	return b.licenseChecker.Level()
}

// selectActiveBots keeps the agents config.AgentInactiveReasons reports as
// active. Inactive agents are omitted from the ensure set (so existing
// Mattermost bot accounts are deactivated) and from the returned DB agent set.
// DB rows are never deleted.
func (b *MMBots) selectActiveBots(pool []llm.BotConfig, dbNames map[string]struct{}) ([]llm.BotConfig, dbAgentBotSet) {
	level := b.licenseLevel()
	reasons := config.AgentInactiveReasons(b.config.GetServices(), pool, level)

	active := make([]llm.BotConfig, 0, len(pool))
	activeDB := newDBAgentBotSet()
	inactive := make(map[config.AgentInactiveReason][]string)
	for i, cfg := range pool {
		if reasons[i] != config.AgentActive {
			inactive[reasons[i]] = append(inactive[reasons[i]], cfg.Name)
			continue
		}
		active = append(active, cfg)
		if _, isDB := dbNames[cfg.Name]; isDB {
			activeDB.add(&pool[i])
		}
	}

	if b.pluginAPI != nil {
		if names := inactive[config.AgentInactiveInvalidConfig]; len(names) > 0 {
			b.pluginAPI.Log.Error("AI agents with an invalid configuration are inactive", "inactive_agents", strings.Join(names, ", "))
		}
		if names := inactive[config.AgentInactiveServiceUnavailable]; len(names) > 0 {
			b.pluginAPI.Log.Error("AI agents referencing a missing or incomplete LLM service are inactive", "inactive_agents", strings.Join(names, ", "))
		}
		if names := inactive[config.AgentInactiveServiceNotLicensed]; len(names) > 0 {
			b.pluginAPI.Log.Warn(
				"Agents referencing LLM services other than the first configured service are inactive; multiple LLM services are available at Enterprise and above",
				"inactive_agents", strings.Join(names, ", "),
				"license_level", level.String(),
			)
		}
		if names := inactive[config.AgentInactiveAgentLimit]; len(names) > 0 {
			limit, _ := enterprise.AgentLimitFor(level)
			b.pluginAPI.Log.Warn(
				"AI agents over the current license agent limit are inactive",
				"inactive_agents", strings.Join(names, ", "),
				"limit", limit,
				"license_level", level.String(),
			)
		}
	}

	return active, activeDB
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
		// Fallback chains are available at Enterprise Advanced. Exclude them
		// from change detection so the snapshot matches getLLM.
		if !b.licenseChecker.Allows(enterprise.CapModelFallback) {
			continue
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

	loggingEnabled := b.licenseChecker.Allows(enterprise.CapTokenAccounting) && b.config.EnableTokenUsageLogging()
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
func (b *MMBots) snapshotForEnsure() (botCfgs []llm.BotConfig, activeDBBots dbAgentBotSet, serviceCfgs map[string]llm.ServiceConfig, unchanged bool, err error) {
	b.reconcileTokenUsageSinks()

	botCfgs, activeDBBots, serviceCfgs, err = b.snapshotBotsAndServices()
	if err != nil {
		return nil, dbAgentBotSet{}, nil, false, err
	}
	b.botsLock.RLock()
	botsAlreadyInitialized := len(b.bots) > 0
	lastBotCfgs := b.lastEnsuredBotCfgs
	lastServiceCfgs := b.lastEnsuredServiceCfgs
	forceRefresh := b.forceRefresh
	b.botsLock.RUnlock()

	unchanged = botsAlreadyInitialized && !forceRefresh && botConfigsEqual(lastBotCfgs, botCfgs) && serviceConfigsEqual(lastServiceCfgs, serviceCfgs)
	return botCfgs, activeDBBots, serviceCfgs, unchanged, nil
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
	b.ensureMu.Lock()
	defer b.ensureMu.Unlock()

	// Re-check after acquiring lock - another node may have already handled this
	currentBotCfgs, activeDBBots, currentServiceCfgs, unchanged, err := b.snapshotForEnsure()
	if err != nil {
		return err
	}
	if unchanged {
		b.pluginAPI.Log.Debug("EnsureBots: skipping after lock - bot/service configuration unchanged")
		return nil
	}

	previousMMBots, err := b.pluginAPI.Bot.List(0, 1000, pluginapi.BotOwner(botOwnerID), pluginapi.BotIncludeDeleted())
	if err != nil {
		return fmt.Errorf("failed to list bots: %w", err)
	}
	botCfgs := currentBotCfgs

	var bots []*Bot
	aiBotsByUsername := make(map[string]*Bot)
	// Services as configured, before an agent's model override is folded into
	// its copy. Agents share the provider client built from these.
	servicesByID := make(map[string]llm.ServiceConfig)
	for _, botCfg := range botCfgs {
		bot, service, ok := b.newUnsyncedBot(botCfg)
		if !ok {
			continue
		}

		if _, ok := aiBotsByUsername[botCfg.Name]; ok {
			// Duplicate bot names have to be fatal because they would cause a bot to be modified inappropreately.
			return fmt.Errorf("duplicate bot name: %s", botCfg.Name)
		}
		servicesByID[service.ID] = service

		bots = append(bots, bot)
		aiBotsByUsername[botCfg.Name] = bot
	}

	matchedMMBots := matchPreviousMMBots(bots, previousMMBots)
	inUseMMBotUserIDs := make(map[string]struct{}, len(matchedMMBots))
	for _, mmBot := range matchedMMBots {
		inUseMMBotUserIDs[mmBot.UserId] = struct{}{}
	}

	// For each of the bots we found, if it's not in the configuration, delete it.
	for _, bot := range previousMMBots {
		if _, ok := inUseMMBotUserIDs[bot.UserId]; ok {
			continue
		}
		if activeDBBots.owns(bot) {
			b.pluginAPI.Log.Debug("EnsureBots: skipping deactivation for active DB agent not in ensure set (missing or invalid service)", "bot_name", bot.Username)
			continue
		}
		if _, deactivateErr := b.pluginAPI.Bot.UpdateActive(bot.UserId, false); deactivateErr != nil {
			b.pluginAPI.Log.Error("Failed to delete bot", "bot_name", bot.Username, "error", deactivateErr.Error())
			continue
		}
	}

	// For each bot in the configuration, update its existing Mattermost bot to match,
	// including the username when the agent was renamed. Otherwise, create a new bot.
	// Bots whose account could not be synced are left out of the registry, and
	// the next EnsureBots retries them.
	ensuredBots := make([]*Bot, 0, len(bots))
	accountSyncFailed := false
	for _, bot := range bots {
		if err := b.syncBot(bot, matchedMMBots[bot], servicesByID[bot.service.ID]); err != nil {
			if errors.Is(err, errBotAccountSync) {
				accountSyncFailed = true
				continue
			}
			return err
		}
		ensuredBots = append(ensuredBots, bot)
	}

	b.botsLock.Lock()
	b.bots = ensuredBots
	if accountSyncFailed {
		b.lastEnsuredBotCfgs = nil
		b.lastEnsuredServiceCfgs = nil
		b.forceRefresh = true
		b.botsLock.Unlock()
		return nil
	}
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

// newUnsyncedBot validates botCfg and returns its bot, with the agent's model
// override folded into the bot's service copy, plus the service as configured.
// ok is false, after logging why, when the bot cannot be served.
func (b *MMBots) newUnsyncedBot(botCfg llm.BotConfig) (*Bot, llm.ServiceConfig, bool) {
	if !botCfg.IsValid() {
		b.pluginAPI.Log.Error("Configured bot is not valid", "bot_name", botCfg.Name, "bot_display_name", botCfg.DisplayName)
		return nil, llm.ServiceConfig{}, false
	}

	configured, ok := b.config.GetServiceByID(botCfg.ServiceID)
	if !ok {
		b.pluginAPI.Log.Error("Bot references non-existent service", "bot_name", botCfg.Name, "service_id", botCfg.ServiceID)
		return nil, llm.ServiceConfig{}, false
	}

	if !llm.IsValidService(configured) {
		b.pluginAPI.Log.Error("Bot references invalid service", "bot_name", botCfg.Name, "service_id", botCfg.ServiceID, "service_type", configured.Type)
		return nil, llm.ServiceConfig{}, false
	}

	service := configured
	if botCfg.Model != "" {
		service.DefaultModel = botCfg.Model
	}
	return &Bot{cfg: botCfg, service: service}, configured, true
}

// errBotAccountSync marks a syncBot failure to bring the bot's Mattermost
// account in line with its config, as opposed to a failure to build its LLM.
var errBotAccountSync = errors.New("bot account sync failed")

// syncBot updates existing, the bot's Mattermost account, to match its config,
// including the username when the agent was renamed, or creates an account
// when existing is nil. It then builds the bot's LLM from configuredService,
// the bot's service as configured. bot must not be in the registry yet.
func (b *MMBots) syncBot(bot *Bot, existing *model.Bot, configuredService llm.ServiceConfig) error {
	description := poweredByDescription(bot.service.Type, bot.service.DefaultModel)
	if existing != nil {
		patch := &model.BotPatch{
			DisplayName: &bot.cfg.DisplayName,
			Description: &description,
		}
		if existing.Username != bot.cfg.Name {
			patch.Username = &bot.cfg.Name
		}
		var patchErr error
		bot.mmBot, patchErr = b.pluginAPI.Bot.Patch(existing.UserId, patch)
		if patchErr != nil {
			b.pluginAPI.Log.Error("Failed to patch bot", "bot_name", bot.cfg.Name, "error", patchErr.Error())
			return fmt.Errorf("%w: %s: %w", errBotAccountSync, bot.cfg.Name, patchErr)
		}
		if _, activeErr := b.pluginAPI.Bot.UpdateActive(existing.UserId, true); activeErr != nil {
			b.pluginAPI.Log.Error("Failed to update bot active", "bot_name", bot.cfg.Name, "error", activeErr.Error())
			return fmt.Errorf("%w: %s: %w", errBotAccountSync, bot.cfg.Name, activeErr)
		}
	} else {
		bot.mmBot = &model.Bot{
			Username:    bot.cfg.Name,
			DisplayName: bot.cfg.DisplayName,
			Description: description,
		}
		if createErr := b.pluginAPI.Bot.Create(bot.mmBot); createErr != nil {
			b.pluginAPI.Log.Error("Failed to ensure bot", "bot_name", bot.cfg.Name, "error", createErr.Error())
			return fmt.Errorf("%w: %s: %w", errBotAccountSync, bot.cfg.Name, createErr)
		}
	}

	b.ensureDefaultProfileImage(bot)

	// Fallback chains are available at Enterprise Advanced.
	var fallbackServices []llm.ServiceConfig
	if b.licenseChecker.Allows(enterprise.CapModelFallback) {
		var err error
		fallbackServices, err = llm.ResolveFallbackChain(bot.service.ID, b.config.GetServiceByID)
		if err != nil {
			return fmt.Errorf("failed to resolve fallback chain for bot %s: %w", bot.cfg.Name, err)
		}
	}

	var err error
	bot.llm, bot.providerServices, err = b.getLLM(configuredService, bot.cfg, fallbackServices)
	return err
}

// RefreshAgent rebuilds the registry entry of one DB-backed agent from the
// store and swaps in just that entry, so a renamed agent is listed under its
// new name and account without waiting for a full rebuild. When the agent
// cannot be rebuilt, or is no longer active, its entry is removed instead.
// Name-based lookups do not depend on it having run: they check the live
// account. It never creates bot accounts (EnsureBots does, under the cluster
// mutex), and it leaves the next EnsureBots to reconcile every other bot.
func (b *MMBots) RefreshAgent(agentID string) error {
	if agentID == "" {
		return errors.New("refresh agent: empty agent ID")
	}

	b.ensureMu.Lock()
	defer b.ensureMu.Unlock()

	bot, err := b.rebuildAgentBot(agentID)
	b.replaceAgentBot(agentID, bot)
	return err
}

// rebuildAgentBot returns agentID's freshly synced bot, or nil when the agent
// is not an active DB-backed agent.
func (b *MMBots) rebuildAgentBot(agentID string) (*Bot, error) {
	if b.config == nil {
		return nil, fmt.Errorf("refresh agent %s: bots are not configured", agentID)
	}

	botCfgs, activeDBBots, _, err := b.snapshotBotsAndServices()
	if err != nil {
		return nil, fmt.Errorf("refresh agent %s: %w", agentID, err)
	}
	idx := slices.IndexFunc(botCfgs, func(cfg llm.BotConfig) bool {
		if cfg.ID != agentID || cfg.BotUserID == "" {
			return false
		}
		_, isDB := activeDBBots.userIDs[cfg.BotUserID]
		return isDB
	})
	if idx < 0 {
		return nil, nil
	}
	botCfg := botCfgs[idx]

	bot, configuredService, ok := b.newUnsyncedBot(botCfg)
	if !ok {
		return nil, fmt.Errorf("refresh agent %s: agent cannot be served with its current configuration", agentID)
	}
	if b.findBot(func(other *Bot) bool {
		return other.cfg.ID != agentID && other.cfg.Name == botCfg.Name
	}) != nil {
		return nil, fmt.Errorf("refresh agent %s: duplicate bot name: %s", agentID, botCfg.Name)
	}

	account, err := b.pluginAPI.Bot.Get(botCfg.BotUserID, true)
	if err != nil {
		return nil, fmt.Errorf("refresh agent %s: failed to get bot account: %w", agentID, err)
	}
	if account.OwnerId != botOwnerID {
		return nil, fmt.Errorf("refresh agent %s: bot account is not owned by this plugin", agentID)
	}

	if err := b.syncBot(bot, account, configuredService); err != nil {
		return nil, fmt.Errorf("refresh agent %s: %w", agentID, err)
	}
	return bot, nil
}

// replaceAgentBot swaps in a new registry where agentID's entry is bot, or is
// absent when bot is nil.
func (b *MMBots) replaceAgentBot(agentID string, bot *Bot) {
	b.botsLock.Lock()
	defer b.botsLock.Unlock()

	next := make([]*Bot, 0, len(b.bots)+1)
	for _, existing := range b.bots {
		if existing.cfg.ID != agentID {
			next = append(next, existing)
			continue
		}
		if bot != nil {
			next = append(next, bot)
			bot = nil
		}
	}
	if bot != nil {
		next = append(next, bot)
	}
	b.bots = next
	b.forceRefresh = true
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

// getLLM returns an agent's wrapped model plus provider services resolved from
// the unwrapped model. Wrappers expose only LanguageModel, so capabilities not
// captured here cannot be recovered later.
//
// serviceConfig must be the service as configured, without the agent's model
// override: agents on the same service share the provider client cached for it.
func (b *MMBots) getLLM(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig, fallbackServices []llm.ServiceConfig) (llm.LanguageModel, *llm.ProviderServices, error) {
	entry, err := b.leaseServiceLLM(serviceConfig, fallbackServices)
	if err != nil {
		return nil, nil, err
	}
	defer entry.inUse.Done()

	base, providerServices := entry.client.model, &llm.ProviderServices{}
	if serviceClient := entry.client.bifrost; serviceClient != nil {
		agentLLM, err := serviceClient.ForAgent(botConfig)
		if err != nil {
			return nil, nil, err
		}
		base, providerServices = agentLLM, agentLLM.ProviderServices()
	}
	return b.wrapLLM(base, serviceConfig, &botConfig, fallbackServices), providerServices, nil
}

// wrapLLM builds the wrapper chain shared by agent LLMs and service LLMs.
// botConfig is nil for direct service calls, which have no agent.
func (b *MMBots) wrapLLM(base llm.LanguageModel, serviceConfig llm.ServiceConfig, botConfig *llm.BotConfig, fallbackServices []llm.ServiceConfig) llm.LanguageModel {
	// Truncation Support
	var result llm.LanguageModel = llm.NewLLMTruncationWrapper(base)

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

	// Outermost so the per-user fallback prefix is resolved once per request
	// and flows down through truncation's repeated CountTokens calls. Agent
	// calls only: a direct service call carries user_id for attribution, not
	// as a principal, so its fallback chain is never trimmed per user.
	if botConfig != nil {
		result = newFallbackAccessLLM(result, b, serviceConfig.ID)
	}

	return result
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

// providerClient is the provider client behind one service and its fallback
// chain. The service LLM registry owns it; direct service calls and every agent
// on the service share it.
type providerClient struct {
	// model is the unwrapped client with no agent settings.
	model llm.LanguageModel
	// bifrost is set when model is a Bifrost client, which agents derive their
	// own settings from. Other clients have no agent settings or provider
	// services, so agents use model as is.
	bifrost *bifrost.ServiceClient
	// shutdown releases the Bifrost worker pool and queue. It is a no-op for
	// the load-test mock.
	shutdown func()
}

func (b *MMBots) newProviderClient(serviceConfig llm.ServiceConfig, fallbackServices []llm.ServiceConfig) (*providerClient, error) {
	if b.baseLLMBuilderForTest != nil {
		model, shutdown, err := b.baseLLMBuilderForTest(serviceConfig, fallbackServices)
		if err != nil {
			return nil, err
		}
		return &providerClient{model: model, shutdown: shutdown}, nil
	}

	if serviceConfig.Type == llm.ServiceTypeLoadTestMock {
		profile, err := loadtest.ParseProfile(serviceConfig.LoadTestMockConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to parse load-test mock profile for service %s: %w", serviceConfig.ID, err)
		}
		if b.pluginAPI != nil {
			// Run-audit snapshot of the active mock profile (once per LLM init; not per request).
			b.pluginAPI.Log.Info(
				"Initialized load-test mock LLM",
				"service_id", serviceConfig.ID,
				"service_name", serviceConfig.Name,
				"profile_summary", profile.Summary(),
			)
		}
		return &providerClient{model: loadtest.NewMockLLM(profile), shutdown: func() {}}, nil
	}

	serviceClient, err := bifrost.NewServiceClient(serviceConfig, fallbackServices)
	if err != nil {
		if b.pluginAPI != nil {
			b.pluginAPI.Log.Error("Failed to create Bifrost client for service", "service_id", serviceConfig.ID, "service_type", serviceConfig.Type)
		}
		return nil, fmt.Errorf("failed to create Bifrost client for %s: %w", serviceConfig.Type, err)
	}
	return &providerClient{model: serviceClient.LLM(), bifrost: serviceClient, shutdown: serviceClient.Shutdown}, nil
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
	return b.GetBotByIDOrUsername(b.config.GetTranscriptBotUserID(), b.config.GetTranscriptGenerator())
}

// GetBotByIDOrUsername resolves a stored or requested agent reference: by its
// bot user ID when one is set, otherwise by the legacy username, which only
// selects the agent whose account currently holds it. A missing agent returns
// nil rather than another agent, which would run with that agent's model,
// instructions, tools, and identity.
func (b *MMBots) GetBotByIDOrUsername(botUserID, legacyUsername string) *Bot {
	if botUserID != "" {
		return b.GetBotByID(botUserID)
	}
	return b.GetBotByUsername(legacyUsername)
}

// UsernameResolver selects the agent whose account currently holds a username.
type UsernameResolver interface {
	GetBotByUsername(username string) *Bot
}

// ResolveAgentUsernames maps each legacy username reference to the bot user
// ID of the agent whose account currently holds it, and returns the usernames
// no agent holds. It reads bot accounts through the Mattermost API, so callers
// must not hold a database transaction open around it.
func ResolveAgentUsernames(resolver UsernameResolver, usernames []string) (resolved map[string]string, unresolved []string) {
	resolved = make(map[string]string, len(usernames))
	for _, username := range usernames {
		if bot := resolver.GetBotByUsername(username); bot != nil {
			resolved[username] = bot.BotUserID()
		} else {
			unresolved = append(unresolved, username)
		}
	}
	return resolved, unresolved
}

// accountHoldsUsername reports whether bot's Mattermost account currently
// holds username. The registry's cached names can lag a rename, which releases
// the old username at once, so name-based lookups must not trust them alone.
// The account is read by its user ID, which the server caches and invalidates
// cluster-wide on every bot patch; a by-username read is uncached and can
// return the name's previous holder during replica lag.
func (b *MMBots) accountHoldsUsername(bot *Bot, username string) bool {
	if b.pluginAPI == nil || bot == nil || bot.mmBot == nil || username == "" {
		return false
	}
	user, err := b.pluginAPI.User.Get(bot.mmBot.UserId)
	if err != nil {
		b.pluginAPI.Log.Warn("Failed to get bot account to verify its username", "bot_user_id", bot.mmBot.UserId, "error", err.Error())
		return false
	}
	return strings.EqualFold(user.Username, username)
}

// GetBotByUsername retrieves the bot whose Mattermost account currently holds
// the given username. A name the registry does not know is resolved through
// the bot account that holds it, so an agent stays reachable under its new
// name while the registry still lists it under the old one after a rename.
func (b *MMBots) GetBotByUsername(botUsername string) *Bot {
	if botUsername == "" {
		return nil
	}
	for _, bot := range b.GetAllBots() {
		if bot.mmBot == nil {
			continue
		}
		if !strings.EqualFold(bot.mmBot.Username, botUsername) && !strings.EqualFold(bot.cfg.Name, botUsername) {
			continue
		}
		if b.accountHoldsUsername(bot, botUsername) {
			return bot
		}
	}
	if b.pluginAPI == nil {
		return nil
	}
	user, err := b.pluginAPI.User.GetByUsername(botUsername)
	if err != nil || !user.IsBot {
		return nil
	}
	if bot := b.GetBotByID(user.Id); b.accountHoldsUsername(bot, botUsername) {
		return bot
	}
	return nil
}

// GetBotByUsernameOrFirst retrieves the bot with the given username, or the
// first bot when no username is given. A named bot that is not available
// returns nil: serving another agent instead would run the request with that
// agent's model, instructions, tools, and identity.
func (b *MMBots) GetBotByUsernameOrFirst(botUsername string) *Bot {
	if botUsername != "" {
		return b.GetBotByUsername(botUsername)
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

// GetBotMentioned returns the bot mentioned in the text, if any. A bot is
// mentioned only under a username its Mattermost account currently holds.
func (b *MMBots) GetBotMentioned(text string) *Bot {
	for _, bot := range b.GetAllBots() {
		if bot.mmBot == nil {
			continue
		}
		if userIsMentionedMarkdown(text, bot.mmBot.Username) && b.accountHoldsUsername(bot, bot.mmBot.Username) {
			return bot
		}
	}
	return nil
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
