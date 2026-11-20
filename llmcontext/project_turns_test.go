// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"slices"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

const (
	testBoardsOrigin    = "plugin://com.mattermost.boardsv2"
	testPlaybooksOrigin = "plugin://playbooks"
	testCursorOrigin    = "plugin://com.mattermost.plugin-cursor"
)

// nameBindingRuntime binds only the tools it knows by name, like the
// projects binder.
type nameBindingRuntime struct {
	bound    []string
	received []string
}

func (r *nameBindingRuntime) Scope(stdcontext.Context, *bots.Bot, *model.Channel) (ProjectScope, error) {
	return ProjectScope{
		WriteChannelIDs:        []string{"project"},
		BotPluginOrigins:       []string{testBoardsOrigin, testPlaybooksOrigin},
		RequesterPluginOrigins: []string{testCursorOrigin},
	}, nil
}

func (r *nameBindingRuntime) BindPluginTools(_ stdcontext.Context, _ *bots.Bot, _ *llm.Context, tools []llm.Tool) []llm.Tool {
	var out []llm.Tool
	for _, tool := range tools {
		r.received = append(r.received, tool.Name)
		if slices.Contains(r.bound, tool.Name) {
			out = append(out, tool)
		}
	}
	return out
}

func TestProjectPluginToolsIgnoreTheEmptyAgentAllowlist(t *testing.T) {
	provider := &selectionHonoringMCPProvider{tools: []llm.Tool{
		{Name: "create_card", ServerOrigin: testBoardsOrigin},
		{Name: "delete_board", ServerOrigin: testBoardsOrigin},
		{Name: "update_checklist_item", ServerOrigin: testPlaybooksOrigin},
		{Name: "launch_agent", ServerOrigin: testCursorOrigin},
		{Name: "send_email", ServerOrigin: "https://external.example.com"},
	}}
	tests := []struct {
		name         string
		disabled     []string
		wantReceived []string
		wantBound    []string
	}{
		{
			name:         "every plugin server reaches the binder",
			wantReceived: []string{"create_card", "delete_board", "update_checklist_item", "launch_agent"},
			wantBound:    []string{"create_card", "update_checklist_item", "launch_agent"},
		},
		{
			name:         "a server the user turned off stays off",
			disabled:     []string{testCursorOrigin},
			wantReceived: []string{"create_card", "delete_board", "update_checklist_item"},
			wantBound:    []string{"create_card", "update_checklist_item"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &nameBindingRuntime{bound: []string{"create_card", "update_checklist_item", "launch_agent", "send_email"}}
			b := &Builder{mcpToolProvider: provider}
			b.SetProjectRuntime(runtime)

			cfg := llm.BotConfig{ID: "agent", BotUserID: "botuser", ProjectChannelID: "project", UseServiceAccountAuth: true, ScopedChannelMode: true}
			bot := bots.NewBot(cfg, llm.ServiceConfig{}, &model.Bot{UserId: "botuser"}, nil)
			c := &llm.Context{Channel: &model.Channel{Id: "project", Type: model.ChannelTypeOpen}}
			c.ToolCatalog.DisabledMCPServerOrigins = tt.disabled

			selection := mcpToolSelection(cfg, c, true, false)
			tools := b.projectPluginTools(stdcontext.Background(), c, bot, "requester", selection)

			var names []string
			for _, tool := range tools {
				names = append(names, tool.Name)
			}
			assert.ElementsMatch(t, tt.wantReceived, runtime.received)
			assert.ElementsMatch(t, tt.wantBound, names)
		})
	}
}
