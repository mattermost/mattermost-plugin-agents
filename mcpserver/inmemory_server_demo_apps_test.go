// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type noopLogger struct{}

func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}
func (noopLogger) Flush() error         { return nil }

var _ logger.Logger = noopLogger{}

func TestInMemoryServerDemoApps(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "enabled registers preview_post and resource", enabled: true},
		{name: "disabled omits preview_post and resource", enabled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := NewInMemoryServer(InMemoryConfig{
				BaseConfig:     BaseConfig{MMServerURL: "http://mm"},
				EnableDemoApps: tt.enabled,
			}, noopLogger{}, nil, nil)
			require.NoError(t, err)

			clientTransport, err := server.CreateConnectionForUser("user1", "", nil, nil)
			require.NoError(t, err)

			client := mcp.NewClient(&mcp.Implementation{Name: "demo-apps-test", Version: "1.0"}, nil)
			session, err := client.Connect(context.Background(), clientTransport, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.Close() })

			tools, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
			require.NoError(t, err)

			var preview *mcp.Tool
			for _, tool := range tools.Tools {
				if tool.Name == "preview_post" {
					preview = tool
					break
				}
			}

			if !tt.enabled {
				assert.Nil(t, preview, "preview_post should be absent when demo apps disabled")
				_, readErr := session.ReadResource(context.Background(), &mcp.ReadResourceParams{
					URI: "ui://mattermost/preview-post.html",
				})
				require.Error(t, readErr)
				return
			}

			require.NotNil(t, preview, "preview_post should be listed when demo apps enabled")
			ui, ok := preview.Meta["ui"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "ui://mattermost/preview-post.html", ui["resourceUri"])

			res, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{
				URI: "ui://mattermost/preview-post.html",
			})
			require.NoError(t, err)
			require.Len(t, res.Contents, 1)
			assert.Equal(t, "text/html;profile=mcp-app", res.Contents[0].MIMEType)
			assert.Contains(t, res.Contents[0].Text, "preview-post-toggle")
			assert.Contains(t, res.Contents[0].Text, "ui/notifications/initialized")
		})
	}
}
