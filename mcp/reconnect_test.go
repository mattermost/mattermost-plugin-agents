// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// A tool call rejected because the user's authorization lapsed drops the
// session and reports the server for reconnection; other failures keep it.
func TestToolCallErrorHandlesLapsedAuthorization(t *testing.T) {
	tests := []struct {
		name           string
		callStatus     int
		serviceAccount bool
		wantReconnect  bool
	}{
		{name: "authorization rejected", callStatus: http.StatusUnauthorized, wantReconnect: true},
		{name: "server error", callStatus: http.StatusInternalServerError},
		{name: "service account never needs per-user authorization", callStatus: http.StatusUnauthorized, serviceAccount: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "tickets", Version: "1.0"}, nil)
			addTestMCPTool(server, "search")
			streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				if bytes.Contains(body, []byte(`"method":"tools/call"`)) {
					if tt.callStatus == http.StatusUnauthorized {
						w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
					}
					w.WriteHeader(tt.callStatus)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				streamable.ServeHTTP(w, r)
			}))
			t.Cleanup(httpServer.Close)

			oauthManager, _ := newStatefulKVManager(t, nil, http.DefaultClient)
			oauthManager.callbackURL = "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback"
			serverConfig := ServerConfig{
				Name:        "Tickets",
				BaseURL:     httpServer.URL,
				Enabled:     true,
				Description: "Support tickets",
				ToolConfigs: []ToolConfig{{Name: "search", Policy: ToolPolicyAsk, Enabled: true}},
			}
			params := clientParams{log: newTestLogService(), httpClient: httpServer.Client(), serviceAccount: tt.serviceAccount}
			if !tt.serviceAccount {
				params.oauthManager = oauthManager
			}
			client, err := newClient(context.Background(), "alice", serverConfig, params)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			uc := NewUserClients("alice", newTestLogService(), oauthManager, httpServer.Client(), nil)
			uc.clients[serverConfig.Name] = client

			resolver := uc.createToolResolver(client, "search")
			_, err = resolver(context.Background(), llm.NewContext(), func(args any) error { return nil })
			require.Error(t, err)

			state, loadErr := oauthManager.LoadAuthNeededState("alice", serverConfig.Name)
			require.NoError(t, loadErr)
			needed, isReconnect := errors.AsType[*ReconnectNeededError](err)
			if !tt.wantReconnect {
				assert.False(t, isReconnect, "got %v", err)
				assert.Contains(t, uc.clients, serverConfig.Name, "the session must be kept")
				assert.Nil(t, state)
				return
			}

			require.True(t, isReconnect, "got %v", err)
			assert.Equal(t, llm.UnconnectedMCPServer{
				Name:         "Tickets",
				ServerOrigin: httpServer.URL,
				Description:  "Support tickets",
				Tools:        []llm.ToolInfo{{Name: "search", ServerOrigin: httpServer.URL}},
			}, needed.Server)
			assert.NotContains(t, uc.clients, serverConfig.Name, "the dead session must be dropped so the next turn reconnects")
			require.NotNil(t, state, "the next catalog build must report the server as needing authorization")
			assert.Contains(t, needed.ConnectGuidance(), `call RequestMCPConnection with server "Tickets"`)
			assert.NotContains(t, needed.Error(), RequestConnectionToolName)
		})
	}
}
