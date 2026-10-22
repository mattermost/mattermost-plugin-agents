// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"go.opentelemetry.io/otel/trace"
)

// ToolProvider provides built-in tools for a bot and context
type ToolProvider interface {
	GetTools(bot *bots.Bot, llmContext *llm.Context) []llm.Tool
}

// MCPToolProvider provides MCP tools for a user or service-account agent.
// selection identifies which servers the request may reach, so ineligible
// servers are never connected rather than connected and then filtered out.
type MCPToolProvider interface {
	GetToolsWithSelection(ctx stdcontext.Context, req mcp.CatalogRequest, selection mcp.ToolSelection) ([]llm.Tool, *mcp.Errors)
}

type MCPToolRetrievalOverrideProvider interface {
	GetToolRetrievalOverrides() map[string]mcp.ToolRetrievalOverride
}

// MCPServerDescriber describes configured MCP servers beyond the tools a
// catalog request returned, so the model can ask the requester to connect one.
type MCPServerDescriber interface {
	DescribeUnconnectedMCPServers(authErrors []llm.ToolAuthError) []llm.UnconnectedMCPServer
	MCPServerOriginsByName() map[string]string
}

var _ MCPServerDescriber = (*mcp.ClientManager)(nil)

// ScopedSessionMinter mints the per-run scoped sessions scoped channel turns
// connect with.
type ScopedSessionMinter interface {
	MintScopedSession(req mcp.ScopedSessionRequest) (*mcp.ScopedRun, error)
}

var _ ScopedSessionMinter = (*mcp.ClientManager)(nil)

// channelToolCallingConfig is the admin toggle for tool calling in channels.
type channelToolCallingConfig interface {
	EnableChannelMentionToolCalling() bool
}

// ConfigProvider provides configuration access
type ConfigProvider interface {
	GetServiceByID(id string) (llm.ServiceConfig, bool)
}

// Builder builds contexts for LLM requests
type Builder struct {
	pluginAPI       *pluginapi.Client
	toolProvider    ToolProvider
	mcpToolProvider MCPToolProvider
	configProvider  ConfigProvider
	licenseChecker  *enterprise.LicenseChecker

	mcpDynamicToolTelemetry llm.MCPDynamicToolTelemetry
}

// NewLLMContextBuilder creates a new LLM context builder
func NewLLMContextBuilder(
	pluginAPI *pluginapi.Client,
	toolProvider ToolProvider,
	mcpToolProvider MCPToolProvider,
	configProvider ConfigProvider,
) *Builder {
	return &Builder{
		pluginAPI:       pluginAPI,
		toolProvider:    toolProvider,
		mcpToolProvider: mcpToolProvider,
		configProvider:  configProvider,
		licenseChecker:  enterprise.NewLicenseChecker(pluginAPI),
	}
}

func (b *Builder) SetMCPDynamicToolTelemetry(telemetry llm.MCPDynamicToolTelemetry) {
	b.mcpDynamicToolTelemetry = telemetry
}

// BuildLLMContextUserRequest is a helper function to collect the required context for a user request.
func (b *Builder) BuildLLMContextUserRequest(bot *bots.Bot, requestingUser *model.User, channel *model.Channel, opts ...llm.ContextOption) *llm.Context {
	allOpts := []llm.ContextOption{
		b.WithLLMContextServerInfo(),
		b.WithLLMContextRequestingUser(requestingUser),
		b.WithLLMContextChannel(channel),
		b.WithLLMContextBot(bot),
	}
	allOpts = append(allOpts, opts...)

	return llm.NewContext(allOpts...)
}

func (b *Builder) WithLLMContextServerInfo() llm.ContextOption {
	return func(c *llm.Context) {
		if b.pluginAPI.Configuration.GetConfig().TeamSettings.SiteName != nil {
			c.ServerName = *b.pluginAPI.Configuration.GetConfig().TeamSettings.SiteName
		}

		if b.pluginAPI.Configuration.GetConfig().ServiceSettings.SiteURL != nil {
			c.SiteURL = *b.pluginAPI.Configuration.GetConfig().ServiceSettings.SiteURL
		}

		if license := b.pluginAPI.System.GetLicense(); license != nil && license.Customer != nil {
			c.CompanyName = license.Customer.Company
		}
	}
}

func (b *Builder) WithLLMContextChannel(channel *model.Channel) llm.ContextOption {
	return func(c *llm.Context) {
		c.Channel = channel
		if channel == nil || (channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup) {
			return
		}

		team, err := b.pluginAPI.Team.Get(channel.TeamId)
		if err != nil {
			b.pluginAPI.Log.Error("Unable to get team for context", "error", err.Error(), "team_id", channel.TeamId)
			return
		}

		c.Team = team
	}
}

func (b *Builder) WithLLMContextRequestingUser(user *model.User) llm.ContextOption {
	return func(c *llm.Context) {
		if user != nil {
			// Create a shallow copy to avoid mutating the original user object,
			// then sanitize profile fields that are rendered into the system prompt.
			sanitizedUser := *user
			sanitizedUser.FirstName = sanitizeUserProfileField(user.FirstName)
			sanitizedUser.LastName = sanitizeUserProfileField(user.LastName)
			sanitizedUser.Position = sanitizeUserProfileField(user.Position)
			sanitizedUser.Nickname = sanitizeUserProfileField(user.Nickname)
			c.RequestingUser = &sanitizedUser

			tz := user.GetPreferredTimezone()
			loc, err := time.LoadLocation(tz)
			if err == nil && loc != nil {
				c.Time = time.Now().In(loc).Format(time.RFC1123)
			}
		}
	}
}

// toolAuthErrorMatchesAllowlist reports whether authErr refers to a server that still
// appears in the per-agent MCP allowlist (by ServerOrigin).
func toolAuthErrorMatchesAllowlist(authErr llm.ToolAuthError, allowlist []llm.EnabledMCPTool) bool {
	errOrigin := llm.NormalizeMCPServerOrigin(authErr.ServerOrigin)
	return slices.ContainsFunc(allowlist, func(enabled llm.EnabledMCPTool) bool {
		return llm.NormalizeMCPServerOrigin(enabled.ServerOrigin) == errOrigin
	})
}

func filterToolAuthErrorsForAllowlist(errors []llm.ToolAuthError, allowlist []llm.EnabledMCPTool) []llm.ToolAuthError {
	return slices.DeleteFunc(slices.Clone(errors), func(e llm.ToolAuthError) bool {
		return !toolAuthErrorMatchesAllowlist(e, allowlist)
	})
}

// mcpToolSelection maps this request's per-agent, per-user, and license rules
// onto the set of MCP servers that may be contacted, so a disallowed server is
// never connected rather than connected and then filtered out. The license
// gate is enforced only here; the agent allowlist and user-disabled origins
// are additionally narrowed per-tool by the filters that follow.
func mcpToolSelection(botCfg llm.BotConfig, c *llm.Context, remoteMCPLicensed bool) mcp.ToolSelection {
	selection := mcp.ToolSelection{
		DeniedOrigins:        c.ToolCatalog.DisabledMCPServerOrigins,
		ExcludeRemoteServers: !remoteMCPLicensed,
	}

	if !botCfg.AutoEnableNewMCPTools {
		// Deliberately non-nil even when the agent allowlists nothing: a nil
		// allowlist would select every server instead of none.
		selection.AllowedOrigins = make([]string, 0, len(botCfg.EnabledMCPTools))
		for _, tool := range botCfg.EnabledMCPTools {
			selection.AllowedOrigins = append(selection.AllowedOrigins, tool.ServerOrigin)
		}
	}

	return selection
}

func (b *Builder) WithLLMContextDisabledMCPServers(origins []string) llm.ContextOption {
	return func(c *llm.Context) {
		normalized := llm.NormalizeMCPServerOrigins(origins)
		if len(normalized) == 0 {
			return
		}
		c.ToolCatalog.DisabledMCPServerOrigins = normalized
	}
}

func (b *Builder) WithLLMContextMCPToolFilter(keep func(llm.Tool) bool) llm.ContextOption {
	return func(c *llm.Context) {
		if keep == nil {
			return
		}
		c.ToolCatalog.KeepMCPTool = keep
	}
}

// WithLLMContextPreloadedMCPTools requests exact-or-bare MCP tools for internal
// predefined flows. The selected tools are still constrained by the normal
// authorized MCP catalog and must be configured before default tools are built.
func (b *Builder) WithLLMContextPreloadedMCPTools(tools []llm.EnabledMCPTool) llm.ContextOption {
	return func(c *llm.Context) {
		c.ToolCatalog.PreloadedMCPTools = slices.Clone(tools)
	}
}

// WithLLMContextInteractive marks the requesting user as interactively present
// and able to answer pending tool approvals. User-interaction tools
// (llm.Tool.UserInteraction) are only cataloged when this is applied, so it
// must be configured before default tools are built. Automated invokers
// (bots, webhooks, bridge API, evals) must never set this.
func (b *Builder) WithLLMContextInteractive() llm.ContextOption {
	return func(c *llm.Context) {
		c.ToolCatalog.InteractiveUserPresent = true
	}
}

// WithLLMContextResponseFiles marks the context as supporting response-post
// file attachments (ToolRuntime.CreatedFiles are attached to the streamed
// response post at end of turn). Must be configured before default tools
// are built so CreateFile is cataloged.
func (b *Builder) WithLLMContextResponseFiles() llm.ContextOption {
	return func(c *llm.Context) {
		c.ToolCatalog.ResponseFilesSupported = true
	}
}

// sanitizeUserProfileField strips characters that could be used for prompt injection
// in user profile fields rendered into the system prompt. It collapses newlines, carriage
// returns, and tabs to spaces, removes other control characters, and trims the result.
func sanitizeUserProfileField(s string) string {
	var result strings.Builder
	result.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			result.WriteRune(' ')
		case unicode.IsControl(r):
			continue
		default:
			result.WriteRune(r)
		}
	}
	return strings.TrimSpace(result.String())
}

// WithLLMContextSessionID removed: embedded MCP manages its own session lifecycle

// isRemoteMCPLicensed reports whether remote and plugin MCP servers are
// available at the current license level. A nil checker fails closed.
func (b *Builder) isRemoteMCPLicensed() bool {
	return b.licenseChecker.Allows(enterprise.CapRemoteMCP)
}

// UsesServiceAccountCatalog reports whether tool catalogs for this bot are built in
// service-account mode. Service-account catalogs are available at Enterprise and
// above; below that, agents use per-user credentials.
func (b *Builder) UsesServiceAccountCatalog(bot *bots.Bot) bool {
	return bot != nil && bot.GetConfig().UseServiceAccountAuth && b.licenseChecker.Allows(enterprise.CapMCPServiceAccount)
}

// WithLLMContextScopedChannelTurn asks for this channel-triggered turn's tools
// to run on a scoped session for the agent's bot. It only takes effect when
// ScopedChannelModeActive holds for the turn's channel; the caller must then
// call ReleaseScopedSession on the context when the run ends. Must be
// configured before tools are built.
func (b *Builder) WithLLMContextScopedChannelTurn(turn llm.ScopedTurn) llm.ContextOption {
	return func(c *llm.Context) {
		c.ToolCatalog.ScopedTurn = &turn
	}
}

// ScopedChannelModeConfigured reports whether bot has scoped channel mode
// turned on, whether or not its prerequisites currently hold.
func ScopedChannelModeConfigured(bot *bots.Bot) bool {
	return bot != nil && bot.GetConfig().ScopedChannelMode
}

// ScopedChannelModeActive is the one decision for whether bot's turns in
// channel run their tools on a scoped session: the agent setting is on, every
// prerequisite holds right now, and channel is a public or private channel.
// DMs and group messages never run scoped.
func (b *Builder) ScopedChannelModeActive(bot *bots.Bot, channel *model.Channel) bool {
	if !ScopedChannelModeConfigured(bot) || channel == nil {
		return false
	}
	if channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate {
		return false
	}
	cfg := bot.GetConfig()
	return cfg.UseServiceAccountAuth &&
		!cfg.AutoEnableNewMCPTools &&
		b.licenseChecker.Allows(enterprise.CapMCPServiceAccount) &&
		b.channelToolCallingEnabled() &&
		b.ServerSupportsScopedSessions()
}

// ServerSupportsScopedSessions reports whether the Mattermost server can mint
// scoped sessions.
func (b *Builder) ServerSupportsScopedSessions() bool {
	return b != nil && b.pluginAPI != nil && scopedsession.ServerSupports(b.pluginAPI.System.GetServerVersion())
}

func (b *Builder) channelToolCallingEnabled() bool {
	cfg, ok := b.configProvider.(channelToolCallingConfig)
	return ok && cfg.EnableChannelMentionToolCalling()
}

// mintScopedRun mints the turn's scoped session and records the grant. The
// returned run is owned by c: ReleaseScopedSession closes it.
func (b *Builder) mintScopedRun(ctx stdcontext.Context, c *llm.Context, bot *bots.Bot, requesterID string) (*mcp.ScopedRun, error) {
	cfg := bot.GetConfig()
	rec := plugin.MakeAuditRecord(audit.EventAgentScopedSessionGrant, model.AuditStatusFail)
	rec.Actor.UserId = requesterID
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.HasTraceID() {
		rec.AddMeta(audit.MetaTraceID, sc.TraceID().String())
	}
	audit.AddParam(rec, audit.KeyAgentID, bot.BotUserID())
	audit.AddParam(rec, audit.KeyChannelID, c.Channel.Id)
	audit.AddParam(rec, audit.KeyPostID, c.ToolCatalog.ScopedTurn.TriggerPostID)
	audit.AddParam(rec, audit.KeyThreadRootPostID, c.ToolCatalog.ScopedTurn.ThreadRootID)
	audit.AddParam(rec, audit.KeyConversationID, c.ToolCatalog.ScopedTurn.ConversationID)
	audit.AddParam(rec, audit.KeyScopeExtraChannelCount, len(cfg.ScopedExtraChannelIDs))
	audit.AddParam(rec, audit.KeyScopeOpenTeamPublicChannels, true)
	defer b.pluginAPI.Audit.Record(rec)

	minter, ok := b.mcpToolProvider.(ScopedSessionMinter)
	if !ok {
		return nil, mcp.ErrScopedSessionsUnavailable
	}
	run, err := minter.MintScopedSession(mcp.ScopedSessionRequest{
		BotUserID:       bot.BotUserID(),
		RequesterID:     requesterID,
		ChannelID:       c.Channel.Id,
		ExtraChannelIDs: cfg.ScopedExtraChannelIDs,
	})
	if err != nil {
		return nil, err
	}
	rec.Success()
	c.SetScopedSessionRelease(func() { _ = run.Close() })
	return run, nil
}

// getToolsStoreForUser returns a tool store for a specific user, including MCP tools.
func (b *Builder) getToolsStoreForUser(ctx stdcontext.Context, c *llm.Context, bot *bots.Bot, userID string, forceConcrete bool) *llm.ToolStore {
	// Check for nil bot, which is unexpected
	if bot == nil {
		b.pluginAPI.Log.Error("Unexpected nil bot when getting tool store for user", "userID", userID)
		return llm.NewToolStore()
	}

	// Check for empty userID, which is unexpected
	if userID == "" {
		b.pluginAPI.Log.Error("Unexpected empty userID when getting tool store for user")
		return llm.NewToolStore()
	}

	// useServiceAccount is only true when CapMCPServiceAccount is available,
	// which is at the same level as CapRemoteMCP, so the selection below never
	// excludes remote service-account servers.
	remoteMCPLicensed := b.isRemoteMCPLicensed()
	useServiceAccount := b.UsesServiceAccountCatalog(bot)

	// Check if tools are disabled for this bot
	if bot.GetConfig().DisableTools {
		return llm.NewToolStore()
	}

	// Create a tool store that requires user approval for tool calls
	store := llm.NewToolStore()
	botCfg := bot.GetConfig()

	// Add built-in tools (always add for LLM awareness; execution controlled via WithToolsDisabled)
	store.AddTools(b.toolProvider.GetTools(bot, c))

	var mcpTools []llm.Tool
	var mcpErrors *mcp.Errors

	// Fetch MCP tools if available. They are filtered before either full-schema
	// insertion or strict private-registry construction.
	if b.mcpToolProvider != nil {
		if ctx == nil {
			b.pluginAPI.Log.Error("Cannot add MCP tools to context: request context is nil", "userID", userID)
			return store
		}

		var scopedRun *mcp.ScopedRun
		if c.ToolCatalog.ScopedTurn != nil && b.ScopedChannelModeActive(bot, c.Channel) {
			run, mintErr := b.mintScopedRun(ctx, c, bot, userID)
			if mintErr != nil {
				// Fail closed: never fall back to the requester's credentials.
				b.pluginAPI.Log.Warn("Scoped session unavailable; this turn has no MCP tools",
					"bot_id", bot.BotUserID(), "channel_id", c.Channel.Id, "error", mintErr.Error())
				return store
			}
			scopedRun = run
			// The caller only learns about the run once the context is built,
			// so a panic while building the catalog must end the run here.
			defer func() {
				if r := recover(); r != nil {
					c.ReleaseScopedSession()
					panic(r)
				}
			}()
		}

		// Build the catalog for the appropriate auth mode. GetTools validates
		// the request fail-closed (for example, an agent with no bot user
		// yields an error, not tools).
		req := mcp.UserCatalogRequest(userID)
		switch {
		case scopedRun != nil:
			c.ToolAuthMode = llm.ToolAuthModeServiceAccount
			req = mcp.ScopedCatalogRequest(scopedRun, userID)
		case useServiceAccount:
			c.ToolAuthMode = llm.ToolAuthModeServiceAccount
			req = mcp.ServiceAccountCatalogRequest(bot.BotUserID(), userID)
		}

		// Resolve server-level eligibility before connecting. The per-tool
		// filters below remain as defense in depth for allowlist and context
		// rules after the provider returns its selected catalog.
		selection := mcpToolSelection(botCfg, c, remoteMCPLicensed)
		mcpTools, mcpErrors = b.mcpToolProvider.GetToolsWithSelection(ctx, req, selection)

		// Per-agent MCP tool filtering: unless the agent is configured to pick up
		// every MCP tool automatically, retain only tools listed in its allowlist.
		// This runs AFTER admin policy (filterToolsByConfig inside the MCP
		// provider) and BEFORE per-user/channel filtering and strict registry
		// construction.
		if !botCfg.AutoEnableNewMCPTools {
			mcpTools = llm.FilterMCPToolsByEnabledAllowlist(mcpTools, botCfg.EnabledMCPTools)
		}
		mcpTools = filterMCPToolsByDisabledOrigins(mcpTools, c.ToolCatalog.DisabledMCPServerOrigins)
		mcpTools = filterMCPToolsByPredicate(mcpTools, c.ToolCatalog.KeepMCPTool)
		if scopedRun != nil {
			mcpTools = filterMCPToolsByPredicate(mcpTools, mcp.ScopedCatalogAllows)
			c.ToolsScoped = true
		}

		if mcpErrors != nil {
			authErrors := mcpErrors.ToolAuthErrors
			if !botCfg.AutoEnableNewMCPTools {
				authErrors = filterToolAuthErrorsForAllowlist(mcpErrors.ToolAuthErrors, botCfg.EnabledMCPTools)
			}
			for _, authError := range authErrors {
				store.AddAuthError(authError)
			}
		}

		// Someone has to click Connect, and service-account catalogs never
		// use the requester's own OAuth grants.
		if c.ToolCatalog.InteractiveUserPresent && !useServiceAccount {
			b.offerMCPConnections(store, c, botCfg)
		}
	}

	if botCfg.MCPDynamicToolLoading && !forceConcrete {
		b.buildStrictMCPToolStore(store, mcpTools, c, b.strictRegistryOptions()...)
		return store
	}

	c.ObserveMCPDynamicToolEvent("flag_off", "disabled")
	b.logDebug("MCP dynamic tool loading disabled for bot", "bot_name", botCfg.Name, "bot_id", botCfg.ID)

	if len(mcpTools) > 0 {
		store.AddTools(mcpTools)
	}
	b.preloadMCPTools(store, mcpTools, c.ToolCatalog.PreloadedMCPTools)

	return store
}

// offerMCPConnections catalogs the connection request tool for the servers in
// store's auth errors, with everything known about them for the prompt.
func (b *Builder) offerMCPConnections(store *llm.ToolStore, c *llm.Context, botCfg llm.BotConfig) {
	describer, ok := b.mcpToolProvider.(MCPServerDescriber)
	if !ok {
		return
	}
	// Set even when every server is connected: an answered request resolves
	// against a context rebuilt after the user connected, and a server can
	// still lapse mid-turn (MCPToolErrorResult).
	c.ToolCatalog.MCPServerOrigins = describer.MCPServerOriginsByName()

	authErrors := store.GetAuthErrors()
	if len(authErrors) == 0 {
		return
	}
	servers := describer.DescribeUnconnectedMCPServers(authErrors)
	if len(servers) == 0 {
		return
	}
	if !botCfg.AutoEnableNewMCPTools {
		for i := range servers {
			servers[i].Tools = filterToolInfosForAllowlist(servers[i].Tools, botCfg.EnabledMCPTools)
		}
	}

	c.UnconnectedMCPServers = servers
	store.AddTools([]llm.Tool{mcp.NewRequestConnectionTool(servers)})
}

// filterToolInfosForAllowlist keeps the bare-named tools the agent's allowlist
// would let it use once the server is connected.
func filterToolInfosForAllowlist(tools []llm.ToolInfo, allowlist []llm.EnabledMCPTool) []llm.ToolInfo {
	return slices.DeleteFunc(tools, func(tool llm.ToolInfo) bool {
		origin := llm.NormalizeMCPServerOrigin(tool.ServerOrigin)
		return !slices.ContainsFunc(allowlist, func(enabled llm.EnabledMCPTool) bool {
			if llm.NormalizeMCPServerOrigin(enabled.ServerOrigin) != origin {
				return false
			}
			return enabled.ToolName == llm.MCPServerToolWildcard || llm.BareMCPToolName(enabled.ToolName) == tool.Name
		})
	})
}

func (b *Builder) buildStrictMCPToolStore(store *llm.ToolStore, mcpTools []llm.Tool, c *llm.Context, registryOpts ...mcp.ToolRegistryOption) {
	if c == nil {
		return
	}
	if len(mcpTools) == 0 {
		// No authorized MCP catalog (policy denial or nothing configured):
		// omit the meta-tools rather than expose them over an empty registry.
		c.ObserveMCPDynamicToolEvent("catalog_empty", "meta_tools_omitted")
		return
	}
	registry := mcp.NewToolRegistry(mcpTools, registryOpts...)

	b.preloadMCPTools(store, mcpTools, c.ToolCatalog.PreloadedMCPTools)
	markUnloadedMCPTools(store, mcpTools)
	store.AddTools(mcp.NewMetaTools(registry))
}

func (b *Builder) preloadMCPTools(store *llm.ToolStore, available []llm.Tool, specs []llm.EnabledMCPTool) {
	if store == nil || len(available) == 0 || len(specs) == 0 {
		return
	}

	for _, spec := range specs {
		specOrigin := llm.NormalizeMCPServerOrigin(spec.ServerOrigin)
		var matches []llm.Tool
		for _, tool := range available {
			if specOrigin != llm.NormalizeMCPServerOrigin(tool.ServerOrigin) {
				continue
			}
			if llm.MCPToolNameMatches(tool.Name, spec.ToolName) {
				matches = append(matches, tool)
			}
		}

		switch len(matches) {
		case 0:
			continue
		case 1:
			tool := matches[0]
			if llm.IsBareMCPToolName(spec.ToolName) && tool.Name != spec.ToolName {
				tool.Name = spec.ToolName
			}
			store.AddTools([]llm.Tool{tool})
		default:
			b.logWarn("Skipping ambiguous preloaded MCP tool selector",
				"server_origin", spec.ServerOrigin,
				"tool_name", spec.ToolName,
				"match_count", len(matches),
			)
		}
	}
}

func (b *Builder) strictRegistryOptions() []mcp.ToolRegistryOption {
	if b == nil || b.mcpToolProvider == nil {
		return nil
	}

	provider, ok := b.mcpToolProvider.(MCPToolRetrievalOverrideProvider)
	if !ok {
		return nil
	}

	overrides := provider.GetToolRetrievalOverrides()
	if len(overrides) == 0 {
		return nil
	}

	return []mcp.ToolRegistryOption{mcp.WithToolRetrievalOverrides(overrides)}
}

func markUnloadedMCPTools(publicStore *llm.ToolStore, mcpTools []llm.Tool) {
	if publicStore == nil {
		return
	}

	// Index the visible store once: tools may be registered under either their
	// fully namespaced name or their bare name, so look up by both keyed on
	// (ServerOrigin, name) to avoid an O(N*M) scan per MCP tool.
	visible := publicStore.GetTools()
	type originKey struct {
		origin string
		name   string
	}
	visibleKeys := make(map[originKey]struct{}, len(visible)*2)
	for _, t := range visible {
		visibleKeys[originKey{t.ServerOrigin, t.Name}] = struct{}{}
		visibleKeys[originKey{t.ServerOrigin, llm.BareMCPToolName(t.Name)}] = struct{}{}
	}

	unloaded := make([]llm.Tool, 0, len(mcpTools))
	for _, tool := range mcpTools {
		if _, ok := visibleKeys[originKey{tool.ServerOrigin, tool.Name}]; ok {
			continue
		}
		if _, ok := visibleKeys[originKey{tool.ServerOrigin, llm.BareMCPToolName(tool.Name)}]; ok {
			continue
		}
		unloaded = append(unloaded, tool)
	}
	publicStore.SetUnloadedMCPTools(unloaded)
}

func (b *Builder) logWarn(message string, keyValuePairs ...any) {
	if b != nil && b.pluginAPI != nil {
		b.pluginAPI.Log.Warn(message, keyValuePairs...)
	}
}

func (b *Builder) logDebug(message string, keyValuePairs ...any) {
	if b != nil && b.pluginAPI != nil {
		b.pluginAPI.Log.Debug(message, keyValuePairs...)
	}
}

func filterMCPToolsByDisabledOrigins(tools []llm.Tool, disabled []string) []llm.Tool {
	if len(tools) == 0 || len(disabled) == 0 {
		return tools
	}

	normalizedDisabled := llm.NormalizeMCPServerOrigins(disabled)
	if len(normalizedDisabled) == 0 {
		return tools
	}

	disabledSet := make(map[string]bool, len(normalizedDisabled))
	for _, origin := range normalizedDisabled {
		disabledSet[origin] = true
	}

	return slices.DeleteFunc(slices.Clone(tools), func(tool llm.Tool) bool {
		return disabledSet[llm.NormalizeMCPServerOrigin(tool.ServerOrigin)]
	})
}

func filterMCPToolsByPredicate(tools []llm.Tool, keep func(llm.Tool) bool) []llm.Tool {
	if len(tools) == 0 || keep == nil {
		return tools
	}

	return slices.DeleteFunc(slices.Clone(tools), func(tool llm.Tool) bool {
		return !keep(tool)
	})
}

// WithLLMContextTools adds tools to the LLM context the requester can access.
// Tools are always added for LLM awareness; execution is controlled via WithToolsDisabled()
// based on the context (e.g., DM vs channel).
func (b *Builder) WithLLMContextTools(ctx stdcontext.Context, bot *bots.Bot) llm.ContextOption {
	return func(c *llm.Context) {
		if c.RequestingUser == nil {
			b.pluginAPI.Log.Error("Cannot add tools to context: RequestingUser is nil")
			return
		}

		markSandboxFileAttachment(c, bot)
		c.Tools = b.getToolsStoreForUser(ctx, c, bot, c.RequestingUser.Id, false)
	}
}

func markSandboxFileAttachment(c *llm.Context, bot *bots.Bot) {
	c.ToolCatalog.SandboxFilesAttached = c.ToolCatalog.ResponseFilesSupported && bot.ShouldPromiseSandboxFiles()
}

// WithLLMContextConcreteTools adds the requester's tools but forces concrete MCP
// tools instead of dynamic-loading meta-tools. Bridge catalog APIs need the full
// concrete MCP tool list regardless of the bot's dynamic-loading setting.
func (b *Builder) WithLLMContextConcreteTools(ctx stdcontext.Context, bot *bots.Bot) llm.ContextOption {
	return func(c *llm.Context) {
		if c.RequestingUser == nil {
			b.pluginAPI.Log.Error("Cannot add tools to context: RequestingUser is nil")
			return
		}
		markSandboxFileAttachment(c, bot)
		c.Tools = b.getToolsStoreForUser(ctx, c, bot, c.RequestingUser.Id, true)
	}
}

// WithLLMContextNoTools explicitly disables tools for this context session only,
// overriding the bot's DisableTools configuration. This allows inter-plugin requests
// to work with tool-enabled bots by bypassing tools for non-streaming calls.
func (b *Builder) WithLLMContextNoTools() llm.ContextOption {
	return func(c *llm.Context) {
		c.Tools = llm.NewToolStore()
	}
}

func (b *Builder) WithLLMContextParameters(params map[string]any) llm.ContextOption {
	return func(c *llm.Context) {
		c.Parameters = params
	}
}

func (b *Builder) WithLLMContextBot(bot *bots.Bot) llm.ContextOption {
	return func(c *llm.Context) {
		c.SetBotFields(bot.GetConfig().DisplayName, bot.GetConfig().Name, bot.BotUserID(), bot.GetService().DefaultModel, bot.GetService().Type, bot.GetConfig().CustomInstructions)
		c.ToolCatalog.MCPDynamicToolLoading = bot.GetConfig().MCPDynamicToolLoading
		c.ToolRuntime.MCPDynamicToolTelemetry = b.mcpDynamicToolTelemetry
	}
}
