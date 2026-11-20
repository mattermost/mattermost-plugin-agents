// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentsetup"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	prompttemplates "github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	setupAgentID       = "setupagent0000000000000000"
	setupBotUserID     = "setupbot000000000000000000"
	setupInstructions  = "SENTINEL-ORIGINAL-INSTRUCTIONS"
	setupNewPromptText = "SENTINEL-NEW-INSTRUCTIONS"
)

type autoSetupConfig struct{ enabled bool }

func (c *autoSetupConfig) EnableAgentAutoSetup() bool { return c.enabled }

// recordingThreads stands in for conversations.StartAgentThread and records
// what the setup service asked for.
type recordingThreads struct {
	mu       sync.Mutex
	requests []conversations.AgentThreadRequest
}

func (r *recordingThreads) StartAgentThread(_ context.Context, req conversations.AgentThreadRequest) (*conversations.AgentThread, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	return &conversations.AgentThread{
		RootPost:  &model.Post{Id: "setuproot00000000000000000", ChannelId: "setupdm0000000000000000000", UserId: req.User.Id, Message: req.Message},
		BotUserID: req.Bot.BotUserID(),
	}, nil
}

// testExtension claims every agent and auto-runs the named setup tools.
type testExtension struct {
	autoRun      []string
	instructions string
}

func (x *testExtension) Claims(*llm.BotConfig) bool { return true }
func (x *testExtension) AutoExecute(_ *llm.BotConfig, toolName string) bool {
	return slices.Contains(x.autoRun, toolName)
}
func (x *testExtension) Instructions(*llm.BotConfig) string { return x.instructions }
func (x *testExtension) Tools(context.Context, *llm.BotConfig, *llm.Context) []llm.Tool {
	return []llm.Tool{{Name: "extension_tool", Schema: llm.NewJSONSchemaFromStruct[struct{}]()}}
}

type setupTestEnv struct {
	*TestEnvironment
	setup   *agentsetup.Service
	config  *autoSetupConfig
	threads *recordingThreads
	bot     *bots.Bot
}

// setupAgentSetupEnv wires the setup service to the API's own agent config
// service, as the plugin does, with one running agent the test user created.
func setupAgentSetupEnv(t *testing.T, enabled bool) *setupTestEnv {
	t.Helper()
	e := setupAgentTestEnvironment(t)
	mockLicensed(e.mockAPI)
	e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Username: "tester", Locale: "en"}, nil).Maybe()
	e.mockAPI.On("HasPermissionTo", mock.Anything, mock.Anything).Return(false).Maybe()
	e.mockAPI.On("PatchBot", mock.Anything, mock.Anything).Return(&model.Bot{}, nil).Maybe()

	agent := &llm.BotConfig{
		ID: setupAgentID, CreatorID: testUserID, BotUserID: setupBotUserID,
		DisplayName: "My Agent", Name: "my-agent", ServiceID: "svc-1",
		CustomInstructions: setupInstructions,
		EnabledNativeTools: []string{"web_search"},
		EnabledMCPTools:    []llm.EnabledMCPTool{{ServerOrigin: "https://mcp.example.com", ToolName: "search"}},
	}
	e.agentStore.agents[setupAgentID] = agent
	bot := bots.NewBot(*agent, llm.ServiceConfig{ID: "svc-1"}, &model.Bot{UserId: setupBotUserID, Username: "my-agent", DisplayName: "My Agent"}, nil)
	e.setBots(bot)

	llmPrompts, err := llm.NewPrompts(prompttemplates.PromptsFolder)
	require.NoError(t, err)
	cfg := &autoSetupConfig{enabled: enabled}
	threads := &recordingThreads{}
	setup := agentsetup.New(agentsetup.Deps{
		Configs: e.api.AgentConfigService(),
		Threads: threads,
		Bots:    e.bots,
		Store:   e.agentStore,
		Config:  cfg,
		Prompts: llmPrompts,
		I18n:    i18n.Init(),
		Audit:   &e.client.Audit,
	})
	e.api.SetAgentSetup(setup)
	return &setupTestEnv{TestEnvironment: e, setup: setup, config: cfg, threads: threads, bot: bot}
}

func requesterDMContext(userID string) *llm.Context {
	return &llm.Context{
		RequestingUser: &model.User{Id: userID},
		Channel:        &model.Channel{Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(setupBotUserID, userID)},
	}
}

func setupTool(t *testing.T, tools []llm.Tool, name string) llm.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	require.FailNowf(t, "setup tool missing", "%s not in the setup tools", name)
	return llm.Tool{}
}

func callSetupTool(t *testing.T, tool llm.Tool, llmCtx *llm.Context, args any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	return tool.Resolver(context.Background(), llmCtx, func(dst any) error { return json.Unmarshal(raw, dst) })
}

func marshaledRecords(t *testing.T, records []*model.AuditRecord) string {
	t.Helper()
	raw, err := json.Marshal(records)
	require.NoError(t, err)
	return string(raw)
}

func TestStartAgentSetup(t *testing.T) {
	tests := []struct {
		name       string
		enabled    bool
		agentID    string
		setup      func(env *setupTestEnv)
		wantStatus int

		wantExtensionPrompt bool
	}{
		{name: "the agent's manager opens a setup thread", enabled: true, agentID: setupAgentID, wantStatus: http.StatusCreated},
		{name: "the setting is off", agentID: setupAgentID, wantStatus: http.StatusForbidden},
		{name: "a project-style extension claims the agent while the setting is off", agentID: setupAgentID, wantStatus: http.StatusCreated, wantExtensionPrompt: true,
			setup: func(env *setupTestEnv) {
				env.setup.RegisterExtension(&testExtension{instructions: "EXTENSION-INSTRUCTIONS"})
			}},
		{name: "someone who cannot manage the agent", enabled: true, agentID: setupAgentID, wantStatus: http.StatusForbidden,
			setup: func(env *setupTestEnv) { env.agentStore.agents[setupAgentID].CreatorID = "someoneelse000000000000000" }},
		{name: "an unknown agent", enabled: true, agentID: "missingagent00000000000000", wantStatus: http.StatusNotFound},
		{name: "an agent that is not running", enabled: true, agentID: setupAgentID, wantStatus: http.StatusConflict,
			setup: func(env *setupTestEnv) { env.bots.SetBotsForTesting(nil) }},
		{name: "auto-setup is not wired", enabled: true, agentID: setupAgentID, wantStatus: http.StatusNotFound,
			setup: func(env *setupTestEnv) { env.api.SetAgentSetup(nil) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupAgentSetupEnv(t, tt.enabled)
			defer env.Cleanup(t)
			if tt.setup != nil {
				tt.setup(env)
			}
			records := env.CaptureAuditRecords()

			recorder := doRequest(env.api, http.MethodPost, "/agents/"+tt.agentID+"/setup", nil, testUserID)
			require.Equal(t, tt.wantStatus, recorder.Code, recorder.Body.String())

			require.Len(t, *records, 1)
			rec := (*records)[0]
			assert.Equal(t, AuditEventStartAgentSetup, rec.EventName)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			assert.Equal(t, tt.agentID, rec.EventData.Parameters[audit.KeyAgentID])
			assert.NotContains(t, marshaledRecords(t, *records), "Help me set up", "the root message never enters the record")
			assert.NotContains(t, marshaledRecords(t, *records), setupInstructions)

			if tt.wantStatus != http.StatusCreated {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Empty(t, env.threads.requests, "no thread is started")
				return
			}
			assert.Equal(t, model.AuditStatusSuccess, rec.Status)
			assert.Equal(t, "setupdm0000000000000000000", rec.EventData.Parameters[audit.KeyChannelID])
			assert.Equal(t, "setuproot00000000000000000", rec.EventData.Parameters[audit.KeyThreadRootPostID])

			var resp agentSetupResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
			assert.Equal(t, agentSetupResponse{ChannelID: "setupdm0000000000000000000", RootPostID: "setuproot00000000000000000", BotUserID: setupBotUserID}, resp)

			require.Len(t, env.threads.requests, 1)
			req := env.threads.requests[0]
			assert.Equal(t, llm.OperationAgentSetup, req.Operation)
			assert.True(t, req.PostAsUser, "the manager is present to approve the agent's changes")
			assert.Nil(t, req.Channel, "setup runs in the manager's DM with the agent")
			assert.Equal(t, testUserID, req.User.Id)
			assert.Equal(t, setupBotUserID, req.Bot.BotUserID())
			assert.Equal(t, "Help me set up My Agent.", req.Message)
			assert.Equal(t, setupAgentID, req.RootProps[agentsetup.RootPostAgentIDProp])

			require.NotNil(t, req.SystemPrompt)
			prompt, err := req.SystemPrompt(&llm.Context{BotName: "My Agent"})
			require.NoError(t, err)
			assert.Contains(t, prompt, "update_my_instructions")
			assert.Equal(t, tt.wantExtensionPrompt, strings.Contains(prompt, "EXTENSION-INSTRUCTIONS"),
				"extension instructions are appended only for claimed agents")
		})
	}
}

func TestListAgentsCanSetUp(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		creatorID string
		want      bool
	}{
		{name: "manager with the setting on", enabled: true, creatorID: testUserID, want: true},
		{name: "manager with the setting off", creatorID: testUserID},
		{name: "a user who cannot manage the agent", enabled: true, creatorID: "someoneelse000000000000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupAgentSetupEnv(t, tt.enabled)
			defer env.Cleanup(t)
			env.agentStore.agents[setupAgentID].CreatorID = tt.creatorID
			env.agentStore.agents[setupAgentID].UserAccessLevel = llm.UserAccessLevelAll

			recorder := doRequest(env.api, http.MethodGet, "/agents", nil, testUserID)
			require.Equal(t, http.StatusOK, recorder.Code)
			var agents []struct {
				ID       string `json:"id"`
				CanSetUp bool   `json:"canSetUp"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &agents))
			require.Len(t, agents, 1)
			assert.Equal(t, tt.want, agents[0].CanSetUp)
		})
	}
}

func TestSetupToolsOffered(t *testing.T) {
	writeTools := []string{agentsetup.ToolUpdateMyInstructions, agentsetup.ToolSetMyDisplayName, agentsetup.ToolDisableMyTools, agentsetup.ToolCompleteSetup}
	tests := []struct {
		name        string
		enabled     bool
		extension   *testExtension
		llmCtx      *llm.Context
		creatorID   string
		wantOffered bool
		wantAutoRun []string
	}{
		{name: "regular agent: writes ask for approval", enabled: true, llmCtx: requesterDMContext(testUserID), wantOffered: true,
			wantAutoRun: []string{agentsetup.ToolGetMyConfig}},
		{name: "setting off", llmCtx: requesterDMContext(testUserID)},
		{name: "an extension auto-runs the tools it chooses", extension: &testExtension{autoRun: []string{agentsetup.ToolUpdateMyInstructions, agentsetup.ToolCompleteSetup}},
			llmCtx: requesterDMContext(testUserID), wantOffered: true,
			wantAutoRun: []string{agentsetup.ToolGetMyConfig, agentsetup.ToolUpdateMyInstructions, agentsetup.ToolCompleteSetup}},
		{name: "outside the requester's DM with the agent", enabled: true, llmCtx: &llm.Context{
			RequestingUser: &model.User{Id: testUserID},
			Channel:        &model.Channel{Type: model.ChannelTypeOpen, Name: "town-square"},
		}},
		{name: "another user's DM with the agent", enabled: true, llmCtx: &llm.Context{
			RequestingUser: &model.User{Id: testUserID},
			Channel:        &model.Channel{Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(setupBotUserID, "someoneelse000000000000000")},
		}},
		{name: "a requester who cannot manage the agent", enabled: true, llmCtx: requesterDMContext(testUserID), creatorID: "someoneelse000000000000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupAgentSetupEnv(t, tt.enabled)
			defer env.Cleanup(t)
			if tt.extension != nil {
				env.setup.RegisterExtension(tt.extension)
			}
			bot := env.bot
			if tt.creatorID != "" {
				cfg := bot.GetConfig()
				cfg.CreatorID = tt.creatorID
				bot = bots.NewBot(cfg, bot.GetService(), bot.GetMMBot(), nil)
			}

			tools := env.setup.OperationTools(context.Background(), bot, tt.llmCtx)
			if !tt.wantOffered {
				assert.Empty(t, tools)
				return
			}
			var autoRun []string
			for _, tool := range tools {
				if tool.AutoExecute {
					autoRun = append(autoRun, tool.Name)
				}
				assert.Empty(t, tool.ServerOrigin, "setup tools are built-ins")
			}
			assert.ElementsMatch(t, tt.wantAutoRun, autoRun)
			for _, name := range writeTools {
				setupTool(t, tools, name)
			}
			if tt.extension != nil {
				setupTool(t, tools, "extension_tool")
			}
		})
	}
}

func TestSetupToolWrites(t *testing.T) {
	tests := []struct {
		name          string
		tool          string
		args          any
		wantErr       string
		wantChanged   []string
		checkAgent    func(t *testing.T, agent *llm.BotConfig)
		wantNoVersion bool
	}{
		{
			name: "update_my_instructions replaces the instructions", tool: agentsetup.ToolUpdateMyInstructions,
			args:        map[string]any{"instructions": setupNewPromptText},
			wantChanged: []string{"customInstructions"},
			checkAgent: func(t *testing.T, agent *llm.BotConfig) {
				assert.Equal(t, setupNewPromptText, agent.CustomInstructions)
			},
		},
		{
			name: "set_my_display_name trims and saves the name", tool: agentsetup.ToolSetMyDisplayName,
			args:        map[string]any{"display_name": "  Helper  "},
			wantChanged: []string{"displayName"},
			checkAgent: func(t *testing.T, agent *llm.BotConfig) {
				assert.Equal(t, "Helper", agent.DisplayName)
				assert.Equal(t, "my-agent", agent.Name, "the username never changes")
			},
		},
		{
			name: "set_my_display_name refuses an empty name", tool: agentsetup.ToolSetMyDisplayName,
			args: map[string]any{"display_name": "   "}, wantErr: "display_name must not be empty", wantNoVersion: true,
		},
		{
			name: "disable_my_tools narrows native and MCP tools", tool: agentsetup.ToolDisableMyTools,
			args: map[string]any{
				"native_tools": []string{"web_search"},
				"mcp_tools":    []map[string]string{{"server_origin": "https://mcp.example.com", "tool_name": "search"}},
			},
			wantChanged: []string{"enabledMCPTools", "enabledNativeTools"},
			checkAgent: func(t *testing.T, agent *llm.BotConfig) {
				assert.Empty(t, agent.EnabledNativeTools)
				assert.Empty(t, agent.EnabledMCPTools)
				assert.False(t, agent.DisableTools)
			},
		},
		{
			name: "disable_my_tools accepts repeated names", tool: agentsetup.ToolDisableMyTools,
			args:        map[string]any{"native_tools": []string{"web_search", " web_search "}},
			wantChanged: []string{"enabledNativeTools"},
			checkAgent: func(t *testing.T, agent *llm.BotConfig) {
				assert.Empty(t, agent.EnabledNativeTools)
				assert.Len(t, agent.EnabledMCPTools, 1, "MCP tools are untouched")
			},
		},
		{
			name: "disable_my_tools refuses an empty tool name", tool: agentsetup.ToolDisableMyTools,
			args:    map[string]any{"native_tools": []string{"web_search", ""}},
			wantErr: "empty name", wantNoVersion: true,
		},
		{
			name: "disable_my_tools refuses an MCP tool without a name", tool: agentsetup.ToolDisableMyTools,
			args:    map[string]any{"mcp_tools": []map[string]string{{"server_origin": "https://mcp.example.com", "tool_name": " "}}},
			wantErr: "tool_name", wantNoVersion: true,
		},
		{
			name: "disable_my_tools turns every tool off", tool: agentsetup.ToolDisableMyTools,
			args: map[string]any{"all": true}, wantChanged: []string{"disableTools"},
			checkAgent: func(t *testing.T, agent *llm.BotConfig) { assert.True(t, agent.DisableTools) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupAgentSetupEnv(t, true)
			defer env.Cleanup(t)
			records := env.CaptureAuditRecords()
			tools := env.setup.OperationTools(context.Background(), env.bot, requesterDMContext(testUserID))

			result, err := callSetupTool(t, setupTool(t, tools, tt.tool), requesterDMContext(testUserID), tt.args)
			versions := env.agentStore.versions[setupAgentID]
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, versions)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, result, "restore")
			tt.checkAgent(t, env.agentStore.agents[setupAgentID])

			require.NotEmpty(t, versions)
			latest := versions[len(versions)-1]
			assert.Equal(t, store.AgentVersionSourceSelfSetup, latest.Source)
			assert.Equal(t, testUserID, latest.CreatedBy)
			assert.ElementsMatch(t, tt.wantChanged, latest.ChangedFields)

			require.Len(t, *records, 1)
			rec := (*records)[0]
			assert.Equal(t, agentsetup.AuditEventAgentSelfSetup, rec.EventName)
			assert.Equal(t, model.AuditStatusSuccess, rec.Status)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			assert.Equal(t, setupAgentID, rec.EventData.Parameters[audit.KeyAgentID])
			assert.Equal(t, tt.tool, rec.EventData.Parameters[audit.KeyToolName])
			marshaled := marshaledRecords(t, *records)
			assert.NotContains(t, marshaled, setupNewPromptText)
			assert.NotContains(t, marshaled, setupInstructions)
			assert.NotContains(t, marshaled, "Helper")
		})
	}
}

func TestSetupToolsRefuseWideningAndRecheckAuthorization(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(env *setupTestEnv)
		tool    string
		args    any
		wantErr string
	}{
		{
			name:    "permission revoked after the tools were offered",
			prepare: func(env *setupTestEnv) { env.agentStore.agents[setupAgentID].CreatorID = "someoneelse000000000000000" },
			tool:    agentsetup.ToolUpdateMyInstructions, args: map[string]any{"instructions": setupNewPromptText},
			wantErr: "not allowed",
		},
		{
			name:    "the agent was deleted after the tools were offered",
			prepare: func(env *setupTestEnv) { env.agentStore.agents[setupAgentID].DeleteAt = 1 },
			tool:    agentsetup.ToolSetMyDisplayName, args: map[string]any{"display_name": "Helper"},
			wantErr: "not allowed",
		},
		{
			name: "turning off one MCP tool of an agent granted every MCP tool",
			prepare: func(env *setupTestEnv) {
				env.agentStore.agents[setupAgentID].AutoEnableNewMCPTools = true
			},
			tool:    agentsetup.ToolDisableMyTools,
			args:    map[string]any{"mcp_tools": []map[string]string{{"server_origin": "https://mcp.example.com", "tool_name": "other"}}},
			wantErr: "single MCP tools cannot be turned off",
		},
		{
			name:    "a service account agent's tool grants stay admin-only",
			prepare: func(env *setupTestEnv) { env.agentStore.agents[setupAgentID].UseServiceAccountAuth = true },
			tool:    agentsetup.ToolDisableMyTools,
			args:    map[string]any{"mcp_tools": []map[string]string{{"server_origin": "https://mcp.example.com", "tool_name": "search"}}},
			wantErr: "service account",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupAgentSetupEnv(t, true)
			defer env.Cleanup(t)
			records := env.CaptureAuditRecords()
			tools := env.setup.OperationTools(context.Background(), env.bot, requesterDMContext(testUserID))
			require.NotEmpty(t, tools)
			before := *cloneBotConfig(env.agentStore.agents[setupAgentID])
			tt.prepare(env)
			prepared := *cloneBotConfig(env.agentStore.agents[setupAgentID])

			_, err := callSetupTool(t, setupTool(t, tools, tt.tool), requesterDMContext(testUserID), tt.args)
			require.ErrorContains(t, err, tt.wantErr)
			assert.Equal(t, prepared, *env.agentStore.agents[setupAgentID], "nothing is written")
			assert.Equal(t, before.CustomInstructions, prepared.CustomInstructions)
			assert.Empty(t, env.agentStore.versions[setupAgentID])

			require.Len(t, *records, 1)
			assert.Equal(t, model.AuditStatusFail, (*records)[0].Status)
		})
	}
}

func TestSelfSetupVersionIsUndoneByRestore(t *testing.T) {
	env := setupAgentSetupEnv(t, true)
	defer env.Cleanup(t)
	tools := env.setup.OperationTools(context.Background(), env.bot, requesterDMContext(testUserID))

	_, err := callSetupTool(t, setupTool(t, tools, agentsetup.ToolUpdateMyInstructions), requesterDMContext(testUserID),
		map[string]any{"instructions": setupNewPromptText})
	require.NoError(t, err)
	require.Equal(t, setupNewPromptText, env.agentStore.agents[setupAgentID].CustomInstructions)

	recorder := doRequest(env.api, http.MethodGet, "/agents/"+setupAgentID+"/versions", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Code)
	var page store.AgentVersionPage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &page))
	require.Len(t, page.Versions, 2)
	assert.Equal(t, store.AgentVersionSourceSelfSetup, page.Versions[0].Source)
	original := page.Versions[1].Version

	recorder = doRequest(env.api, http.MethodPost, fmt.Sprintf("/agents/%s/versions/%d/restore", setupAgentID, original), nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, setupInstructions, env.agentStore.agents[setupAgentID].CustomInstructions)
}

func TestGetMyConfigAndCompleteSetup(t *testing.T) {
	env := setupAgentSetupEnv(t, true)
	defer env.Cleanup(t)
	records := env.CaptureAuditRecords()
	tools := env.setup.OperationTools(context.Background(), env.bot, requesterDMContext(testUserID))

	result, err := callSetupTool(t, setupTool(t, tools, agentsetup.ToolGetMyConfig), requesterDMContext(testUserID), map[string]any{})
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(result), &cfg))
	assert.Equal(t, "My Agent", cfg["display_name"])
	assert.Equal(t, setupInstructions, cfg["instructions"])
	assert.Equal(t, false, cfg["setup_completed"])
	assert.Empty(t, *records, "reading the configuration is not a write")

	_, err = callSetupTool(t, setupTool(t, tools, agentsetup.ToolCompleteSetup), requesterDMContext(testUserID), map[string]any{})
	require.NoError(t, err)
	assert.NotZero(t, env.agentStore.agents[setupAgentID].SetupCompletedAt)
	assert.Empty(t, env.agentStore.versions[setupAgentID], "completing setup is not a configuration version")
	require.Len(t, *records, 1)
	assert.Equal(t, agentsetup.AuditEventAgentSelfSetup, (*records)[0].EventName)
	assert.Equal(t, agentsetup.ToolCompleteSetup, (*records)[0].EventData.Parameters[audit.KeyToolName])

	env.agentStore.agents[setupAgentID].CreatorID = "someoneelse000000000000000"
	_, err = callSetupTool(t, setupTool(t, tools, agentsetup.ToolGetMyConfig), requesterDMContext(testUserID), map[string]any{})
	require.ErrorContains(t, err, "not allowed", "reads are re-authorized too")
}

func TestSetupToolWriteKeepsAConcurrentManagerSave(t *testing.T) {
	env := setupAgentSetupEnv(t, true)
	defer env.Cleanup(t)
	tools := env.setup.OperationTools(context.Background(), env.bot, requesterDMContext(testUserID))

	raced := false
	env.agentStore.beforeConditionalUpdate = func() {
		if raced {
			return
		}
		raced = true
		stored := env.agentStore.agents[setupAgentID]
		stored.EnabledNativeTools = nil
		stored.UpdateAt++
	}

	_, err := callSetupTool(t, setupTool(t, tools, agentsetup.ToolUpdateMyInstructions), requesterDMContext(testUserID),
		map[string]any{"instructions": setupNewPromptText})
	require.NoError(t, err)

	stored := env.agentStore.agents[setupAgentID]
	assert.True(t, raced)
	assert.Equal(t, setupNewPromptText, stored.CustomInstructions)
	assert.Empty(t, stored.EnabledNativeTools, "the manager's concurrent change must not be overwritten")
}
