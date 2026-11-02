// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	scopedAccessAgentID = "agent-1"
	scopedAccessWiki    = "https://wiki.example.com"
	scopedAccessPlugin  = "plugin://com.example.other"
)

func scopedAccessAgent(botUserID string, extras []string) llm.BotConfig {
	return llm.BotConfig{
		ID:                    scopedAccessAgentID,
		Name:                  "scoped",
		BotUserID:             botUserID,
		UseServiceAccountAuth: true,
		ScopedChannelMode:     true,
		ScopedExtraChannelIDs: extras,
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "read_channel"},
			{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "create_post"},
			{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "get_user_channels"},
			{ServerOrigin: scopedAccessPlugin, ToolName: "lookup"},
			{ServerOrigin: scopedBridgeOrigin, ToolName: "get_issue"},
			{ServerOrigin: scopedBridgeOrigin, ToolName: "retired_tool"},
			{ServerOrigin: scopedAccessWiki, ToolName: llm.MCPServerToolWildcard},
			{ServerOrigin: llm.BuiltinToolsOrigin, ToolName: "WebSearch"},
		},
	}
}

func scopedAccessCatalog() []llm.Tool {
	embeddedRead := bridgeMCPTool("mattermost", "read_channel", mcp.EmbeddedClientKey)
	embeddedRead.ReadOnly = true
	embeddedMemberships := bridgeMCPTool("mattermost", "get_user_channels", mcp.EmbeddedClientKey)
	embeddedMemberships.ReadOnly = true
	embeddedMemberships.ScopedUnsupported = true
	return []llm.Tool{
		embeddedRead,
		embeddedMemberships,
		bridgeMCPTool("mattermost", "create_post", mcp.EmbeddedClientKey),
		bridgeMCPTool("other", "lookup", scopedAccessPlugin),
		bridgeMCPTool("jira", "get_issue", scopedBridgeOrigin),
		bridgeMCPTool("wiki", "get_page", scopedAccessWiki),
	}
}

func scopedAccessEnvironment(t *testing.T, cfg llm.BotConfig) *TestEnvironment {
	t.Helper()
	e := SetupTestEnvironment(t)
	t.Cleanup(func() { e.Cleanup(t) })

	e.mockAPI.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
	manager := mcp.NewClientManager(mcp.Config{IdleTimeoutMinutes: 30}, e.client.Log, e.client, nil, nil, http.DefaultClient, nil, mcp.RemoteMCPAlwaysAllowed)
	t.Cleanup(manager.Close)
	manager.SetScopedSessions(&scopedBridgeMinter{})
	catalog := &scopedBridgeCatalog{ClientManager: manager, tools: scopedAccessCatalog()}
	webSearch := llm.Tool{Name: "WebSearch", ScopedOptIn: true}
	fetchSource := llm.Tool{Name: "WebSearchFetchSource", ScopedOptIn: true}
	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(e.client, &testLLMContextToolProvider{tools: []llm.Tool{webSearch, fetchSource}}, catalog, scopedBridgeConfig{channelToolCalling: true})

	botUserID := model.NewId()
	cfg.BotUserID = botUserID
	e.agentStore.agents[scopedAccessAgentID] = &cfg
	e.setBots(bots.NewBot(cfg, llm.ServiceConfig{}, &model.Bot{UserId: botUserID, Username: "scoped"}, nil))
	return e
}

type scopedAccessResponse struct {
	Configured bool     `json:"configured"`
	Active     bool     `json:"active"`
	Blockers   []string `json:"blockers"`
	Channels   struct {
		TriggeringTeamPublicChannels bool `json:"triggeringTeamPublicChannels"`
		OpenTeams                    struct {
			Enabled        bool   `json:"enabled"`
			DisabledReason string `json:"disabledReason"`
			OpenCount      int    `json:"openCount"`
			ClosedCount    int    `json:"closedCount"`
			Open           []struct {
				ID string `json:"id"`
			} `json:"open"`
			Closed []struct {
				ID      string   `json:"id"`
				Reasons []string `json:"reasons"`
			} `json:"closed"`
		} `json:"openTeams"`
		ExtraChannels []struct {
			ID       string `json:"id"`
			TeamName string `json:"teamName"`
			Status   string `json:"status"`
		} `json:"extraChannels"`
	} `json:"channels"`
	Tools []struct {
		ServerOrigin string `json:"serverOrigin"`
		Name         string `json:"name"`
		Offered      bool   `json:"offered"`
		Reason       string `json:"reason"`
	} `json:"tools"`
}

func TestScopedAccessReport(t *testing.T) {
	openID, inviteOnlyID, domainID, groupID, policyID := model.NewId(), model.NewId(), model.NewId(), model.NewId(), model.NewId()
	teams := []*model.Team{
		{Id: openID, DisplayName: "Open", AllowOpenInvite: true},
		{Id: inviteOnlyID, DisplayName: "Invite only"},
		{Id: domainID, DisplayName: "Domain", AllowOpenInvite: true, AllowedDomains: "example.com"},
		{Id: groupID, DisplayName: "Group", AllowOpenInvite: true, GroupConstrained: model.NewPointer(true)},
		{Id: policyID, DisplayName: "Policy", AllowOpenInvite: true, PolicyEnforced: true},
	}
	readableID, archivedID, missingID, dmID := model.NewId(), model.NewId(), model.NewId(), model.NewId()

	tests := []struct {
		name         string
		compliance   bool
		wantEnabled  bool
		wantReason   string
		wantOpen     []string
		wantClosed   map[string][]string
		wantClosedN  int
		wantStatuses map[string]string
	}{
		{
			name:        "open-team grant on",
			wantEnabled: true,
			wantOpen:    []string{openID},
			wantClosed: map[string][]string{
				inviteOnlyID: {"invite_only"},
				domainID:     {"domain_restricted"},
				groupID:      {"group_constrained"},
				policyID:     {"policy_enforced"},
			},
			wantClosedN: 4,
		},
		{
			name:       "compliance mode turns the open-team grant off",
			compliance: true,
			wantReason: "compliance_mode",
			wantClosed: map[string][]string{},
			// No team counts as open, and none is listed with a reason.
			wantClosedN: 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := scopedAccessEnvironment(t, scopedAccessAgent("", []string{readableID, archivedID, missingID, dmID}))
			e.OverrideConfig(&model.Config{ComplianceSettings: model.ComplianceSettings{Enable: model.NewPointer(tt.compliance)}})
			e.mockAPI.On("RolesGrantPermission", []string{model.SystemUserRoleId}, model.PermissionJoinPublicTeams.Id).Return(true).Maybe()
			e.mockAPI.On("GetTeams").Return(teams, nil)
			e.mockAPI.On("GetTeam", openID).Return(teams[0], nil).Maybe()
			e.mockAPI.On("GetChannel", readableID).Return(&model.Channel{Id: readableID, TeamId: openID, Type: model.ChannelTypePrivate, DisplayName: "Private"}, nil)
			e.mockAPI.On("GetChannel", archivedID).Return(&model.Channel{Id: archivedID, TeamId: openID, Type: model.ChannelTypeOpen, DeleteAt: 1}, nil)
			e.mockAPI.On("GetChannel", missingID).Return(nil, model.NewAppError("GetChannel", "app.channel.missing", nil, "", http.StatusNotFound))
			e.mockAPI.On("GetChannel", dmID).Return(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}, nil)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true)

			recorder := doRequest(e.api, http.MethodGet, "/admin/agents/"+scopedAccessAgentID+"/scoped_access", nil, testUserID)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var report scopedAccessResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &report))

			assert.True(t, report.Configured)
			assert.True(t, report.Active)
			assert.Empty(t, report.Blockers)
			assert.True(t, report.Channels.TriggeringTeamPublicChannels)

			openTeams := report.Channels.OpenTeams
			assert.Equal(t, tt.wantEnabled, openTeams.Enabled)
			assert.Equal(t, tt.wantReason, openTeams.DisabledReason)
			assert.Equal(t, len(tt.wantOpen), openTeams.OpenCount)
			assert.Equal(t, tt.wantClosedN, openTeams.ClosedCount)
			var open []string
			for _, team := range openTeams.Open {
				open = append(open, team.ID)
			}
			assert.ElementsMatch(t, tt.wantOpen, open)
			closed := map[string][]string{}
			for _, team := range openTeams.Closed {
				closed[team.ID] = team.Reasons
			}
			assert.Equal(t, tt.wantClosed, closed)

			statuses := map[string]string{}
			for _, channel := range report.Channels.ExtraChannels {
				statuses[channel.ID] = channel.Status
			}
			assert.Equal(t, map[string]string{
				readableID: llmcontext.ScopedExtraChannelReadable,
				archivedID: llmcontext.ScopedExtraChannelArchived,
				missingID:  llmcontext.ScopedExtraChannelMissing,
				dmID:       llmcontext.ScopedExtraChannelUnsupportedType,
			}, statuses)
			assert.Equal(t, "Open", report.Channels.ExtraChannels[0].TeamName)

			type toolKey struct{ origin, name string }
			tools := map[toolKey]string{}
			offered := map[toolKey]bool{}
			for _, tool := range report.Tools {
				tools[toolKey{tool.ServerOrigin, tool.Name}] = tool.Reason
				offered[toolKey{tool.ServerOrigin, tool.Name}] = tool.Offered
			}
			assert.Equal(t, map[toolKey]string{
				{mcp.EmbeddedClientKey, "read_channel"}:          "",
				{mcp.EmbeddedClientKey, "create_post"}:           "write_tool",
				{mcp.EmbeddedClientKey, "get_user_channels"}:     "needs_wider_access",
				{scopedAccessPlugin, "lookup"}:                   "plugin_server",
				{scopedBridgeOrigin, "get_issue"}:                "",
				{scopedBridgeOrigin, "retired_tool"}:             "unavailable",
				{scopedAccessWiki, llm.MCPServerToolWildcard}:    "not_named",
				{llm.BuiltinToolsOrigin, "WebSearch"}:            "",
				{llm.BuiltinToolsOrigin, "WebSearchFetchSource"}: "not_opted_in",
			}, tools)
			assert.Equal(t, map[toolKey]bool{
				{mcp.EmbeddedClientKey, "read_channel"}:          true,
				{mcp.EmbeddedClientKey, "create_post"}:           false,
				{mcp.EmbeddedClientKey, "get_user_channels"}:     false,
				{scopedAccessPlugin, "lookup"}:                   false,
				{scopedBridgeOrigin, "get_issue"}:                true,
				{scopedBridgeOrigin, "retired_tool"}:             false,
				{scopedAccessWiki, llm.MCPServerToolWildcard}:    false,
				{llm.BuiltinToolsOrigin, "WebSearch"}:            true,
				{llm.BuiltinToolsOrigin, "WebSearchFetchSource"}: false,
			}, offered)
		})
	}
}

func TestScopedAccessReportRequests(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		admin      bool
		wantStatus int
	}{
		{name: "not a system admin", path: "/admin/agents/" + scopedAccessAgentID + "/scoped_access", wantStatus: http.StatusForbidden},
		{name: "unknown agent", path: "/admin/agents/missing/scoped_access", admin: true, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := scopedAccessEnvironment(t, scopedAccessAgent("", nil))
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(tt.admin)
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			recorder := doRequest(e.api, http.MethodGet, tt.path, nil, testUserID)
			assert.Equal(t, tt.wantStatus, recorder.Code)
		})
	}
}

func TestScopedAccessReportBlockers(t *testing.T) {
	cfg := scopedAccessAgent("", nil)
	cfg.UseServiceAccountAuth = false
	e := scopedAccessEnvironment(t, cfg)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true)
	e.mockAPI.On("GetTeams").Return([]*model.Team{}, nil)

	recorder := doRequest(e.api, http.MethodGet, "/admin/agents/"+scopedAccessAgentID+"/scoped_access", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var report scopedAccessResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &report))

	assert.True(t, report.Configured)
	assert.False(t, report.Active)
	assert.Equal(t, []string{"service_account_off"}, report.Blockers)
	for _, tool := range report.Tools {
		assert.Equal(t, tool.ServerOrigin == llm.BuiltinToolsOrigin && tool.Name == "WebSearch", tool.Offered, "an agent without service accounts has no MCP catalog: %s", tool.Name)
	}
}
