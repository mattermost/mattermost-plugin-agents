// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func newAppResourceMCPServer(uri, mimeType, text string, blob []byte, uiMeta map[string]any) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "app-resource-server", Version: "1.0"}, nil)
	addTestMCPTool(server, "probe")
	server.AddResource(&mcp.Resource{
		URI:      uri,
		MIMEType: mimeType,
		Name:     "app",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      uri,
				MIMEType: mimeType,
				Text:     text,
				Blob:     blob,
				Meta:     uiMeta,
			}},
		}, nil
	})
	return server
}

func connectClientToAppResourceServer(t *testing.T, server *mcp.Server) *Client {
	t.Helper()
	httpServer := startStreamableMCPServer(t, server)
	cache := newTestToolsCache()
	client, err := NewClient(context.Background(), "user-id", ServerConfig{
		Name:    "app-srv",
		BaseURL: httpServer.URL,
		Enabled: true,
	}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), cache, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestClientReadAppResource(t *testing.T) {
	const uri = "ui://srv/app.html"
	prefersBorder := true

	tests := []struct {
		name       string
		server     *mcp.Server
		wantHTML   string
		wantUIMeta *AppResourceUIMeta
		wantErr    error
		checkErr   func(t *testing.T, err error)
	}{
		{
			name: "happy path text",
			server: newAppResourceMCPServer(uri, UIResourceMIMEType, "<html>hi</html>", nil, map[string]any{
				"ui": map[string]any{
					"csp":           map[string]any{"connectDomains": []any{"https://api.example"}},
					"prefersBorder": true,
				},
			}),
			wantHTML: "<html>hi</html>",
			wantUIMeta: &AppResourceUIMeta{
				CSP:           &AppResourceCSP{ConnectDomains: []string{"https://api.example"}},
				PrefersBorder: &prefersBorder,
			},
		},
		{
			name:     "blob content",
			server:   newAppResourceMCPServer(uri, UIResourceMIMEType, "", []byte("<html>blob</html>"), nil),
			wantHTML: "<html>blob</html>",
		},
		{
			name:       "no ui meta",
			server:     newAppResourceMCPServer(uri, UIResourceMIMEType, "<html>x</html>", nil, nil),
			wantHTML:   "<html>x</html>",
			wantUIMeta: nil,
		},
		{
			name:   "wrong MIME",
			server: newAppResourceMCPServer(uri, "text/html", "<html>x</html>", nil, nil),
			checkErr: func(t *testing.T, err error) {
				var invalid *InvalidAppResourceError
				require.ErrorAs(t, err, &invalid)
				require.Equal(t, "text/html", invalid.MIMEType)
			},
		},
		{
			name:   "empty content",
			server: newAppResourceMCPServer(uri, UIResourceMIMEType, "", nil, nil),
			checkErr: func(t *testing.T, err error) {
				var invalid *InvalidAppResourceError
				require.ErrorAs(t, err, &invalid)
			},
		},
		{
			name: "resource not found",
			server: func() *mcp.Server {
				server := mcp.NewServer(&mcp.Implementation{Name: "missing", Version: "1.0"}, nil)
				addTestMCPTool(server, "probe")
				server.AddResource(&mcp.Resource{
					URI:      uri,
					MIMEType: UIResourceMIMEType,
					Name:     "app",
				}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
					return nil, mcp.ResourceNotFoundError(uri)
				})
				return server
			}(),
			checkErr: func(t *testing.T, err error) {
				require.Error(t, err)
				var invalid *InvalidAppResourceError
				require.False(t, errors.As(err, &invalid))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := connectClientToAppResourceServer(t, tt.server)
			got, err := client.ReadAppResource(context.Background(), uri)
			if tt.checkErr != nil {
				tt.checkErr(t, err)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, uri, got.URI)
			require.Equal(t, UIResourceMIMEType, got.MIMEType)
			require.Equal(t, tt.wantHTML, got.HTML)
			require.Equal(t, tt.wantUIMeta, got.UIMeta)
		})
	}
}

func TestClientReadAppResourceReconnects(t *testing.T) {
	const uri = "ui://embedded/app.html"
	server := newAppResourceMCPServer(uri, UIResourceMIMEType, "<html>reconnected</html>", nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pluginAPI := newTestPluginAPIWithSession("session-id")

	embeddedClient := NewEmbeddedServerClient(&fakeEmbeddedMCPServer{ctx: ctx, server: server}, pluginAPI.Log, pluginAPI)
	client, err := embeddedClient.CreateClient(context.Background(), "test-user", "session-id")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, client.session.Close())

	got, err := client.ReadAppResource(context.Background(), uri)
	require.NoError(t, err)
	require.Equal(t, "<html>reconnected</html>", got.HTML)
}

func TestUserClientsReadAppResourceRouting(t *testing.T) {
	const uri = "ui://srv-a/app.html"
	server := newAppResourceMCPServer(uri, UIResourceMIMEType, "<html>routed</html>", nil, nil)
	client := connectClientToAppResourceServer(t, server)
	client.config.BaseURL = "http://srv-a/"

	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			"srv-a": client,
		},
		log: newTestLogService(),
	}

	tests := []struct {
		name   string
		origin string
		wantOK bool
	}{
		{name: "exact origin", origin: "http://srv-a/", wantOK: true},
		{name: "trailing-slash variance", origin: "http://srv-a", wantOK: true},
		{name: "unknown origin", origin: "http://other/", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := userClients.ReadAppResource(context.Background(), tt.origin, uri)
			if !tt.wantOK {
				require.ErrorIs(t, err, ErrServerNotConnected)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "<html>routed</html>", got.HTML)
		})
	}
}

func TestClientManagerReadUserAppResource(t *testing.T) {
	const (
		userID  = "user-id"
		origin  = "http://srv-a/"
		uri     = "ui://srv-a/app.html"
		authURL = "https://auth.example/start"
	)

	t.Run("needs auth from initial connect errors", func(t *testing.T) {
		userClients := NewUserClients(userID, newTestLogService(), nil, nil, nil)
		userClients.setInitialRemoteConnectErrors(&Errors{
			ToolAuthErrors: []llm.ToolAuthError{{
				ServerOrigin: origin,
				AuthURL:      authURL,
			}},
		})
		m := &ClientManager{
			clients:  map[string]*UserClients{userID: userClients},
			activity: map[string]time.Time{userID: time.Now()},
			log:      newTestLogService(),
		}
		t.Cleanup(m.Close)

		got, err := m.ReadUserAppResource(context.Background(), userID, origin, uri)
		require.Nil(t, got)
		var oauthErr *OAuthNeededError
		require.ErrorAs(t, err, &oauthErr)
		require.Equal(t, authURL, oauthErr.AuthURL())
	})

	t.Run("unknown origin", func(t *testing.T) {
		userClients := NewUserClients(userID, newTestLogService(), nil, nil, nil)
		m := &ClientManager{
			clients:  map[string]*UserClients{userID: userClients},
			activity: map[string]time.Time{userID: time.Now()},
			log:      newTestLogService(),
		}
		t.Cleanup(m.Close)

		got, err := m.ReadUserAppResource(context.Background(), userID, "http://unknown/", uri)
		require.Nil(t, got)
		require.ErrorIs(t, err, ErrServerNotConnected)
	})
}
