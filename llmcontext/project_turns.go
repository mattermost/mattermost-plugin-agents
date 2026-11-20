// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"errors"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

// ErrProjectTurnUnavailable is returned when a project agent's turn cannot be
// scoped to its project. Project turns never fall back to an unscoped run.
var ErrProjectTurnUnavailable = errors.New("this project agent cannot run here right now")

// ProjectScope is how one turn of a project agent is scoped.
type ProjectScope struct {
	// ExtraChannelIDs are read besides the project channel: its board channel.
	ExtraChannelIDs []string
	// WriteChannelIDs may also get reactions and bookmarks.
	WriteChannelIDs []string
	// BotPluginOrigins are the plugin MCP servers connected as the project
	// bot, every call going through the project's argument binder.
	BotPluginOrigins []string
	// RequesterPluginOrigins are the plugin MCP servers connected as the
	// human requester, whose calls are credited to that person.
	RequesterPluginOrigins []string
}

// ProjectRuntime supplies what project agents' turns need from the projects
// service. Every method fails closed.
type ProjectRuntime interface {
	// Scope returns how bot's turn in channel is scoped, or an error when the
	// turn must not run.
	Scope(ctx stdcontext.Context, bot *bots.Bot, channel *model.Channel) (ProjectScope, error)
	// BindPluginTools wraps plugin MCP tools in the project's argument binder.
	// Tools without a binder rule are dropped.
	BindPluginTools(ctx stdcontext.Context, bot *bots.Bot, llmCtx *llm.Context, tools []llm.Tool) []llm.Tool
}

// SetProjectRuntime installs the projects service. Without one, project
// agents' turns get no tools.
func (b *Builder) SetProjectRuntime(runtime ProjectRuntime) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	b.projectRuntime = runtime
}

func (b *Builder) projects() ProjectRuntime {
	b.projectMu.RLock()
	defer b.projectMu.RUnlock()
	return b.projectRuntime
}

// projectScope returns the scope of a project agent's turn, or an error that
// refuses it.
func (b *Builder) projectScope(ctx stdcontext.Context, bot *bots.Bot, channel *model.Channel) (ProjectScope, error) {
	runtime := b.projects()
	if runtime == nil || channel == nil || channel.Id != bot.GetConfig().ProjectChannelID {
		return ProjectScope{}, ErrProjectTurnUnavailable
	}
	return runtime.Scope(ctx, bot, channel)
}

// projectPluginTools connects the project's plugin MCP servers, the binder
// ones as the bot and the rest as the requester, and returns their tools
// wrapped in the project's argument binder.
func (b *Builder) projectPluginTools(ctx stdcontext.Context, c *llm.Context, bot *bots.Bot, requesterID string, selection mcp.ToolSelection) []llm.Tool {
	runtime := b.projects()
	if runtime == nil || b.mcpToolProvider == nil {
		return nil
	}
	scope, err := runtime.Scope(ctx, bot, c.Channel)
	if err != nil {
		return nil
	}
	cfg := bot.GetConfig()
	fetch := func(identity string, origins []string) []llm.Tool {
		if identity == "" || len(origins) == 0 {
			return nil
		}
		sel := selection
		sel.AllowedOrigins = slices.DeleteFunc(slices.Clone(origins), func(origin string) bool {
			return !selection.Allows(origin)
		})
		if len(sel.AllowedOrigins) == 0 {
			return nil
		}
		tools, _ := b.mcpToolProvider.GetToolsWithSelection(ctx, mcp.UserCatalogRequest(identity), sel)
		tools = slices.DeleteFunc(tools, func(tool llm.Tool) bool {
			return !slices.Contains(llm.NormalizeMCPServerOrigins(origins), llm.NormalizeMCPServerOrigin(tool.ServerOrigin))
		})
		return llm.FilterMCPToolsByEnabledAllowlist(tools, cfg.EnabledMCPTools)
	}
	var tools []llm.Tool
	tools = append(tools, fetch(bot.BotUserID(), scope.BotPluginOrigins)...)
	tools = append(tools, fetch(requesterID, scope.RequesterPluginOrigins)...)
	return runtime.BindPluginTools(ctx, bot, c, tools)
}
