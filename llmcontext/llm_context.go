// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	storepkg "github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// ToolProvider provides built-in tools for a bot and context
type ToolProvider interface {
	GetTools(bot *bots.Bot) []llm.Tool
}

// MCPToolProvider provides MCP tools for a user
type MCPToolProvider interface {
	GetToolsForUser(userID string) ([]llm.Tool, *mcp.Errors)
}

type MCPToolRetrievalOverrideProvider interface {
	GetToolRetrievalOverrides() map[string]mcp.MCPToolRetrievalOverride
}

type LoadedMCPToolStore interface {
	UpsertLoadedMCPTool(tool storepkg.LoadedMCPTool) error
	ListLoadedMCPTools(conversationID, botID, userID string) ([]storepkg.LoadedMCPTool, error)
	DeleteLoadedMCPTool(conversationID, botID, userID, toolName string) error
	DeleteLoadedMCPToolsByNames(conversationID, botID, userID string, toolNames []string) error
}

// ConfigProvider provides configuration access
type ConfigProvider interface {
	GetEnableLLMTrace() bool
	GetServiceByID(id string) (llm.ServiceConfig, bool)
}

// Builder builds contexts for LLM requests
type Builder struct {
	pluginAPI       *pluginapi.Client
	toolProvider    ToolProvider
	mcpToolProvider MCPToolProvider
	configProvider  ConfigProvider

	loadedMCPToolStore      LoadedMCPToolStore
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
	}
}

func (b *Builder) SetLoadedMCPToolStore(store LoadedMCPToolStore) {
	b.loadedMCPToolStore = store
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

func (b *Builder) WithLLMContextConversationID(conversationID string) llm.ContextOption {
	return func(c *llm.Context) {
		if conversationID == "" {
			return
		}
		c.ConversationID = conversationID
	}
}

// normalizeMCPServerOrigin trims whitespace and trailing slashes so allowlist
// ServerOrigin values match ToolAuthError.ServerOrigin across formatting variants.
func normalizeMCPServerOrigin(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// toolAuthErrorMatchesAllowlist reports whether authErr refers to a server that still
// appears in the per-agent MCP allowlist (by ServerOrigin).
func toolAuthErrorMatchesAllowlist(authErr llm.ToolAuthError, allowlist []llm.EnabledMCPTool) bool {
	errOrigin := normalizeMCPServerOrigin(authErr.ServerOrigin)
	for i := range allowlist {
		if normalizeMCPServerOrigin(allowlist[i].ServerOrigin) == errOrigin {
			return true
		}
	}
	return false
}

func filterToolAuthErrorsForAllowlist(errors []llm.ToolAuthError, allowlist []llm.EnabledMCPTool) []llm.ToolAuthError {
	return slices.DeleteFunc(slices.Clone(errors), func(e llm.ToolAuthError) bool {
		return !toolAuthErrorMatchesAllowlist(e, allowlist)
	})
}

func (b *Builder) WithLLMContextDisabledMCPServers(origins []string) llm.ContextOption {
	return func(c *llm.Context) {
		if len(origins) == 0 {
			return
		}
		c.DisabledMCPServerOrigins = slices.Clone(origins)
	}
}

func (b *Builder) WithLLMContextMCPToolFilter(keep func(llm.Tool) bool) llm.ContextOption {
	return func(c *llm.Context) {
		if keep == nil {
			return
		}
		c.KeepMCPTool = keep
	}
}

// WithLLMContextPreloadedMCPTools requests exact-or-bare MCP tools for internal
// predefined flows. The selected tools are still constrained by the normal
// authorized MCP catalog and must be configured before default tools are built.
func (b *Builder) WithLLMContextPreloadedMCPTools(tools []llm.EnabledMCPTool) llm.ContextOption {
	return func(c *llm.Context) {
		c.PreloadedMCPTools = slices.Clone(tools)
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

// getToolsStoreForUser returns a tool store for a specific user, including MCP tools
// Session information is extracted from the llm.Context
func (b *Builder) getToolsStoreForUser(c *llm.Context, bot *bots.Bot, userID string) *llm.ToolStore {
	// Check for nil bot, which is unexpected
	if bot == nil {
		b.pluginAPI.Log.Error("Unexpected nil bot when getting tool store for user", "userID", userID)
		return llm.NewNoTools()
	}

	// Check for empty userID, which is unexpected
	if userID == "" {
		b.pluginAPI.Log.Error("Unexpected empty userID when getting tool store for user")
		return llm.NewNoTools()
	}

	// Check if tools are disabled for this bot
	if bot.GetConfig().DisableTools {
		return llm.NewNoTools()
	}

	// Create a tool store that requires user approval for tool calls
	store := llm.NewToolStore(&b.pluginAPI.Log, b.configProvider.GetEnableLLMTrace())
	botCfg := bot.GetConfig()

	// Add built-in tools (always add for LLM awareness; execution controlled via WithToolsDisabled)
	store.AddTools(b.toolProvider.GetTools(bot))

	var mcpTools []llm.Tool
	var mcpErrors *mcp.Errors

	// Fetch MCP tools if available. They are filtered before either full-schema
	// insertion or strict private-registry construction.
	if b.mcpToolProvider != nil {
		// Get tools from all connected servers
		mcpTools, mcpErrors = b.mcpToolProvider.GetToolsForUser(userID)

		// Per-agent MCP tool filtering: unless the agent is configured to pick up
		// every MCP tool automatically, retain only tools listed in its allowlist.
		// This runs AFTER admin policy (filterToolsByConfig inside GetToolsForUser)
		// and BEFORE per-user/channel filtering and strict registry construction.
		if !botCfg.AutoEnableNewMCPTools {
			mcpTools = retainOnlyAllowedMCPTools(mcpTools, botCfg.EnabledMCPTools)
		}
		mcpTools = filterMCPToolsByDisabledOrigins(mcpTools, c.DisabledMCPServerOrigins)
		mcpTools = filterMCPToolsByPredicate(mcpTools, c.KeepMCPTool)

		if mcpErrors != nil {
			authErrors := mcpErrors.ToolAuthErrors
			if !botCfg.AutoEnableNewMCPTools {
				authErrors = filterToolAuthErrorsForAllowlist(mcpErrors.ToolAuthErrors, botCfg.EnabledMCPTools)
			}
			for _, authError := range authErrors {
				store.AddAuthError(authError)
			}
		}
	}

	if botCfg.MCPDynamicToolLoading {
		b.buildStrictMCPToolStore(store, mcpTools, c, botIDForLoadedMCPTools(c, bot), userID, b.strictRegistryOptions()...)
		return store
	}

	c.ObserveMCPDynamicToolEvent("flag_off", "disabled")
	b.logDebug("MCP dynamic tool loading disabled for bot", "bot_name", botCfg.Name, "bot_id", botCfg.ID)

	if len(mcpTools) > 0 {
		store.AddTools(mcpTools)
	}
	b.preloadMCPTools(store, mcpTools, c.PreloadedMCPTools)

	return store
}

func (b *Builder) buildStrictMCPToolStore(store *llm.ToolStore, mcpTools []llm.Tool, c *llm.Context, botID, userID string, registryOpts ...mcp.MCPToolRegistryOption) {
	registry := mcp.NewMCPToolRegistry(mcpTools, registryOpts...)
	b.restoreLoadedMCPTools(store, registry, c, botID, userID)
	b.preloadMCPTools(store, mcpTools, c.PreloadedMCPTools)
	markUnloadedMCPTools(store, mcpTools)
	store.AddTools(mcp.NewMetaTools(registry, mcp.WithLoadedToolRecorder(b.loadedToolRecorder(c.ConversationID, botID, userID))))
}

func (b *Builder) preloadMCPTools(store *llm.ToolStore, available []llm.Tool, specs []llm.EnabledMCPTool) {
	if store == nil || len(available) == 0 || len(specs) == 0 {
		return
	}

	for _, spec := range specs {
		var matches []llm.Tool
		for _, tool := range available {
			if spec.ServerOrigin != tool.ServerOrigin {
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

func (b *Builder) strictRegistryOptions() []mcp.MCPToolRegistryOption {
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

	return []mcp.MCPToolRegistryOption{mcp.WithMCPToolRetrievalOverrides(overrides)}
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

func botIDForLoadedMCPTools(c *llm.Context, bot *bots.Bot) string {
	if c != nil && c.BotUserID != "" {
		return c.BotUserID
	}
	if bot != nil && bot.GetMMBot() != nil {
		return bot.GetMMBot().UserId
	}
	return ""
}

func (b *Builder) loadedToolRecorder(conversationID, botID, userID string) mcp.LoadedToolRecorder {
	if b.loadedMCPToolStore == nil {
		return nil
	}

	return func(llmContext *llm.Context, entry mcp.MCPToolRegistryEntry) error {
		resolvedConversationID := conversationID
		if llmContext != nil && llmContext.ConversationID != "" {
			resolvedConversationID = llmContext.ConversationID
		}
		if resolvedConversationID == "" || botID == "" || userID == "" {
			b.logWarn("Unable to persist loaded MCP tool without full context identity",
				"conversation_id", resolvedConversationID,
				"bot_id", botID,
				"user_id", userID,
				"tool_name", entry.Name,
			)
			return nil
		}

		now := model.GetMillis()
		return b.loadedMCPToolStore.UpsertLoadedMCPTool(storepkg.LoadedMCPTool{
			ConversationID: resolvedConversationID,
			BotID:          botID,
			UserID:         userID,
			ToolName:       entry.Name,
			ServerOrigin:   entry.ServerOrigin,
			BareName:       entry.BareName,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
}

func (b *Builder) restoreLoadedMCPTools(publicStore *llm.ToolStore, registry *mcp.MCPToolRegistry, c *llm.Context, botID, userID string) {
	if b.loadedMCPToolStore == nil || publicStore == nil || registry == nil || c == nil ||
		c.ConversationID == "" || botID == "" || userID == "" {
		return
	}

	loaded, err := b.loadedMCPToolStore.ListLoadedMCPTools(c.ConversationID, botID, userID)
	if err != nil {
		b.logWarn("Failed to list loaded MCP tools", "error", err.Error(), "conversation_id", c.ConversationID)
		return
	}

	deleteStale := c.KeepMCPTool == nil
	var staleNames []string
	for _, row := range loaded {
		entry, ok := registry.Lookup(row.ToolName)
		if ok {
			publicStore.AddTools([]llm.Tool{entry.Tool})
			continue
		}

		if !deleteStale {
			b.logDebug("Omitting stale loaded MCP tool during scoped rebuild",
				"conversation_id", c.ConversationID,
				"tool_name", row.ToolName,
			)
			continue
		}

		staleNames = append(staleNames, row.ToolName)
	}

	if len(staleNames) == 0 {
		return
	}

	if err := b.loadedMCPToolStore.DeleteLoadedMCPToolsByNames(c.ConversationID, botID, userID, staleNames); err != nil {
		b.logWarn("Failed to delete stale loaded MCP tools",
			"error", err.Error(),
			"conversation_id", c.ConversationID,
			"tool_names", staleNames,
		)
		return
	}
	b.logDebug("Deleted stale loaded MCP tools",
		"conversation_id", c.ConversationID,
		"tool_names", staleNames,
	)
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

	disabledSet := make(map[string]bool, len(disabled))
	for _, origin := range disabled {
		disabledSet[origin] = true
	}

	filtered := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		if disabledSet[tool.ServerOrigin] {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func filterMCPToolsByPredicate(tools []llm.Tool, keep func(llm.Tool) bool) []llm.Tool {
	if len(tools) == 0 || keep == nil {
		return tools
	}

	filtered := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		if keep(tool) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func retainOnlyAllowedMCPTools(tools []llm.Tool, allowlist []llm.EnabledMCPTool) []llm.Tool {
	if len(tools) == 0 {
		return tools
	}

	// Build the allowlist set in the shape FilterMCPToolsByAllowlist expects
	// (key: "serverOrigin\x00toolName"). Wildcard entries grant every runtime
	// tool from that origin, so pre-expand them by walking the tool list once.
	allowed := make(map[string]bool, len(allowlist))
	wildcardOrigins := make(map[string]bool, len(allowlist))
	for _, t := range allowlist {
		if t.ToolName == llm.MCPServerToolWildcard {
			wildcardOrigins[t.ServerOrigin] = true
			continue
		}
		allowed[t.ServerOrigin+"\x00"+t.ToolName] = true
	}
	if len(wildcardOrigins) > 0 {
		for _, tool := range tools {
			if wildcardOrigins[tool.ServerOrigin] {
				allowed[tool.ServerOrigin+"\x00"+tool.Name] = true
			}
		}
	}

	return llm.FilterMCPToolsByAllowlist(tools, allowed)
}

// WithLLMContextTools adds tools to the LLM context the requester can access.
// Tools are always added for LLM awareness; execution is controlled via WithToolsDisabled()
// based on the context (e.g., DM vs channel).
func (b *Builder) WithLLMContextTools(bot *bots.Bot) llm.ContextOption {
	return func(c *llm.Context) {
		if c.RequestingUser == nil {
			b.pluginAPI.Log.Error("Cannot add tools to context: RequestingUser is nil")
			return
		}

		// Get tools using session info from llm.Context
		c.Tools = b.getToolsStoreForUser(c, bot, c.RequestingUser.Id)
	}
}

// WithLLMContextDefaultTools adds default tools to the LLM context for the requesting user
func (b *Builder) WithLLMContextDefaultTools(bot *bots.Bot) llm.ContextOption {
	return b.WithLLMContextTools(bot)
}

// WithLLMContextNoTools explicitly disables tools for this context session only,
// overriding the bot's DisableTools configuration. This allows inter-plugin requests
// to work with tool-enabled bots by bypassing tools for non-streaming calls.
func (b *Builder) WithLLMContextNoTools() llm.ContextOption {
	return func(c *llm.Context) {
		c.Tools = llm.NewNoTools()
	}
}

func (b *Builder) WithLLMContextParameters(params map[string]interface{}) llm.ContextOption {
	return func(c *llm.Context) {
		c.Parameters = params
	}
}

func (b *Builder) WithLLMContextBot(bot *bots.Bot) llm.ContextOption {
	return func(c *llm.Context) {
		var botUserID string
		if mmbot := bot.GetMMBot(); mmbot != nil {
			botUserID = mmbot.UserId
		}
		c.SetBotFields(bot.GetConfig().DisplayName, bot.GetConfig().Name, botUserID, bot.GetService().DefaultModel, bot.GetService().Type, bot.GetConfig().CustomInstructions)
		c.MCPDynamicToolLoading = bot.GetConfig().MCPDynamicToolLoading
		c.MCPDynamicToolTelemetry = b.mcpDynamicToolTelemetry
	}
}
