// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi"
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

	require.NoError(t, client.currentSession().Close())

	got, err := client.ReadAppResource(context.Background(), uri)
	require.NoError(t, err)
	require.Equal(t, "<html>reconnected</html>", got.HTML)
}

type countingEmbeddedMCPServer struct {
	fakeEmbeddedMCPServer
	creates atomic.Int32
}

func (c *countingEmbeddedMCPServer) CreateClientTransport(userID, sessionID string, pluginAPI *pluginapi.Client) (*mcp.InMemoryTransport, error) {
	c.creates.Add(1)
	return c.fakeEmbeddedMCPServer.CreateClientTransport(userID, sessionID, pluginAPI)
}

func TestClientReconnectSingleFlightUnderConcurrentReads(t *testing.T) {
	const uri = "ui://embedded/app.html"
	server := newAppResourceMCPServer(uri, UIResourceMIMEType, "<html>race</html>", nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pluginAPI := newTestPluginAPIWithSession("session-id")

	counting := &countingEmbeddedMCPServer{fakeEmbeddedMCPServer: fakeEmbeddedMCPServer{ctx: ctx, server: server}}
	embeddedClient := NewEmbeddedServerClient(counting, pluginAPI.Log, pluginAPI)
	client, err := embeddedClient.CreateClient(context.Background(), "test-user", "session-id")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.Equal(t, int32(1), counting.creates.Load())

	require.NoError(t, client.currentSession().Close())

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, readErr := client.ReadAppResource(context.Background(), uri)
			if readErr != nil {
				errs <- readErr
				return
			}
			if got.HTML != "<html>race</html>" {
				errs <- fmt.Errorf("unexpected html %q", got.HTML)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	// Initial create + exactly one reconnect.
	require.Equal(t, int32(2), counting.creates.Load())
}

func TestClientConcurrentReadAndToolCallAfterClose(t *testing.T) {
	const uri = "ui://embedded/app.html"
	server := newAppResourceMCPServer(uri, UIResourceMIMEType, "<html>both</html>", nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pluginAPI := newTestPluginAPIWithSession("session-id")

	counting := &countingEmbeddedMCPServer{fakeEmbeddedMCPServer: fakeEmbeddedMCPServer{ctx: ctx, server: server}}
	embeddedClient := NewEmbeddedServerClient(counting, pluginAPI.Log, pluginAPI)
	client, err := embeddedClient.CreateClient(context.Background(), "test-user", "session-id")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, client.currentSession().Close())

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, readErr := client.ReadAppResource(context.Background(), uri)
		errs <- readErr
	}()
	go func() {
		defer wg.Done()
		_, callErr := client.CallTool(context.Background(), "probe", map[string]any{})
		errs <- callErr
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(2), counting.creates.Load())
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
		userID = "user-id"
		uri    = "ui://srv-a/app.html"
	)

	t.Run("unknown origin", func(t *testing.T) {
		m := &ClientManager{
			clients:  make(map[string]*UserClients),
			activity: make(map[string]time.Time),
			log:      newTestLogService(),
			config:   Config{Servers: []ServerConfig{{Name: "srv-a", BaseURL: "http://srv-a/", Enabled: true}}},
		}
		t.Cleanup(m.Close)

		got, err := m.ReadUserAppResource(context.Background(), userID, "http://unknown/", uri)
		require.Nil(t, got)
		require.ErrorIs(t, err, ErrServerNotConfigured)
	})

	t.Run("disabled origin is not configured", func(t *testing.T) {
		m := &ClientManager{
			clients:  make(map[string]*UserClients),
			activity: make(map[string]time.Time),
			log:      newTestLogService(),
			config: Config{Servers: []ServerConfig{
				{Name: "srv-a", BaseURL: "http://srv-a/", Enabled: false},
			}},
		}
		t.Cleanup(m.Close)

		got, err := m.ReadUserAppResource(context.Background(), userID, "http://srv-a/", uri)
		require.Nil(t, got)
		require.ErrorIs(t, err, ErrServerNotConfigured)
	})

	t.Run("lazy connect dials only the target enabled server", func(t *testing.T) {
		targetServer := newAppResourceMCPServer(uri, UIResourceMIMEType, "<html>target</html>", nil, nil)
		otherServer := newTestMCPServer(0, "other_tool")
		targetHTTP := startStreamableMCPServer(t, targetServer)
		otherHTTP := startStreamableMCPServer(t, otherServer)

		var targetHits, otherHits atomic.Int32
		counting := &countingHostTransport{
			base: http.DefaultTransport,
			counts: map[string]*atomic.Int32{
				targetHTTP.Listener.Addr().String(): &targetHits,
				otherHTTP.Listener.Addr().String():  &otherHits,
			},
		}
		httpClient := &http.Client{Transport: counting}

		m := &ClientManager{
			clients:      make(map[string]*UserClients),
			activity:     make(map[string]time.Time),
			log:          newTestLogService(),
			httpClient:   httpClient,
			toolsCache:   newTestToolsCache(),
			oauthManager: newTestOAuthManager(),
			config: Config{
				Servers: []ServerConfig{
					{Name: "target", BaseURL: targetHTTP.URL, Enabled: true},
					{Name: "other", BaseURL: otherHTTP.URL, Enabled: true},
					{Name: "disabled", BaseURL: "http://disabled.example/", Enabled: false},
				},
			},
		}
		// Close clients before httptest cleanup so SSE GETs are released.
		t.Cleanup(func() {
			m.Close()
			targetHTTP.CloseClientConnections()
			otherHTTP.CloseClientConnections()
		})

		got, err := m.ReadUserAppResource(context.Background(), userID, targetHTTP.URL, uri)
		require.NoError(t, err)
		require.Equal(t, "<html>target</html>", got.HTML)
		require.Greater(t, targetHits.Load(), int32(0))
		require.Zero(t, otherHits.Load(), "other enabled servers must not be dialed")
		require.False(t, m.clients[userID].hasRemoteFanOutDone())
	})

	t.Run("live resources/read 401 returns OAuthNeededError", func(t *testing.T) {
		server := newAppResourceMCPServer(uri, UIResourceMIMEType, "<html>x</html>", nil, nil)
		// Manage lifecycle here: a mid-session 401 can leave the streamable
		// transport wedged, so close the HTTP server before Client.Close.
		httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil))
		challenge := &resourcesReadChallengeTransport{base: httpServer.Client().Transport}
		client, err := NewClient(context.Background(), userID, ServerConfig{
			Name: "oauth-srv", BaseURL: httpServer.URL, Enabled: true,
		}, newTestLogService(), newTestOAuthManager(), &http.Client{Transport: challenge}, newTestToolsCache(), false)
		require.NoError(t, err)

		got, readErr := client.ReadAppResource(context.Background(), uri)
		// Close the client first so the SSE GET is released before httptest.Close.
		_ = client.Close()
		httpServer.CloseClientConnections()
		httpServer.Close()

		require.Nil(t, got)
		var oauthErr *OAuthNeededError
		require.ErrorAs(t, readErr, &oauthErr)
		require.NotEmpty(t, oauthErr.AuthURL())
	})
}

type countingHostTransport struct {
	base   http.RoundTripper
	counts map[string]*atomic.Int32
}

func (t *countingHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if counter, ok := t.counts[req.URL.Host]; ok {
		counter.Add(1)
	}
	return t.base.RoundTrip(req)
}

// resourcesReadChallengeTransport lets initialize/tools succeed, then returns a
// 401 WWW-Authenticate challenge for resources/read so oauthNeededError fires.
type resourcesReadChallengeTransport struct {
	base http.RoundTripper
}

func (t *resourcesReadChallengeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	// SSE session GETs and other body-less requests must pass through unchanged.
	if req.Body == nil || req.Method == http.MethodGet {
		return base.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	if bytes.Contains(body, []byte(`"method":"resources/read"`)) || bytes.Contains(body, []byte(`"method": "resources/read"`)) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header: http.Header{
				"Www-Authenticate": []string{`Bearer resource_metadata="https://auth.example/.well-known/oauth-protected-resource"`},
			},
			Body:    io.NopCloser(strings.NewReader("")),
			Request: req,
		}, nil
	}
	return base.RoundTrip(req)
}

func TestAppResourceFromReadResultBoundary(t *testing.T) {
	const uri = "ui://srv/app.html"

	tests := []struct {
		name     string
		uri      string
		result   *mcp.ReadResourceResult
		checkErr func(t *testing.T, err error)
		wantHTML string
	}{
		{
			name: "mismatched URI rejected",
			uri:  uri,
			result: &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
				URI: "ui://other/app.html", MIMEType: UIResourceMIMEType, Text: "<html>x</html>",
			}}},
			checkErr: func(t *testing.T, err error) {
				var invalid *InvalidAppResourceError
				require.ErrorAs(t, err, &invalid)
				require.Contains(t, invalid.Reason, "matching URI")
			},
		},
		{
			name: "charset MIME parameter allowed",
			uri:  uri,
			result: &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
				URI: uri, MIMEType: "text/html;profile=mcp-app;charset=utf-8", Text: "<html>ok</html>",
			}}},
			wantHTML: "<html>ok</html>",
		},
		{
			name: "oversize rejected",
			uri:  uri,
			result: &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
				URI: uri, MIMEType: UIResourceMIMEType, Text: strings.Repeat("a", MaxAppResourceBytes+1),
			}}},
			checkErr: func(t *testing.T, err error) {
				var invalid *InvalidAppResourceError
				require.ErrorAs(t, err, &invalid)
				require.Contains(t, invalid.Reason, "size limit")
			},
		},
		{
			name: "invalid UTF-8 blob rejected",
			uri:  uri,
			result: &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
				URI: uri, MIMEType: UIResourceMIMEType, Blob: []byte{0xff, 0xfe, 0xfd},
			}}},
			checkErr: func(t *testing.T, err error) {
				var invalid *InvalidAppResourceError
				require.ErrorAs(t, err, &invalid)
				require.Contains(t, invalid.Reason, "UTF-8")
			},
		},
		{
			name: "unicode ui URI accepted via validate",
			uri:  "ui://srv/アプリ.html",
			result: &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
				URI: "ui://srv/アプリ.html", MIMEType: UIResourceMIMEType, Text: "<html>ok</html>",
			}}},
			wantHTML: "<html>ok</html>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := appResourceFromReadResult(tt.uri, tt.result)
			if tt.checkErr != nil {
				tt.checkErr(t, err)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantHTML, got.HTML)
		})
	}
}

func TestValidateUIResourceURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{name: "valid", uri: "ui://srv/app.html"},
		{name: "unicode", uri: "ui://srv/アプリ.html"},
		{name: "empty", uri: "", wantErr: true},
		{name: "http scheme", uri: "http://evil.example/x", wantErr: true},
		{name: "control char", uri: "ui://srv/app\x00.html", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUIResourceURI(tt.uri)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
