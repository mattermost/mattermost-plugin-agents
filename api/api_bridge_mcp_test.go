// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Test plugin IDs. These are not validated for format — the handlers trust
// the Mattermost-Plugin-ID header (populated by the Mattermost server on
// inter-plugin dispatch), which in production is an actual plugin manifest ID.
const (
	testCallerPluginID = "com.mattermost.plugin-playbooks"
	testOtherPluginID  = "com.mattermost.plugin-calls"
	testEvilPluginID   = "com.evil.plugin"
)

// spyRebuilder is a test double for externalServerRebuilder.
type spyRebuilder struct {
	callCount int
}

func (s *spyRebuilder) RebuildExternalServer() { s.callCount++ }

// mcpRegisterRequest is a convenience wrapper to build JSON bodies in tests.
func mcpRegisterRequest(t *testing.T, body any) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	return httptest.NewRequest(http.MethodPost, "/bridge/v1/mcp/register", &buf)
}

func mcpUnregisterRequest(t *testing.T, body any) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	return httptest.NewRequest(http.MethodPost, "/bridge/v1/mcp/unregister", &buf)
}

// serveAndReturn drives the full API router so the middleware chain runs.
// We can't call handleMCPRegister directly with a raw gin.Context because the
// security model depends on interPluginAuthorizationRequired being applied by
// the group — using ServeHTTP is the only way to exercise the real stack.
func serveAndReturn(e *TestEnvironment, req *http.Request) *http.Response {
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, req)
	return recorder.Result()
}

func readJSONError(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if len(body) == 0 {
		return ""
	}
	var er bridgeclient.ErrorResponse
	require.NoError(t, json.Unmarshal(body, &er))
	return er.Error
}

// =============================================================================
// handleMCPRegister
// =============================================================================

func TestHandleMCPRegister(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	validCfg := mcp.PluginServerConfig{
		PluginID:       testCallerPluginID,
		Name:           "Playbooks MCP",
		Path:           "/mcp",
		Enabled:        true,
		ExposeExternal: false,
	}

	tests := []struct {
		name       string
		body       any    // nil => empty body
		header     string // "" => no Mattermost-Plugin-ID header
		raw        []byte // when non-nil, bypasses JSON encoding (for invalid-body tests)
		wantStatus int
		wantErrSub string // substring expected in ErrorResponse.Error
		assertMock func(t *testing.T, m *mockMCPClientManager)
	}{
		{
			name:       "happy path: valid body + matching header",
			body:       validCfg,
			header:     testCallerPluginID,
			wantStatus: http.StatusOK,
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Len(t, m.registerCalls, 1)
				require.Equal(t, validCfg, m.registerCalls[0])
				require.Empty(t, m.unregisterCalls)
			},
		},
		{
			name:       "missing header — middleware rejects with 401",
			body:       validCfg,
			header:     "",
			wantStatus: http.StatusUnauthorized,
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Empty(t, m.registerCalls)
			},
		},
		{
			name: "missing plugin_id in body",
			body: mcp.PluginServerConfig{
				Name: "Playbooks MCP", Path: "/mcp", Enabled: true,
			},
			header:     testCallerPluginID,
			wantStatus: http.StatusBadRequest,
			wantErrSub: "plugin_id is required",
			assertMock: func(t *testing.T, m *mockMCPClientManager) { require.Empty(t, m.registerCalls) },
		},
		{
			name: "missing name in body",
			body: mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Path: "/mcp", Enabled: true,
			},
			header:     testCallerPluginID,
			wantStatus: http.StatusBadRequest,
			wantErrSub: "name is required",
			assertMock: func(t *testing.T, m *mockMCPClientManager) { require.Empty(t, m.registerCalls) },
		},
		{
			name: "missing path in body",
			body: mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Playbooks MCP", Enabled: true,
			},
			header:     testCallerPluginID,
			wantStatus: http.StatusBadRequest,
			wantErrSub: "path is required",
			assertMock: func(t *testing.T, m *mockMCPClientManager) { require.Empty(t, m.registerCalls) },
		},
		{
			// RELEASE-GATE SECURITY TEST. Must pass before merge.
			// A caller plugin claims to be registering a server for a different
			// plugin — 403, and the registry is NOT mutated.
			name:       "SECURITY: plugin_id mismatch — 403, no registry mutation",
			body:       mcp.PluginServerConfig{PluginID: testOtherPluginID, Name: "Fake", Path: "/mcp", Enabled: true},
			header:     testEvilPluginID,
			wantStatus: http.StatusForbidden,
			wantErrSub: "plugin_id does not match",
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Empty(t, m.registerCalls, "RegisterPluginServer must not be called on identity mismatch")
			},
		},
		{
			name:       "invalid JSON body — 400",
			raw:        []byte("{not-json"),
			header:     testCallerPluginID,
			wantStatus: http.StatusBadRequest,
			wantErrSub: "invalid request body",
			assertMock: func(t *testing.T, m *mockMCPClientManager) { require.Empty(t, m.registerCalls) },
		},
		{
			name: "boolean zero values (Enabled=false, ExposeExternal=false) are accepted",
			body: mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
				Enabled: false, ExposeExternal: false,
			},
			header:     testCallerPluginID,
			wantStatus: http.StatusOK,
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Len(t, m.registerCalls, 1)
				require.False(t, m.registerCalls[0].Enabled)
				require.False(t, m.registerCalls[0].ExposeExternal)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.mockAPI.On("LogError", mock.Anything).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

			var req *http.Request
			if tc.raw != nil {
				req = httptest.NewRequest(http.MethodPost, "/bridge/v1/mcp/register", bytes.NewReader(tc.raw))
			} else {
				req = mcpRegisterRequest(t, tc.body)
			}
			if tc.header != "" {
				req.Header.Set("Mattermost-Plugin-ID", tc.header)
			}

			resp := serveAndReturn(e, req)
			require.Equal(t, tc.wantStatus, resp.StatusCode)
			if tc.wantErrSub != "" {
				require.Contains(t, readJSONError(t, resp), tc.wantErrSub)
			}
			if tc.assertMock != nil {
				tc.assertMock(t, e.mcp)
			}
		})
	}
}

func TestHandleMCPRegister_PluginIDMismatch_Returns403(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	// RELEASE-GATE: Dedicated top-level test of the register identity check
	// so CI can single-out this security gate in its required-checks list.
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.mockAPI.On("LogError", mock.Anything).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

	req := mcpRegisterRequest(t, mcp.PluginServerConfig{
		PluginID: testOtherPluginID,
		Name:     "Fake",
		Path:     "/mcp",
		Enabled:  true,
	})
	req.Header.Set("Mattermost-Plugin-ID", testEvilPluginID)

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Contains(t, readJSONError(t, resp), "plugin_id does not match")
	require.Empty(t, e.mcp.registerCalls, "RegisterPluginServer must not be called on identity mismatch")
}

func TestHandleMCPUnregister_PluginIDMismatch_Returns403(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	// RELEASE-GATE: Dedicated top-level test of the unregister identity check
	// so CI can single-out this security gate in its required-checks list.
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.mockAPI.On("LogError", mock.Anything).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

	req := mcpUnregisterRequest(t, map[string]string{"plugin_id": testOtherPluginID})
	req.Header.Set("Mattermost-Plugin-ID", testEvilPluginID)

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Contains(t, readJSONError(t, resp), "plugin_id does not match")
	require.Empty(t, e.mcp.unregisterCalls, "UnregisterPluginServer must not be called on identity mismatch")
}

// TestHandleMCPRegister_PreservesAdminSetFieldsOnReregister covers the bug where
// a plugin's OnActivate auto-restart (post crash / kill -9) triggers a new
// Register() that used to clobber admin-set Enabled / ExposeExternal values.
//
// Design: plugins own identity (PluginID/Name/Path); admins own Enabled /
// ExposeExternal POST-registration. On a re-registration we must preserve the
// admin-set flags and only refresh identity fields.
func TestHandleMCPRegister_PreservesAdminSetFieldsOnReregister(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name                 string
		existing             *mcp.PluginServerConfig // nil => first registration
		incoming             mcp.PluginServerConfig
		wantEnabledAfter     bool
		wantExposeAfter      bool
		wantName             string
		wantPath             string
		wantToolConfigsAfter []mcp.ToolConfig
		wantRebuilderInvoked bool
	}{
		{
			// First registration — plugin's self-declared state is honored
			// verbatim (letting first-party plugins default ExposeExternal=true
			// on install). Rebuilder fires because incoming ExposeExternal=true.
			name:     "first registration: plugin state honored as-is",
			existing: nil,
			incoming: mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
				Enabled: false, ExposeExternal: true,
			},
			wantEnabledAfter:     false,
			wantExposeAfter:      true,
			wantName:             "Playbooks MCP",
			wantPath:             "/mcp",
			wantRebuilderInvoked: true,
		},
		{
			// Re-registration: admin previously set Enabled=true + Expose=true.
			// Plugin defaults on restart are Enabled=false, Expose=false.
			// Admin state MUST win.
			name: "re-register: admin-set Enabled=true / Expose=true preserved",
			existing: &mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
				Enabled: true, ExposeExternal: true,
			},
			incoming: mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
				Enabled: false, ExposeExternal: false,
			},
			wantEnabledAfter:     true,
			wantExposeAfter:      true,
			wantName:             "Playbooks MCP",
			wantPath:             "/mcp",
			wantRebuilderInvoked: true, // existing.ExposeExternal=true => persisted
		},
		{
			// Re-registration where plugin upgraded Path/Name — identity
			// refresh wins for those fields but admin flags still preserved.
			name: "re-register: identity refreshed, admin flags preserved",
			existing: &mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Old Name", Path: "/old",
				Enabled: true, ExposeExternal: false,
			},
			incoming: mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "New Name", Path: "/new",
				Enabled: false, ExposeExternal: true, // plugin claims expose=true — ignored
			},
			wantEnabledAfter:     true,  // from existing admin state
			wantExposeAfter:      false, // from existing admin state, NOT from plugin
			wantName:             "New Name",
			wantPath:             "/new",
			wantRebuilderInvoked: false, // expose stays false => no rebuild
		},
		{
			// M2 release gate: ToolConfigs is admin-owned post-registration.
			// Source plugin re-registers with no ToolConfigs in its payload;
			// existing admin policy must survive verbatim.
			name: "re-register: admin-set ToolConfigs preserved",
			existing: &mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
				Enabled: true, ExposeExternal: false,
				ToolConfigs: []mcp.ToolConfig{
					{Name: "echo", Policy: "ask", Enabled: false},
					{Name: "sum", Policy: "auto_run_in_dm", Enabled: true},
				},
			},
			incoming: mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
				Enabled: false, ExposeExternal: false,
				// ToolConfigs intentionally omitted — plugin doesn't carry admin policy.
			},
			wantEnabledAfter: true,  // from existing admin state
			wantExposeAfter:  false, // from existing admin state
			wantName:         "Playbooks MCP",
			wantPath:         "/mcp",
			wantToolConfigsAfter: []mcp.ToolConfig{
				{Name: "echo", Policy: "ask", Enabled: false},
				{Name: "sum", Policy: "auto_run_in_dm", Enabled: true},
			},
			wantRebuilderInvoked: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			spy := &spyRebuilder{}
			e.api.SetExternalRebuilderForTest(spy)

			e.mockAPI.On("LogError", mock.Anything).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

			if tc.existing != nil {
				e.mcp.pluginServers = []mcp.PluginServerConfig{*tc.existing}
			}

			req := mcpRegisterRequest(t, tc.incoming)
			req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)

			resp := serveAndReturn(e, req)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Len(t, e.mcp.registerCalls, 1)

			saved := e.mcp.registerCalls[0]
			require.Equal(t, tc.wantEnabledAfter, saved.Enabled, "Enabled flag")
			require.Equal(t, tc.wantExposeAfter, saved.ExposeExternal, "ExposeExternal flag")
			require.Equal(t, tc.wantName, saved.Name, "Name (identity field)")
			require.Equal(t, tc.wantPath, saved.Path, "Path (identity field)")

			if tc.wantToolConfigsAfter != nil {
				require.Equal(t, tc.wantToolConfigsAfter, saved.ToolConfigs, "ToolConfigs (admin-owned) preserved on re-register")
			} else {
				require.Empty(t, saved.ToolConfigs, "no ToolConfigs expected for this case")
			}

			if tc.wantRebuilderInvoked {
				require.Equal(t, 1, spy.callCount, "rebuilder must be invoked")
			} else {
				require.Equal(t, 0, spy.callCount, "rebuilder must NOT be invoked")
			}
		})
	}
}

func TestHandleMCPRegister_ExposeExternal_TriggersRebuild(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	// RELEASE-GATE: Verifies the rebuild-trigger wiring for both branches
	// (rebuilder present via injected spy → called; rebuilder absent → no-op).
	tests := []struct {
		name           string
		exposeExternal bool
		injectSpy      bool
		wantCalls      int
	}{
		{"ExposeExternal=true, rebuilder present — triggers rebuild", true, true, 1},
		{"ExposeExternal=false, rebuilder present — does NOT trigger", false, true, 0},
		{"ExposeExternal=true, rebuilder absent — pre-1G no-op path", true, false, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			var spy *spyRebuilder
			if tc.injectSpy {
				spy = &spyRebuilder{}
				e.api.SetExternalRebuilderForTest(spy)
			}

			e.mockAPI.On("LogError", mock.Anything).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

			req := mcpRegisterRequest(t, mcp.PluginServerConfig{
				PluginID:       testCallerPluginID,
				Name:           "Playbooks MCP",
				Path:           "/mcp",
				Enabled:        true,
				ExposeExternal: tc.exposeExternal,
			})
			req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)

			resp := serveAndReturn(e, req)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Len(t, e.mcp.registerCalls, 1, "registry mutation must happen regardless of rebuilder state")
			if tc.injectSpy {
				require.Equal(t, tc.wantCalls, spy.callCount)
			}
		})
	}
}

// =============================================================================
// handleMCPUnregister
// =============================================================================

func TestHandleMCPUnregister(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name       string
		body       any
		header     string
		raw        []byte
		wantStatus int
		wantErrSub string
		assertMock func(t *testing.T, m *mockMCPClientManager)
	}{
		{
			name:       "happy path: valid body + matching header",
			body:       map[string]string{"plugin_id": testCallerPluginID},
			header:     testCallerPluginID,
			wantStatus: http.StatusOK,
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Equal(t, []string{testCallerPluginID}, m.unregisterCalls)
				require.Empty(t, m.registerCalls)
			},
		},
		{
			name:       "missing header — middleware rejects with 401",
			body:       map[string]string{"plugin_id": testCallerPluginID},
			header:     "",
			wantStatus: http.StatusUnauthorized,
			assertMock: func(t *testing.T, m *mockMCPClientManager) { require.Empty(t, m.unregisterCalls) },
		},
		{
			name:       "missing plugin_id in body — 400",
			body:       map[string]string{},
			header:     testCallerPluginID,
			wantStatus: http.StatusBadRequest,
			wantErrSub: "plugin_id is required",
			assertMock: func(t *testing.T, m *mockMCPClientManager) { require.Empty(t, m.unregisterCalls) },
		},
		{
			// RELEASE-GATE SECURITY TEST (unregister mirror). Must pass before merge.
			name:       "SECURITY: plugin_id mismatch — 403, no registry mutation",
			body:       map[string]string{"plugin_id": testOtherPluginID},
			header:     testEvilPluginID,
			wantStatus: http.StatusForbidden,
			wantErrSub: "plugin_id does not match",
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Empty(t, m.unregisterCalls, "UnregisterPluginServer must not be called on identity mismatch")
			},
		},
		{
			name:       "invalid JSON body — 400",
			raw:        []byte("{bad"),
			header:     testCallerPluginID,
			wantStatus: http.StatusBadRequest,
			wantErrSub: "invalid request body",
			assertMock: func(t *testing.T, m *mockMCPClientManager) { require.Empty(t, m.unregisterCalls) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.mockAPI.On("LogError", mock.Anything).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

			var req *http.Request
			if tc.raw != nil {
				req = httptest.NewRequest(http.MethodPost, "/bridge/v1/mcp/unregister", bytes.NewReader(tc.raw))
			} else {
				req = mcpUnregisterRequest(t, tc.body)
			}
			if tc.header != "" {
				req.Header.Set("Mattermost-Plugin-ID", tc.header)
			}

			resp := serveAndReturn(e, req)
			require.Equal(t, tc.wantStatus, resp.StatusCode)
			if tc.wantErrSub != "" {
				require.Contains(t, readJSONError(t, resp), tc.wantErrSub)
			}
			if tc.assertMock != nil {
				tc.assertMock(t, e.mcp)
			}
		})
	}
}

func TestHandleMCPUnregister_TriggersRebuild(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	// Unregister always triggers rebuild regardless of ExposeExternal — the
	// server must drop any stale proxy tools for the departing plugin.
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	spy := &spyRebuilder{}
	e.api.SetExternalRebuilderForTest(spy)

	e.mockAPI.On("LogError", mock.Anything).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

	req := mcpUnregisterRequest(t, map[string]string{"plugin_id": testCallerPluginID})
	req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 1, spy.callCount, "unregister must always trigger external rebuild")
}

// =============================================================================
// Pre-1G sanity: nil rebuilder must not panic
// =============================================================================

// TestHandleMCPRegister_NilRebuilderSafe confirms that when no rebuilder is
// available (pre-1G state: mcpHandlers is nil AND no test spy is injected),
// the register handler does not panic and still returns 200. This captures
// the behavior our "option (b)" design guarantees.
func TestHandleMCPRegister_NilRebuilderSafe(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	// SetupTestEnvironment constructs the API with mcpHandlers=nil (the 18th
	// positional argument to New(...) is nil). No test spy injected. The
	// resolver should return nil and the handler should skip the rebuild.
	require.Nil(t, e.api.mcpHandlers, "precondition: production mcpHandlers must be nil in this test")

	e.mockAPI.On("LogError", mock.Anything).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

	req := mcpRegisterRequest(t, mcp.PluginServerConfig{
		PluginID: testCallerPluginID, Name: "X", Path: "/mcp",
		Enabled: true, ExposeExternal: true, // <-- would trigger rebuild if available
	})
	req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusOK, resp.StatusCode, "handler must succeed even when rebuilder is unavailable")
	require.Len(t, e.mcp.registerCalls, 1, "registry mutation must still happen")
}

// =============================================================================
// Unregister/Register cycle: admin fields recovered from persisted config
// =============================================================================

// TestHandleMCPRegister_PreservesAdminFieldsAfterUnregister covers the M2 P1
// regression where a source plugin's OnDeactivate→OnActivate cycle (e.g.
// plugin restart on upgrade or crash) drops admin-owned state.
//
// Sequence under test:
//
//  1. Steady state: plugin previously registered + admin set Enabled=true,
//     ExposeExternal=true, ToolConfigs=[...]. State lives in BOTH the
//     in-memory mcpClientManager (refreshed on every Register +
//     hydrated from config on ReInit) AND the persisted MCPConfig.PluginServers
//     (admin-owned).
//
//  2. Plugin OnDeactivate → POST /bridge/v1/mcp/unregister → in-memory entry
//     is DELETED. Persisted config is NOT touched (admin still owns it).
//
//  3. Plugin OnActivate → POST /bridge/v1/mcp/register with zero-valued admin
//     fields (mcphelper's wire payload only carries PluginID/Name/Path/Version
//     — see public/mcphelper/mcphelper.go: PluginMCPServer).
//
//  4. The Phase 1 preserve block's GetPluginServer lookup MISSES (just wiped).
//     Without the config-fallback, RegisterPluginServer would store zero-valued
//     admin fields and tools would silently drop from the external endpoint.
//
//  5. The fix: handleMCPRegister falls back to configStore.GetConfig().MCP.
//     PluginServers, finds the entry by PluginID, and recovers Enabled /
//     ExposeExternal / ToolConfigs.
func TestHandleMCPRegister_PreservesAdminFieldsAfterUnregister(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	persistedAdmin := mcp.PluginServerConfig{
		PluginID:       testCallerPluginID,
		Name:           "Playbooks MCP",
		Path:           "/mcp",
		Enabled:        true,
		ExposeExternal: true,
		ToolConfigs: []mcp.ToolConfig{
			{Name: "echo", Policy: "ask", Enabled: false},
			{Name: "sum", Policy: "auto_run_in_dm", Enabled: true},
		},
	}

	t.Run("Unregister then Register: admin fields recovered from persisted config", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		spy := &spyRebuilder{}
		e.api.SetExternalRebuilderForTest(spy)

		// Wire a configStore with the persisted admin state. The default
		// SetupTestEnvironment passes nil for configStore — install our test
		// double explicitly so the fallback path has something to read.
		e.api.configStore = &testConfigStore{
			cfg: &config.Config{
				MCP: config.MCPConfig{
					PluginServers: []config.PluginServerConfig{persistedAdmin},
				},
			},
		}

		// Pre-populate in-memory entry to mirror steady state (post-startup
		// hydration via syncPluginServersFromConfig).
		e.mcp.pluginServers = []mcp.PluginServerConfig{persistedAdmin}

		e.mockAPI.On("LogError", mock.Anything).Maybe()
		e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

		// Step 1: Unregister wipes in-memory entry. Persisted config untouched.
		unregReq := mcpUnregisterRequest(t, map[string]string{"plugin_id": testCallerPluginID})
		unregReq.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
		unregResp := serveAndReturn(e, unregReq)
		require.Equal(t, http.StatusOK, unregResp.StatusCode)
		require.Equal(t, []string{testCallerPluginID}, e.mcp.unregisterCalls, "unregister must dispatch")
		require.Empty(t, e.mcp.pluginServers, "in-memory entry must be wiped after unregister")
		// Unregister always fires the rebuild (per
		// TestHandleMCPUnregister_TriggersRebuild). Reset the spy so the
		// next assertion isolates the rebuild fired by the recovered-state
		// Register call.
		require.Equal(t, 1, spy.callCount, "unregister always triggers rebuild")
		spy.callCount = 0

		// Step 2: Register with the zero-valued admin payload that mcphelper's
		// wire format produces (no Enabled/ExposeExternal/ToolConfigs on the
		// wire).
		incoming := mcp.PluginServerConfig{
			PluginID:       testCallerPluginID,
			Name:           "Playbooks MCP",
			Path:           "/mcp",
			Enabled:        false, // zero value — plugin payload doesn't carry admin state
			ExposeExternal: false, // zero value
			// ToolConfigs intentionally omitted
		}
		regReq := mcpRegisterRequest(t, incoming)
		regReq.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
		regResp := serveAndReturn(e, regReq)
		require.Equal(t, http.StatusOK, regResp.StatusCode)
		require.Len(t, e.mcp.registerCalls, 1, "register must dispatch exactly once")

		saved := e.mcp.registerCalls[0]
		require.Equal(t, true, saved.Enabled, "Enabled recovered from persisted config")
		require.Equal(t, true, saved.ExposeExternal, "ExposeExternal recovered from persisted config")
		require.Equal(t, persistedAdmin.ToolConfigs, saved.ToolConfigs, "ToolConfigs recovered from persisted config")

		// Identity fields come from the new request as before.
		require.Equal(t, "Playbooks MCP", saved.Name)
		require.Equal(t, "/mcp", saved.Path)

		// Recovered ExposeExternal=true must trigger the external rebuild.
		require.Equal(t, 1, spy.callCount, "rebuild must fire because recovered ExposeExternal=true")
	})

	t.Run("first register ever: no persisted entry — plugin payload wins (no regression)", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		spy := &spyRebuilder{}
		e.api.SetExternalRebuilderForTest(spy)

		// configStore wired but with NO entry for this pluginID. Helper must
		// return (_, false) so the preserve+fallback skip and zero-value path
		// runs (matches first-time-install behavior).
		e.api.configStore = &testConfigStore{
			cfg: &config.Config{
				MCP: config.MCPConfig{
					PluginServers: []config.PluginServerConfig{
						{PluginID: testOtherPluginID, Name: "Other", Path: "/mcp", Enabled: true, ExposeExternal: true},
					},
				},
			},
		}

		// Direct unit-test on the helper for this slice/PluginID combination —
		// proves nil/miss paths work without depending on the outer flow.
		_, ok := e.api.findPersistedPluginServer(testCallerPluginID)
		require.False(t, ok, "findPersistedPluginServer must return false when pluginID is absent from persisted config")

		e.mockAPI.On("LogError", mock.Anything).Maybe()
		e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

		// Plugin's first Register declares Enabled=false, ExposeExternal=true
		// (e.g. first-party plugin opting into external aggregation by default).
		incoming := mcp.PluginServerConfig{
			PluginID:       testCallerPluginID,
			Name:           "Playbooks MCP",
			Path:           "/mcp",
			Enabled:        false,
			ExposeExternal: true,
		}
		regReq := mcpRegisterRequest(t, incoming)
		regReq.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
		regResp := serveAndReturn(e, regReq)
		require.Equal(t, http.StatusOK, regResp.StatusCode)
		require.Len(t, e.mcp.registerCalls, 1)

		saved := e.mcp.registerCalls[0]
		require.Equal(t, false, saved.Enabled, "first register: plugin payload preserved (Enabled)")
		require.Equal(t, true, saved.ExposeExternal, "first register: plugin payload preserved (ExposeExternal)")
		require.Empty(t, saved.ToolConfigs, "first register: plugin payload preserved (no ToolConfigs)")
	})

	t.Run("nil configStore: helper returns false; in-memory miss falls through to plugin payload", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		// configStore stays nil (the SetupTestEnvironment default). Helper
		// must short-circuit cleanly without panicking.
		require.Nil(t, e.api.configStore, "precondition: configStore must be nil")

		_, ok := e.api.findPersistedPluginServer(testCallerPluginID)
		require.False(t, ok, "findPersistedPluginServer must return false when configStore is nil")

		spy := &spyRebuilder{}
		e.api.SetExternalRebuilderForTest(spy)
		e.mockAPI.On("LogError", mock.Anything).Maybe()
		e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

		incoming := mcp.PluginServerConfig{
			PluginID: testCallerPluginID, Name: "X", Path: "/mcp",
			Enabled: true, ExposeExternal: false,
		}
		regReq := mcpRegisterRequest(t, incoming)
		regReq.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
		regResp := serveAndReturn(e, regReq)
		require.Equal(t, http.StatusOK, regResp.StatusCode)
		require.Len(t, e.mcp.registerCalls, 1)

		saved := e.mcp.registerCalls[0]
		require.Equal(t, true, saved.Enabled, "nil configStore: plugin payload preserved")
		require.Equal(t, false, saved.ExposeExternal, "nil configStore: plugin payload preserved")
	})
}
