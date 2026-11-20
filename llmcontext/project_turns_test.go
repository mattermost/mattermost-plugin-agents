// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"cmp"
	stdcontext "context"
	"slices"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testBoardsOrigin    = "plugin://com.mattermost.boardsv2"
	testPlaybooksOrigin = "plugin://playbooks"
	testCursorOrigin    = "plugin://com.mattermost.plugin-cursor"
)

// nameBindingRuntime binds only the tools it knows by name, like the
// projects binder.
type nameBindingRuntime struct {
	bound     []string
	received  []string
	channelID string
}

func (r *nameBindingRuntime) Scope(stdcontext.Context, *bots.Bot, *model.Channel) (ProjectScope, error) {
	return ProjectScope{
		WriteChannelIDs:        []string{cmp.Or(r.channelID, "project")},
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

// identityCatalogProvider serves the same Cursor tool to every catalog, each
// copy reporting the identity its catalog connected as.
type identityCatalogProvider struct {
	*scopedCatalogProvider
}

func (p *identityCatalogProvider) GetToolsWithSelection(_ stdcontext.Context, req mcp.CatalogRequest, selection mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
	identity := req.InvokingUserID
	if req.Scoped != nil {
		identity = "scoped bot session"
	}
	tool := testMCPTool("cursor__launch_agent", testCursorOrigin, "launch a Cursor agent")
	tool.Resolver = func(stdcontext.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
		return "ran as " + identity, nil
	}
	if !selection.Allows(tool.ServerOrigin) {
		return nil, nil
	}
	return []llm.Tool{tool}, nil
}

// Cursor tools are credited to the person who asked: the bot's scoped catalog
// never carries them, so the requester's copy is the one a turn, or the
// approval that resumes it, executes.
func TestProjectCursorToolsRunAsTheRequester(t *testing.T) {
	provider := &identityCatalogProvider{newScopedCatalogProvider(t, &recordingScopedMinter{}, nil)}
	builder, _ := newScopedTestBuilder(t, defaultScopedTestEnv(), &emptyToolProvider{}, provider)
	projectChannelID := model.NewId()
	builder.SetProjectRuntime(&nameBindingRuntime{bound: []string{"cursor__launch_agent"}, channelID: projectChannelID})

	cfg := scopedTestBotConfig()
	cfg.ProjectChannelID = projectChannelID
	cfg.ScopedExtraChannelIDs = nil
	bot := newScopedTestBot(cfg)
	requester := &model.User{Id: model.NewId(), Username: "alice"}
	channel := &model.Channel{Id: projectChannelID, TeamId: scopedTestTeamID, Type: model.ChannelTypeOpen}

	for _, phase := range []string{"turn", "approval resume"} {
		t.Run(phase, func(t *testing.T) {
			llmContext := builder.BuildLLMContextUserRequest(bot, requester, channel,
				builder.WithLLMContextScopedChannelTurn(llm.ScopedTurn{TriggerPostID: model.NewId(), ThreadRootID: model.NewId(), ConversationID: model.NewId()}),
				builder.WithLLMContextTools(stdcontext.Background(), bot),
			)
			defer llmContext.ReleaseScopedSession()
			require.True(t, llmContext.ToolsScoped)

			var cursorTools int
			for _, tool := range llmContext.Tools.GetTools() {
				if llm.NormalizeMCPServerOrigin(tool.ServerOrigin) == testCursorOrigin {
					cursorTools++
				}
			}
			require.Equal(t, 1, cursorTools, "only the requester's copy is in the catalog")
			result, err := llmContext.Tools.ResolveTool(stdcontext.Background(), "cursor__launch_agent", func(any) error { return nil }, llmContext)
			require.NoError(t, err)
			assert.Equal(t, "ran as "+requester.Id, result)
		})
	}
}

func TestAuditScopedToolCallsCoversToolsAddedAfterTheCatalog(t *testing.T) {
	tests := []struct {
		name       string
		scoped     bool
		wantAudits int
	}{
		{name: "scoped turn", scoped: true, wantAudits: 1},
		{name: "unscoped turn", scoped: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder, auditRecords := newScopedTestBuilder(t, defaultScopedTestEnv(), &emptyToolProvider{}, nil)
			bot := newScopedTestBot(scopedTestBotConfig())
			c := &llm.Context{ToolsScoped: tt.scoped, Channel: &model.Channel{Id: model.NewId()}}

			tools := builder.AuditScopedToolCalls(c, bot, "requester", []llm.Tool{testBuiltinTool("update_my_instructions")})
			require.Len(t, tools, 1)
			_, err := tools[0].Resolver(stdcontext.Background(), c, func(any) error { return nil })
			require.NoError(t, err)

			records := auditRecords()
			require.Len(t, records, tt.wantAudits)
			for _, rec := range records {
				assert.Equal(t, audit.EventAgentScopedToolCall, rec.EventName)
				assert.Equal(t, "update_my_instructions", rec.EventData.Parameters[audit.KeyToolName])
			}
		})
	}
}
