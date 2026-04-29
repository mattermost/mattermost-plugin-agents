// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
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
		Ctx:          context.Background(),
		MMServerURL:  "https://mm.example.com",
		HookPluginID: "com.example.plugin",
		UserID:       "user-1",
		ToolHooks:    nil,
	}
	err := RunBeforeHook(mcpCtx, "search_posts", map[string]any{"q": "x"})
	require.NoError(t, err)
}

func TestRunBeforeHook_ErrorRejects(t *testing.T) {
	var capturedAuth string
	var capturedBody mcptool.BeforeHookRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/plugins/com.example.plugin/hooks/before", r.URL.Path)
		capturedAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		_ = json.NewEncoder(w).Encode(mcptool.BeforeHookResponse{Error: "not allowed"})
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:          context.Background(),
		Client:       newHookTestClient("tok"),
		MMServerURL:  srv.URL,
		HookPluginID: "com.example.plugin",
		UserID:       "user-1",
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeCallback: "/hooks/before"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", map[string]any{"query": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
	assert.Equal(t, "Bearer tok", capturedAuth)
	assert.Equal(t, "user-1", capturedBody.UserID)
}

func TestRunBeforeHook_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:          context.Background(),
		MMServerURL:  srv.URL,
		HookPluginID: "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeCallback: "/hooks/before"},
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
		Ctx:          context.Background(),
		MMServerURL:  srv.URL,
		HookPluginID: "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeCallback: "/hooks/before"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid response")
}

// TestRunBeforeHook_NoTokenSendsNoAuthHeader verifies that when no Mattermost
// client is attached to the tool context (e.g. unauthenticated transport) the
// hook callback is still invoked but without an Authorization header.
func TestRunBeforeHook_NoTokenSendsNoAuthHeader(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(mcptool.BeforeHookResponse{})
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:          context.Background(),
		MMServerURL:  srv.URL,
		HookPluginID: "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeCallback: "/hooks/before"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", nil)
	require.NoError(t, err)
	assert.Empty(t, capturedAuth)
}

func TestBuildHookURL_RejectsBadPaths(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		errContains string
	}{
		{"missing leading slash", "hooks/before", "must start with /"},
		{"empty", "", "must start with /"},
		{"parent traversal", "/hooks/../../api/v4/users/me", "escapes plugin namespace"},
		{"parent traversal at root", "/..", "escapes plugin namespace"},
		{"parent traversal in middle", "/foo/../../bar", "escapes plugin namespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildHookURL("https://mm.example.com", "com.example.plugin", tc.path)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func TestBuildHookURL_AcceptsScopedPath(t *testing.T) {
	got, err := buildHookURL("https://mm.example.com/", "com.example.plugin", "/hooks/before")
	require.NoError(t, err)
	assert.Equal(t, "https://mm.example.com/plugins/com.example.plugin/hooks/before", got)
}

// TestRunBeforeHook_RejectsTraversal makes sure the path-traversal rejection in
// buildHookURL surfaces as a hook failure (i.e. tool execution is blocked) and
// no HTTP request is made.
func TestRunBeforeHook_RejectsTraversal(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mcpCtx := &MCPToolContext{
		Ctx:          context.Background(),
		Client:       newHookTestClient("tok"),
		MMServerURL:  srv.URL,
		HookPluginID: "com.example.plugin",
		UserID:       "user-1",
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeCallback: "/../../api/v4/users/me"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes plugin namespace")
	assert.False(t, called, "hook server must not be reached on traversal attempt")
}
