// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/public/mcptool"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newHookTestClient(authToken string) *model.Client4 {
	c := model.NewAPIv4Client("https://example.invalid")
	if authToken != "" {
		c.SetToken(authToken)
	}
	return c
}

func TestRunBeforeHook_NoHook(t *testing.T) {
	mcpCtx := &MCPToolContext{
		Ctx:         context.Background(),
		MMServerURL: "https://mm.example.com",
		UserID:      "user-1",
		ToolHooks:   nil,
	}
	err := RunBeforeHook(mcpCtx, "search_posts", map[string]any{"q": "x"})
	require.NoError(t, err)
}

func TestRunBeforeHook_ResolvesKeyThenPostsCallback(t *testing.T) {
	var capturedHookAuth string
	var capturedBody mcptool.BeforeHookRequest
	var resolverUserID string
	var resolverToolName string
	var resolverKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/plugins/com.example.plugin/hooks/before", r.URL.Path)
		capturedHookAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))
		require.NoError(t, json.NewEncoder(w).Encode(mcptool.BeforeHookResponse{}))
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:         context.Background(),
		Client:      newHookTestClient("tok"),
		MMServerURL: srv.URL,
		UserID:      "user-1",
		BeforeHookResolver: auth.BeforeHookResolver(func(userID, toolName, hookKey string) (string, error) {
			resolverUserID = userID
			resolverToolName = toolName
			resolverKey = hookKey
			return "/plugins/com.example.plugin/hooks/before", nil
		}),
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeHookKey: "beforeHook:secret"},
		},
	}

	err := RunBeforeHook(mcpCtx, "search_posts", map[string]any{"query": "x"})
	require.NoError(t, err)
	assert.Equal(t, "user-1", resolverUserID)
	assert.Equal(t, "search_posts", resolverToolName)
	assert.Equal(t, "beforeHook:secret", resolverKey)
	assert.Equal(t, "Bearer tok", capturedHookAuth)
	assert.Equal(t, "search_posts", capturedBody.ToolName)
	assert.Equal(t, "user-1", capturedBody.UserID)
}

func TestRunBeforeHook_ErrorRejects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(mcptool.BeforeHookResponse{Error: "not allowed"})
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:         context.Background(),
		Client:      newHookTestClient("tok"),
		MMServerURL: srv.URL,
		UserID:      "user-1",
		BeforeHookResolver: auth.BeforeHookResolver(func(_, _, _ string) (string, error) {
			return "/plugins/com.example.plugin/hooks/before", nil
		}),
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeHookKey: "beforeHook:secret"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", map[string]any{"query": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}

func TestRunBeforeHook_MissingResolverBlocksHook(t *testing.T) {
	hookCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hookCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:         context.Background(),
		Client:      newHookTestClient("tok"),
		MMServerURL: srv.URL,
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeHookKey: "beforeHook:secret"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing before-hook resolver")
	assert.False(t, hookCalled)
}

func TestRunBeforeHook_ResolverFailureBlocksHook(t *testing.T) {
	hookCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hookCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:         context.Background(),
		Client:      newHookTestClient("tok"),
		MMServerURL: srv.URL,
		BeforeHookResolver: auth.BeforeHookResolver(func(_, _, _ string) (string, error) {
			return "", errors.New("missing key")
		}),
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeHookKey: "beforeHook:missing"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing key")
	assert.False(t, hookCalled)
}

func TestRunBeforeHook_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:         context.Background(),
		MMServerURL: srv.URL,
		BeforeHookResolver: auth.BeforeHookResolver(func(_, _, _ string) (string, error) {
			return "/plugins/com.example.plugin/hooks/before", nil
		}),
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeHookKey: "beforeHook:secret"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

func TestRunBeforeHook_InvalidJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:         context.Background(),
		MMServerURL: srv.URL,
		BeforeHookResolver: auth.BeforeHookResolver(func(_, _, _ string) (string, error) {
			return "/plugins/com.example.plugin/hooks/before", nil
		}),
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeHookKey: "beforeHook:secret"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid response")
}

func TestRunBeforeHook_RejectsEscapingResolvedEndpoint(t *testing.T) {
	hookCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hookCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:         context.Background(),
		Client:      newHookTestClient("tok"),
		MMServerURL: srv.URL,
		UserID:      "user-1",
		BeforeHookResolver: auth.BeforeHookResolver(func(_, _, _ string) (string, error) {
			return "/plugins/com.example.plugin/../../api/v4/users/me", nil
		}),
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeHookKey: "beforeHook:secret"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid callback URL")
	assert.False(t, hookCalled)
}

func TestBuildHookURL_AcceptsPluginCallbackPath(t *testing.T) {
	got, err := buildHookURL("https://mm.example.com/", "/plugins/com.example.plugin/hooks/before")
	require.NoError(t, err)
	assert.Equal(t, "https://mm.example.com/plugins/com.example.plugin/hooks/before", got)
}

func TestBuildHookURL_RejectsBadCallbackURLs(t *testing.T) {
	cases := []struct {
		name        string
		callbackURL string
		errContains string
	}{
		{"missing leading slash", "plugins/com.example.plugin/hooks/before", "must start with /plugins/"},
		{"empty", "", "must start with /plugins/"},
		{"wrong root", "/api/v4/users/me", "must start with /plugins/"},
		{"parent traversal", "/plugins/com.example.plugin/../../api/v4/users/me", "invalid callback URL"},
		{"encoded parent traversal", "/plugins/com.example.plugin/hooks/%2e%2e/%2e%2e/api/v4/users/me", "invalid callback URL"},
		{"absolute url", "https://evil.example/hooks/before", "invalid callback URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildHookURL("https://mm.example.com", tc.callbackURL)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errContains)
		})
	}
}
