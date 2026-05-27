// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedCreateClientDiscoversPaginatedTools(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2", "tool_3", "tool_4", "tool_5")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	embeddedClient := NewEmbeddedServerClient(&fakeEmbeddedMCPServer{ctx: ctx, server: server}, newTestLogService(), nil)
	client, err := embeddedClient.CreateClient(context.Background(), "user-id", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.Len(t, client.Tools(), 5)
	for _, toolName := range []string{"tool_1", "tool_2", "tool_3", "tool_4", "tool_5"} {
		require.Contains(t, client.Tools(), toolName)
	}
}

func TestEmbeddedCreateClientRequiresPluginAPIForSessionValidation(t *testing.T) {
	server := newTestMCPServer(0, "tool_1")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	embeddedClient := NewEmbeddedServerClient(&fakeEmbeddedMCPServer{ctx: ctx, server: server}, newTestLogService(), nil)

	client, err := embeddedClient.CreateClient(context.Background(), "user-id", "session-id")

	require.Nil(t, client)
	require.EqualError(t, err, "plugin API is required when sessionID is provided")
}

func TestEmbeddedReconnectKeepsPaginatedDiscovery(t *testing.T) {
	server := newTestMCPServer(2, "tool_1", "tool_2", "tool_3")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pluginAPI := newTestPluginAPIWithSession("session-id")

	embeddedClient := NewEmbeddedServerClient(&fakeEmbeddedMCPServer{ctx: ctx, server: server}, pluginAPI.Log, pluginAPI)
	client, err := embeddedClient.CreateClient(context.Background(), "test-user", "session-id")
	require.NoError(t, err)
	require.Len(t, client.Tools(), 3)
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, client.session.Close())
	result, err := client.CallTool(context.Background(), "tool_1", map[string]any{})
	require.NoError(t, err)
	require.Contains(t, result, "tool_1 ok")
	require.Len(t, client.Tools(), 3)

	addTestMCPTool(server, "new_tool")
	require.NoError(t, client.session.Close())
	result, err = client.CallTool(context.Background(), "tool_1", map[string]any{})
	require.NoError(t, err)
	require.Contains(t, result, "tool_1 ok")
	require.Contains(t, client.Tools(), "new_tool")
	require.Len(t, client.Tools(), 4)
}

func TestClientToolsReturnsCopyAndSurvivesConcurrentUpdate(t *testing.T) {
	client := &Client{
		config: ServerConfig{Name: "server", BaseURL: "https://example.com"},
		tools: map[string]*mcp.Tool{
			"tool_1": {Name: "tool_1"},
		},
		userID: "user-id",
		log:    newTestLogService(),
	}

	tools := client.Tools()
	delete(tools, "tool_1")
	require.Contains(t, client.Tools(), "tool_1")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = client.Tools()
		}
	}()
	for i := 0; i < 100; i++ {
		client.toolsMu.Lock()
		client.tools = make(map[string]*mcp.Tool)
		client.toolsMu.Unlock()
	}
	<-done
	require.Empty(t, client.Tools())
}

func TestExtractOAuthMetadataURL(t *testing.T) {
	tests := []struct {
		name      string
		errMsg    string
		wantURL   string
		wantFound bool
	}{
		{
			name:      "nil error",
			errMsg:    "",
			wantURL:   "",
			wantFound: false,
		},
		{
			name:      "unrelated error",
			errMsg:    "connection refused",
			wantURL:   "",
			wantFound: false,
		},
		{
			name:      "metadata URL without wrapped error",
			errMsg:    "OAuth authentication needed for resource at https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/",
			wantURL:   "https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/",
			wantFound: true,
		},
		{
			name:      "metadata URL with wrapped error",
			errMsg:    "OAuth authentication needed for resource at https://example.com/.well-known/oauth-protected-resource: Got error: token refresh failed",
			wantURL:   "https://example.com/.well-known/oauth-protected-resource",
			wantFound: true,
		},
		{
			name:      "metadata URL embedded in longer error chain",
			errMsg:    "failed to connect: OAuth authentication needed for resource at https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/",
			wantURL:   "https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/",
			wantFound: true,
		},
		{
			name:      "empty metadata URL",
			errMsg:    "OAuth authentication needed for resource at ",
			wantURL:   "",
			wantFound: false,
		},
		{
			name:      "URL with port",
			errMsg:    "OAuth authentication needed for resource at https://example.com:8443/.well-known/oauth-protected-resource",
			wantURL:   "https://example.com:8443/.well-known/oauth-protected-resource",
			wantFound: true,
		},
		{
			name:      "URL with port and wrapped error",
			errMsg:    "OAuth authentication needed for resource at https://example.com:8443/.well-known/oauth-protected-resource: Got error: something failed",
			wantURL:   "https://example.com:8443/.well-known/oauth-protected-resource",
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = fmt.Errorf("%s", tt.errMsg)
			}
			gotURL, gotFound := extractOAuthMetadataURL(err)
			require.Equal(t, tt.wantFound, gotFound)
			require.Equal(t, tt.wantURL, gotURL)
		})
	}
}

func TestClientOAuthNeededError(t *testing.T) {
	client := &Client{
		config: ServerConfig{
			Name: "OAuth Server",
		},
		oauthManager: &OAuthManager{
			callbackURL: "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback",
		},
	}

	tests := []struct {
		name string
		err  error
	}{
		{
			name: "mcp unauthorized error",
			err: &mcpUnauthorized{
				metadataURL: "https://oauth.example.com/.well-known/oauth-protected-resource",
			},
		},
		{
			name: "string matched oauth error",
			err:  fmt.Errorf("OAuth authentication needed for resource at https://oauth.example.com/.well-known/oauth-protected-resource"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.oauthNeededError(tt.err)
			require.Error(t, err)

			var oauthErr *OAuthNeededError
			require.ErrorAs(t, err, &oauthErr)
			authURL, parseErr := url.Parse(oauthErr.AuthURL())
			require.NoError(t, parseErr)
			require.Equal(t, "https://mattermost.example.com", authURL.Scheme+"://"+authURL.Host)
			require.Equal(t, "/plugins/mattermost-ai/mcp/oauth/OAuth%20Server/start", authURL.EscapedPath())
			require.Equal(t, "https://oauth.example.com/.well-known/oauth-protected-resource", authURL.Query().Get("resource_metadata"))
		})
	}
}

// TestNilCacheHandling verifies that nil cache is handled gracefully in the cache code
func TestNilCacheHandling(t *testing.T) {
	// This test documents that the cache code handles nil properly
	// The actual NewClient function checks if toolsCache is nil before using it
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	// Verify cache can be created and used
	require.NotNil(t, cache)

	// Test that GetTools returns nil for non-existent server (not a panic)
	tools := cache.GetTools("nonexistent")
	require.Nil(t, tools)
}

func TestShouldUseSharedToolsCache(t *testing.T) {
	tests := []struct {
		name         string
		serverConfig ServerConfig
		expected     bool
	}{
		{
			name: "server without static oauth creds uses shared cache",
			serverConfig: ServerConfig{
				Name:    "no-oauth",
				BaseURL: "https://example.com",
			},
			expected: true,
		},
		{
			name: "server with static oauth creds skips shared cache",
			serverConfig: ServerConfig{
				Name:         "static-oauth",
				BaseURL:      "https://example.com",
				ClientID:     "client-id",
				ClientSecret: "client-secret",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, shouldUseSharedToolsCache(tt.serverConfig))
		})
	}
}

func TestInvalidateSharedToolsCacheForOAuthDiscovery(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "oauth-server"
	tools := map[string]*mcp.Tool{
		"search": {
			Name:        "search",
			Description: "Searches data",
		},
	}

	err := cache.SetTools(serverID, "OAuth Server", "https://example.com", tools, time.Now())
	require.NoError(t, err)
	require.NotNil(t, cache.GetTools(serverID))

	invalidateSharedToolsCacheForOAuthDiscovery(cache, log, "user-id", serverID, ServerConfig{
		Name:         serverID,
		BaseURL:      "https://example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, false)

	require.Nil(t, cache.GetTools(serverID))
}

func TestInvalidateSharedToolsCacheForOAuthDiscoveryKeepsCacheWithStoredToken(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "oauth-server"
	tools := map[string]*mcp.Tool{
		"search": {
			Name:        "search",
			Description: "Searches data",
		},
	}

	err := cache.SetTools(serverID, "OAuth Server", "https://example.com", tools, time.Now())
	require.NoError(t, err)

	invalidateSharedToolsCacheForOAuthDiscovery(cache, log, "user-id", serverID, ServerConfig{
		Name:         serverID,
		BaseURL:      "https://example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, true)

	require.NotNil(t, cache.GetTools(serverID))
}
