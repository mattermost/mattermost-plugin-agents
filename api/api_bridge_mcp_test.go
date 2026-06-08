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

const (
	testCallerPluginID = "com.mattermost.plugin-playbooks"
	testOtherPluginID  = "com.mattermost.plugin-calls"
	testEvilPluginID   = "com.evil.plugin"
)

type spyRebuilder struct {
	callCount int
}

func (s *spyRebuilder) RebuildExternalServer() { s.callCount++ }

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

// serveAndReturn exercises the full router so auth middleware runs.
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
			name: "missing plugin_id in body uses trusted header",
			body: mcp.PluginServerConfig{
				Name: "Playbooks MCP", Path: "/mcp", Enabled: true,
			},
			header:     testCallerPluginID,
			wantStatus: http.StatusOK,
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Len(t, m.registerCalls, 1)
				require.Equal(t, testCallerPluginID, m.registerCalls[0].PluginID)
			},
		},
		{
			name: "omitted enabled defaults to true on first registration",
			body: map[string]any{
				"name":            "Playbooks MCP",
				"path":            "/mcp",
				"expose_external": true,
			},
			header:     testCallerPluginID,
			wantStatus: http.StatusOK,
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Len(t, m.registerCalls, 1)
				require.True(t, m.registerCalls[0].Enabled)
				require.True(t, m.registerCalls[0].ExposeExternal)
			},
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
			name: "non-absolute path rejected — would yield /<pluginID><path> at runtime",
			body: mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "mcp", Enabled: true,
			},
			header:     testCallerPluginID,
			wantStatus: http.StatusBadRequest,
			wantErrSub: "path must be absolute",
			assertMock: func(t *testing.T, m *mockMCPClientManager) { require.Empty(t, m.registerCalls) },
		},
		{
			name:       "body plugin_id mismatch ignored in favor of trusted header",
			body:       mcp.PluginServerConfig{PluginID: testOtherPluginID, Name: "Fake", Path: "/mcp", Enabled: true},
			header:     testEvilPluginID,
			wantStatus: http.StatusOK,
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Len(t, m.registerCalls, 1)
				require.Equal(t, testEvilPluginID, m.registerCalls[0].PluginID)
				require.Equal(t, "Fake", m.registerCalls[0].Name)
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
			name: "explicit enabled=false still respected when sent",
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

func TestHandleMCPRegister_TrustedHeaderControlsIdentityLookups(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.mockAPI.On("LogError", mock.Anything).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

	e.mcp.pluginServers = []mcp.PluginServerConfig{{
		PluginID:       testEvilPluginID,
		Name:           "Existing",
		Path:           "/existing",
		Enabled:        true,
		ExposeExternal: false,
		ToolConfigs: []mcp.ToolConfig{
			{Name: "existing", Policy: "ask", Enabled: false},
		},
	}}

	req := mcpRegisterRequest(t, mcp.PluginServerConfig{
		PluginID: testOtherPluginID,
		Name:     "Updated",
		Path:     "/updated",
		Enabled:  false,
	})
	req.Header.Set("Mattermost-Plugin-ID", testEvilPluginID)

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, e.mcp.registerCalls, 1)
	require.Equal(t, testEvilPluginID, e.mcp.registerCalls[0].PluginID)
	require.True(t, e.mcp.registerCalls[0].Enabled, "existing settings must be loaded using the trusted header plugin ID")
	require.Equal(t, []mcp.ToolConfig{{Name: "existing", Policy: "ask", Enabled: false}}, e.mcp.registerCalls[0].ToolConfigs)
}

func TestHandleMCPUnregister_BodyPluginIDIgnored_UsesTrustedHeader(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.mockAPI.On("LogError", mock.Anything).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

	req := mcpUnregisterRequest(t, map[string]string{"plugin_id": testOtherPluginID})
	req.Header.Set("Mattermost-Plugin-ID", testEvilPluginID)

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, []string{testEvilPluginID}, e.mcp.unregisterCalls)
}

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
			wantRebuilderInvoked: false,
		},
		{
			name: "re-register: Enabled preserved; ExposeExternal from plugin payload (can turn off)",
			existing: &mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
				Enabled: true, ExposeExternal: true,
			},
			incoming: mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
				Enabled: false, ExposeExternal: false,
			},
			wantEnabledAfter:     true,
			wantExposeAfter:      false,
			wantName:             "Playbooks MCP",
			wantPath:             "/mcp",
			wantRebuilderInvoked: true,
		},
		{
			name: "re-register: identity refreshed; Enabled preserved; ExposeExternal from plugin payload",
			existing: &mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "Old Name", Path: "/old",
				Enabled: true, ExposeExternal: false,
			},
			incoming: mcp.PluginServerConfig{
				PluginID: testCallerPluginID, Name: "New Name", Path: "/new",
				Enabled: false, ExposeExternal: true,
			},
			wantEnabledAfter:     true,
			wantExposeAfter:      true,
			wantName:             "New Name",
			wantPath:             "/new",
			wantRebuilderInvoked: true,
		},
		{
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
			},
			wantEnabledAfter: true,
			wantExposeAfter:  false,
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

	tests := []struct {
		name           string
		exposeExternal bool
		injectSpy      bool
		wantCalls      int
	}{
		{"ExposeExternal=true, rebuilder present — triggers rebuild", true, true, 1},
		{"ExposeExternal=false, first register — does NOT trigger", false, true, 0},
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

func TestHandleMCPRegister_RebuildWhenDroppingEffectiveExternalExposure(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	spy := &spyRebuilder{}
	e.api.SetExternalRebuilderForTest(spy)

	e.mockAPI.On("LogError", mock.Anything).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

	e.mcp.pluginServers = []mcp.PluginServerConfig{{
		PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
		Enabled: true, ExposeExternal: true,
	}}

	req := mcpRegisterRequest(t, mcp.PluginServerConfig{
		PluginID:       testCallerPluginID,
		Name:           "Playbooks MCP",
		Path:           "/mcp",
		Enabled:        false,
		ExposeExternal: false,
	})
	req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, e.mcp.registerCalls, 1)
	require.True(t, e.mcp.registerCalls[0].Enabled, "Enabled preserved from existing")
	require.False(t, e.mcp.registerCalls[0].ExposeExternal, "ExposeExternal from plugin payload")
	require.Equal(t, 1, spy.callCount, "rebuild when effective external drops from true to false")
}

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
			name:       "missing plugin_id in body uses trusted header",
			body:       map[string]string{},
			header:     testCallerPluginID,
			wantStatus: http.StatusOK,
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Equal(t, []string{testCallerPluginID}, m.unregisterCalls)
			},
		},
		{
			name:       "body plugin_id mismatch ignored in favor of trusted header",
			body:       map[string]string{"plugin_id": testOtherPluginID},
			header:     testEvilPluginID,
			wantStatus: http.StatusOK,
			assertMock: func(t *testing.T, m *mockMCPClientManager) {
				require.Equal(t, []string{testEvilPluginID}, m.unregisterCalls)
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

// Unregister always rebuilds so stale proxy tools are removed.
func TestHandleMCPUnregister_TriggersRebuild(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

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

func TestHandleMCPRegister_NilRebuilderSafe(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	require.Nil(t, e.api.mcpHandlers, "precondition: production mcpHandlers must be nil in this test")

	e.mockAPI.On("LogError", mock.Anything).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

	req := mcpRegisterRequest(t, mcp.PluginServerConfig{
		PluginID: testCallerPluginID, Name: "X", Path: "/mcp",
		Enabled: true, ExposeExternal: true,
	})
	req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusOK, resp.StatusCode, "handler must succeed even when rebuilder is unavailable")
	require.Len(t, e.mcp.registerCalls, 1, "registry mutation must still happen")
}

// Persisted state must not override the latest ExposeExternal payload.
func TestHandleMCPRegister_PersistedExposeExternal_DoesNotOverridePluginRequest(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	t.Run("persisted false does not cap plugin true", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		e.api.configStore = &testConfigStore{
			cfg: &config.Config{
				MCP: config.MCPConfig{
					PluginServers: []config.PluginServerConfig{{
						PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
						Enabled: true, ExposeExternal: false,
					}},
				},
			},
		}

		e.mockAPI.On("LogError", mock.Anything).Maybe()
		e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

		req := mcpRegisterRequest(t, mcp.PluginServerConfig{
			PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
			Enabled: true, ExposeExternal: true,
		})
		req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
		resp := serveAndReturn(e, req)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, e.mcp.registerCalls, 1)
		require.True(t, e.mcp.registerCalls[0].ExposeExternal,
			"plugin-requested expose_external=true must remain authoritative")
	})

	t.Run("persisted true preserves plugin true", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		e.api.configStore = &testConfigStore{
			cfg: &config.Config{
				MCP: config.MCPConfig{
					PluginServers: []config.PluginServerConfig{{
						PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
						Enabled: true, ExposeExternal: true,
					}},
				},
			},
		}

		e.mockAPI.On("LogError", mock.Anything).Maybe()
		e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

		req := mcpRegisterRequest(t, mcp.PluginServerConfig{
			PluginID: testCallerPluginID, Name: "Playbooks MCP", Path: "/mcp",
			Enabled: true, ExposeExternal: true,
		})
		req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
		resp := serveAndReturn(e, req)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, e.mcp.registerCalls, 1)
		require.True(t, e.mcp.registerCalls[0].ExposeExternal)
	})
}

// Persisted admin fields must survive unregister plus re-register.
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

		e.api.configStore = &testConfigStore{
			cfg: &config.Config{
				MCP: config.MCPConfig{
					PluginServers: []config.PluginServerConfig{persistedAdmin},
				},
			},
		}

		e.mcp.pluginServers = []mcp.PluginServerConfig{persistedAdmin}

		e.mockAPI.On("LogError", mock.Anything).Maybe()
		e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

		unregReq := mcpUnregisterRequest(t, map[string]string{"plugin_id": testCallerPluginID})
		unregReq.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
		unregResp := serveAndReturn(e, unregReq)
		require.Equal(t, http.StatusOK, unregResp.StatusCode)
		require.Equal(t, []string{testCallerPluginID}, e.mcp.unregisterCalls, "unregister must dispatch")
		require.Empty(t, e.mcp.pluginServers, "in-memory entry must be wiped after unregister")
		require.Equal(t, 1, spy.callCount, "unregister always triggers rebuild")
		spy.callCount = 0

		incoming := mcp.PluginServerConfig{
			PluginID:       testCallerPluginID,
			Name:           "Playbooks MCP",
			Path:           "/mcp",
			Enabled:        false,
			ExposeExternal: false,
		}
		regReq := mcpRegisterRequest(t, incoming)
		regReq.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)
		regResp := serveAndReturn(e, regReq)
		require.Equal(t, http.StatusOK, regResp.StatusCode)
		require.Len(t, e.mcp.registerCalls, 1, "register must dispatch exactly once")

		saved := e.mcp.registerCalls[0]
		require.Equal(t, true, saved.Enabled, "Enabled recovered from persisted config")
		require.Equal(t, false, saved.ExposeExternal, "ExposeExternal comes from plugin payload (authoritative)")
		require.Equal(t, persistedAdmin.ToolConfigs, saved.ToolConfigs, "ToolConfigs recovered from persisted config")

		require.Equal(t, "Playbooks MCP", saved.Name)
		require.Equal(t, "/mcp", saved.Path)

		require.Equal(t, 1, spy.callCount, "rebuild must fire when dropping external exposure after prior effective external")
	})

	t.Run("first register ever: no persisted entry — plugin payload wins (no regression)", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		spy := &spyRebuilder{}
		e.api.SetExternalRebuilderForTest(spy)

		e.api.configStore = &testConfigStore{
			cfg: &config.Config{
				MCP: config.MCPConfig{
					PluginServers: []config.PluginServerConfig{
						{PluginID: testOtherPluginID, Name: "Other", Path: "/mcp", Enabled: true, ExposeExternal: true},
					},
				},
			},
		}

		_, ok := e.api.findPersistedPluginServer(testCallerPluginID)
		require.False(t, ok, "findPersistedPluginServer must return false when pluginID is absent from persisted config")

		e.mockAPI.On("LogError", mock.Anything).Maybe()
		e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

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
