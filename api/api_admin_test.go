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
	"github.com/mattermost/mattermost-plugin-agents/config"
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

// adminTestStores bundles the config-save spies so tests can assert on them
// without reaching through several interface casts.
type adminTestStores struct {
	configStore     *testConfigStore
	configUpdater   *testConfigUpdater
	clusterNotifier *testClusterNotifier
}

// setupAdminTestEnvironment creates a test environment for admin endpoint testing.
//
// handleUpdatePluginServer performs a configStore/configUpdater/clusterNotifier
// save path. Tests that do not care about save side effects ignore the returned
// stores.
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

	api := New(nil, nil, nil, nil, nil, client, noopMetrics, nil, cfg, nil, nil, nil, nil, nil, nil, &mockMCPClientManager{}, nil, nil, stores.configStore, nil, stores.configUpdater, stores.clusterNotifier, nil, nil, nil, nil)

	return api, mockAPI, stores
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

// TestHandleGetMCPTools_PluginServer verifies plugin-registered MCP servers
// render alongside embedded and remote rows on GET /admin/mcp/tools:
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
			if tt.expectToolConfigs != nil {
				require.Equal(t, tt.expectToolConfigs, pluginRow.ToolConfigs, "plugin row must surface ToolConfigs verbatim")
			}
		})
	}
}

// TestHandleUpdatePluginServer verifies the admin-only
// PUT /admin/mcp/plugin-servers/:pluginID endpoint:
//   - happy path flips Enabled while preserving identity fields;
//   - 404 when the pluginID has no registration;
//   - 400 on malformed JSON body;
//   - admin-auth gate: requests without PermissionManageSystem return 403.
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
			// REGRESSION: sending only expose_external must NOT zero Enabled.
			// Before pointer-valued Enabled, the omitted field JSON-decoded
			// to false and clobbered the admin-set true value.
			name:     "enabled omitted preserves existing true value",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
				Enabled: true, ExposeExternal: false,
			}},
			body:                `{"expose_external": true}`,
			hasAdminPerm:        true,
			expectStatus:        http.StatusOK,
			expectRegisterCalls: 1,
			expectEnabledAfter:  true, // preserved — request omitted the field
			expectExposeAfter:   true,
			expectRebuildCalls:  1,
		},
		{
			// REGRESSION mirror: omitted enabled with existing false preserved.
			name:     "enabled omitted preserves existing false value",
			pluginID: "com.mattermost.demo",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
				Enabled: false, ExposeExternal: false,
			}},
			body:                `{"expose_external": true}`,
			hasAdminPerm:        true,
			expectStatus:        http.StatusOK,
			expectRegisterCalls: 1,
			expectEnabledAfter:  false, // preserved
			expectExposeAfter:   true,
			expectRebuildCalls:  1,
		},
		{
			// Empty body is a valid no-op that re-registers the existing
			// config unchanged. Ensures neither pointer flip accidentally
			// mutates state.
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
			expectRebuildCalls:  1, // found.ExposeExternal=true still triggers rebuild
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
			expectEnabledAfter:  true,  // preserved
			expectExposeAfter:   false, // preserved
			expectToolConfigsAfter: []mcp.ToolConfig{
				{Name: "echo", Policy: "ask", Enabled: false},
			},
			expectRebuildCalls: 0, // ExposeExternal never true, no rebuild
		},
		{
			// Clearing policy: non-nil empty slice — distinct from omitted field.
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
			expectToolConfigsAfter: []mcp.ToolConfig{}, // cleared
			expectRebuildCalls:     0,
		},
		{
			// Preserve: tool_configs omitted while enabled flipped must not
			// clear existing policy.
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
			api, mockAPI, _ := setupAdminTestEnvironment(t)
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
				if tt.expectToolConfigsAfter != nil {
					require.Equal(t, tt.expectToolConfigsAfter, mgr.registerCalls[0].ToolConfigs, "ToolConfigs assertion")
				}
			}
			require.Equal(t, tt.expectRebuildCalls, spy.callCount)
		})
	}
}

// TestHandleUpdatePluginServer_PersistsToConfig covers the durable persistence
// path: a successful PATCH MUST call configStore.SaveConfig →
// configUpdater.Update → clusterNotifier.PublishConfigUpdate, in that order,
// carrying a cfg whose MCP.PluginServers slice contains the updated snapshot.
//
// Also covers the error paths: SaveConfig failure → 500 (no Update, no
// Publish); PublishConfigUpdate failure → 500 (Save and Update both ran).
func TestHandleUpdatePluginServer_PersistsToConfig(t *testing.T) {
	tests := []struct {
		name                 string
		preRegistered        []mcp.PluginServerConfig
		body                 string
		getErr               error
		saveErr              error
		publishErr           error
		expectStatus         int
		expectSaveCalls      int
		expectUpdateCalls    int
		expectPublishCalls   int
		assertPersistedState func(t *testing.T, savedCfg *config.Config)
	}{
		{
			name: "happy path — saves full snapshot and broadcasts",
			preRegistered: []mcp.PluginServerConfig{
				{
					PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp",
					Enabled: true, ExposeExternal: false,
				},
				{
					// Second pre-registered plugin — the full snapshot MUST
					// include this one even though we're only updating the first.
					PluginID: "com.mattermost.other", Name: "Other", Path: "/mcp",
					Enabled: false, ExposeExternal: false,
				},
			},
			body:               `{"tool_configs": [{"name": "echo", "policy": "ask", "enabled": false}]}`,
			expectStatus:       http.StatusOK,
			expectSaveCalls:    1,
			expectUpdateCalls:  1,
			expectPublishCalls: 1,
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
			body:               `{"enabled": false}`,
			getErr:             errors.New("config store unavailable"),
			expectStatus:       http.StatusInternalServerError,
			expectSaveCalls:    0,
			expectUpdateCalls:  0,
			expectPublishCalls: 0,
		},
		{
			name: "SaveConfig failure returns 500 and skips Update/Publish",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}},
			body:               `{"enabled": false}`,
			saveErr:            errors.New("db unreachable"),
			expectStatus:       http.StatusInternalServerError,
			expectSaveCalls:    1,
			expectUpdateCalls:  0,
			expectPublishCalls: 0,
		},
		{
			name: "PublishConfigUpdate failure returns 500 after Save+Update succeeded",
			preRegistered: []mcp.PluginServerConfig{{
				PluginID: "com.mattermost.demo", Name: "Demo", Path: "/mcp", Enabled: true,
			}},
			body:               `{"enabled": false}`,
			publishErr:         errors.New("cluster broadcast failed"),
			expectStatus:       http.StatusInternalServerError,
			expectSaveCalls:    1,
			expectUpdateCalls:  1,
			expectPublishCalls: 1,
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

			// Inject failure modes via the spy stores.
			var failingStore *failingConfigStore
			if tt.getErr != nil || tt.saveErr != nil {
				// testConfigStore doesn't currently fail — swap in a failing
				// store directly on the API instance for this test.
				failingStore = &failingConfigStore{getErr: tt.getErr, saveErr: tt.saveErr}
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

			// Save-call count: on SaveConfig failure, we expect 1 save
			// attempt and 0 update/publish. On PublishConfigUpdate failure,
			// we expect 1 each of save+update+publish. Happy path: 1 each.
			if tt.getErr != nil || tt.saveErr != nil {
				require.Equal(t, tt.expectSaveCalls, failingStore.saveCallCount)
			}
			require.Equal(t, tt.expectUpdateCalls, stores.configUpdater.callCount)
			require.Equal(t, tt.expectPublishCalls, stores.clusterNotifier.callCount)

			if tt.assertPersistedState != nil {
				require.NotNil(t, stores.configStore.cfg, "SaveConfig must have been called and persisted cfg")
				tt.assertPersistedState(t, stores.configStore.cfg)
			}
		})
	}
}

// failingConfigStore wraps testConfigStore with configurable error injection
// for the SaveConfig path. testConfigStore in api_config_test.go doesn't
// support error injection; rather than editing that file, we supply a local
// test-only store for the failure-path cases.
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
