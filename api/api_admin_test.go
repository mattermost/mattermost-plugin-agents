// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/indexer"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/metrics"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type adminTestStores struct {
	configStore     *testConfigStore
	configUpdater   *testConfigUpdater
	clusterNotifier *testClusterNotifier
}

func setupAdminTestEnvironment(t *testing.T) (*API, *plugintest.API, *adminTestStores) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)

	cfg := &testConfigImpl{}
	noopMetrics := &metrics.NoopMetrics{}

	stores := &adminTestStores{
		configStore:     &testConfigStore{},
		configUpdater:   &testConfigUpdater{},
		clusterNotifier: &testClusterNotifier{},
	}

	api := New(nil, nil, nil, nil, nil, client, noopMetrics, nil, cfg, nil, nil, nil, nil, nil, nil, &mockMCPClientManager{}, nil, nil, stores.configStore, nil, stores.configUpdater, stores.clusterNotifier, nil, nil, nil, nil, nil)

	return api, mockAPI, stores
}

func TestHandleGetJobStatusIncludesStale(t *testing.T) {
	tests := []struct {
		name              string
		indexerNil        bool
		jobStatus         *indexer.JobStatus
		expectedStatus    int
		expectedStale     bool
		expectedBodyField string // optional: when set, asserts JSON contains {"status": <field>}
	}{
		{
			name:              "returns 404 when indexer is nil",
			indexerNil:        true,
			expectedStatus:    http.StatusNotFound,
			expectedBodyField: "no_job",
		},
		{
			// Fresh install: missing KV key must surface as 404 with
			// {"status":"no_job"} (the contract use_job_status.tsx relies on).
			name:              "returns 404 with no_job when no job has ever run",
			indexerNil:        false,
			jobStatus:         nil,
			expectedStatus:    http.StatusNotFound,
			expectedBodyField: "no_job",
		},
		{
			name:       "running job with recent heartbeat is not stale",
			indexerNil: false,
			jobStatus: &indexer.JobStatus{
				Status:        indexer.JobStatusRunning,
				LastUpdatedAt: time.Now().Add(-5 * time.Minute),
			},
			expectedStatus: http.StatusOK,
			expectedStale:  false,
		},
		{
			name:       "running job with old heartbeat is stale",
			indexerNil: false,
			jobStatus: &indexer.JobStatus{
				Status:        indexer.JobStatusRunning,
				LastUpdatedAt: time.Now().Add(-45 * time.Minute),
			},
			expectedStatus: http.StatusOK,
			expectedStale:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI, _ := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()

			if !tt.indexerNil {
				mockIndexer := &mockIndexerService{
					jobStatus: tt.jobStatus,
				}
				api.indexerService = createMockIndexer(t, mockIndexer)
			}

			req := httptest.NewRequest(http.MethodGet, "/admin/reindex/status", nil)
			req.Header.Set("Mattermost-User-Id", "admin-user")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			resp := recorder.Result()
			require.Equal(t, tt.expectedStatus, resp.StatusCode)

			if tt.expectedStatus == http.StatusOK {
				var response indexer.JobStatus
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				require.Equal(t, tt.expectedStale, response.IsStale)
			}
			if tt.expectedBodyField != "" {
				var body map[string]string
				err := json.NewDecoder(resp.Body).Decode(&body)
				require.NoError(t, err)
				require.Equal(t, tt.expectedBodyField, body["status"])
			}
		})
	}
}

// Fresh install: clicking Cancel when no job has ever run must surface as
// 404 {"status":"no_job"}, not a 500. Pre-fix, the wrapper masked the
// missing key as a present-but-zero JobStatus and CancelJob returned
// "not running" — which the handler matched. The wrapper fix promotes the
// missing key to ErrKVNotFound, so the handler must branch on
// IsKVNotFound or fall through to a 500.
func TestHandleCancelJob_FreshInstallReturns404NoJob(t *testing.T) {
	api, mockAPI, _ := setupAdminTestEnvironment(t)
	defer mockAPI.AssertExpectations(t)

	mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
	mockAPI.On("LogError", mock.Anything).Return().Maybe()

	api.indexerService = createMockIndexer(t, &mockIndexerService{jobStatus: nil})

	req := httptest.NewRequest(http.MethodPost, "/admin/reindex/cancel", nil)
	req.Header.Set("Mattermost-User-Id", "admin-user")

	recorder := httptest.NewRecorder()
	api.ServeHTTP(&plugin.Context{}, recorder, req)

	resp := recorder.Result()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "no_job", body["status"])
}

func TestHandleIndexHealthCheck(t *testing.T) {
	tests := []struct {
		name                 string
		indexerNil           bool
		getSearchInitError   func() string
		expectedStatus       int
		expectedResultStatus string
		expectedError        string
	}{
		{
			name:                 "returns 200 with not_configured when indexer is nil",
			indexerNil:           true,
			expectedStatus:       http.StatusOK,
			expectedResultStatus: "not_configured",
		},
		{
			name:       "returns 200 with init_error when indexer is nil and init error exists",
			indexerNil: true,
			getSearchInitError: func() string {
				return "failed to connect to database"
			},
			expectedStatus:       http.StatusOK,
			expectedResultStatus: "init_error",
			expectedError:        "failed to connect to database",
		},
		{
			name:       "returns 200 with not_configured when init error is empty string",
			indexerNil: true,
			getSearchInitError: func() string {
				return ""
			},
			expectedStatus:       http.StatusOK,
			expectedResultStatus: "not_configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI, _ := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()

			if tt.getSearchInitError != nil {
				api.getSearchInitError = tt.getSearchInitError
			}

			req := httptest.NewRequest(http.MethodGet, "/admin/reindex/health-check", nil)
			req.Header.Set("Mattermost-User-Id", "admin-user")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			resp := recorder.Result()
			require.Equal(t, tt.expectedStatus, resp.StatusCode)

			var result indexer.HealthCheckResult
			err := json.NewDecoder(resp.Body).Decode(&result)
			require.NoError(t, err)
			require.Equal(t, tt.expectedResultStatus, result.Status)
			if tt.expectedError != "" {
				require.Equal(t, tt.expectedError, result.Error)
			}
			// Not configured health checks should report model as compatible
			if tt.expectedResultStatus == "not_configured" {
				require.True(t, result.ModelCompatible)
			}
		})
	}
}

// TestHandleFetchModelsVertexAndGeminiValidation covers the switch in
// handleFetchModels (POST /admin/models/fetch): Vertex requires project + region;
// other supported types require an API key unless they are openaicompatible/Vertex.
func TestHandleFetchModelsVertexAndGeminiValidation(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "Vertex missing project ID",
			body: map[string]any{
				"serviceType": llm.ServiceTypeVertex,
				"region":      "us-central1",
			},
		},
		{
			name: "Vertex missing region",
			body: map[string]any{
				"serviceType":     llm.ServiceTypeVertex,
				"vertexProjectID": "my-project",
			},
		},
		{
			name: "Gemini missing API key",
			body: map[string]any{
				"serviceType": llm.ServiceTypeGemini,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI, _ := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()

			raw, err := json.Marshal(tt.body)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/admin/models/fetch", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Mattermost-User-Id", "admin-user")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
		})
	}
}

// mockIndexerService holds the mock configuration for creating test indexers
type mockIndexerService struct {
	jobStatus *indexer.JobStatus
}

// createMockIndexer creates a real indexer.Indexer with mocked dependencies
func createMockIndexer(t *testing.T, mockService *mockIndexerService) *indexer.Indexer {
	t.Helper()

	mockMutexAPI := &plugintest.API{}
	mockClient := mocks.NewMockClient(t)

	if mockService.jobStatus == nil {
		mockClient.On("KVGet", indexer.ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(mmapi.ErrKVNotFound).Maybe()
	} else {
		mockClient.On("KVGet", indexer.ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*indexer.JobStatus)
				*status = *mockService.jobStatus
			}).
			Return(nil).Maybe()
	}

	mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

	return indexer.New(nil, nil, mockClient, nil, nil, mockMutexAPI)
}

func TestHandleGetMCPTools_PluginServer(t *testing.T) {
	tests := []struct {
		name              string
		pluginServers     []mcp.PluginServerConfig
		discoverToolsResp []mcp.ToolInfo
		discoverToolsErr  error
		expectServerType  string
		expectEnabled     bool
		expectToolCount   int
		expectErrorNotNil bool
		expectProbeCalls  int
		expectToolConfigs []mcp.ToolConfig // nil => skip assertion
	}{
		{
			name: "enabled plugin server returns tools",
			pluginServers: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo",
				Name:     "Demo",
				Path:     "/mcp",
				Enabled:  true,
			}},
			discoverToolsResp: []mcp.ToolInfo{
				{Name: "echo", Description: "echoes input"},
				{Name: "add", Description: "adds numbers"},
			},
			expectServerType: "plugin",
			expectEnabled:    true,
			expectToolCount:  2,
			expectProbeCalls: 1,
		},
		{
			name: "disabled plugin server renders row with no probe",
			pluginServers: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo",
				Name:     "Demo",
				Path:     "/mcp",
				Enabled:  false,
			}},
			expectServerType: "plugin",
			expectEnabled:    false,
			expectToolCount:  0,
			expectProbeCalls: 0,
		},
		{
			name: "unreachable plugin populates Error",
			pluginServers: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo",
				Name:     "Demo",
				Path:     "/mcp",
				Enabled:  true,
			}},
			discoverToolsErr:  errors.New("connection refused"),
			expectServerType:  "plugin",
			expectEnabled:     true,
			expectErrorNotNil: true,
			expectProbeCalls:  1,
		},
		{
			name: "enabled plugin server with per-tool policy surfaces ToolConfigs",
			pluginServers: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo",
				Name:     "Demo",
				Path:     "/mcp",
				Enabled:  true,
				ToolConfigs: []mcp.ToolConfig{
					{Name: "echo", Policy: "ask", Enabled: false},
					{Name: "sum", Policy: "auto_run_in_dm", Enabled: true},
				},
			}},
			discoverToolsResp: []mcp.ToolInfo{
				{Name: "echo", Description: "echoes input"},
				{Name: "sum", Description: "adds numbers"},
			},
			expectServerType: "plugin",
			expectEnabled:    true,
			expectToolCount:  2,
			expectProbeCalls: 1,
			expectToolConfigs: []mcp.ToolConfig{
				{Name: "echo", Policy: "ask", Enabled: false},
				{Name: "sum", Policy: "auto_run_in_dm", Enabled: true},
			},
		},
		{
			name: "ExposeExternal remains hidden even when true",
			pluginServers: []mcp.PluginServerConfig{{
				PluginID:       "com.mattermost.demo",
				Name:           "Demo",
				Path:           "/mcp",
				Enabled:        true,
				ExposeExternal: true,
			}},
			discoverToolsResp: []mcp.ToolInfo{{Name: "echo", Description: "echoes input"}},
			expectServerType:  "plugin",
			expectEnabled:     true,
			expectToolCount:   1,
			expectProbeCalls:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI, _ := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()
			mockAPI.On("LogDebug", mock.Anything).Return().Maybe()

			mgr := api.mcpClientManager.(*mockMCPClientManager)
			mgr.pluginServers = tt.pluginServers
			mgr.discoverPluginToolsResponse = tt.discoverToolsResp
			mgr.discoverPluginToolsErr = tt.discoverToolsErr

			req := httptest.NewRequest(http.MethodGet, "/admin/mcp/tools", nil)
			req.Header.Set("Mattermost-User-Id", "admin-user")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			resp := recorder.Result()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			rawBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			var body MCPToolsResponse
			require.NoError(t, json.Unmarshal(rawBody, &body))

			var pluginRow *MCPServerInfo
			for i := range body.Servers {
				if body.Servers[i].ServerType == "plugin" {
					pluginRow = &body.Servers[i]
					break
				}
			}
			require.NotNil(t, pluginRow, "expected a plugin-type row in response.Servers")
			require.Equal(t, tt.expectServerType, pluginRow.ServerType)
			require.Equal(t, tt.expectEnabled, pluginRow.Enabled)
			require.Equal(t, tt.expectToolCount, len(pluginRow.Tools))
			if tt.expectErrorNotNil {
				require.NotNil(t, pluginRow.Error)
			} else {
				require.Nil(t, pluginRow.Error)
			}
			require.Equal(t, tt.expectProbeCalls, mgr.discoverPluginToolsCallCount)
			if tt.expectToolConfigs != nil {
				require.Equal(t, tt.expectToolConfigs, pluginRow.ToolConfigs, "plugin row must surface ToolConfigs verbatim")
			}

			// The admin tools response must never expose ExposeExternal on plugin rows.
			var rawResp struct {
				Servers []map[string]json.RawMessage `json:"servers"`
			}
			require.NoError(t, json.Unmarshal(rawBody, &rawResp))
			var rawPluginRow map[string]json.RawMessage
			for _, s := range rawResp.Servers {
				if st, ok := s["serverType"]; ok && string(st) == `"plugin"` {
					rawPluginRow = s
					break
				}
			}
			require.NotNil(t, rawPluginRow, "expected raw plugin row in JSON")
			_, hasField := rawPluginRow["exposeExternal"]
			require.False(t, hasField, "exposeExternal must be omitted from admin tools payloads")
		})
	}
}

func TestHandleUpdatePluginServer(t *testing.T) {
	tests := []struct {
		name                   string
		pluginID               string
		preRegistered          []mcp.PluginServerConfig
		body                   string
		hasAdminPerm           bool
		expectStatus           int
		expectRegisterCalls    int
		expectEnabledAfter     bool
		expectExposeAfter      bool
		expectToolConfigsAfter []mcp.ToolConfig
		expectRebuildCalls     int
	}{
		{
			name:     "happy path: flips Enabled true->false",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}},
			body:                `{"enabled": false}`,
			hasAdminPerm:        true,
			expectStatus:        http.StatusOK,
			expectRegisterCalls: 1,
			expectEnabledAfter:  false,
			expectExposeAfter:   false,
			expectRebuildCalls:  0,
		},
		{
			name:     "enabled update preserves existing ExposeExternal",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
				Enabled: true, ExposeExternal: true,
			}},
			body:                `{"enabled": false}`,
			hasAdminPerm:        true,
			expectStatus:        http.StatusOK,
			expectRegisterCalls: 1,
			expectEnabledAfter:  false,
			expectExposeAfter:   true,
			expectRebuildCalls:  1,
		},
		{
			name:     "expose_external field is ignored",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
				Enabled: true, ExposeExternal: false,
			}},
			body:                `{"expose_external": true}`,
			hasAdminPerm:        true,
			expectStatus:        http.StatusOK,
			expectRegisterCalls: 1,
			expectEnabledAfter:  true,
			expectExposeAfter:   false,
			expectRebuildCalls:  0,
		},
		{
			name:     "empty body preserves both fields",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
				Enabled: true, ExposeExternal: true,
			}},
			body:                `{}`,
			hasAdminPerm:        true,
			expectStatus:        http.StatusOK,
			expectRegisterCalls: 1,
			expectEnabledAfter:  true,
			expectExposeAfter:   true,
			expectRebuildCalls:  1,
		},
		{
			name:         "404 when pluginID not registered",
			pluginID:     "com.missing",
			body:         `{"enabled": true}`,
			hasAdminPerm: true,
			expectStatus: http.StatusNotFound,
		},
		{
			name:     "400 on malformed body",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}},
			body:         `not json`,
			hasAdminPerm: true,
			expectStatus: http.StatusBadRequest,
		},
		{
			name:     "403 when caller is not an admin",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}},
			body:                `{"enabled": false}`,
			hasAdminPerm:        false,
			expectStatus:        http.StatusForbidden,
			expectRegisterCalls: 0,
		},
		{
			name:     "tool_configs partial PUT sets policy, preserves enabled",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
				Enabled: true, ExposeExternal: false,
			}},
			body:                `{"tool_configs": [{"name": "echo", "policy": "ask", "enabled": false}]}`,
			hasAdminPerm:        true,
			expectStatus:        http.StatusOK,
			expectRegisterCalls: 1,
			expectEnabledAfter:  true,
			expectExposeAfter:   false,
			expectToolConfigsAfter: []mcp.ToolConfig{
				{Name: "echo", Policy: "ask", Enabled: false},
			},
			expectRebuildCalls: 0,
		},
		{
			// Non-nil empty slice clears policy; distinct from an omitted field.
			name:     "tool_configs empty slice clears policy",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
				Enabled: true, ExposeExternal: false,
				ToolConfigs: []mcp.ToolConfig{{Name: "echo", Policy: "ask", Enabled: false}},
			}},
			body:                   `{"tool_configs": []}`,
			hasAdminPerm:           true,
			expectStatus:           http.StatusOK,
			expectRegisterCalls:    1,
			expectEnabledAfter:     true,
			expectExposeAfter:      false,
			expectToolConfigsAfter: []mcp.ToolConfig{},
			expectRebuildCalls:     0,
		},
		{
			name:     "tool_configs omitted preserves existing policy",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
				Enabled: true, ExposeExternal: false,
				ToolConfigs: []mcp.ToolConfig{
					{Name: "echo", Policy: "auto_run_in_dm", Enabled: true},
				},
			}},
			body:                `{"enabled": false}`,
			hasAdminPerm:        true,
			expectStatus:        http.StatusOK,
			expectRegisterCalls: 1,
			expectEnabledAfter:  false,
			expectExposeAfter:   false,
			expectToolConfigsAfter: []mcp.ToolConfig{
				{Name: "echo", Policy: "auto_run_in_dm", Enabled: true},
			},
			expectRebuildCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI, stores := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(tt.hasAdminPerm).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()

			mgr := api.mcpClientManager.(*mockMCPClientManager)
			mgr.pluginServers = tt.preRegistered

			// Seed a baseline persisted config so the handler can clone it
			// instead of treating the store's nil as a 500.
			stores.configStore.cfg = &config.Config{}

			spy := &spyRebuilder{}
			api.SetExternalRebuilderForTest(spy)

			req := httptest.NewRequest(http.MethodPut, "/admin/mcp/plugin-servers/"+tt.pluginID, strings.NewReader(tt.body))
			req.Header.Set("Mattermost-User-Id", "admin-user")
			req.Header.Set("Content-Type", "application/json")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			resp := recorder.Result()
			require.Equal(t, tt.expectStatus, resp.StatusCode)

			require.Len(t, mgr.registerCalls, tt.expectRegisterCalls)
			if tt.expectStatus == http.StatusOK {
				require.Equal(t, tt.expectEnabledAfter, mgr.registerCalls[0].Enabled)
				require.Equal(t, tt.expectExposeAfter, mgr.registerCalls[0].ExposeExternal)
				require.Equal(t, "Demo", mgr.registerCalls[0].Name)
				require.Equal(t, "/mcp", mgr.registerCalls[0].Path)
				require.Equal(t, "com.mattermost.demo", mgr.registerCalls[0].PluginID)
				if tt.expectToolConfigsAfter != nil {
					require.Equal(t, tt.expectToolConfigsAfter, mgr.registerCalls[0].ToolConfigs, "ToolConfigs assertion")
				}
			}
			require.Equal(t, tt.expectRebuildCalls, spy.callCount)
		})
	}
}

func TestHandleUpdatePluginServer_PersistsToConfig(t *testing.T) {
	tests := []struct {
		name                  string
		preRegistered         []mcp.PluginServerConfig
		body                  string
		nilStoredConfig       bool
		getErr                error
		saveErr               error
		publishErr            error
		expectStatus          int
		expectSaveCalls       int
		expectUpdateCalls     int
		expectPublishCalls    int
		expectRegisterCalls   int
		expectUnregisterCalls int
		assertPersistedState  func(t *testing.T, savedCfg *config.Config)
	}{
		{
			name: "happy path — saves full snapshot and broadcasts",
			preRegistered: []mcp.PluginServerConfig{
				{
					PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
					Enabled: true, ExposeExternal: false,
				},
				{
					PluginID: "com.mattermost.other", Name: "Other", Path: "/mcp",
					Enabled: false, ExposeExternal: false,
				},
			},
			body:                  `{"tool_configs": [{"name": "echo", "policy": "ask", "enabled": false}]}`,
			expectStatus:          http.StatusOK,
			expectSaveCalls:       1,
			expectUpdateCalls:     1,
			expectPublishCalls:    1,
			expectRegisterCalls:   1,
			expectUnregisterCalls: 0,
			assertPersistedState: func(t *testing.T, savedCfg *config.Config) {
				require.Len(t, savedCfg.MCP.PluginServers, 2, "full snapshot includes all registered plugins")

				byID := map[string]config.PluginServerConfig{}
				for _, ps := range savedCfg.MCP.PluginServers {
					byID[ps.PluginID] = ps
				}

				updated := byID["com.mattermost.demo"]
				require.True(t, updated.Enabled, "Enabled preserved")
				require.Len(t, updated.ToolConfigs, 1)
				require.Equal(t, "echo", updated.ToolConfigs[0].Name)
				require.False(t, updated.ToolConfigs[0].Enabled)

				other := byID["com.mattermost.other"]
				require.False(t, other.Enabled)
				require.Empty(t, other.ToolConfigs)
			},
		},
		{
			name: "GetConfig failure returns 500 and skips Save/Update/Publish",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}},
			body:                  `{"enabled": false}`,
			getErr:                errors.New("config store unavailable"),
			expectStatus:          http.StatusInternalServerError,
			expectSaveCalls:       0,
			expectUpdateCalls:     0,
			expectPublishCalls:    0,
			expectRegisterCalls:   0,
			expectUnregisterCalls: 0,
		},
		{
			name: "SaveConfig failure returns 500 and skips Update/Publish",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}},
			body:                  `{"enabled": false}`,
			saveErr:               errors.New("db unreachable"),
			expectStatus:          http.StatusInternalServerError,
			expectSaveCalls:       1,
			expectUpdateCalls:     0,
			expectPublishCalls:    0,
			expectRegisterCalls:   0,
			expectUnregisterCalls: 0,
		},
		{
			name: "PublishConfigUpdate failure returns 500 after Save and skips Update",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}},
			body:                  `{"enabled": false}`,
			publishErr:            errors.New("cluster broadcast failed"),
			expectStatus:          http.StatusInternalServerError,
			expectSaveCalls:       1,
			expectUpdateCalls:     0,
			expectPublishCalls:    1,
			expectRegisterCalls:   0,
			expectUnregisterCalls: 0,
		},
		{
			// A nil persisted config must not be silently replaced by a
			// zero-value baseline; doing so would clobber unrelated settings
			// (services, bots, MCP flags) on the next save.
			name: "nil persisted config returns 500 and skips Save/Update/Publish/Register",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}},
			body:                  `{"enabled": false}`,
			nilStoredConfig:       true,
			expectStatus:          http.StatusInternalServerError,
			expectSaveCalls:       0,
			expectUpdateCalls:     0,
			expectPublishCalls:    0,
			expectRegisterCalls:   0,
			expectUnregisterCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI, stores := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()
			mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

			mgr := api.mcpClientManager.(*mockMCPClientManager)
			mgr.pluginServers = tt.preRegistered

			// Seed a baseline persisted config unless the case explicitly
			// exercises the nil path. Without a seed, GetConfig returns nil
			// and the handler aborts before Save/Publish are invoked.
			var seedCfg *config.Config
			if !tt.nilStoredConfig {
				seedCfg = &config.Config{}
			}
			stores.configStore.cfg = seedCfg

			var failingStore *failingConfigStore
			if tt.getErr != nil || tt.saveErr != nil {
				failingStore = &failingConfigStore{cfg: seedCfg, getErr: tt.getErr, saveErr: tt.saveErr}
				api.configStore = failingStore
			}
			if tt.publishErr != nil {
				stores.clusterNotifier.err = tt.publishErr
			}

			req := httptest.NewRequest(http.MethodPut, "/admin/mcp/plugin-servers/com.mattermost.demo", strings.NewReader(tt.body))
			req.Header.Set("Mattermost-User-Id", "admin-user")
			req.Header.Set("Content-Type", "application/json")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			resp := recorder.Result()
			require.Equal(t, tt.expectStatus, resp.StatusCode)

			if tt.getErr != nil || tt.saveErr != nil {
				require.Equal(t, tt.expectSaveCalls, failingStore.saveCallCount)
			}
			require.Equal(t, tt.expectUpdateCalls, stores.configUpdater.callCount)
			require.Equal(t, tt.expectPublishCalls, stores.clusterNotifier.callCount)

			require.Len(t, mgr.registerCalls, tt.expectRegisterCalls, "live plugin registry must not be mutated on failure paths")
			require.Len(t, mgr.unregisterCalls, tt.expectUnregisterCalls, "live plugin registry must not be mutated on failure paths")

			if tt.assertPersistedState != nil {
				require.NotNil(t, stores.configStore.cfg, "SaveConfig must have been called and persisted cfg")
				tt.assertPersistedState(t, stores.configStore.cfg)
			}
		})
	}
}

// failingConfigStore is a testConfigStore variant with configurable error injection on Get/Save.
type failingConfigStore struct {
	cfg           *config.Config
	getErr        error
	saveErr       error
	saveCallCount int
}

func (s *failingConfigStore) GetConfig() (*config.Config, error) {
	return s.cfg, s.getErr
}

func (s *failingConfigStore) SaveConfig(cfg config.Config) error {
	s.saveCallCount++
	if s.saveErr != nil {
		return s.saveErr
	}
	clone := cfg
	s.cfg = &clone
	return nil
}
