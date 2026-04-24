// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"runtime"
	"testing"
	"time"

	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCallTool_PluginServerDisconnects_RecoversViaReconnect is a regression
// test for the following contract:
//
//  1. A tool call to a plugin-registered MCP server is in flight.
//  2. The plugin server's session is closed (the go-sdk returns
//     ErrConnectionClosed on the next session call).
//  3. mcp/client.go:431-479 observes the error, detects it as
//     ErrConnectionClosed, and falls into the remote-reconnect branch
//     (plugin clients have nil embeddedClient; see mcp/user_clients.go:327).
//  4. c.createSession runs with c.config.BaseURL == "plugin://<pluginID>".
//     Because the Client's cached httpClient already has a
//     PluginHTTPRoundTripper baked in (set up by ConnectToPluginServer),
//     the "plugin://" endpoint is rewritten to the target plugin's actual
//     MCP route and routed via PluginHTTP — so createSession succeeds.
//  5. The retried tool call on the fresh session returns normally. No
//     panic, no caller-visible error.
//  6. The goroutine count has not grown after the call settles.
//
// This pins the transparent-recovery behavior of the reconnect path for
// plugin-registered MCP servers. Any future change to the reconnect branch
// (e.g. a dedicated PluginHTTP-aware reconnect implementation) should
// continue to satisfy this contract, or update this test.
//
// Historical note: a previous revision of mcp/http_client.go's
// httpClientForMCP unconditionally wrapped the transport in an
// authenticationTransport that dereferenced c.oauthManager. Because
// plugin-server clients carry a nil oauthManager (plugin servers don't
// participate in user OAuth), the reconnect path panicked. The guard in
// httpClientForMCP now skips the auth wrapper when oauthManager is nil,
// restoring the transparent-recovery behavior this test pins.
func TestCallTool_PluginServerDisconnects_RecoversViaReconnect(t *testing.T) {
	// Capture goroutine count before any test activity. We allow ±2 slack
	// for the testing framework's own goroutines.
	before := runtime.NumGoroutine()

	target := newFakePluginMCPServer(t, 1)
	t.Cleanup(target.Close)

	mockAPI := newPluginHTTPForwarder(t, target)

	pluginTestAPI := &plugintest.API{}
	setupTestLogger(pluginTestAPI)
	client := pluginapi.NewClient(pluginTestAPI, nil)
	uc := NewUserClients("alice", client.Log, nil, nil, nil)

	cfg := PluginServerConfig{
		PluginID: "com.example.disconnect-test",
		Name:     "Disconnect Test",
		Path:     "/mcp",
		Enabled:  true,
	}

	// Step 1: successful connect.
	require.NoError(t, uc.ConnectToPluginServer(context.Background(), cfg, mockAPI))

	originKey := pluginServerOriginKey(cfg.PluginID)
	c, ok := uc.clients[originKey]
	require.True(t, ok, "expected client under origin key %s", originKey)
	require.Len(t, c.tools, 1)
	originalSession := c.session

	// Step 2: discover the tool's real name. newFakePluginMCPServer uses the
	// "test_tool" prefix; the only tool is "test_tool_0".
	var toolName string
	for name := range c.tools {
		toolName = name
		break
	}
	require.Equal(t, "test_tool_0", toolName)

	// Step 3: force a disconnect by closing the go-sdk session directly.
	// Subsequent CallTool on the same session returns ErrConnectionClosed,
	// which mimics a plugin server dying after the initial connect.
	require.NoError(t, c.session.Close())

	// Step 4: issue a tool call. Expect transparent recovery: the reconnect
	// branch builds a fresh session over the same PluginHTTP transport chain
	// and retries the call, returning the tool's response normally.
	result, err := c.CallToolWithMetadata(
		context.Background(),
		toolName,
		map[string]any{"message": "hello"},
		nil,
	)
	require.NoError(t, err, "expected transparent reconnect; got error: %v", err)
	assert.Contains(t, result, "hello",
		"expected fake-tool echo response to contain the input message; got: %s", result)

	// Step 5: the reconnect path replaces c.session. Confirm we are not
	// still holding the closed original.
	assert.NotSame(t, originalSession, c.session,
		"expected c.session to be replaced by createSession after reconnect")

	// Step 6: goroutine-leak check. Allow a short settle for the go-sdk
	// transport's own cleanup.
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	// Slack: allow up to 2 transient goroutines (test framework internals,
	// pluginapi.LogService async buffering, etc.).
	assert.LessOrEqual(t, after, before+2,
		"goroutine count grew unexpectedly: before=%d after=%d (reconnect path may be leaking)", before, after)
}
