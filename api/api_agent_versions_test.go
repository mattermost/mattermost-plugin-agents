// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
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
		{name: "delete versions", method: http.MethodDelete, path: "/agents/agent-1/versions"},
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
	var list store.AgentVersionPage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &list))
	assert.Equal(t, 2, list.CurrentVersion)
	assert.False(t, list.HasMore)
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
		{name: "version beyond the 32-bit column range", path: "/agents/agent-1/versions/2147483648", expectedStatus: http.StatusBadRequest},
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

func TestListAgentVersionsPaging(t *testing.T) {
	const totalVersions = 250
	versionRange := func(from, to int) []int {
		out := []int{}
		for v := from; v >= to; v-- {
			out = append(out, v)
		}
		return out
	}

	tests := []struct {
		name             string
		query            string
		expectedStatus   int
		expectedVersions []int
		expectedHasMore  bool
	}{
		{name: "defaults to the newest 50", query: "", expectedStatus: http.StatusOK, expectedVersions: versionRange(250, 201), expectedHasMore: true},
		{name: "before and limit", query: "?before=100&limit=10", expectedStatus: http.StatusOK, expectedVersions: versionRange(99, 90), expectedHasMore: true},
		{name: "limit is clamped to 200", query: "?limit=500", expectedStatus: http.StatusOK, expectedVersions: versionRange(250, 51), expectedHasMore: true},
		{name: "last page", query: "?before=3", expectedStatus: http.StatusOK, expectedVersions: []int{2, 1}},
		{name: "nothing before version 1", query: "?before=1", expectedStatus: http.StatusOK, expectedVersions: []int{}},
		{name: "zero limit", query: "?limit=0", expectedStatus: http.StatusBadRequest},
		{name: "negative limit", query: "?limit=-5", expectedStatus: http.StatusBadRequest},
		{name: "non-integer limit", query: "?limit=ten", expectedStatus: http.StatusBadRequest},
		{name: "zero before", query: "?before=0", expectedStatus: http.StatusBadRequest},
		{name: "empty before", query: "?before=", expectedStatus: http.StatusBadRequest},
		{name: "fractional before", query: "?before=1.5", expectedStatus: http.StatusBadRequest},
		{name: "largest before the column holds", query: "?before=2147483647", expectedStatus: http.StatusOK, expectedVersions: versionRange(250, 201), expectedHasMore: true},
		{name: "before beyond the 32-bit column range", query: "?before=2147483648", expectedStatus: http.StatusBadRequest},
		{name: "limit beyond the 32-bit range", query: "?limit=2147483648", expectedStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			mockLicensed(e.mockAPI)
			agent := versionTestAgent()
			e.agentStore.agents[agent.ID] = agent
			for range totalVersions {
				e.agentStore.recordVersion(agent, nil, store.AgentVersionMeta{Source: store.AgentVersionSourceUpdate})
			}

			recorder := doRequest(e.api, http.MethodGet, "/agents/agent-1/versions"+tt.query, nil, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())
			if tt.expectedStatus != http.StatusOK {
				return
			}

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &raw))
			assert.ElementsMatch(t, []string{"currentVersion", "versions", "hasMore"}, slices.Collect(maps.Keys(raw)))

			var page store.AgentVersionPage
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &page))
			assert.Equal(t, totalVersions, page.CurrentVersion, "currentVersion is the latest version on every page")
			assert.Equal(t, tt.expectedHasMore, page.HasMore)
			got := make([]int, 0, len(page.Versions))
			for _, v := range page.Versions {
				got = append(got, v.Version)
			}
			assert.Equal(t, tt.expectedVersions, got)
		})
	}
}

// allAgentVersions returns every version the mock store holds for agentID, newest first.
func allAgentVersions(t *testing.T, e *TestEnvironment, agentID string) []store.AgentVersion {
	t.Helper()
	page, err := e.agentStore.ListAgentVersions(agentID, 0, len(e.agentStore.versions[agentID])+1)
	require.NoError(t, err)
	return page.Versions
}

// enableAttributeBasedAccess licenses attribute-based access and sends policy
// calls to the mock plugin API, where tests can assert on them.
func enableAttributeBasedAccess(e *TestEnvironment) {
	e.OverrideLicense(enterprisetest.LicenseFor(enterprise.LevelEnterpriseAdvanced))
	e.api.accessChecker = accesscontrol.New(accesscontrol.PassthroughClient{}, e.mockAPI, accesscontrol.NoMCPServerIDs, nil)
	e.mockAPI.On("GetAccessControlVisualAST", testUserID, accesscontrol.ResourceTypeAgent, mock.Anything).
		Return(&model.VisualExpression{Conditions: []model.Condition{}}, nil).Maybe()
	e.mockAPI.On("DeleteAccessControlPolicy", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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

				versions := allAgentVersions(t, e, versionTestAgentID)
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
				assert.Equal(t, 2, mockLatestVersion(e.agentStore.versions[versionTestAgentID]), "a rejected restore writes no version")
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
			name: "version without attribute-based access cannot replace an attribute-based agent's policy",
			setup: func(t *testing.T, e *TestEnvironment) {
				enableAttributeBasedAccess(e)
				seedVersionedAgent(t, e, versionTestAgent(), map[string]any{"userAccessLevel": int(llm.UserAccessLevelAttributeBased)})
			},
			path:           "/agents/agent-1/versions/1/restore",
			expectedStatus: http.StatusBadRequest,
			errorContains:  "different user access level",
			validate: func(t *testing.T, e *TestEnvironment, _ *llm.BotConfig) {
				e.mockAPI.AssertNotCalled(t, "DeleteAccessControlPolicy", mock.Anything, mock.Anything, mock.Anything)
				assert.Equal(t, llm.UserAccessLevelAttributeBased, e.agentStore.agents[versionTestAgentID].UserAccessLevel)
				assert.Equal(t, 2, mockLatestVersion(e.agentStore.versions[versionTestAgentID]), "a rejected restore writes no version")
			},
		},
		{
			name: "attribute-based version restores onto an attribute-based agent and keeps its policy",
			setup: func(t *testing.T, e *TestEnvironment) {
				enableAttributeBasedAccess(e)
				original := versionTestAgent()
				original.UserAccessLevel = llm.UserAccessLevelAttributeBased
				seedVersionedAgent(t, e, original, map[string]any{"customInstructions": "edited instructions"})
			},
			path:           "/agents/agent-1/versions/1/restore",
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, e *TestEnvironment, resp *llm.BotConfig) {
				e.mockAPI.AssertNotCalled(t, "DeleteAccessControlPolicy", mock.Anything, mock.Anything, mock.Anything)
				assert.Equal(t, "original instructions", resp.CustomInstructions)
				assert.Equal(t, llm.UserAccessLevelAttributeBased, e.agentStore.agents[versionTestAgentID].UserAccessLevel)
			},
		},
		{
			name: "attribute-based version restores onto an agent that no longer uses attribute-based access",
			setup: func(t *testing.T, e *TestEnvironment) {
				enableAttributeBasedAccess(e)
				original := versionTestAgent()
				original.UserAccessLevel = llm.UserAccessLevelAttributeBased
				seedVersionedAgent(t, e, original, map[string]any{"userAccessLevel": int(llm.UserAccessLevelAll)})
			},
			path:           "/agents/agent-1/versions/1/restore",
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, e *TestEnvironment, _ *llm.BotConfig) {
				assert.Equal(t, llm.UserAccessLevelAttributeBased, e.agentStore.agents[versionTestAgentID].UserAccessLevel)
				assert.Equal(t, 3, mockLatestVersion(e.agentStore.versions[versionTestAgentID]))
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
		{
			name: "version beyond the 32-bit column range",
			setup: func(t *testing.T, e *TestEnvironment) {
				seedVersionedAgent(t, e, versionTestAgent(), map[string]any{"customInstructions": "edited"})
			},
			path:           "/agents/agent-1/versions/2147483648/restore",
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
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false).Maybe()
	seedVersionedAgent(t, e, versionTestAgent(), map[string]any{"customInstructions": "edited"})
	records := e.CaptureAuditRecords()

	for _, path := range []string{"/agents/agent-1/versions", "/agents/agent-1/versions/1"} {
		recorder := doRequest(e.api, http.MethodGet, path, nil, testUserID)
		require.Equal(t, http.StatusOK, recorder.Result().StatusCode, path)
	}
	assert.Empty(t, *records)
}

// Restore rebuilds a full update request from a stored config, so a
// request field that requestFieldsFromConfig misses would silently be reset.
func TestRequestFieldsFromConfigRoundTrip(t *testing.T) {
	var want AgentRequestFields
	v := reflect.ValueOf(&want).Elem()
	for i := range v.NumField() {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("value-" + v.Type().Field(i).Name)
		case reflect.Int:
			f.SetInt(int64(i + 1))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Slice:
			f.Set(reflect.MakeSlice(f.Type(), 1, 1))
			if elem := f.Index(0); elem.Kind() == reflect.String {
				elem.SetString("item")
			}
		default:
			t.Fatalf("field %s has kind %s; extend this test", v.Type().Field(i).Name, f.Kind())
		}
	}

	var cfg llm.BotConfig
	want.applyTo(&cfg)
	assert.Equal(t, want, requestFieldsFromConfig(&cfg), "every request-controlled field must survive a restore")
}

// seedAgentWithVersions stores an agent owned by testUserID and saves edits
// through PUT /agents/:agentid until it has n versions.
func seedAgentWithVersions(t *testing.T, e *TestEnvironment, n int) {
	t.Helper()
	original := versionTestAgent()
	seedVersionedAgent(t, e, original, map[string]any{"customInstructions": "edit 2"})
	for i := 3; i <= n; i++ {
		recorder := doRequest(e.api, http.MethodPut, "/agents/"+original.ID, updateAgentBodyFromStored(e.agentStore.agents[original.ID], map[string]any{"customInstructions": fmt.Sprintf("edit %d", i)}), testUserID)
		require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())
	}
}

func TestDeleteAgentVersions(t *testing.T) {
	tests := []struct {
		name            string
		body            any
		expectedStatus  int
		errorContains   string
		expectedDeleted int64
		expectedKept    []int
	}{
		{name: "selected versions", body: map[string]any{"versions": []int{1, 3}}, expectedStatus: http.StatusOK, expectedDeleted: 2, expectedKept: []int{4, 2}},
		{name: "a single version", body: map[string]any{"versions": []int{2}}, expectedStatus: http.StatusOK, expectedDeleted: 1, expectedKept: []int{4, 3, 1}},
		{name: "every previous version", body: map[string]any{"all": true}, expectedStatus: http.StatusOK, expectedDeleted: 3, expectedKept: []int{4}},
		{name: "the current version is refused", body: map[string]any{"versions": []int{2, 4}}, expectedStatus: http.StatusBadRequest, errorContains: "current version", expectedKept: []int{4, 3, 2, 1}},
		{name: "both versions and all", body: map[string]any{"versions": []int{1}, "all": true}, expectedStatus: http.StatusBadRequest, expectedKept: []int{4, 3, 2, 1}},
		{name: "nothing to delete", body: map[string]any{"versions": []int{}}, expectedStatus: http.StatusBadRequest, expectedKept: []int{4, 3, 2, 1}},
		{name: "invalid version number", body: map[string]any{"versions": []int{0}}, expectedStatus: http.StatusBadRequest, expectedKept: []int{4, 3, 2, 1}},
		{name: "malformed body", body: "not json", expectedStatus: http.StatusBadRequest, expectedKept: []int{4, 3, 2, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			mockLicensed(e.mockAPI)
			seedAgentWithVersions(t, e, 4)

			recorder := doRequest(e.api, http.MethodDelete, "/agents/agent-1/versions", tt.body, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())
			if tt.errorContains != "" {
				assert.Contains(t, recorder.Body.String(), tt.errorContains)
			}
			if tt.expectedStatus == http.StatusOK {
				var resp deleteAgentVersionsResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
				assert.Equal(t, tt.expectedDeleted, resp.Deleted)
			}

			list := doRequest(e.api, http.MethodGet, "/agents/agent-1/versions", nil, testUserID)
			require.Equal(t, http.StatusOK, list.Result().StatusCode)
			var page store.AgentVersionPage
			require.NoError(t, json.Unmarshal(list.Body.Bytes(), &page))
			kept := []int{}
			for _, v := range page.Versions {
				kept = append(kept, v.Version)
			}
			assert.Equal(t, tt.expectedKept, kept)
			assert.Equal(t, 4, page.CurrentVersion, "the current version always stays")
		})
	}
}

func TestAuditDeleteAgentVersions(t *testing.T) {
	const plantedInstructions = "planted-deleted-instructions-that-must-never-leak"

	tests := []struct {
		name           string
		body           map[string]any
		manage         bool
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name:           "selected versions record their numbers and the count",
			body:           map[string]any{"versions": []int{1}},
			manage:         true,
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, versionTestAgentID, rec.EventData.Parameters[audit.KeyAgentID])
				assert.Equal(t, []string{"1"}, rec.EventData.Parameters["versions"])
				assert.Equal(t, false, rec.EventData.Parameters["all"])
				assert.EqualValues(t, 1, rec.EventData.Parameters["deleted"])
			},
		},
		{
			name:           "all previous versions",
			body:           map[string]any{"all": true},
			manage:         true,
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, true, rec.EventData.Parameters["all"])
				assert.EqualValues(t, 1, rec.EventData.Parameters["deleted"])
			},
		},
		{
			name:           "non-manager records a 403 fail identifying the target",
			body:           map[string]any{"all": true},
			expectedStatus: http.StatusForbidden,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusForbidden, rec.Error.Code)
				assert.Equal(t, versionTestAgentID, rec.EventData.Parameters[audit.KeyAgentID])
				assert.NotContains(t, rec.EventData.Parameters, "deleted")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			mockLicensed(e.mockAPI)
			original := versionTestAgent()
			original.CustomInstructions = plantedInstructions
			seedVersionedAgent(t, e, original, map[string]any{"customInstructions": "edited"})
			if !tt.manage {
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false)
				e.agentStore.agents[versionTestAgentID].CreatorID = "someone-else"
			}
			records := e.CaptureAuditRecords()

			recorder := doRequest(e.api, http.MethodDelete, "/agents/agent-1/versions", tt.body, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventDeleteAgentVersions, rec.EventName)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			tt.validateRecord(t, rec)

			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), plantedInstructions, "audit record must never carry custom instructions")
		})
	}
}
