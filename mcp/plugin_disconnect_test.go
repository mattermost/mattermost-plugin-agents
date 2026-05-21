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

// Plugin clients have no OAuth manager and must reuse their PluginHTTP-backed
// httpClient when transparently reconnecting after a session close.
func TestCallTool_PluginServerDisconnects_RecoversViaReconnect(t *testing.T) {
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

	require.NoError(t, uc.ConnectToPluginServer(context.Background(), cfg, mockAPI))

	originKey := pluginServerOriginKey(cfg.PluginID)
	c, ok := uc.clients[originKey]
	require.True(t, ok, "expected client under origin key %s", originKey)
	require.Len(t, c.tools, 1)
	originalSession := c.session

	var toolName string
	for name := range c.tools {
		toolName = name
		break
	}
	require.Equal(t, "test_tool_0", toolName)

	// Force ErrConnectionClosed on the next tool call.
	require.NoError(t, c.session.Close())

	result, err := c.CallToolWithMetadata(
		context.Background(),
		toolName,
		map[string]any{"message": "hello"},
		nil,
	)
	require.NoError(t, err, "expected transparent reconnect; got error: %v", err)
	assert.Contains(t, result, "hello",
		"expected fake-tool echo response to contain the input message; got: %s", result)

	assert.NotSame(t, originalSession, c.session,
		"expected c.session to be replaced by createSession after reconnect")

	// Settle for go-sdk transport cleanup; allow ±2 transient goroutines.
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+2,
		"goroutine count grew unexpectedly: before=%d after=%d (reconnect path may be leaking)", before, after)
}
