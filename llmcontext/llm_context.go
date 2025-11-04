// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"time"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// ToolProvider provides built-in tools for a bot and context
type ToolProvider interface {
	GetTools(isDM bool, bot *bots.Bot) []llm.Tool
}

// MCPToolProvider provides MCP tools for a user
type MCPToolProvider interface {
	GetToolsForUser(userID string) ([]llm.Tool, *mcp.Errors)
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
		c.RequestingUser = user
		if user != nil {
			tz := user.GetPreferredTimezone()
			loc, err := time.LoadLocation(tz)
			if err == nil && loc != nil {
				c.Time = time.Now().In(loc).Format(time.RFC1123)
			}
		}
	}
}

// WithLLMContextSessionID removed: embedded MCP manages its own session lifecycle

// getToolsStoreForUser returns a tool store for a specific user, including MCP tools
// Session information is extracted from the llm.Context
func (b *Builder) getToolsStoreForUser(c *llm.Context, bot *bots.Bot, isDM bool, userID string) *llm.ToolStore {
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

	// Add built-in tools
	store.AddTools(b.toolProvider.GetTools(isDM, bot))

	// Add MCP tools if available, enabled, and in a DM
	if b.mcpToolProvider != nil && isDM {
		// Get tools from all connected servers
		mcpTools, mcpErrors := b.mcpToolProvider.GetToolsForUser(userID)

		// Add tools from successfully connected servers even if some had errors
		if len(mcpTools) > 0 {
			store.AddTools(mcpTools)
		}

		// Handle MCP errors if any occurred
		if mcpErrors != nil {
			for _, authError := range mcpErrors.ToolAuthErrors {
				store.AddAuthError(authError)
			}
		}
	}

	return store
}

// WithLLMContextTools adds tools to the LLM context the requester can access
func (b *Builder) WithLLMContextTools(bot *bots.Bot, isDM bool) llm.ContextOption {
	return func(c *llm.Context) {
		if c.RequestingUser == nil {
			b.pluginAPI.Log.Error("Cannot add tools to context: RequestingUser is nil")
			return
		}

		// Get tools using session info from llm.Context
		c.Tools = b.getToolsStoreForUser(c, bot, isDM, c.RequestingUser.Id)
	}
}

// WithLLMContextDefaultTools adds default tools to the LLM context for the requesting user
func (b *Builder) WithLLMContextDefaultTools(bot *bots.Bot, isDM bool) llm.ContextOption {
	return b.WithLLMContextTools(bot, isDM)
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
		c.BotName = bot.GetConfig().DisplayName
		c.BotUsername = bot.GetConfig().Name
		c.CustomInstructions = bot.GetConfig().CustomInstructions
		c.BotModel = bot.GetService().DefaultModel
	}
}
