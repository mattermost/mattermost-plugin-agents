// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type catalogTestMinter struct{}

func (catalogTestMinter) CreateScopedSession(userID string, _ *model.SessionScope, expiresAt int64) (*model.Session, error) {
	return &model.Session{Id: model.NewId(), UserId: userID, Token: model.NewId(), ExpiresAt: expiresAt}, nil
}

func (catalogTestMinter) RevokeSession(string) error { return nil }

// liveCatalog serves the catalogs a live server returned: the embedded
// Mattermost tools on the scoped session, and plugin tools namespaced twice,
// each reporting who its catalog connected as.
type liveCatalog struct {
	*mcp.ClientManager
}

func (liveCatalog) GetToolsWithSelection(_ context.Context, req mcp.CatalogRequest, selection mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
	identity := req.InvokingUserID
	if req.Scoped != nil {
		identity = "scoped bot session"
	}
	tool := func(name, origin string, readOnly bool) llm.Tool {
		return llm.Tool{
			Name: name, ServerOrigin: origin, ReadOnly: readOnly,
			Schema: map[string]any{"type": "object", "properties": map[string]any{"board_id": map[string]any{"type": "string"}, "run_id": map[string]any{"type": "string"}}},
			Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
				return "ran as " + identity, nil
			},
		}
	}
	all := []llm.Tool{
		tool("mattermost__read_channel", mcp.EmbeddedClientKey, true),
		tool("mattermost__search_posts", mcp.EmbeddedClientKey, true),
		tool("mattermost__list_channel_bookmarks", mcp.EmbeddedClientKey, true),
		tool("mattermost__create_channel_bookmark", mcp.EmbeddedClientKey, false),
		tool("mattermost__add_reaction", mcp.EmbeddedClientKey, false),
		tool("mattermost__delete_post", mcp.EmbeddedClientKey, false),
		tool("boards__com_mattermost_boardsv2__create_card", BoardsOrigin, false),
		tool("boards__com_mattermost_boardsv2__list_boards", BoardsOrigin, true),
		tool("playbooks__playbooks__check_item", PlaybooksOrigin, false),
		tool("cursor_cloud_agents__com_mattermost_plugin-cursor__create_agent", CursorOrigin, false),
	}
	return slices.DeleteFunc(all, func(t llm.Tool) bool { return !selection.Allows(t.ServerOrigin) }), nil
}

type noBuiltinTools struct{}

func (noBuiltinTools) GetTools(*bots.Bot, *llm.Context) []llm.Tool { return nil }

type catalogTestConfig struct{}

func (catalogTestConfig) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}
func (catalogTestConfig) EnableChannelMentionToolCalling() bool { return true }

func newCatalogTestBuilder(t *testing.T) *llmcontext.Builder {
	t.Helper()
	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("RolesGrantPermission", mock.Anything, model.PermissionJoinPublicTeams.Id).Return(true).Maybe()
	mockAPI.On("GetLicense").Return(enterprisetest.LicenseFor(enterprise.LevelEnterprise)).Maybe()
	mockAPI.On("GetTeam", mock.Anything).Return(&model.Team{Id: "T", Name: "team"}, nil).Maybe()
	mockAPI.On("GetChannel", mock.Anything).Return(&model.Channel{}, nil).Maybe()
	mockAPI.On("HasPermissionToChannel", mock.Anything, mock.Anything, model.PermissionReadChannel).Return(true).Maybe()
	mockAPI.On("HasPermissionToTeam", mock.Anything, mock.Anything, model.PermissionReadPublicChannel).Return(true).Maybe()
	mockAPI.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
	mockAPI.On("LogAuditRec", mock.Anything).Maybe().Return()
	for i := 1; i <= 10; i++ {
		args := make([]any, i)
		for j := range args {
			args[j] = mock.Anything
		}
		for _, level := range []string{"LogDebug", "LogInfo", "LogWarn", "LogError"} {
			mockAPI.On(level, args...).Maybe().Return()
		}
	}
	client := pluginapi.NewClient(mockAPI, nil)
	manager := mcp.NewClientManager(mcp.Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, http.DefaultClient, nil, mcp.RemoteMCPAlwaysAllowed)
	t.Cleanup(manager.Close)
	manager.SetScopedSessions(catalogTestMinter{})
	return llmcontext.NewLLMContextBuilder(client, noBuiltinTools{}, liveCatalog{manager}, catalogTestConfig{})
}

// A project agent's turn, built by the real catalog builder and binder from
// the names a live server returns, offers its embedded Mattermost tools and
// its plugin tools bound to the project.
func TestProjectTurnCatalog(t *testing.T) {
	channelID, boardID, botUserID, leadID := model.NewId(), model.NewId(), model.NewId(), model.NewId()
	project := activeProject()
	project.ChannelID, project.BoardChannelID, project.PlaybookRunID = channelID, boardID, "R"
	project.AgentBotUserID, project.LeadUserID = botUserID, leadID
	f := newFixture(t, true, project)
	f.platform.botChannels = []string{channelID, boardID}
	builder := newCatalogTestBuilder(t)
	builder.SetProjectRuntime(f.svc)

	tests := []struct {
		name     string
		agentCfg func(*llm.BotConfig)
	}{
		{name: "new project agent"},
		{name: "existing project agent after the backfill", agentCfg: func(cfg *llm.BotConfig) { cfg.EnabledMCPTools = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := llm.BotConfig{
				ID: "agent", BotUserID: botUserID, ProjectChannelID: channelID,
				UseServiceAccountAuth: true, ScopedChannelMode: true, EnabledMCPTools: ProjectEmbeddedTools(),
			}
			if tt.agentCfg != nil {
				tt.agentCfg(&cfg)
				f.store.agents = []llm.BotConfig{cfg}
				_, err := BackfillAgentTools(f.store)
				require.NoError(t, err)
				cfg = f.store.agents[0]
			}
			bot := bots.NewBot(cfg, llm.ServiceConfig{}, &model.Bot{UserId: botUserID}, nil)
			requester := &model.User{Id: leadID, Username: "alice"}
			channel := &model.Channel{Id: channelID, TeamId: model.NewId(), Type: model.ChannelTypeOpen}

			llmCtx := builder.BuildLLMContextUserRequest(bot, requester, channel,
				builder.WithLLMContextScopedChannelTurn(llm.ScopedTurn{TriggerPostID: model.NewId(), ThreadRootID: model.NewId(), ConversationID: model.NewId()}),
				func(c *llm.Context) { c.ToolCatalog.InteractiveUserPresent = true },
				builder.WithLLMContextTools(context.Background(), bot),
			)
			defer llmCtx.ReleaseScopedSession()
			require.True(t, llmCtx.ToolsScoped)

			byName := map[string]llm.Tool{}
			for _, tool := range llmCtx.Tools.GetTools() {
				byName[tool.Name] = tool
			}
			for _, name := range []string{
				"mattermost__read_channel", "mattermost__search_posts", "mattermost__list_channel_bookmarks",
				"mattermost__create_channel_bookmark", "mattermost__add_reaction",
				"boards__com_mattermost_boardsv2__create_card", "playbooks__playbooks__check_item",
				"cursor_cloud_agents__com_mattermost_plugin-cursor__create_agent",
			} {
				assert.Contains(t, byName, name)
			}
			assert.NotContains(t, byName, "mattermost__delete_post", "embedded tools outside the allowlist stay out")
			assert.NotContains(t, byName, "boards__com_mattermost_boardsv2__list_boards", "the binder drops tools that reach other boards")

			cursor := byName["cursor_cloud_agents__com_mattermost_plugin-cursor__create_agent"]
			assert.True(t, cursor.AlwaysAsk)
			result, err := llmCtx.Tools.ResolveTool(context.Background(), cursor.Name, func(any) error { return nil }, llmCtx)
			require.NoError(t, err)
			assert.Equal(t, "ran as "+leadID, result, "Cursor runs are credited to the requester")

			result, err = llmCtx.Tools.ResolveTool(context.Background(), "boards__com_mattermost_boardsv2__create_card", func(any) error { return nil }, llmCtx)
			require.NoError(t, err)
			assert.Equal(t, "ran as "+botUserID, result, "board tools run as the project bot")
		})
	}
}
