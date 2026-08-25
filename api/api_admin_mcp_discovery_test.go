// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// discoveryMCPServer is a real MCP server (go-sdk streamable HTTP) that counts
// the requests it receives and can stall its first handshake.
type discoveryMCPServer struct {
	*httptest.Server

	delayOnce sync.Once
	requests  atomic.Int64
}

func newDiscoveryMCPServer(t *testing.T, toolName string, firstRequestDelay time.Duration) *discoveryMCPServer {
	t.Helper()

	server := gomcp.NewServer(&gomcp.Implementation{Name: toolName, Version: "1.0"}, nil)
	server.AddTool(&gomcp.Tool{
		Name:        toolName,
		Description: "discovery test tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: "ok"}}}, nil
	})

	handler := gomcp.NewStreamableHTTPHandler(
		func(*http.Request) *gomcp.Server { return server },
		&gomcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)

	discovery := &discoveryMCPServer{}
	discovery.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discovery.requests.Add(1)
		if firstRequestDelay > 0 && r.Method == http.MethodPost {
			discovery.delayOnce.Do(func() { time.Sleep(firstRequestDelay) })
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(discovery.Close)

	return discovery
}

// delayedDiscoveryEmbeddedServer is a real in-memory embedded MCP server whose
// first connection stalls.
type delayedDiscoveryEmbeddedServer struct {
	ctx       context.Context
	server    *gomcp.Server
	delay     time.Duration
	delayOnce sync.Once
}

func newDelayedDiscoveryEmbeddedServer(t *testing.T, toolName string, delay time.Duration) *delayedDiscoveryEmbeddedServer {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	server := gomcp.NewServer(&gomcp.Implementation{Name: "embedded", Version: "1.0"}, nil)
	server.AddTool(&gomcp.Tool{
		Name:        toolName,
		Description: "embedded discovery test tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: "ok"}}}, nil
	})

	return &delayedDiscoveryEmbeddedServer{ctx: ctx, server: server, delay: delay}
}

func (d *delayedDiscoveryEmbeddedServer) CreateClientTransport(_ string, _ string, _ *pluginapi.Client) (*gomcp.InMemoryTransport, error) {
	if d.delay > 0 {
		d.delayOnce.Do(func() { time.Sleep(d.delay) })
	}

	serverTransport, clientTransport := gomcp.NewInMemoryTransports()
	go func() {
		_ = d.server.Run(d.ctx, serverTransport)
	}()
	return clientTransport, nil
}

func getAdminMCPTools(t *testing.T, api *API) MCPToolsResponse {
	t.Helper()

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
	return body
}

func serverRowByName(t *testing.T, response MCPToolsResponse, name string) MCPServerInfo {
	t.Helper()

	for _, server := range response.Servers {
		if server.Name == name {
			return server
		}
	}
	t.Fatalf("no %q row in response %+v", name, response.Servers)
	return MCPServerInfo{}
}

// Admin discovery probes embedded, remote, and plugin servers in one batch, so
// the response costs about one slow server instead of the sum of all of them.
func TestHandleGetMCPToolsDiscoversEveryServerTypeConcurrently(t *testing.T) {
	const probeDelay = 400 * time.Millisecond

	api, mockAPI, _ := setupAdminTestEnvironment(t)
	mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
	allowAnyPluginAPILogging(mockAPI)

	remoteA := newDiscoveryMCPServer(t, "remote_a", probeDelay)
	remoteB := newDiscoveryMCPServer(t, "remote_b", probeDelay)
	remoteC := newDiscoveryMCPServer(t, "remote_c", probeDelay)

	api.config.(*testConfigImpl).mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{Name: "Remote A", BaseURL: remoteA.URL, Enabled: true},
			{Name: "Remote B", BaseURL: remoteB.URL, Enabled: true},
			{Name: "Remote C", BaseURL: remoteC.URL, Enabled: true},
		},
		EmbeddedServer: mcp.EmbeddedServerConfig{Enabled: true},
	}

	mgr := api.mcpClientManager.(*mockMCPClientManager)
	mgr.httpClient = &http.Client{}
	mgr.embeddedServer = newDelayedDiscoveryEmbeddedServer(t, "embedded_tool", probeDelay)
	mgr.pluginServers = []mcp.PluginServerConfig{
		{PluginID: "com.example.one", Name: "Plugin One", Path: "/mcp", Enabled: true},
		{PluginID: "com.example.two", Name: "Plugin Two", Path: "/mcp", Enabled: true},
	}
	mgr.discoverPluginToolsFunc = func(cfg mcp.PluginServerConfig) ([]mcp.ToolInfo, error) {
		time.Sleep(probeDelay)
		return []mcp.ToolInfo{{Name: cfg.PluginID + "_tool"}}, nil
	}

	start := time.Now()
	response := getAdminMCPTools(t, api)
	elapsed := time.Since(start)

	require.Len(t, response.Servers, 6)
	for _, server := range response.Servers {
		require.Nil(t, server.Error, "server %q reported %v", server.Name, server.Error)
		require.NotEmpty(t, server.Tools, "server %q returned no tools", server.Name)
	}

	// Six servers each stall for probeDelay; probing them one after another
	// would take at least 6x that.
	require.Less(t, elapsed, 3*probeDelay,
		"admin discovery took %s; six %s servers probed sequentially would take at least %s",
		elapsed, probeDelay, 6*probeDelay)
}

// Rows keep their configured identity and per-server outcome no matter which
// server answers first.
func TestHandleGetMCPToolsKeepsRowsAlignedWithConfiguration(t *testing.T) {
	api, mockAPI, _ := setupAdminTestEnvironment(t)
	mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
	allowAnyPluginAPILogging(mockAPI)

	// The healthy server is slow and the broken one is fast, so completion
	// order is the reverse of configured order.
	healthy := newDiscoveryMCPServer(t, "healthy_tool", 200*time.Millisecond)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	disabled := newDiscoveryMCPServer(t, "disabled_tool", 0)

	api.config.(*testConfigImpl).mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{Name: "Healthy", BaseURL: healthy.URL, Enabled: true},
			{Name: "Broken", BaseURL: broken.URL, Enabled: true},
			{Name: "Disabled", BaseURL: disabled.URL, Enabled: false},
		},
	}

	mgr := api.mcpClientManager.(*mockMCPClientManager)
	mgr.httpClient = &http.Client{}

	response := getAdminMCPTools(t, api)

	require.Len(t, response.Servers, 2, "a disabled server must not be rendered")

	healthyRow := serverRowByName(t, response, "Healthy")
	require.Equal(t, healthy.URL, healthyRow.URL)
	require.Nil(t, healthyRow.Error)
	require.Len(t, healthyRow.Tools, 1)
	require.Equal(t, "healthy_tool", healthyRow.Tools[0].Name)

	brokenRow := serverRowByName(t, response, "Broken")
	require.Equal(t, broken.URL, brokenRow.URL)
	require.NotNil(t, brokenRow.Error)
	require.Empty(t, brokenRow.Tools)

	require.Zero(t, disabled.requests.Load(), "a disabled server must not be contacted")
}

// Conflicting entries are excluded from the runtime, so admin discovery has to
// explain why rather than silently showing an empty server.
func TestHandleGetMCPToolsSurfacesDuplicateServerConflicts(t *testing.T) {
	testCases := []struct {
		name          string
		servers       func(healthy, dupOne, dupTwo *discoveryMCPServer) []mcp.ServerConfig
		expectMessage string
	}{
		{
			name: "duplicate names",
			servers: func(healthy, dupOne, dupTwo *discoveryMCPServer) []mcp.ServerConfig {
				return []mcp.ServerConfig{
					{Name: "Healthy", BaseURL: healthy.URL, Enabled: true},
					{Name: "Duplicate", BaseURL: dupOne.URL, Enabled: true},
					{Name: "Duplicate", BaseURL: dupTwo.URL, Enabled: true},
				}
			},
			expectMessage: `name "Duplicate" is used by more than one server`,
		},
		{
			name: "canonically equivalent URLs",
			servers: func(healthy, dupOne, _ *discoveryMCPServer) []mcp.ServerConfig {
				return []mcp.ServerConfig{
					{Name: "Healthy", BaseURL: healthy.URL, Enabled: true},
					{Name: "Duplicate One", BaseURL: dupOne.URL, Enabled: true},
					{Name: "Duplicate Two", BaseURL: dupOne.URL + "/", Enabled: true},
				}
			},
			expectMessage: "is configured on more than one server",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			api, mockAPI, _ := setupAdminTestEnvironment(t)
			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			allowAnyPluginAPILogging(mockAPI)

			healthy := newDiscoveryMCPServer(t, "healthy_tool", 0)
			dupOne := newDiscoveryMCPServer(t, "dup_one_tool", 0)
			dupTwo := newDiscoveryMCPServer(t, "dup_two_tool", 0)

			api.config.(*testConfigImpl).mcpConfig = mcp.Config{
				Enabled: true,
				Servers: tc.servers(healthy, dupOne, dupTwo),
			}
			mgr := api.mcpClientManager.(*mockMCPClientManager)
			mgr.httpClient = &http.Client{}

			response := getAdminMCPTools(t, api)
			require.Len(t, response.Servers, 3)

			healthyRow := serverRowByName(t, response, "Healthy")
			require.Nil(t, healthyRow.Error)
			require.Len(t, healthyRow.Tools, 1)

			for _, server := range response.Servers[1:] {
				require.NotNil(t, server.Error, "conflicting server %q must report the conflict", server.Name)
				require.Contains(t, *server.Error, tc.expectMessage)
				require.Empty(t, server.Tools)
			}

			require.Zero(t, dupOne.requests.Load(), "a conflicting server must not be contacted")
			require.Zero(t, dupTwo.requests.Load(), "a conflicting server must not be contacted")
		})
	}
}

// One failing plugin must not remove healthy plugins' tools from the response.
func TestHandleGetMCPToolsKeepsPartialPluginFailuresPerServer(t *testing.T) {
	api, mockAPI, _ := setupAdminTestEnvironment(t)
	mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
	allowAnyPluginAPILogging(mockAPI)

	mgr := api.mcpClientManager.(*mockMCPClientManager)
	mgr.pluginServers = []mcp.PluginServerConfig{
		{PluginID: "com.example.healthy", Name: "Healthy Plugin", Path: "/mcp", Enabled: true},
		{PluginID: "com.example.broken", Name: "Broken Plugin", Path: "/mcp", Enabled: true},
	}
	mgr.discoverPluginToolsFunc = func(cfg mcp.PluginServerConfig) ([]mcp.ToolInfo, error) {
		if cfg.PluginID == "com.example.broken" {
			return nil, fmt.Errorf("connection refused")
		}
		return []mcp.ToolInfo{{Name: "healthy_plugin_tool"}}, nil
	}

	response := getAdminMCPTools(t, api)
	require.Len(t, response.Servers, 2)

	healthyRow := serverRowByName(t, response, "Healthy Plugin")
	require.Nil(t, healthyRow.Error)
	require.Len(t, healthyRow.Tools, 1)

	brokenRow := serverRowByName(t, response, "Broken Plugin")
	require.NotNil(t, brokenRow.Error)
	require.Contains(t, *brokenRow.Error, "connection refused")
	require.Empty(t, brokenRow.Tools)
}
