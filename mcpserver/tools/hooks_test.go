// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunBeforeHook_NoHook(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user-1")
	mcpCtx := &MCPToolContext{
		Ctx:               ctx,
		MMServerURL: "https://mm.example.com",
		HookPluginID:      "com.example.plugin",
		ToolHooks:         nil,
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
		Ctx:               ctx,
		MMServerURL: strings.TrimSuffix(srv.URL, ""),
		HookPluginID:      "com.example.plugin",
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
		Ctx:               ctx,
		MMServerURL: strings.TrimSuffix(srv.URL, ""),
		HookPluginID:      "com.example.plugin",
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
		Ctx:               ctx,
		MMServerURL: strings.TrimSuffix(srv.URL, ""),
		HookPluginID:      "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"search_posts": {BeforeCallback: "/hooks/before"},
		},
	}
	err := RunBeforeHook(mcpCtx, "search_posts", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid response")
}

func TestRunAfterHook_TransformsOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcptool.AfterHookRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "user-after-hook", req.UserID)
		var out mcptool.ReadPostOutput
		require.NoError(t, json.Unmarshal(req.Output, &out))
		out.ChannelName = "modified"
		outBytes, _ := json.Marshal(out)
		_ = json.NewEncoder(w).Encode(mcptool.AfterHookResponse{Output: outBytes})
	}))
	defer srv.Close()

	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user-after-hook")
	mcpCtx := &MCPToolContext{
		Ctx:          ctx,
		MMServerURL:  strings.TrimSuffix(srv.URL, ""),
		HookPluginID: "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"read_post": {AfterCallback: "/hooks/after"},
		},
	}
	in := mcptool.ReadPostOutput{
		Posts:       []*model.Post{{Id: "p1", ChannelId: "c1", Message: "m"}},
		ChannelName: "orig",
	}
	out, err := RunAfterHook(mcpCtx, "read_post", in)
	require.NoError(t, err)
	assert.Equal(t, "modified", out.ChannelName)
}

func TestRunAfterHook_ErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(mcptool.AfterHookResponse{Error: "policy"})
	}))
	defer srv.Close()

	ctx := context.Background()
	mcpCtx := &MCPToolContext{
		Ctx:               ctx,
		MMServerURL: strings.TrimSuffix(srv.URL, ""),
		HookPluginID:      "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"read_post": {AfterCallback: "/hooks/after"},
		},
	}
	_, err := RunAfterHook(mcpCtx, "read_post", mcptool.ReadPostOutput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aborted by policy")
}

func TestRunAfterHook_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(60 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	old := hookHTTPClient
	hookHTTPClient = &http.Client{Timeout: 20 * time.Millisecond}
	t.Cleanup(func() { hookHTTPClient = old })

	ctx := context.Background()
	mcpCtx := &MCPToolContext{
		Ctx:               ctx,
		MMServerURL: strings.TrimSuffix(srv.URL, ""),
		HookPluginID:      "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"read_post": {AfterCallback: "/hooks/after"},
		},
	}
	_, err := RunAfterHook(mcpCtx, "read_post", mcptool.ReadPostOutput{Posts: []*model.Post{{Id: "p1"}}})
	require.Error(t, err)
}

func TestRunAfterHookError_NoHook(t *testing.T) {
	orig := fmt.Errorf("raw mm error")
	mcpCtx := &MCPToolContext{
		Ctx:               context.Background(),
		MMServerURL: "https://mm.example.com",
		HookPluginID:      "com.example.plugin",
		ToolHooks:         nil,
	}
	err := RunAfterHookError(mcpCtx, "read_post", orig)
	require.Error(t, err)
	assert.Equal(t, orig, err)

	mcpCtx2 := &MCPToolContext{
		Ctx:               context.Background(),
		MMServerURL: "https://mm.example.com",
		HookPluginID:      "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"read_post": {AfterCallback: ""},
		},
	}
	err = RunAfterHookError(mcpCtx2, "read_post", orig)
	require.Error(t, err)
	assert.Equal(t, orig, err)
}

func TestRunAfterHookError_Sanitizes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcptool.AfterHookRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "read_post", req.ToolName)
		assert.Equal(t, "user-err-hook", req.UserID)
		assert.Equal(t, "raw mm error", req.Error)
		assert.Nil(t, req.Output)
		_ = json.NewEncoder(w).Encode(mcptool.AfterHookResponse{Error: "redacted"})
	}))
	defer srv.Close()

	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user-err-hook")
	mcpCtx := &MCPToolContext{
		Ctx:          ctx,
		MMServerURL:  strings.TrimSuffix(srv.URL, ""),
		HookPluginID: "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"read_post": {AfterCallback: "/hooks/after"},
		},
	}
	err := RunAfterHookError(mcpCtx, "read_post", fmt.Errorf("raw mm error"))
	require.Error(t, err)
	assert.Equal(t, "redacted", err.Error())
}

func TestRunAfterHookError_PassThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	ctx := context.Background()
	mcpCtx := &MCPToolContext{
		Ctx:               ctx,
		MMServerURL: strings.TrimSuffix(srv.URL, ""),
		HookPluginID:      "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"read_post": {AfterCallback: "/hooks/after"},
		},
	}
	orig := fmt.Errorf("unchanged message")
	err := RunAfterHookError(mcpCtx, "read_post", orig)
	require.Error(t, err)
	assert.Equal(t, orig, err)
}

func TestRunAfterHookError_HTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx := context.Background()
	mcpCtx := &MCPToolContext{
		Ctx:               ctx,
		MMServerURL: strings.TrimSuffix(srv.URL, ""),
		HookPluginID:      "com.example.plugin",
		ToolHooks: map[string]ToolHookConfig{
			"read_post": {AfterCallback: "/hooks/after"},
		},
	}
	orig := fmt.Errorf("sensitive detail")
	err := RunAfterHookError(mcpCtx, "read_post", orig)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after-hook failed")
	assert.Contains(t, err.Error(), "HTTP 500")
}
