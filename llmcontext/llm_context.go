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
	store := llm.NewToolStore()

	// Add built-in tools (always add for LLM awareness; execution controlled via WithToolsDisabled)
	store.AddTools(b.toolProvider.GetTools(bot))

	// Add MCP tools if available and enabled
	// Note: MCP tools are only executable in DMs, but we always add them to the store
	// so that GetToolsInfo() can inform the LLM about their availability.
	// Actual execution is controlled via WithToolsDisabled() based on channel type.
	if b.mcpToolProvider != nil {
		// Get tools from all connected servers
		mcpTools, mcpErrors := b.mcpToolProvider.GetToolsForUser(userID)

		// Add tools from successfully connected servers even if some had errors
		// These will be disabled in non-DM channels via WithToolsDisabled()
		if len(mcpTools) > 0 {
			store.AddTools(mcpTools)
		}

		// Per-agent MCP tool filtering: unless the agent is configured to pick up
		// every MCP tool automatically, retain only tools listed in its allowlist.
		// This runs AFTER admin policy (filterToolsByConfig inside GetToolsForUser)
		// and BEFORE per-user filtering (RemoveToolsByServerOrigin in conversations.go).
		botCfg := bot.GetConfig()
		if !botCfg.AutoEnableNewMCPTools {
			store.RetainOnlyMCPTools(botCfg.EnabledMCPTools)
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
	}

	return store
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
	}
}
