// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMCPToolsCacheClusterRoundTrip(t *testing.T) {
	server := gomcp.NewServer(&gomcp.Implementation{Name: "catalog", Version: "1.0"}, nil)
	addTool := func(name string) {
		server.AddTool(&gomcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			return &gomcp.CallToolResult{}, nil
		})
	}
	addTool("original")
	remote := httptest.NewServer(gomcp.NewStreamableHTTPHandler(func(*http.Request) *gomcp.Server { return server }, &gomcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	t.Cleanup(remote.Close)
	var mu sync.Mutex
	kv := map[string][]byte{}
	nodes := make([]*Plugin, 2)
	mocks := make([]*plugintest.API, 2)
	cfg := mcp.Config{Enabled: true, Servers: []mcp.ServerConfig{{Name: "catalog", BaseURL: remote.URL, Enabled: true}}}
	for i := range nodes {
		mockAPI := &plugintest.API{}
		for _, method := range []string{"LogDebug", "LogInfo", "LogWarn", "LogError"} {
			for count := 1; count <= 11; count++ {
				args := make([]any, count)
				for j := range args {
					args[j] = mock.Anything
				}
				mockAPI.On(method, args...).Return().Maybe()
			}
		}
		mockAPI.On("KVGet", mock.Anything).Return(func(key string) ([]byte, *model.AppError) {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(kv[key]), nil
		})
		mockAPI.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(func(key string, value []byte, _ model.PluginKVSetOptions) (bool, *model.AppError) {
			mu.Lock()
			defer mu.Unlock()
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
			keys := slices.Sorted(maps.Keys(kv))
			return keys[min(page*count, len(keys)):min((page+1)*count, len(keys))], nil
		})
		client := pluginapi.NewClient(mockAPI, nil)
		manager := mcp.NewClientManager(cfg, client.Log, client, nil, nil, &http.Client{Timeout: 5 * time.Second}, nil, mcp.RemoteMCPAlwaysAllowed)
		t.Cleanup(manager.Close)
		nodes[i] = &Plugin{pluginAPI: client, mcpClientManager: manager}
		nodes[i].SetAPI(mockAPI)
		mocks[i] = mockAPI
	}
	for _, node := range nodes {
		for _, user := range []string{"alice", "bob"} {
			tools, errs := node.mcpClientManager.GetTools(context.Background(), mcp.UserCatalogRequest(user))
			require.Nil(t, errs)
			require.Len(t, tools, 1)
		}
	}
	addTool("added")
	_, err := nodes[0].mcpClientManager.GetToolsCache().ClearAll()
	require.NoError(t, err)
	nodes[0].mcpClientManager.InvalidateAllClients()
	mocks[0].On("PublishPluginClusterEvent", mock.AnythingOfType("model.PluginClusterEvent"), mock.MatchedBy(func(opts model.PluginClusterEventSendOptions) bool {
		return opts.SendType == model.PluginClusterEventSendTypeReliable
	})).Return(nil).Run(func(args mock.Arguments) {
		event := args.Get(0).(model.PluginClusterEvent)
		nodes[1].OnPluginClusterEvent(&plugin.Context{}, event)
		nodes[1].OnPluginClusterEvent(&plugin.Context{}, event)
	}).Once()
	require.NoError(t, nodes[0].PublishMCPToolsInvalidate())
	for _, node := range nodes {
		for _, user := range []string{"alice", "bob"} {
			tools, errs := node.mcpClientManager.GetTools(context.Background(), mcp.UserCatalogRequest(user))
			require.Nil(t, errs)
			require.Len(t, tools, 2)
		}
	}
	mocks[0].AssertExpectations(t)
	// The existing event boundary also accepts delivery during activation.
	(&Plugin{}).OnPluginClusterEvent(&plugin.Context{}, model.PluginClusterEvent{Id: clusterEventMCPToolsInvalidate})
}
