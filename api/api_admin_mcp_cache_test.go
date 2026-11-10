// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestClearMCPToolsCacheRefreshesUserCatalogs checks catalog refresh through the admin route.
// It covers user and service-account catalogs, failure responses, and unrelated KV data.
func TestClearMCPToolsCacheRefreshesUserCatalogs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure string
		status  int
	}{
		{name: "success", status: http.StatusOK},
		{name: "cluster publish failure", failure: "cluster", status: http.StatusInternalServerError},
		{name: "generation write failure", failure: "generation", status: http.StatusInternalServerError},
		{name: "key listing failure", failure: "list", status: http.StatusInternalServerError},
		{name: "cache deletion failure", failure: "delete", status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, mockAPI, stores := setupAdminTestEnvironment(t)
			allowAnyPluginAPILogging(mockAPI)
			mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(true)
			mockAPI.On("LogAuditRec", mock.Anything).Return()
			var mu sync.Mutex
			kv := map[string][]byte{"unrelated": []byte(`"preserve"`)}
			clearing := false
			storageError := model.NewAppError("test", "kv.failed", nil, "storage unavailable", http.StatusInternalServerError)
			mockAPI.On("KVGet", mock.Anything).Return(func(key string) ([]byte, *model.AppError) {
				mu.Lock()
				defer mu.Unlock()
				return slices.Clone(kv[key]), nil
			})
			mockAPI.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(func(key string, value []byte, _ model.PluginKVSetOptions) (bool, *model.AppError) {
				mu.Lock()
				defer mu.Unlock()
				if clearing && ((tc.failure == "generation" && value != nil) || (tc.failure == "delete" && value == nil)) {
					return false, storageError
				}
				if value == nil {
					delete(kv, key)
				} else {
					kv[key] = slices.Clone(value)
				}
				return true, nil
			})
			mockAPI.On("KVList", mock.Anything, mock.Anything).Return(func(page, count int) ([]string, *model.AppError) {
				mu.Lock()
				defer mu.Unlock()
				if clearing && tc.failure == "list" {
					return nil, storageError
				}
				keys := slices.Sorted(maps.Keys(kv))
				return keys[min(page*count, len(keys)):min((page+1)*count, len(keys))], nil
			})
			servers := map[string]*gomcp.Server{}
			for _, mode := range []string{"user", "service"} {
				servers[mode] = gomcp.NewServer(&gomcp.Implementation{Name: mode, Version: "1.0"}, nil)
			}
			addTool := func(suffix string) {
				for mode, server := range servers {
					server.AddTool(&gomcp.Tool{Name: mode + suffix, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
						return &gomcp.CallToolResult{}, nil
					})
				}
			}
			addTool("_original")
			remote := httptest.NewServer(gomcp.NewStreamableHTTPHandler(func(r *http.Request) *gomcp.Server {
				if r.Header.Get("Authorization") == "Bearer service-secret" {
					return servers["service"]
				}
				return servers["user"]
			}, &gomcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
			t.Cleanup(remote.Close)
			cfg := mcp.Config{Enabled: true, Servers: []mcp.ServerConfig{{Name: "catalog", BaseURL: remote.URL, Enabled: true, ServiceAccountHeaders: map[string]string{"Authorization": "Bearer service-secret"}}}}
			api.config.(*testConfigImpl).mcpConfig = cfg
			client := pluginapi.NewClient(mockAPI, nil)
			manager := mcp.NewClientManager(cfg, client.Log, client, nil, nil, &http.Client{Timeout: 5 * time.Second}, nil, mcp.RemoteMCPAlwaysAllowed)
			t.Cleanup(manager.Close)
			api.mcpClientManager = manager
			catalogs := []struct{ user, path, mode string }{{"alice", "/mcp/tools", "user"}, {"bob", "/mcp/tools", "user"}, {"admin-user", "/mcp/tools?catalog=service_account", "service"}}
			catalog := func(user, path string) []UserMCPToolInfo {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("Mattermost-User-Id", user)
				recorder := httptest.NewRecorder()
				api.ServeHTTP(&plugin.Context{}, recorder, req)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				var body UserMCPToolsResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
				require.Len(t, body.Servers, 1)
				return body.Servers[0].Tools
			}
			for _, c := range catalogs {
				require.Len(t, catalog(c.user, c.path), 1)
			}
			mu.Lock()
			cached := 0
			for key := range kv {
				if strings.HasPrefix(key, "mcp_tools_cache_v1_") {
					cached++
				}
			}
			mu.Unlock()
			require.Equal(t, 2, cached, "user and service-account catalogs must use separate shared entries")
			addTool("_added")
			for _, c := range catalogs {
				require.Len(t, catalog(c.user, c.path), 1, "the warmed catalog must remain stale until clear")
			}
			mu.Lock()
			clearing = true
			mu.Unlock()
			if tc.failure == "cluster" {
				stores.clusterNotifier.err = errors.New("cluster unavailable")
			}
			req := httptest.NewRequest(http.MethodPost, "/admin/mcp/tools/cache/clear", nil)
			req.Header.Set("Mattermost-User-Id", "admin-user")
			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			mu.Lock()
			clearing = false
			unrelated := string(kv["unrelated"])
			mu.Unlock()
			require.Equal(t, `"preserve"`, unrelated)
			if tc.status != http.StatusOK {
				return
			}
			for _, c := range catalogs {
				tools := catalog(c.user, c.path)
				require.Len(t, tools, 2, c.user)
				for _, tool := range tools {
					require.True(t, strings.HasPrefix(tool.Name, c.mode+"_"), tool.Name)
				}
			}
			require.Equal(t, 1, stores.clusterNotifier.mcpToolsInvalidations)
		})
	}
}
