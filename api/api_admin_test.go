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
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	embeddingsmocks "github.com/mattermost/mattermost-plugin-agents/v2/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/indexer"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/metrics"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
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

	api := New(nil, nil, nil, nil, nil, client, noopMetrics, nil, cfg, nil, nil, nil, nil, nil, nil, &mockMCPClientManager{}, nil, nil, stores.configStore, nil, stores.configUpdater, stores.clusterNotifier, nil, nil, nil, nil, nil, nil)

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
	// searchConfigured makes getSearch return a mock embedding search so the
	// indexer gets past its "search functionality is not configured" guards.
	searchConfigured bool
	// lastIndexedAt seeds IndexerLastIndexedKey (used by StartCatchUpJob).
	lastIndexedAt int64
	// casOK is the result of KVCompareAndSet on the job status row (used by
	// CancelJob's running -> cancel_requested transition).
	casOK bool
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

	if mockService.lastIndexedAt > 0 {
		mockClient.On("KVGet", indexer.IndexerLastIndexedKey, mock.AnythingOfType("*int64")).
			Run(func(args mock.Arguments) {
				*(args.Get(1).(*int64)) = mockService.lastIndexedAt
			}).
			Return(nil).Maybe()
	} else {
		mockClient.On("KVGet", indexer.IndexerLastIndexedKey, mock.AnythingOfType("*int64")).
			Return(mmapi.ErrKVNotFound).Maybe()
	}

	mockClient.On("KVCompareAndSet", indexer.ReindexJobKey, mock.Anything, mock.Anything).
		Return(mockService.casOK, nil).Maybe()

	mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

	var getSearch func() embeddings.EmbeddingSearch
	if mockService.searchConfigured {
		searchMock := embeddingsmocks.NewMockEmbeddingSearch(t)
		getSearch = func() embeddings.EmbeddingSearch { return searchMock }
	}

	return indexer.New(getSearch, nil, mockClient, nil, nil, mockMutexAPI)
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

func TestHandleGetMCPTools_OmitsOrphanPluginServers(t *testing.T) {
	api, mockAPI, _ := setupAdminTestEnvironment(t)
	defer mockAPI.AssertExpectations(t)

	mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
	mockAPI.On("LogError", mock.Anything).Return().Maybe()
	mockAPI.On("LogDebug", mock.Anything).Return().Maybe()

	mgr := api.mcpClientManager.(*mockMCPClientManager)
	mgr.pluginServers = []mcp.PluginServerConfig{
		{PluginID: "com.mattermost.live", Name: "Live", Path: "/mcp", Enabled: true},
		{PluginID: "com.mattermost.inactive", Name: "Inactive", Path: "/mcp", Enabled: true},
	}
	mgr.orphanPluginIDs = map[string]bool{"com.mattermost.inactive": true}
	mgr.discoverPluginToolsResponse = []mcp.ToolInfo{{Name: "echo"}}

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

	pluginRows := []MCPServerInfo{}
	for _, s := range body.Servers {
		if s.ServerType == "plugin" {
			pluginRows = append(pluginRows, s)
		}
	}
	require.Len(t, pluginRows, 1, "orphan plugin must be omitted from admin tools listing")
	require.Equal(t, "Live", pluginRows[0].Name)

	require.Equal(t, 1, mgr.discoverPluginToolsCallCount,
		"orphan plugin must not be probed; probing would surface a misleading session-not-found error")
}

func TestHandleUpdatePluginServer(t *testing.T) {
	tests := []struct {
		name                   string
		pluginID               string
		preRegistered          []mcp.PluginServerConfig
		body                   string
		hasAdminPerm           bool
		expectStatus           int
		expectRegistryCalls    int
		expectEnabledAfter     bool
		expectExposeAfter      bool
		expectToolConfigsAfter []mcp.ToolConfig
		expectRebuildCalls     int
		orphanPluginIDs        map[string]bool
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
			expectRegistryCalls: 1,
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
			expectRegistryCalls: 1,
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
			expectRegistryCalls: 1,
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
			expectRegistryCalls: 1,
			expectEnabledAfter:  true,
			expectExposeAfter:   true,
			expectRebuildCalls:  1,
		},
		{
			name:     "admin update keeps config-only orphan unregistered",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}},
			orphanPluginIDs:     map[string]bool{"com.mattermost.demo": true},
			body:                `{"enabled": false}`,
			hasAdminPerm:        true,
			expectStatus:        http.StatusOK,
			expectRegistryCalls: 1,
			expectEnabledAfter:  false,
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
			expectRegistryCalls: 0,
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
			expectRegistryCalls: 1,
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
			expectRegistryCalls:    1,
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
			expectRegistryCalls: 1,
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
			mgr.orphanPluginIDs = tt.orphanPluginIDs

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

			require.Empty(t, mgr.registerCalls)
			require.Len(t, mgr.updateCalls, tt.expectRegistryCalls)
			if tt.expectStatus == http.StatusOK {
				require.Equal(t, tt.expectEnabledAfter, mgr.updateCalls[0].Enabled)
				require.Equal(t, tt.expectExposeAfter, mgr.updateCalls[0].ExposeExternal)
				require.Equal(t, "Demo", mgr.updateCalls[0].Name)
				require.Equal(t, "/mcp", mgr.updateCalls[0].Path)
				require.Equal(t, "com.mattermost.demo", mgr.updateCalls[0].PluginID)
				if tt.expectToolConfigsAfter != nil {
					require.Equal(t, tt.expectToolConfigsAfter, mgr.updateCalls[0].ToolConfigs, "ToolConfigs assertion")
				}
				if tt.orphanPluginIDs[tt.pluginID] {
					require.False(t, mgr.IsPluginRegistered(tt.pluginID))
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
		seededPersisted       []config.PluginServerConfig
		body                  string
		nilStoredConfig       bool
		getErr                error
		saveErr               error
		publishErr            error
		expectStatus          int
		expectSaveCalls       int
		expectUpdateCalls     int
		expectPublishCalls    int
		expectRegistryCalls   int
		expectUnregisterCalls int
		assertPersistedState  func(t *testing.T, savedCfg *config.Config)
	}{
		{
			name: "happy path — persists only the edited entry and broadcasts",
			preRegistered: []mcp.PluginServerConfig{
				{
					PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
					Enabled: true, ExposeExternal: false,
				},
			},
			body:                  `{"tool_configs": [{"name": "echo", "policy": "ask", "enabled": false}]}`,
			expectStatus:          http.StatusOK,
			expectSaveCalls:       1,
			expectUpdateCalls:     1,
			expectPublishCalls:    1,
			expectRegistryCalls:   1,
			expectUnregisterCalls: 0,
			assertPersistedState: func(t *testing.T, savedCfg *config.Config) {
				require.Len(t, savedCfg.MCP.PluginServers, 1)

				updated := savedCfg.MCP.PluginServers[0]
				require.Equal(t, "com.mattermost.demo", updated.PluginID)
				require.True(t, updated.Enabled, "Enabled preserved")
				require.Len(t, updated.ToolConfigs, 1)
				require.Equal(t, "echo", updated.ToolConfigs[0].Name)
				require.False(t, updated.ToolConfigs[0].Enabled)
			},
		},
		{
			// Regression for MM-68980: admin updates one plugin while another
			// plugin (with admin-customized config) is currently inactive in
			// memory. The persisted entry for the inactive plugin must
			// survive — replacing PluginServers with the in-memory snapshot
			// would silently drop it and revert to plugin-supplied defaults
			// on next re-registration.
			name: "preserves persisted entries for plugins currently inactive in memory",
			preRegistered: []mcp.PluginServerConfig{
				{
					PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
					Enabled: true, ExposeExternal: false,
				},
			},
			seededPersisted: []config.PluginServerConfig{
				{
					PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
					Enabled: true, ExposeExternal: false,
				},
				{
					PluginID: "com.mattermost.inactive", Name: "Inactive", Path: "/mcp",
					Enabled: true, ExposeExternal: false,
					ToolConfigs: []config.MCPToolConfig{
						{Name: "destructive_tool", Policy: config.MCPToolPolicyAsk, Enabled: false},
					},
				},
			},
			body:                  `{"enabled": false}`,
			expectStatus:          http.StatusOK,
			expectSaveCalls:       1,
			expectUpdateCalls:     1,
			expectPublishCalls:    1,
			expectRegistryCalls:   1,
			expectUnregisterCalls: 0,
			assertPersistedState: func(t *testing.T, savedCfg *config.Config) {
				require.Len(t, savedCfg.MCP.PluginServers, 2,
					"inactive plugin's persisted entry must not be dropped")

				byID := map[string]config.PluginServerConfig{}
				for _, ps := range savedCfg.MCP.PluginServers {
					byID[ps.PluginID] = ps
				}

				demo, ok := byID["com.mattermost.demo"]
				require.True(t, ok, "edited plugin must be persisted")
				require.False(t, demo.Enabled, "edited Enabled flag must apply")

				inactive, ok := byID["com.mattermost.inactive"]
				require.True(t, ok, "inactive plugin's persisted entry must be preserved")
				require.True(t, inactive.Enabled, "inactive plugin's Enabled flag must be untouched")
				require.Len(t, inactive.ToolConfigs, 1,
					"inactive plugin's admin-set tool configs must be untouched")
				require.Equal(t, "destructive_tool", inactive.ToolConfigs[0].Name)
				require.False(t, inactive.ToolConfigs[0].Enabled,
					"admin-disabled tool must remain disabled across unrelated updates")
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
			expectRegistryCalls:   0,
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
			expectRegistryCalls:   0,
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
			expectRegistryCalls:   0,
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
			expectRegistryCalls:   0,
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
				if tt.seededPersisted != nil {
					seedCfg.MCP.PluginServers = tt.seededPersisted
				}
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

			require.Empty(t, mgr.registerCalls)
			require.Len(t, mgr.updateCalls, tt.expectRegistryCalls, "live plugin registry must not be mutated on failure paths")
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

// setupAdminAuditTest wires the full-router environment with audit capture so
// the audit middleware runs end to end for admin routes.
func setupAdminAuditTest(t *testing.T) (*TestEnvironment, *[]*model.AuditRecord) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	records := e.CaptureAuditRecords()
	return e, records
}

// runningReindexJob returns a fresh (non-stale) running job status.
func runningReindexJob() *indexer.JobStatus {
	return &indexer.JobStatus{
		JobID:         "reindexjobid12345678901234",
		Status:        indexer.JobStatusRunning,
		StartedAt:     time.Now().Add(-2 * time.Minute),
		LastUpdatedAt: time.Now().Add(-time.Minute),
	}
}

func TestAuditReindexPosts(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		isAdmin        bool
		mockIndexer    *mockIndexerService // nil leaves indexerService nil
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name:           "explicit clearIndex=false is recorded on the job-already-running fail path",
			body:           `{"clearIndex": false}`,
			isAdmin:        true,
			mockIndexer:    &mockIndexerService{jobStatus: runningReindexJob(), searchConfigured: true},
			expectedStatus: http.StatusConflict,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusConflict, rec.Error.Code)
				assert.Equal(t, false, rec.EventData.Parameters["clear_index"])
			},
		},
		{
			name:           "empty body records the clear_index=true default",
			body:           "",
			isAdmin:        true,
			mockIndexer:    &mockIndexerService{jobStatus: runningReindexJob(), searchConfigured: true},
			expectedStatus: http.StatusConflict,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusConflict, rec.Error.Code)
				assert.Equal(t, true, rec.EventData.Parameters["clear_index"])
			},
		},
		{
			name:           "search not configured records a 400 fail before clear_index is known",
			body:           "",
			isAdmin:        true,
			mockIndexer:    nil,
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.Empty(t, rec.Error.Description,
					"free-form handler error text must never enter audit records")
				assert.NotContains(t, rec.EventData.Parameters, "clear_index")
			},
		},
		{
			// Covers the non-admin denial once for all admin routes: the audit
			// middleware runs before authorization, so the denial is recorded.
			name:           "non-admin request records a 403 fail",
			body:           `{"clearIndex": false}`,
			isAdmin:        false,
			mockIndexer:    nil,
			expectedStatus: http.StatusForbidden,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusForbidden, rec.Error.Code)
				assert.NotContains(t, rec.EventData.Parameters, "clear_index")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, records := setupAdminAuditTest(t)
			defer e.Cleanup(t)

			e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(tt.isAdmin)
			if tt.mockIndexer != nil {
				e.api.indexerService = createMockIndexer(t, tt.mockIndexer)
			}

			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(http.MethodPost, "/admin/reindex", body)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Mattermost-User-Id", "userid")

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventReindexPosts, rec.EventName)
			assert.Equal(t, "userid", rec.Actor.UserId)
			assert.Equal(t, "/admin/reindex", rec.Meta[model.AuditKeyAPIPath])
			tt.validateRecord(t, rec)
		})
	}
}

func TestAuditCancelReindexJob(t *testing.T) {
	tests := []struct {
		name           string
		mockIndexer    *mockIndexerService // nil leaves indexerService nil
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name:           "successful cancel records cancel_requested",
			mockIndexer:    &mockIndexerService{jobStatus: runningReindexJob(), casOK: true},
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, indexer.JobStatusCancelRequested, rec.EventData.Parameters["job_status"])
			},
		},
		{
			name:           "no indexer records a 404 fail with no_job",
			mockIndexer:    nil,
			expectedStatus: http.StatusNotFound,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusNotFound, rec.Error.Code)
				assert.Equal(t, "no_job", rec.EventData.Parameters["job_status"])
			},
		},
		{
			name:           "terminal job records a 400 fail with not_running",
			mockIndexer:    &mockIndexerService{jobStatus: &indexer.JobStatus{Status: indexer.JobStatusCompleted}},
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.Equal(t, "not_running", rec.EventData.Parameters["job_status"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, records := setupAdminAuditTest(t)
			defer e.Cleanup(t)

			e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(true)
			if tt.mockIndexer != nil {
				e.api.indexerService = createMockIndexer(t, tt.mockIndexer)
			}

			req := httptest.NewRequest(http.MethodPost, "/admin/reindex/cancel", nil)
			req.Header.Set("Mattermost-User-Id", "userid")

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventCancelReindexJob, rec.EventName)
			assert.Equal(t, "userid", rec.Actor.UserId)
			tt.validateRecord(t, rec)
		})
	}
}

func TestAuditCatchUpReindex(t *testing.T) {
	tests := []struct {
		name           string
		mockIndexer    *mockIndexerService // nil leaves indexerService nil
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name: "job already running records a 409 fail with the blocking job status",
			mockIndexer: &mockIndexerService{
				jobStatus:        runningReindexJob(),
				searchConfigured: true,
				lastIndexedAt:    time.Now().Add(-time.Hour).UnixMilli(),
			},
			expectedStatus: http.StatusConflict,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusConflict, rec.Error.Code)
				assert.Equal(t, indexer.JobStatusRunning, rec.EventData.Parameters["job_status"])
			},
		},
		{
			name:           "no previous index records a 400 fail without job_status",
			mockIndexer:    &mockIndexerService{searchConfigured: true},
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.NotContains(t, rec.EventData.Parameters, "job_status")
			},
		},
		{
			name:           "search not configured records a 400 fail",
			mockIndexer:    nil,
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.Empty(t, rec.Error.Description,
					"free-form handler error text must never enter audit records")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, records := setupAdminAuditTest(t)
			defer e.Cleanup(t)

			e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(true)
			if tt.mockIndexer != nil {
				e.api.indexerService = createMockIndexer(t, tt.mockIndexer)
			}

			req := httptest.NewRequest(http.MethodPost, "/admin/reindex/catchup", nil)
			req.Header.Set("Mattermost-User-Id", "userid")

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventCatchUpReindex, rec.EventName)
			assert.Equal(t, "userid", rec.Actor.UserId)
			tt.validateRecord(t, rec)
		})
	}
}

const plantedRebuildSentinel = "SENTINEL_REBUILD_PROMPT_MUST_NOT_APPEAR"

func TestAuditRebuildVectorIndex(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		mockIndexer    *mockIndexerService // nil leaves indexerService nil
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name:           "job already running records a 409 fail with the blocking job status",
			body:           `{"prompt":"` + plantedRebuildSentinel + `"}`,
			mockIndexer:    &mockIndexerService{jobStatus: runningReindexJob(), searchConfigured: true},
			expectedStatus: http.StatusConflict,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusConflict, rec.Error.Code)
				assert.Equal(t, indexer.JobStatusRunning, rec.EventData.Parameters["job_status"])

				raw, err := json.Marshal(rec)
				require.NoError(t, err)
				assert.NotContains(t, string(raw), plantedRebuildSentinel,
					"audit record must never carry request content")
			},
		},
		{
			name:           "search not configured records a 400 fail",
			body:           `{"prompt":"` + plantedRebuildSentinel + `"}`,
			mockIndexer:    nil,
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.Empty(t, rec.Error.Description,
					"free-form handler error text must never enter audit records")
				assert.NotContains(t, rec.EventData.Parameters, "hnswM")
				assert.NotContains(t, rec.EventData.Parameters, "hnsw_m")

				raw, err := json.Marshal(rec)
				require.NoError(t, err)
				assert.NotContains(t, string(raw), plantedRebuildSentinel,
					"audit record must never carry request content")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, records := setupAdminAuditTest(t)
			defer e.Cleanup(t)

			e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(true)
			if tt.mockIndexer != nil {
				e.api.indexerService = createMockIndexer(t, tt.mockIndexer)
			}

			req := httptest.NewRequest(http.MethodPost, "/admin/reindex/rebuild-vector-index", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Mattermost-User-Id", "userid")

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventRebuildVectorIndex, rec.EventName)
			assert.Equal(t, "userid", rec.Actor.UserId)
			tt.validateRecord(t, rec)
		})
	}
}

// toolsCacheMCPClientManager overrides GetToolsCache so the clear-cache
// success path can run against a real mcp.ToolsCache over the mocked KV store.
type toolsCacheMCPClientManager struct {
	*mockMCPClientManager
	cache *mcp.ToolsCache
}

func (m *toolsCacheMCPClientManager) GetToolsCache() *mcp.ToolsCache { return m.cache }

func TestAuditClearMCPToolsCache(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(e *TestEnvironment)
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name: "success records cleared_servers",
			setup: func(e *TestEnvironment) {
				// ClearAll lists all KV keys and deletes the cache-prefixed
				// ones; pluginapi implements Delete as KVSetWithOptions with
				// a nil value.
				e.mockAPI.On("KVList", 0, 1000).Return([]string{
					"mcp_tools_cache_v1_server1",
					"mcp_tools_cache_v1_server2",
					"unrelated_key",
				}, nil)
				e.mockAPI.On("KVSetWithOptions", "mcp_tools_cache_v1_server1", []byte(nil), model.PluginKVSetOptions{}).Return(true, nil)
				e.mockAPI.On("KVSetWithOptions", "mcp_tools_cache_v1_server2", []byte(nil), model.PluginKVSetOptions{}).Return(true, nil)

				e.api.mcpClientManager = &toolsCacheMCPClientManager{
					mockMCPClientManager: e.mcp,
					cache:                mcp.NewToolsCache(&e.client.KV, &e.client.Log),
				}
			},
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, 2, rec.EventData.Parameters["cleared_servers"])
			},
		},
		{
			name: "missing tools cache records a 500 fail",
			setup: func(e *TestEnvironment) {
				// The default mockMCPClientManager returns a nil tools cache.
			},
			expectedStatus: http.StatusInternalServerError,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusInternalServerError, rec.Error.Code)
				assert.Empty(t, rec.Error.Description,
					"free-form handler error text must never enter audit records")
				assert.NotContains(t, rec.EventData.Parameters, "cleared_servers")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, records := setupAdminAuditTest(t)
			defer e.Cleanup(t)

			e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(true)
			tt.setup(e)

			req := httptest.NewRequest(http.MethodPost, "/admin/mcp/tools/cache/clear", nil)
			req.Header.Set("Mattermost-User-Id", "userid")

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventClearMCPToolsCache, rec.EventName)
			assert.Equal(t, "userid", rec.Actor.UserId)
			tt.validateRecord(t, rec)
		})
	}
}

func TestAuditUpdatePluginServer(t *testing.T) {
	// Planted tool config content that must never leak into the audit record.
	const plantedToolName = "planted-secret-tool-name"

	tests := []struct {
		name           string
		pluginID       string
		body           string
		nilStoredCfg   bool
		getErr         error
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name:           "disabling the server records the effective enabled=false",
			pluginID:       "com.mattermost.demo",
			body:           `{"enabled": false}`,
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, "com.mattermost.demo", rec.EventData.Parameters[audit.KeyMCPPluginID])
				assert.Equal(t, false, rec.EventData.Parameters["enabled"])
				assert.Equal(t, false, rec.EventData.Parameters["tool_configs_changed"])
				assert.Equal(t, true, rec.EventData.Parameters["persisted"])
			},
		},
		{
			name:           "tool config update records tool_configs_changed but never the configs",
			pluginID:       "com.mattermost.demo",
			body:           `{"tool_configs": [{"name": "` + plantedToolName + `", "policy": "ask", "enabled": false}]}`,
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, "com.mattermost.demo", rec.EventData.Parameters[audit.KeyMCPPluginID])
				// Enabled was not in the request; the preserved value is recorded.
				assert.Equal(t, true, rec.EventData.Parameters["enabled"])
				assert.Equal(t, true, rec.EventData.Parameters["tool_configs_changed"])

				raw, err := json.Marshal(rec)
				require.NoError(t, err)
				assert.NotContains(t, string(raw), plantedToolName, "audit record must never carry tool config content")
			},
		},
		{
			name:           "unregistered plugin records a 404 fail with the target id",
			pluginID:       "com.missing",
			body:           `{"enabled": true}`,
			expectedStatus: http.StatusNotFound,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusNotFound, rec.Error.Code)
				assert.Equal(t, "com.missing", rec.EventData.Parameters[audit.KeyMCPPluginID])
			},
		},
		{
			name:           "malformed body records a 400 fail with the target id",
			pluginID:       "com.mattermost.demo",
			body:           `not json`,
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.Equal(t, "com.mattermost.demo", rec.EventData.Parameters[audit.KeyMCPPluginID])
				assert.NotContains(t, rec.EventData.Parameters, "tool_configs_changed")
			},
		},
		{
			name:           "nil stored config records a 500 fail with the merged parameters",
			pluginID:       "com.mattermost.demo",
			body:           `{"enabled": false}`,
			nilStoredCfg:   true,
			expectedStatus: http.StatusInternalServerError,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusInternalServerError, rec.Error.Code)
				assert.Equal(t, "com.mattermost.demo", rec.EventData.Parameters[audit.KeyMCPPluginID])
				assert.Equal(t, false, rec.EventData.Parameters["enabled"])
				assert.Equal(t, false, rec.EventData.Parameters["tool_configs_changed"])
				assert.NotContains(t, rec.EventData.Parameters, "persisted")
			},
		},
		{
			name:           "prior-config read failure records a 500 fail with the merged parameters",
			pluginID:       "com.mattermost.demo",
			body:           `{"enabled": false}`,
			getErr:         errors.New("config store unavailable"),
			expectedStatus: http.StatusInternalServerError,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusInternalServerError, rec.Error.Code)
				assert.Equal(t, "com.mattermost.demo", rec.EventData.Parameters[audit.KeyMCPPluginID])
				assert.Equal(t, false, rec.EventData.Parameters["enabled"])
				assert.NotContains(t, rec.EventData.Parameters, "persisted")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, records := setupAdminAuditTest(t)
			defer e.Cleanup(t)

			e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(true)

			e.mcp.pluginServers = []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}}

			var storedCfg *config.Config
			if !tt.nilStoredCfg {
				storedCfg = &config.Config{}
			}
			e.api.configStore = &testConfigStore{cfg: storedCfg, getErr: tt.getErr}
			e.api.configUpdater = &testConfigUpdater{}
			e.api.clusterNotifier = &testClusterNotifier{}

			req := httptest.NewRequest(http.MethodPut, "/admin/mcp/plugin-servers/"+tt.pluginID, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Mattermost-User-Id", "userid")

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventUpdateMCPPluginServer, rec.EventName)
			assert.Equal(t, "userid", rec.Actor.UserId)
			tt.validateRecord(t, rec)
		})
	}
}
