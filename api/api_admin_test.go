// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/indexer"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/metrics"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// setupAdminTestEnvironment creates a test environment for admin endpoint testing
func setupAdminTestEnvironment(t *testing.T) (*API, *plugintest.API) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)

	cfg := &testConfigImpl{}
	noopMetrics := &metrics.NoopMetrics{}

	api := New(nil, nil, nil, nil, nil, client, noopMetrics, nil, cfg, nil, nil, nil, nil, nil, nil, &mockMCPClientManager{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	return api, mockAPI
}

func TestHandleGetJobStatusIncludesStale(t *testing.T) {
	tests := []struct {
		name           string
		indexerNil     bool
		jobStatus      *indexer.JobStatus
		expectedStatus int
		expectedStale  bool
	}{
		{
			name:           "returns 404 when indexer is nil",
			indexerNil:     true,
			expectedStatus: http.StatusNotFound,
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
			api, mockAPI := setupAdminTestEnvironment(t)
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
		})
	}
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
			api, mockAPI := setupAdminTestEnvironment(t)
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

// notFoundError simulates the "not found" error that the indexer checks for
type notFoundError struct{}

func (e notFoundError) Error() string {
	return "not found"
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

	// Setup mock for GetJobStatus - always handle the ReindexJobKey
	if mockService.jobStatus == nil {
		// No job exists - return "not found" error
		mockClient.On("KVGet", indexer.ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(notFoundError{}).Maybe()
	} else {
		// Job exists - populate the status
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

// TestHandleGetMCPTools_PluginServer exercises the Phase 1F third-loop that
// renders plugin-registered MCP servers alongside embedded and remote rows
// on GET /admin/mcp/tools. It verifies:
//   - enabled plugin entries are probed via DiscoverPluginServerTools;
//   - disabled plugin entries are rendered with an empty tool list and NO probe;
//   - probe errors surface through MCPServerInfo.Error;
//   - ServerType and Enabled discriminator fields are populated.
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI := setupAdminTestEnvironment(t)
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

			var body MCPToolsResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

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
		})
	}
}

// TestHandleUpdatePluginServer exercises the admin-only enable/disable toggle
// endpoint PUT /admin/mcp/plugin-servers/:pluginID introduced in Phase 1F.
// It verifies:
//   - happy path flips Enabled while preserving identity fields;
//   - 404 when the pluginID has no registration;
//   - 400 on malformed JSON body;
//   - admin-auth gate: requests without PermissionManageSystem return 403.
func TestHandleUpdatePluginServer(t *testing.T) {
	tests := []struct {
		name                string
		pluginID            string
		preRegistered       []mcp.PluginServerConfig
		body                string
		hasAdminPerm        bool
		expectStatus        int
		expectRegisterCalls int
		expectEnabledAfter  bool
		expectExposeAfter   bool
		expectRebuildCalls  int
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
			name:     "expose_external toggle: false -> true triggers rebuild",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
				Enabled: true, ExposeExternal: false,
			}},
			body:                `{"enabled": true, "expose_external": true}`,
			hasAdminPerm:        true,
			expectStatus:        http.StatusOK,
			expectRegisterCalls: 1,
			expectEnabledAfter:  true,
			expectExposeAfter:   true,
			expectRebuildCalls:  1,
		},
		{
			name:     "expose_external omitted preserves existing value",
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
			expectExposeAfter:   true, // unchanged because request omitted the field
			expectRebuildCalls:  1,    // still rebuilds because FOUND had ExposeExternal=true
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(tt.hasAdminPerm).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()

			mgr := api.mcpClientManager.(*mockMCPClientManager)
			mgr.pluginServers = tt.preRegistered

			// Inject a spy rebuilder so we can observe RebuildExternalServer
			// invocations in the ExposeExternal-toggle cases. The production
			// rebuilder lookup falls through to nil in tests that don't need
			// it, so unconditionally wiring the spy is safe.
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
				// Identity fields must be preserved.
				require.Equal(t, "Demo", mgr.registerCalls[0].Name)
				require.Equal(t, "/mcp", mgr.registerCalls[0].Path)
				require.Equal(t, "com.mattermost.demo", mgr.registerCalls[0].PluginID)
			}
			require.Equal(t, tt.expectRebuildCalls, spy.callCount)
		})
	}
}
