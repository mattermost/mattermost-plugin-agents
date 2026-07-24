// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const previewPostResourceURI = "ui://mattermost/preview-post.html"

// demoAppHTML mirrors the embedded demo guest app markers exercised by the
// host path (registration itself is covered in mcpserver).
const demoAppHTML = `<!doctype html><html><body>
<button data-testid="preview-post-toggle">Show raw JSON</button>
<script>/* ui/notifications/initialized */</script>
</body></html>`

// newDemoAppsMCPServer builds a go-sdk server with the same tool _meta.ui and
// ui:// resource shape as mcpserver's preview_post demo app. The mcp package
// cannot import mcpserver in unit tests (mcpserver → mcp cycle via
// plugin_handlers.go); registration is covered by mcpserver's own tests.
func newDemoAppsMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "demo-apps", Version: "1.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "preview_post",
		Description: "Preview a Mattermost post as an interactive MCP App card.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"post_id": map[string]any{"type": "string"}},
			"required":   []any{"post_id"},
		},
		Meta: mcp.Meta{"ui": map[string]any{
			"resourceUri": previewPostResourceURI,
		}},
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"post_id":"x"}`}}}, nil
	})
	server.AddResource(&mcp.Resource{
		URI:      previewPostResourceURI,
		Name:     "preview-post-app",
		MIMEType: UIResourceMIMEType,
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      previewPostResourceURI,
			MIMEType: UIResourceMIMEType,
			Text:     demoAppHTML,
		}}}, nil
	})
	return server
}

// demoEmbeddedServer wraps a go-sdk server as EmbeddedMCPServer (same pattern
// as fakeEmbeddedMCPServer) for ClientManager wiring.
type demoEmbeddedServer struct {
	ctx    context.Context
	server *mcp.Server
}

func (d *demoEmbeddedServer) CreateClientTransport(_ string, _ string, _ *pluginapi.Client) (*mcp.InMemoryTransport, error) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() {
		_ = d.server.Run(d.ctx, serverTransport)
	}()
	return clientTransport, nil
}

func TestEmbeddedDemoAppsUIMetaAndResourceRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const userID = "user1"
	const sessionID = "session-demo-apps"
	pluginAPI := newTestPluginAPIForEmbeddedManager(userID, sessionID)

	manager := NewClientManager(
		Config{
			EmbeddedServer: EmbeddedServerConfig{
				Enabled: true,
				ToolConfigs: []ToolConfig{
					{Name: "preview_post", Policy: "auto_run_in_dm", Enabled: true},
				},
			},
			Servers:            []ServerConfig{},
			IdleTimeoutMinutes: 30,
		},
		pluginAPI.Log,
		pluginAPI,
		nil,
		&demoEmbeddedServer{ctx: ctx, server: newDemoAppsMCPServer()},
		nil,
		nil,
	)
	t.Cleanup(manager.Close)

	tools, mcpErrors := manager.GetToolsForUser(context.Background(), userID)
	require.Nil(t, mcpErrors)

	var found bool
	for i := range tools {
		// Embedded tools are namespaced as mattermost__<bare>.
		if tools[i].Name != "preview_post" && tools[i].Name != "mattermost__preview_post" {
			continue
		}
		found = true
		require.NotNil(t, tools[i].UIMeta, "parseToolUIMeta must populate UIMeta for embedded demo tool")
		require.Equal(t, previewPostResourceURI, tools[i].UIMeta.ResourceURI)
		break
	}
	require.True(t, found, "preview_post must be discoverable via GetToolsForUser")

	got, err := manager.ReadUserAppResource(context.Background(), userID, EmbeddedClientKey, previewPostResourceURI)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, UIResourceMIMEType, got.MIMEType)
	require.Contains(t, got.HTML, "preview-post-toggle")
	require.Contains(t, got.HTML, "ui/notifications/initialized")
}
