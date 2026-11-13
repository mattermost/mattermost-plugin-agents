// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// startingSourcePlugin is a source plugin as the Mattermost server sees it
// while the plugin is still in OnActivate: requests to it are not routed until
// it is running.
type startingSourcePlugin struct {
	mmapi.Client
	state   atomic.Int32
	calls   atomic.Int32
	handler http.Handler
}

func (s *startingSourcePlugin) GetPluginStatus(pluginID string) (*model.PluginStatus, error) {
	return &model.PluginStatus{PluginId: pluginID, State: int(s.state.Load())}, nil
}

func (s *startingSourcePlugin) PluginHTTP(req *http.Request) *http.Response {
	s.calls.Add(1)
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec.Result()
}

// A source plugin registers from its own OnActivate. The register route must
// answer without calling back into it, and its tools must reach the external
// MCP server once it is running.
func TestHandleMCPRegister_SourcePluginStillStarting(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.mockAPI.On("LogError", mock.Anything).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()

	mcpServer := gosdkmcp.NewServer(&gosdkmcp.Implementation{Name: testCallerPluginID, Version: "1.0"}, nil)
	source := &startingSourcePlugin{handler: gosdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *gosdkmcp.Server { return mcpServer },
		&gosdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true},
	)}
	source.state.Store(int32(model.PluginStateStarting))

	handlers, err := mcpserver.NewPluginMCPHandlers("https://mm.test", "http://mm.internal", nil, e.mcp, source, nil, nil, func() bool { return true })
	require.NoError(t, err)
	t.Cleanup(handlers.Close)
	e.api.mcpHandlers = handlers

	req := mcpRegisterRequest(t, mcp.PluginServerConfig{Name: "Playbooks MCP", Path: "/mcp", Enabled: true, ExposeExternal: true})
	req.Header.Set("Mattermost-Plugin-ID", testCallerPluginID)

	start := time.Now()
	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Less(t, time.Since(start), time.Second)

	time.Sleep(100 * time.Millisecond)
	require.Zero(t, source.calls.Load(), "a source plugin that is not running yet must not be called")

	source.state.Store(int32(model.PluginStateRunning))
	require.Eventually(t, func() bool { return source.calls.Load() > 0 }, 10*time.Second, 20*time.Millisecond,
		"discovery must retry once the source plugin is running")
}
