// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
// cannot work. A server that challenges on connect must still pass its
// resource_metadata to the connect link, since it may advertise its
// authorization server nowhere else.
func TestConnectRequiresAuthorizationForStaticOAuthServer(t *testing.T) {
	tests := []struct {
		name               string
		clientID           string
		storedToken        bool
		challengeOnConnect bool
		wantConnected      bool
	}{
		{name: "static client without a stored token needs authorization", clientID: "static-client"},
		{name: "static client without a stored token keeps the connect challenge's metadata", clientID: "static-client", challengeOnConnect: true},
		{name: "static client with a stored token connects", clientID: "static-client", storedToken: true, wantConnected: true},
		{name: "server without static client connects anonymously", wantConnected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "drive", Version: "1.0"}, nil)
			addTestMCPTool(server, "search_files")
			streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				if r.Header.Get("Authorization") == "" && (tt.challengeOnConnect || bytes.Contains(body, []byte(`"method":"tools/call"`))) {
					w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://`+r.Host+`/.well-known/oauth-protected-resource/mcp"`)
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
			wantAuthURL := oauthManager.StartURL(serverConfig.Name)
			if tt.challengeOnConnect {
				wantAuthURL += "?resource_metadata=" + url.QueryEscape(httpServer.URL+"/.well-known/oauth-protected-resource/mcp")
			}
			assert.Equal(t, wantAuthURL, mcpErrors.ToolAuthErrors[0].AuthURL)
			assert.Equal(t, "Drive", mcpErrors.ToolAuthErrors[0].ServerName)
			assert.Empty(t, uc.GetTools(context.Background()), "no tools may be offered before the user authorizes")
		})
	}
}
