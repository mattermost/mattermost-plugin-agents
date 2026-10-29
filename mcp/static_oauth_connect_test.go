// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// Some OAuth servers (e.g. Google's Workspace MCP servers) answer initialize
// and tools/list anonymously and only reject tools/call. When the admin has
// configured OAuth client credentials for such a server, a user who never
// authorized it must be asked to connect instead of being offered tools that
// cannot work.
func TestConnectRequiresAuthorizationForStaticOAuthServer(t *testing.T) {
	tests := []struct {
		name          string
		clientID      string
		storedToken   bool
		wantConnected bool
	}{
		{name: "static client without a stored token needs authorization", clientID: "static-client"},
		{name: "static client with a stored token connects", clientID: "static-client", storedToken: true, wantConnected: true},
		{name: "server without static client connects anonymously", wantConnected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "drive", Version: "1.0"}, nil)
			addTestMCPTool(server, "search_files")
			streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
			var requests atomic.Int32
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				if bytes.Contains(body, []byte(`"method":"tools/call"`)) && r.Header.Get("Authorization") == "" {
					w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+"http://"+r.Host+`/.well-known/oauth-protected-resource/search_files"`)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				streamable.ServeHTTP(w, r)
			}))
			t.Cleanup(httpServer.Close)

			oauthManager, kv := newStatefulKVManager(t, nil, http.DefaultClient)
			oauthManager.callbackURL = "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback"
			serverConfig := ServerConfig{
				Name:     "Drive",
				BaseURL:  httpServer.URL,
				Enabled:  true,
				ClientID: tt.clientID,
			}
			if tt.storedToken {
				kv.putEnvelope(t, "alice", serverConfig.Name, &storedTokenEnvelope{
					Version:  tokenEnvelopeVersion,
					Token:    &oauth2.Token{AccessToken: "access", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)},
					ClientID: tt.clientID,
				})
			}

			uc := NewUserClients("alice", newTestLogService(), oauthManager, httpServer.Client(), newTestToolsCache())
			t.Cleanup(uc.Close)
			mcpErrors := uc.ConnectToRemoteServers(context.Background(), []ServerConfig{serverConfig}, false)

			if tt.wantConnected {
				require.Nil(t, mcpErrors)
				assert.Len(t, uc.GetTools(context.Background()), 1)
				return
			}

			require.NotNil(t, mcpErrors)
			require.Len(t, mcpErrors.ToolAuthErrors, 1)
			assert.Equal(t, oauthManager.StartURL(serverConfig.Name), mcpErrors.ToolAuthErrors[0].AuthURL)
			assert.Equal(t, "Drive", mcpErrors.ToolAuthErrors[0].ServerName)
			assert.Empty(t, uc.GetTools(context.Background()), "no tools may be offered before the user authorizes")
			assert.Zero(t, requests.Load(), "the server must not be contacted anonymously")
		})
	}
}
