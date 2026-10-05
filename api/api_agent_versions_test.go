// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const versionTestAgentID = "agent-1"

// seedVersionedAgent stores an agent owned by testUserID and saves one edit
// through PUT /agents/:agentid, so it has version 1 (original state) and
// version 2 (the edit).
func seedVersionedAgent(t *testing.T, e *TestEnvironment, original *llm.BotConfig, edit map[string]any) {
	t.Helper()
	e.agentStore.agents[original.ID] = original
	e.mockAPI.On("PatchBot", original.BotUserID, mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
	recorder := doRequest(e.api, http.MethodPut, "/agents/"+original.ID, updateAgentBodyFromStored(original, edit), testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())
}

func versionTestAgent() *llm.BotConfig {
	return &llm.BotConfig{
		ID:                 versionTestAgentID,
		CreatorID:          testUserID,
		BotUserID:          "bot-1",
		DisplayName:        "Original",
		Name:               "my-agent",
		ServiceID:          "svc-1",
		CustomInstructions: "original instructions",
		MaxToolTurns:       10,
	}
}

func TestAgentVersionRoutesRequireManage(t *testing.T) {
	routes := []struct {
		name   string
		method string
		path   string
	}{
		{name: "list versions", method: http.MethodGet, path: "/agents/agent-1/versions"},
		{name: "get version", method: http.MethodGet, path: "/agents/agent-1/versions/1"},
		{name: "restore version", method: http.MethodPost, path: "/agents/agent-1/versions/1/restore"},
		{name: "export", method: http.MethodGet, path: "/agents/agent-1/export"},
	}
	callers := []struct {
		name           string
		seed           bool
		expectedStatus int
	}{
		{name: "missing agent", seed: false, expectedStatus: http.StatusNotFound},
		{name: "caller cannot manage the agent", seed: true, expectedStatus: http.StatusForbidden},
	}

	for _, route := range routes {
		for _, caller := range callers {
			t.Run(route.name+"/"+caller.name, func(t *testing.T) {
				e := setupAgentTestEnvironment(t)
				defer e.Cleanup(t)
				mockLicensed(e.mockAPI)
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false).Maybe()

				if caller.seed {
					agent := versionTestAgent()
					agent.CreatorID = "someone-else"
					agent.CustomInstructions = "secret instructions of someone else"
					e.agentStore.agents[agent.ID] = agent
					require.NoError(t, e.agentStore.UpdateAgent(agent, store.AgentVersionMeta{Source: store.AgentVersionSourceUpdate}))
				}

				recorder := doRequest(e.api, route.method, route.path, nil, testUserID)
				require.Equal(t, caller.expectedStatus, recorder.Result().StatusCode)
				assert.NotContains(t, recorder.Body.String(), "secret instructions")
			})
		}
	}
}

func TestListAndGetAgentVersions(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)
	mockLicensed(e.mockAPI)

	seedVersionedAgent(t, e, versionTestAgent(), map[string]any{
		"displayName":        "Edited",
		"customInstructions": "edited instructions",
	})

	recorder := doRequest(e.api, http.MethodGet, "/agents/agent-1/versions", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	var list agentVersionListResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &list))
	assert.Equal(t, 2, list.CurrentVersion)
	require.Len(t, list.Versions, 2)
	assert.Equal(t, 2, list.Versions[0].Version)
	assert.Equal(t, testUserID, list.Versions[0].CreatedBy)
	assert.Equal(t, store.AgentVersionSourceUpdate, list.Versions[0].Source)
	assert.Equal(t, []string{"customInstructions", "displayName"}, list.Versions[0].ChangedFields)
	assert.Equal(t, 1, list.Versions[1].Version)
	assert.Equal(t, store.AgentVersionSourceInitial, list.Versions[1].Source)

	tests := []struct {
		name                 string
		path                 string
		expectedStatus       int
		expectedInstructions string
	}{
		{name: "original version", path: "/agents/agent-1/versions/1", expectedStatus: http.StatusOK, expectedInstructions: "original instructions"},
		{name: "current version", path: "/agents/agent-1/versions/2", expectedStatus: http.StatusOK, expectedInstructions: "edited instructions"},
		{name: "unknown version", path: "/agents/agent-1/versions/3", expectedStatus: http.StatusNotFound},
		{name: "non-integer version", path: "/agents/agent-1/versions/latest", expectedStatus: http.StatusBadRequest},
		{name: "zero version", path: "/agents/agent-1/versions/0", expectedStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := doRequest(e.api, http.MethodGet, tt.path, nil, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			if tt.expectedStatus != http.StatusOK {
				return
			}
			var detail store.AgentVersionDetail
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &detail))
			assert.Equal(t, tt.expectedInstructions, detail.Config.CustomInstructions)
			assert.Equal(t, "my-agent", detail.Config.Name)
		})
	}
}

func TestRestoreAgentVersion(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(t *testing.T, e *TestEnvironment)
		path           string
		expectedStatus int
		errorContains  string
		validate       func(t *testing.T, e *TestEnvironment, resp *llm.BotConfig)
	}{
		{
			name: "restore writes the version back as a new latest version",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedVersionedAgent(t, e, versionTestAgent(), map[string]any{
					"displayName":        "Edited",
					"customInstructions": "edited instructions",
					"maxToolTurns":       99,
				})
			},
			path:           "/agents/agent-1/versions/1/restore",
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, e *TestEnvironment, resp *llm.BotConfig) {
				assert.Equal(t, "original instructions", resp.CustomInstructions)
				assert.Equal(t, "Original", resp.DisplayName)
				assert.Equal(t, 10, resp.MaxToolTurns)
				assert.Equal(t, "my-agent", resp.Name)

				stored := e.agentStore.agents[versionTestAgentID]
				assert.Equal(t, "original instructions", stored.CustomInstructions)
				assert.Equal(t, "bot-1", stored.BotUserID, "identity is untouched")

				versions, err := e.agentStore.ListAgentVersions(versionTestAgentID)
				require.NoError(t, err)
				require.Len(t, versions, 3, "history is never rewritten")
				assert.Equal(t, store.AgentVersionSourceRestore, versions[0].Source)
				assert.Equal(t, 1, versions[0].RestoredFromVersion)
				assert.Equal(t, testUserID, versions[0].CreatedBy)
				assert.Equal(t, []string{"customInstructions", "displayName", "maxToolTurns"}, versions[0].ChangedFields)
			},
		},
		{
			name: "version whose AI service was deleted cannot be restored",
			setup: func(t *testing.T, e *TestEnvironment) {
				cfgStore := e.api.configStore.(*mockConfigStore)
				cfgStore.cfg.Services = append(cfgStore.cfg.Services, llm.ServiceConfig{ID: "svc-old", Name: "Old", Type: "openai", APIKey: "k"})
				original := versionTestAgent()
				original.ServiceID = "svc-old"
				seedVersionedAgent(t, e, original, map[string]any{"serviceID": "svc-1"})
				cfgStore.cfg.Services = cfgStore.cfg.Services[:1]
			},
			path:           "/agents/agent-1/versions/1/restore",
			expectedStatus: http.StatusBadRequest,
			errorContains:  "AI service that no longer exists",
			validate: func(t *testing.T, e *TestEnvironment, _ *llm.BotConfig) {
				assert.Equal(t, "svc-1", e.agentStore.agents[versionTestAgentID].ServiceID)
				latest, err := e.agentStore.GetLatestAgentVersion(versionTestAgentID)
				require.NoError(t, err)
				assert.Equal(t, 2, latest, "a rejected restore writes no version")
			},
		},
		{
			name: "restoring service account auth is admin-only like an update",
			setup: func(t *testing.T, e *TestEnvironment) {
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
				original := versionTestAgent()
				original.UseServiceAccountAuth = true
				seedVersionedAgent(t, e, original, map[string]any{"useServiceAccountAuth": false})
			},
			path:           "/agents/agent-1/versions/1/restore",
			expectedStatus: http.StatusForbidden,
			errorContains:  "service account authentication",
			validate: func(t *testing.T, e *TestEnvironment, _ *llm.BotConfig) {
				assert.False(t, e.agentStore.agents[versionTestAgentID].UseServiceAccountAuth)
			},
		},
		{
			name: "unknown version",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedVersionedAgent(t, e, versionTestAgent(), map[string]any{"customInstructions": "edited"})
			},
			path:           "/agents/agent-1/versions/9/restore",
			expectedStatus: http.StatusNotFound,
		},
		{
			name: "non-integer version",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedVersionedAgent(t, e, versionTestAgent(), map[string]any{"customInstructions": "edited"})
			},
			path:           "/agents/agent-1/versions/one/restore",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			mockLicensed(e.mockAPI)
			tt.setup(t, e)

			recorder := doRequest(e.api, http.MethodPost, tt.path, nil, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			var resp llm.BotConfig
			if tt.expectedStatus == http.StatusOK {
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
			}
			if tt.errorContains != "" {
				var errResp agentErrorResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &errResp))
				assert.Contains(t, errResp.Error, tt.errorContains)
			}
			if tt.validate != nil {
				tt.validate(t, e, &resp)
			}
		})
	}
}

func TestAuditRestoreAgentVersion(t *testing.T) {
	const plantedInstructions = "planted-restored-instructions-that-must-never-leak"

	tests := []struct {
		name           string
		setup          func(t *testing.T, e *TestEnvironment)
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name: "success records the agent, version and changed field names only",
			setup: func(t *testing.T, e *TestEnvironment) {
				original := versionTestAgent()
				original.CustomInstructions = plantedInstructions
				seedVersionedAgent(t, e, original, map[string]any{"customInstructions": "edited"})
			},
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, versionTestAgentID, rec.EventData.Parameters[audit.KeyAgentID])
				assert.Equal(t, "my-agent", rec.EventData.Parameters[audit.KeyAgentName])
				assert.Equal(t, 1, rec.EventData.Parameters["version"])
				assert.Equal(t, []string{"customInstructions"}, rec.EventData.Parameters["changed_fields"])
			},
		},
		{
			name: "non-manager records a 403 fail identifying the target",
			setup: func(t *testing.T, e *TestEnvironment) {
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false)
				agent := versionTestAgent()
				agent.CreatorID = "someone-else"
				agent.CustomInstructions = plantedInstructions
				e.agentStore.agents[agent.ID] = agent
			},
			expectedStatus: http.StatusForbidden,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusForbidden, rec.Error.Code)
				assert.Equal(t, versionTestAgentID, rec.EventData.Parameters[audit.KeyAgentID])
				assert.Equal(t, "my-agent", rec.EventData.Parameters[audit.KeyAgentName])
				assert.NotContains(t, rec.EventData.Parameters, "changed_fields")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			mockLicensed(e.mockAPI)
			tt.setup(t, e)
			records := e.CaptureAuditRecords()

			recorder := doRequest(e.api, http.MethodPost, "/agents/agent-1/versions/1/restore", nil, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventRestoreAgentVersion, rec.EventName)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			tt.validateRecord(t, rec)

			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), plantedInstructions, "audit record must never carry custom instructions")
		})
	}
}

func TestAgentVersionReadsAreNotAudited(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)
	mockLicensed(e.mockAPI)
	seedVersionedAgent(t, e, versionTestAgent(), map[string]any{"customInstructions": "edited"})
	records := e.CaptureAuditRecords()

	for _, path := range []string{"/agents/agent-1/versions", "/agents/agent-1/versions/1", "/agents/agent-1/export"} {
		recorder := doRequest(e.api, http.MethodGet, path, nil, testUserID)
		require.Equal(t, http.StatusOK, recorder.Result().StatusCode, path)
	}
	assert.Empty(t, *records)
}

func TestRequestFieldsFromConfigRoundTrip(t *testing.T) {
	cfg := llm.BotConfig{
		DisplayName:             "Display",
		ServiceID:               "svc",
		CustomInstructions:      "instructions",
		ChannelAccessLevel:      llm.ChannelAccessLevelAllow,
		ChannelIDs:              []string{"c"},
		UserAccessLevel:         llm.UserAccessLevelBlock,
		UserIDs:                 []string{"u"},
		TeamIDs:                 []string{"t"},
		AdminUserIDs:            []string{"a"},
		EnabledMCPTools:         []llm.EnabledMCPTool{{ServerOrigin: "o", ToolName: "n"}},
		AutoEnableNewMCPTools:   true,
		MCPDynamicToolLoading:   true,
		UseServiceAccountAuth:   true,
		Model:                   "m",
		EnableVision:            true,
		DisableTools:            true,
		EnabledNativeTools:      []string{"web_search"},
		ReasoningEnabled:        true,
		ReasoningEffort:         "high",
		ThinkingBudget:          1024,
		StructuredOutputEnabled: true, //nolint:staticcheck // deprecated field is still persisted verbatim
		MaxToolTurns:            5,
	}

	fields := requestFieldsFromConfig(&cfg)
	var applied llm.BotConfig
	fields.applyTo(&applied)
	assert.Equal(t, cfg, applied, "every request-controlled field must survive a restore")

	// Documents are re-resolved from the stored documents rather than applied.
	cfg.Documents = []llm.AgentDocument{{ID: "doc-1", Name: "handbook.pdf", MimeType: "application/pdf", Size: 3, SHA256: "abc", TextRunes: 2}}
	assert.Equal(t, []AgentDocumentRef{{ID: "doc-1", Name: "handbook.pdf"}}, requestFieldsFromConfig(&cfg).Documents)
}
