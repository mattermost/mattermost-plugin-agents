// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestToolResolverRecordsSourceChannelsOnlyFromEmbeddedServer(t *testing.T) {
	tests := []struct {
		name         string
		serverName   string
		wantRecorded bool
	}{
		{name: "embedded server", serverName: EmbeddedClientKey, wantRecorded: true},
		{name: "remote server", serverName: "Tickets", wantRecorded: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "source", Version: "1.0"}, nil)
			server.AddTool(&sdkmcp.Tool{
				Name:        "read_channel",
				InputSchema: map[string]any{"type": "object"},
			}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				return &sdkmcp.CallToolResult{
					Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}},
					Meta: sdkmcp.Meta{SourceChannelsParam: []any{
						map[string]any{"id": "ch-dest", "type": string(model.ChannelTypePrivate), "team_id": "team-a"},
					}},
				}, nil
			})
			streamable := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)
			httpServer := httptest.NewServer(streamable)
			t.Cleanup(httpServer.Close)

			logService := pluginapi.NewClient(&fixedPluginAPI{}, nil).Log
			serverConfig := ServerConfig{
				Name:        tt.serverName,
				BaseURL:     httpServer.URL,
				Enabled:     true,
				ToolConfigs: []ToolConfig{{Name: "read_channel", Policy: ToolPolicyAutoRunEverywhere, Enabled: true}},
			}
			client, err := newClient(context.Background(), "alice", serverConfig, clientParams{
				log:            logService,
				httpClient:     httpServer.Client(),
				serviceAccount: true,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			uc := NewUserClients("alice", logService, nil, httpServer.Client(), nil)
			uc.clients[serverConfig.Name] = client

			llmContext := llm.NewContext()
			_, err = uc.createToolResolver(client, "read_channel")(context.Background(), llmContext, func(args any) error {
				target, ok := args.(*map[string]any)
				require.True(t, ok)
				*target = map[string]any{}
				return nil
			})
			require.NoError(t, err)

			_, recordedSources := llmContext.Parameters[SourceChannelsParam]
			reported, _ := llmContext.Parameters[ReportedSourceToolsParam].(map[string]bool)
			assert.Equal(t, tt.wantRecorded, recordedSources)
			assert.Equal(t, tt.wantRecorded, reported["read_channel"])
		})
	}
}
