// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type scopedTestConfig struct {
	channelToolCalling bool
}

func (c *scopedTestConfig) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

func (c *scopedTestConfig) EnableChannelMentionToolCalling() bool {
	return c.channelToolCalling
}

type recordingScopedMinter struct {
	mu        sync.Mutex
	createErr error
	userIDs   []string
	scopes    []scopedsession.Scope
	revoked   []string
}

func (m *recordingScopedMinter) CreateScopedSession(userID string, scope *scopedsession.Scope, expiresAt int64) (*model.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return nil, m.createErr
	}
	m.userIDs = append(m.userIDs, userID)
	m.scopes = append(m.scopes, *scope)
	return &model.Session{Id: model.NewId(), UserId: userID, Token: model.NewId(), ExpiresAt: expiresAt}, nil
}

func (m *recordingScopedMinter) RevokeSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked = append(m.revoked, sessionID)
	return nil
}

// scopedCatalogProvider mints through a real ClientManager and serves a fixed
// catalog, recording each request so tests can see which identity it used.
type scopedCatalogProvider struct {
	*mcp.ClientManager
	tools []llm.Tool
	reqs  []mcp.CatalogRequest
}

func (p *scopedCatalogProvider) GetToolsWithSelection(_ stdcontext.Context, req mcp.CatalogRequest, _ mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
	p.reqs = append(p.reqs, req)
	return p.tools, nil
}

type scopedTestEnv struct {
	level              enterprise.Level
	serverVersion      string
	channelToolCalling bool
}

func defaultScopedTestEnv() scopedTestEnv {
	return scopedTestEnv{level: enterprise.LevelEnterprise, serverVersion: scopedsession.MinServerVersion, channelToolCalling: true}
}

func newScopedTestBuilder(t *testing.T, env scopedTestEnv, toolProvider ToolProvider, mcpProvider MCPToolProvider) (*Builder, func() []*model.AuditRecord) {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(enterprisetest.LicenseFor(env.level)).Maybe()
	mockAPI.On("GetServerVersion").Return(env.serverVersion).Maybe()
	mockAPI.On("GetTeam", mock.Anything).Return(&model.Team{Id: "team-id"}, nil).Maybe()
	for i := 1; i <= 10; i++ {
		args := make([]any, i)
		for j := range args {
			args[j] = mock.Anything
		}
		mockAPI.On("LogDebug", args...).Maybe().Return()
		mockAPI.On("LogInfo", args...).Maybe().Return()
		mockAPI.On("LogWarn", args...).Maybe().Return()
		mockAPI.On("LogError", args...).Maybe().Return()
	}
	var mu sync.Mutex
	var records []*model.AuditRecord
	mockAPI.On("LogAuditRec", mock.Anything).Run(func(args mock.Arguments) {
		mu.Lock()
		defer mu.Unlock()
		records = append(records, args.Get(0).(*model.AuditRecord))
	}).Maybe().Return()

	builder := NewLLMContextBuilder(pluginapi.NewClient(mockAPI, nil), toolProvider, mcpProvider, &scopedTestConfig{channelToolCalling: env.channelToolCalling})
	return builder, func() []*model.AuditRecord {
		mu.Lock()
		defer mu.Unlock()
		return append([]*model.AuditRecord(nil), records...)
	}
}

func scopedTestBotConfig() llm.BotConfig {
	return llm.BotConfig{
		ID:                    "agent-id",
		Name:                  "matty",
		ScopedChannelMode:     true,
		UseServiceAccountAuth: true,
		ScopedExtraChannelIDs: []string{model.NewId()},
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: mcp.EmbeddedClientKey, ToolName: llm.MCPServerToolWildcard},
			{ServerOrigin: "https://jira.example.com", ToolName: llm.MCPServerToolWildcard},
			{ServerOrigin: "plugin://com.example.mcp", ToolName: llm.MCPServerToolWildcard},
		},
	}
}

func newScopedTestBot(cfg llm.BotConfig) *bots.Bot {
	return newTestBotWithMMBot(cfg, &model.Bot{UserId: model.NewId(), Username: "matty"})
}

func TestScopedChannelModeActive(t *testing.T) {
	publicChannel := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeOpen}
	privateChannel := &model.Channel{Id: model.NewId(), Type: model.ChannelTypePrivate}

	tests := []struct {
		name    string
		cfg     func(*llm.BotConfig)
		env     func(*scopedTestEnv)
		channel *model.Channel
		active  bool
	}{
		{name: "every prerequisite holds in a public channel", channel: publicChannel, active: true},
		{name: "every prerequisite holds in a private channel", channel: privateChannel, active: true},
		{name: "setting off", cfg: func(c *llm.BotConfig) { c.ScopedChannelMode = false }, channel: publicChannel},
		{name: "service accounts off", cfg: func(c *llm.BotConfig) { c.UseServiceAccountAuth = false }, channel: publicChannel},
		{name: "every MCP tool enabled automatically", cfg: func(c *llm.BotConfig) { c.AutoEnableNewMCPTools = true }, channel: publicChannel},
		{name: "below Enterprise", env: func(e *scopedTestEnv) { e.level = enterprise.LevelProfessional }, channel: publicChannel},
		{name: "server too old", env: func(e *scopedTestEnv) { e.serverVersion = "12.1.0" }, channel: publicChannel},
		{name: "unknown server version", env: func(e *scopedTestEnv) { e.serverVersion = "" }, channel: publicChannel},
		{name: "channel tool calling off", env: func(e *scopedTestEnv) { e.channelToolCalling = false }, channel: publicChannel},
		{name: "DM", channel: &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect}},
		{name: "group message", channel: &model.Channel{Id: model.NewId(), Type: model.ChannelTypeGroup}},
		{name: "no channel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := scopedTestBotConfig()
			if tt.cfg != nil {
				tt.cfg(&cfg)
			}
			env := defaultScopedTestEnv()
			if tt.env != nil {
				tt.env(&env)
			}
			builder, _ := newScopedTestBuilder(t, env, &emptyToolProvider{}, nil)
			assert.Equal(t, tt.active, builder.ScopedChannelModeActive(newScopedTestBot(cfg), tt.channel))
		})
	}
}

func newScopedCatalogProvider(t *testing.T, minter scopedsession.Minter, tools []llm.Tool) *scopedCatalogProvider {
	t.Helper()
	mockAPI := &plugintest.API{}
	for i := 1; i <= 10; i++ {
		args := make([]any, i)
		for j := range args {
			args[j] = mock.Anything
		}
		mockAPI.On("LogDebug", args...).Maybe().Return()
		mockAPI.On("LogWarn", args...).Maybe().Return()
	}
	mockAPI.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
	client := pluginapi.NewClient(mockAPI, nil)
	manager := mcp.NewClientManager(mcp.Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, http.DefaultClient, nil, mcp.RemoteMCPAlwaysAllowed)
	t.Cleanup(manager.Close)
	manager.SetScopedSessions(minter, scopedsession.NewRegistry(nil))
	return &scopedCatalogProvider{ClientManager: manager, tools: tools}
}

func scopedTestMCPCatalog() []llm.Tool {
	readTool := testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read")
	readTool.ReadOnly = true
	return []llm.Tool{
		readTool,
		testMCPTool("mattermost__create_post", mcp.EmbeddedClientKey, "write"),
		testMCPTool("jira__get_issue", "https://jira.example.com", "service account Jira"),
		testMCPTool("example__plugin_tool", "plugin://com.example.mcp", "another plugin"),
	}
}

func TestScopedChannelTurnCatalog(t *testing.T) {
	requester := &model.User{Id: model.NewId(), Username: "alice"}
	channel := &model.Channel{Id: model.NewId(), TeamId: "team-id", Type: model.ChannelTypePrivate}
	turn := llm.ScopedTurn{TriggerPostID: model.NewId(), ThreadRootID: model.NewId(), ConversationID: model.NewId()}

	minter := &recordingScopedMinter{}
	provider := newScopedCatalogProvider(t, minter, scopedTestMCPCatalog())
	builder, auditRecords := newScopedTestBuilder(t, defaultScopedTestEnv(), &staticToolProvider{tools: []llm.Tool{testBuiltinTool("web_search")}}, provider)
	cfg := scopedTestBotConfig()
	bot := newScopedTestBot(cfg)

	llmContext := builder.BuildLLMContextUserRequest(bot, requester, channel,
		builder.WithLLMContextScopedChannelTurn(turn),
		builder.WithLLMContextTools(stdcontext.Background(), bot),
	)

	require.True(t, llmContext.ToolsScoped)
	assert.ElementsMatch(t, []string{"web_search", "mattermost__read_channel", "jira__get_issue"}, toolNames(llmContext.Tools),
		"write tools and other plugins' MCP tools never enter a scoped catalog")

	require.Len(t, provider.reqs, 1)
	req := provider.reqs[0]
	require.NotNil(t, req.Scoped, "the catalog must connect through the scoped session")
	assert.True(t, req.ServiceAccount)
	assert.Equal(t, bot.BotUserID(), req.RemoteOwnerID)
	assert.Equal(t, requester.Id, req.InvokingUserID, "access policy is still evaluated for the requester")

	require.Equal(t, []string{bot.BotUserID()}, minter.userIDs, "the session belongs to the agent's bot")
	scope := minter.scopes[0]
	assert.Equal(t, append([]string{channel.Id}, cfg.ScopedExtraChannelIDs...), scope.Resources.ChannelIDs)
	assert.True(t, scope.Resources.OpenTeamPublicChannels)
	assert.Equal(t, requester.Id, scope.OnBehalfOfUserID)

	records := auditRecords()
	require.Len(t, records, 1)
	grant := records[0]
	assert.Equal(t, audit.EventAgentScopedSessionGrant, grant.EventName)
	assert.Equal(t, model.AuditStatusSuccess, grant.Status)
	assert.Equal(t, requester.Id, grant.Actor.UserId)
	params := grant.EventData.Parameters
	assert.Equal(t, bot.BotUserID(), params[audit.KeyAgentID])
	assert.Equal(t, channel.Id, params[audit.KeyChannelID])
	assert.Equal(t, turn.TriggerPostID, params[audit.KeyPostID])
	assert.Equal(t, turn.ConversationID, params[audit.KeyConversationID])
	assert.Equal(t, 1, params[audit.KeyScopeExtraChannelCount])
	encoded, err := json.Marshal(grant)
	require.NoError(t, err)
	sessionID := req.Scoped.Session().Id
	assert.NotContains(t, string(encoded), sessionID, "audit records never carry the session ID")
	assert.NotContains(t, string(encoded), req.Scoped.Session().Token)

	assert.Empty(t, minter.revoked)
	llmContext.ReleaseScopedSession()
	llmContext.ReleaseScopedSession()
	assert.Equal(t, []string{sessionID}, minter.revoked, "releasing the turn revokes its session once")
}

func TestScopedChannelTurnFallsBackOnlyWithApproval(t *testing.T) {
	publicChannel := &model.Channel{Id: model.NewId(), TeamId: "team-id", Type: model.ChannelTypeOpen}
	dm := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect}

	tests := []struct {
		name       string
		channel    *model.Channel
		scopedTurn bool
		env        func(*scopedTestEnv)
	}{
		{name: "entry point without a scoped turn", channel: publicChannel},
		{name: "DM", channel: dm, scopedTurn: true},
		{name: "server too old", channel: publicChannel, scopedTurn: true, env: func(e *scopedTestEnv) { e.serverVersion = "12.1.0" }},
		{name: "channel tool calling off", channel: publicChannel, scopedTurn: true, env: func(e *scopedTestEnv) { e.channelToolCalling = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minter := &recordingScopedMinter{}
			provider := newScopedCatalogProvider(t, minter, scopedTestMCPCatalog())
			env := defaultScopedTestEnv()
			if tt.env != nil {
				tt.env(&env)
			}
			builder, auditRecords := newScopedTestBuilder(t, env, &emptyToolProvider{}, provider)
			bot := newScopedTestBot(scopedTestBotConfig())

			opts := []llm.ContextOption{}
			if tt.scopedTurn {
				opts = append(opts, builder.WithLLMContextScopedChannelTurn(llm.ScopedTurn{TriggerPostID: model.NewId()}))
			}
			opts = append(opts, builder.WithLLMContextTools(stdcontext.Background(), bot))
			llmContext := builder.BuildLLMContextUserRequest(bot, &model.User{Id: model.NewId()}, tt.channel, opts...)

			assert.False(t, llmContext.ToolsScoped, "an unscoped catalog must keep asking for approval")
			assert.Empty(t, minter.userIDs)
			assert.Empty(t, auditRecords())
			require.Len(t, provider.reqs, 1)
			assert.Nil(t, provider.reqs[0].Scoped)
		})
	}
}

func TestScopedChannelTurnMintFailureHasNoMCPTools(t *testing.T) {
	minter := &recordingScopedMinter{createErr: errors.New("server unavailable")}
	provider := newScopedCatalogProvider(t, minter, scopedTestMCPCatalog())
	builder, auditRecords := newScopedTestBuilder(t, defaultScopedTestEnv(), &staticToolProvider{tools: []llm.Tool{testBuiltinTool("web_search")}}, provider)
	bot := newScopedTestBot(scopedTestBotConfig())
	requester := &model.User{Id: model.NewId()}

	llmContext := builder.BuildLLMContextUserRequest(bot, requester, &model.Channel{Id: model.NewId(), TeamId: "team-id", Type: model.ChannelTypeOpen},
		builder.WithLLMContextScopedChannelTurn(llm.ScopedTurn{TriggerPostID: model.NewId()}),
		builder.WithLLMContextTools(stdcontext.Background(), bot),
	)

	assert.False(t, llmContext.ToolsScoped)
	assert.Equal(t, []string{"web_search"}, toolNames(llmContext.Tools))
	assert.Empty(t, provider.reqs, "a failed mint must never fall back to the requester's credentials")

	records := auditRecords()
	require.Len(t, records, 1)
	assert.Equal(t, audit.EventAgentScopedSessionGrant, records[0].EventName)
	assert.Equal(t, model.AuditStatusFail, records[0].Status)
	assert.Equal(t, requester.Id, records[0].Actor.UserId)
}
