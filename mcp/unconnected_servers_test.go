// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func connectCallableStore() *llm.ToolStore {
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{
		Name: llm.ConnectMCPServerToolName,
		Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
			return "", nil
		},
	}})
	return store
}

// search_tools results may be shared with a channel, so the connect hint must
// not name the requester's unconnected servers.
func TestSearchToolsConnectHint(t *testing.T) {
	unconnected := []llm.UnconnectedMCPServer{
		{Name: "Jira", Origin: "https://jira.example.com", Description: "Atlassian issue tracker", AuthURL: "https://mm/jira/start", ToolNames: []string{"create_issue"}},
	}
	registry := NewToolRegistry([]llm.Tool{testRegistryTool("github__create_pr", "Open a pull request", "https://github.example.com")})
	connectContext := func() *llm.Context {
		return &llm.Context{
			Tools:       connectCallableStore(),
			ToolCatalog: llm.ToolCatalogContext{UnconnectedMCPServers: unconnected},
		}
	}

	tests := []struct {
		name       string
		query      string
		llmContext *llm.Context
		wantTools  bool
		wantHint   bool
	}{
		{name: "no match with ConnectMCPServer callable", query: "weather forecast", llmContext: connectContext(), wantHint: true},
		{name: "empty query with ConnectMCPServer callable", query: " ", llmContext: connectContext(), wantHint: true},
		{name: "matching tools", query: "pull request", llmContext: connectContext(), wantTools: true},
		{
			name:  "ConnectMCPServer not callable",
			query: "weather forecast",
			llmContext: &llm.Context{
				Tools:       llm.NewToolStore(),
				ToolCatalog: llm.ToolCatalogContext{UnconnectedMCPServers: unconnected},
			},
		},
		{
			name:  "ConnectMCPServer disabled in this context",
			query: "weather forecast",
			llmContext: &llm.Context{
				Tools:             connectCallableStore(),
				DisabledToolsInfo: []llm.ToolInfo{{Name: llm.ConnectMCPServerToolName}},
				ToolCatalog:       llm.ToolCatalogContext{UnconnectedMCPServers: unconnected},
			},
		},
		{name: "nil context", query: "weather forecast"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			searchTool := metaToolByName(t, NewMetaTools(registry), SearchToolsName)
			args, err := json.Marshal(SearchToolsArgs{Query: tt.query})
			require.NoError(t, err)

			raw, err := searchTool.Resolver(context.Background(), tt.llmContext, metaToolArgs(string(args)))
			require.NoError(t, err)

			var result SearchToolsResult
			require.NoError(t, json.Unmarshal([]byte(raw), &result))
			assert.Equal(t, tt.wantTools, len(result.Tools) > 0)
			if tt.wantHint {
				assert.Contains(t, result.Hint, llm.ConnectMCPServerToolName)
			} else {
				assert.Empty(t, result.Hint)
			}
			for _, server := range unconnected {
				assert.NotContains(t, raw, server.Name)
				assert.NotContains(t, raw, server.Description)
				for _, toolName := range server.ToolNames {
					assert.NotContains(t, raw, toolName)
				}
			}
		})
	}
}

// An unconnected server is advertised with admin-authored metadata only. The
// shared tools cache holds whichever catalog the last authenticated user saw,
// so it must not leak into another user's advertisement.
func TestOAuthNeededConnectFailureCarriesAdminMetadataOnly(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(httpServer.Close)

	serverConfig := ServerConfig{
		Name:        "Jira",
		BaseURL:     httpServer.URL,
		Enabled:     true,
		Description: "Jira issues for Engineering",
		ToolConfigs: []ToolConfig{
			{Name: "create_issue", Enabled: true},
			{Name: "delete_project", Enabled: false},
			{Name: "", Enabled: true},
			{Name: "search_issues", Enabled: true},
		},
	}

	toolsCache := newTestToolsCache()
	require.NoError(t, toolsCache.SetTools(serverConfig.Name, serverConfig.Name, serverConfig.BaseURL, map[string]*gomcp.Tool{
		"other_users_private_tool": {Name: "other_users_private_tool", Description: "cached from another user"},
	}, time.Now()))
	require.NotEmpty(t, toolsCache.GetTools(serverConfig.Name))

	uc := NewUserClients("alice", newTestLogService(), newTestOAuthManager(), httpServer.Client(), toolsCache)
	t.Cleanup(uc.Close)

	mcpErrors := uc.ConnectToRemoteServers(context.Background(), []ServerConfig{serverConfig}, false)
	require.NotNil(t, mcpErrors)
	require.Len(t, mcpErrors.ToolAuthErrors, 1)

	authErr := mcpErrors.ToolAuthErrors[0]
	assert.Equal(t, "Jira", authErr.ServerName)
	assert.Equal(t, "Jira issues for Engineering", authErr.ServerDescription)
	assert.Equal(t, []string{"create_issue", "search_issues"}, authErr.ToolNames)
	assert.NotContains(t, authErr.ToolNames, "other_users_private_tool")
	assert.NotEmpty(t, authErr.AuthURL)
}

// A tool call that fails because the user's authorization lapsed reports the
// server as needing a reconnect and stops offering its tools; any other
// failure, and every service-account failure, leaves the session alone.
func TestToolCallErrorReportsLapsedAuthorization(t *testing.T) {
	const origin = "https://jira.example.com"
	const startURL = "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/Jira/start"
	const issuedURL = "https://auth.example.com/authorize?state=1"

	tests := []struct {
		name           string
		err            error
		serviceAccount bool
		noStartURL     bool
		wantAuthURL    string
	}{
		{name: "oauth needed", err: &OAuthNeededError{authURL: issuedURL}, wantAuthURL: issuedURL},
		{name: "wrapped oauth needed", err: errors.Join(errors.New("call failed"), &OAuthNeededError{authURL: issuedURL}), wantAuthURL: issuedURL},
		{name: "oauth needed without URL falls back to start route", err: &OAuthNeededError{}, wantAuthURL: startURL},
		{name: "oauth needed with no way to start it keeps the session", err: &OAuthNeededError{}, noStartURL: true},
		{name: "ordinary failure", err: errors.New("tool exploded")},
		{name: "service account never needs per-user oauth", err: &OAuthNeededError{authURL: issuedURL}, serviceAccount: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oauthManager, _ := newStatefulKVManager(t, nil, http.DefaultClient)
			oauthManager.callbackURL = "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback"
			if tt.noStartURL {
				oauthManager.callbackURL = ""
			}
			uc := NewUserClients("alice", newTestLogService(), oauthManager, nil, nil)
			client := testClientWithTools("Jira", origin, "create_issue")
			client.serviceAccount = tt.serviceAccount
			client.config.Description = "Jira issues"
			client.config.ToolConfigs = []ToolConfig{{Name: "create_issue", Enabled: true}}
			uc.clients["Jira"] = client

			err := uc.toolCallError(client, tt.err)
			require.ErrorIs(t, err, tt.err)

			offered := collectToolsFromSnapshots("alice", newTestLogService(), uc.snapshotClients())
			state, loadErr := oauthManager.LoadAuthNeededState("alice", "Jira")
			require.NoError(t, loadErr)

			needed, isReconnect := errors.AsType[*ReconnectNeededError](err)
			if tt.wantAuthURL == "" {
				assert.False(t, isReconnect)
				assert.Len(t, offered, 1)
				assert.Nil(t, state)
				return
			}

			require.True(t, isReconnect)
			assert.Equal(t, llm.ToolAuthError{
				ServerName:        "Jira",
				ServerOrigin:      origin,
				AuthURL:           tt.wantAuthURL,
				Error:             tt.err,
				ServerDescription: "Jira issues",
				ToolNames:         []string{"create_issue"},
			}, needed.Auth)
			assert.Empty(t, offered)
			require.NotNil(t, state)
			assert.Equal(t, tt.wantAuthURL, state.AuthURL)
			assert.Contains(t, needed.ConnectGuidance(), "Jira")
		})
	}
}
