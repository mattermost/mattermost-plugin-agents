// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunBeforeHook_NoHook(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user-1")
	mcpCtx := &MCPToolContext{
		Ctx:          ctx,
		MMServerURL:  "https://mm.example.com",
		HookPluginID: "com.example.plugin",
		ToolHooks:    nil,
	}
	err := RunBeforeHook(mcpCtx, "search_posts", map[string]any{"q": "x"})
	require.NoError(t, err)
}

func TestRunBeforeHook_ErrorRejects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/plugins/com.example.plugin/hooks/before", r.URL.Path)
		_ = json.NewEncoder(w).Encode(mcptool.BeforeHookResponse{Error: "not allowed"})
	}))
	defer srv.Close()

	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user-1")
	ctx = context.WithValue(ctx, auth.AuthTokenContextKey, "tok")
	mcpCtx := &MCPToolContext{
		Ctx:          ctx,
		MMServerURL:  strings.TrimSuffix(srv.URL, ""),
		HookPluginID: "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeCallback: "/hooks/before"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", map[string]any{"query": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}

func TestRunBeforeHook_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx := context.Background()
	mcpCtx := &MCPToolContext{
		Ctx:          ctx,
		MMServerURL:  strings.TrimSuffix(srv.URL, ""),
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

	ctx := context.Background()
	mcpCtx := &MCPToolContext{
		Ctx:          ctx,
		MMServerURL:  strings.TrimSuffix(srv.URL, ""),
		HookPluginID: "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeCallback: "/hooks/before"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid response")
}
