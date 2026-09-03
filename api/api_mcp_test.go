// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type stubEmbeddedServer struct{}

func (s *stubEmbeddedServer) CreateClientTransport(string, string, *pluginapi.Client) (*gomcp.InMemoryTransport, error) {
	return nil, nil
}

type mcpRequestContextKey struct{}

func TestHandleGetUserMCPToolsIncludesZeroToolConfiguredServers(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	zeroToolServer := mcp.ServerConfig{
		Name:    "Zero Tools",
		Enabled: true,
		BaseURL: "https://zero-tools.example.com",
	}
	toolServer := mcp.ServerConfig{
		Name:    "With Tools",
		Enabled: true,
		BaseURL: "https://with-tools.example.com",
	}

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{
			{
				Name:    "Disabled",
				Enabled: false,
				BaseURL: "https://disabled.example.com",
			},
			zeroToolServer,
			toolServer,
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		tools: []llm.Tool{
			{
				Name:         "z_tool",
				Description:  "second tool",
				ServerOrigin: toolServer.BaseURL,
			},
			{
				Name:         "a_tool",
				Description:  "first tool",
				ServerOrigin: toolServer.BaseURL,
			},
		},
	}

	response := getUserMCPToolsResponse(t, e.api)

	require.Len(t, response.Servers, 2)

	require.Equal(t, zeroToolServer.Name, response.Servers[0].Name)
	require.Equal(t, zeroToolServer.BaseURL, response.Servers[0].ServerOrigin)
	require.Equal(t, mcp.ServerKindRemote, response.Servers[0].Kind)
	require.False(t, response.Servers[0].Authenticated)
	require.Empty(t, response.Servers[0].Tools)

	require.Equal(t, toolServer.Name, response.Servers[1].Name)
	require.Equal(t, toolServer.BaseURL, response.Servers[1].ServerOrigin)
	require.Equal(t, mcp.ServerKindRemote, response.Servers[1].Kind)
	require.True(t, response.Servers[1].Authenticated)
	require.Len(t, response.Servers[1].Tools, 2)
	require.Equal(t, "a_tool", response.Servers[1].Tools[0].Name)
	require.Equal(t, "z_tool", response.Servers[1].Tools[1].Name)

	require.False(t, response.Servers[0].NeedsOAuth)
	require.False(t, response.Servers[1].NeedsOAuth)
}

func TestHandleGetUserMCPToolsPassesRequestContext(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	mcpMock := &mockMCPClientManager{}
	e.api.mcpClientManager = mcpMock

	requestCtx := context.WithValue(context.Background(), mcpRequestContextKey{}, "request-context")
	request := httptest.NewRequest(http.MethodGet, "/mcp/tools", nil).WithContext(requestCtx)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	require.Len(t, mcpMock.getContexts, 1)
	require.Equal(t, "request-context", mcpMock.getContexts[0].Value(mcpRequestContextKey{}))
}

func TestHandleRefreshUserMCPToolsUsesForcedRefresh(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := mcp.ServerConfig{
		Name:    "With Tools",
		Enabled: true,
		BaseURL: "https://with-tools.example.com",
	}
	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{server},
	}
	mcpMock := &mockMCPClientManager{
		tools: []llm.Tool{
			{
				Name:         "refreshed_tool",
				Description:  "refreshed tool",
				ServerOrigin: server.BaseURL,
			},
		},
	}
	e.api.mcpClientManager = mcpMock

	response := refreshUserMCPToolsResponse(t, e.api, nil)

	require.Equal(t, []string{testUserID}, mcpMock.refreshCalls)
	require.Len(t, response.Servers, 1)
	require.Equal(t, server.Name, response.Servers[0].Name)
	require.Len(t, response.Servers[0].Tools, 1)
	require.Equal(t, "refreshed_tool", response.Servers[0].Tools[0].Name)
}

func TestHandleRefreshUserMCPToolsPassesRequestContext(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	mcpMock := &mockMCPClientManager{}
	e.api.mcpClientManager = mcpMock

	requestCtx := context.WithValue(context.Background(), mcpRequestContextKey{}, "refresh-context")
	request := httptest.NewRequest(http.MethodPost, "/mcp/tools/refresh", nil).WithContext(requestCtx)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	require.Len(t, mcpMock.refreshContexts, 1)
	require.Equal(t, "refresh-context", mcpMock.refreshContexts[0].Value(mcpRequestContextKey{}))
}

func TestHandleRefreshUserMCPToolsRejectsRequestBody(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	mcpMock := &mockMCPClientManager{}
	e.api.mcpClientManager = mcpMock

	request := httptest.NewRequest(http.MethodPost, "/mcp/tools/refresh", strings.NewReader("{}"))
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
	require.Empty(t, mcpMock.refreshCalls)
}

func TestHandleGetUserMCPToolsDynamicToolVariants(t *testing.T) {
	tests := []struct {
		name                string
		server              mcp.ServerConfig
		tool                llm.Tool
		expectedName        string
		expectedEnabled     bool
		expectedPolicy      string
		expectedDescription string
	}{
		{
			name: "returns bare names for namespaced runtime tools",
			server: mcp.ServerConfig{
				Name:    "Jira",
				Enabled: true,
				BaseURL: "https://mcp.atlassian.com",
				ToolConfigs: []mcp.ToolConfig{
					{Name: "get_issue", Policy: mcp.ToolPolicyAutoRunEverywhere, Enabled: false},
				},
			},
			tool: llm.Tool{
				Name:         "jira__get_issue",
				Description:  "Get issue",
				ServerOrigin: "https://mcp.atlassian.com",
			},
			expectedName:    "get_issue",
			expectedEnabled: false,
			expectedPolicy:  mcp.ToolPolicyAutoRunEverywhere,
		},
		{
			name: "bare allowlist UI compatibility",
			server: mcp.ServerConfig{
				Name:    "GitHub",
				Enabled: true,
				BaseURL: "https://api.githubcopilot.com",
			},
			tool: llm.Tool{
				Name:         "github__search",
				Description:  "Search",
				ServerOrigin: "https://api.githubcopilot.com",
			},
			expectedName:    "search",
			expectedEnabled: true,
			expectedPolicy:  mcp.ToolPolicyAsk,
		},
		{
			name: "returns upstream description not override",
			server: mcp.ServerConfig{
				Name:    "Jira",
				Enabled: true,
				BaseURL: "https://jira.example.com",
				ToolConfigs: []mcp.ToolConfig{
					{
						Name:                         "get_issue",
						Policy:                       mcp.ToolPolicyAsk,
						Enabled:                      true,
						RetrievalDescriptionOverride: "Admin-only retrieval tuning text",
					},
				},
			},
			tool: llm.Tool{
				Name:         "jira__get_issue",
				Description:  "Upstream MCP description",
				ServerOrigin: "https://jira.example.com",
			},
			expectedName:        "get_issue",
			expectedEnabled:     true,
			expectedPolicy:      mcp.ToolPolicyAsk,
			expectedDescription: "Upstream MCP description",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.ReleaseMode)
			gin.DefaultWriter = io.Discard

			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.config.mcpConfig = mcp.Config{
				Enabled: true,
				Servers: []mcp.ServerConfig{tt.server},
			}
			e.api.mcpClientManager = &mockMCPClientManager{
				tools: []llm.Tool{tt.tool},
			}

			response := getUserMCPToolsResponse(t, e.api)

			require.Len(t, response.Servers, 1)
			require.Len(t, response.Servers[0].Tools, 1)
			require.Equal(t, tt.expectedName, response.Servers[0].Tools[0].Name)
			require.Equal(t, tt.expectedEnabled, response.Servers[0].Tools[0].Enabled)
			require.Equal(t, tt.expectedPolicy, response.Servers[0].Tools[0].Policy)
			if tt.expectedDescription != "" {
				require.Equal(t, tt.expectedDescription, response.Servers[0].Tools[0].Description)
			}
		})
	}
}

func TestHandleGetUserMCPToolsStaticOAuthCredentialsNeedOAuthWhenUnauthenticated(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := mcp.ServerConfig{
		Name:         "static-oauth-server",
		Enabled:      true,
		BaseURL:      "https://static-oauth.example.com",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	}
	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{server},
	}

	mmClient := mmapimocks.NewMockClient(t)
	mmClient.On("KVGet", "mcp_oauth_token_v1_"+testUserID+"_"+server.Name, mock.AnythingOfType("*[]uint8")).Return(mmapi.ErrKVNotFound)
	mmClient.On("KVGet", "mcp_oauth_needed_v1_"+testUserID+"_"+server.Name, mock.AnythingOfType("*mcp.OAuthNeededState")).Return(nil)

	oauthManager := mcp.NewOAuthManager(mmClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", &http.Client{}, func(serverID string) (mcp.ServerConfig, bool) {
		if serverID == server.Name {
			return server, true
		}
		return mcp.ServerConfig{}, false
	})

	e.api.mcpClientManager = &mockMCPClientManager{
		oauthManager: oauthManager,
	}

	response := getUserMCPToolsResponse(t, e.api)

	require.Len(t, response.Servers, 1)
	require.Equal(t, server.Name, response.Servers[0].Name)
	require.False(t, response.Servers[0].Authenticated)
	require.True(t, response.Servers[0].NeedsOAuth)
	require.Equal(t, "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/static-oauth-server/start", response.Servers[0].AuthURL)
	require.Empty(t, response.Servers[0].Tools)
}

func TestHandleGetUserMCPToolsStoredTokenMarksZeroToolServerAuthenticated(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := mcp.ServerConfig{
		Name:    "OAuth Server",
		Enabled: true,
		BaseURL: "https://oauth.example.com",
	}
	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{server},
	}

	mmClient := mmapimocks.NewMockClient(t)
	mmClient.On("KVGet", "mcp_oauth_token_v1_"+testUserID+"_"+server.Name, mock.AnythingOfType("*[]uint8")).
		Run(func(args mock.Arguments) {
			raw := args.Get(1).(*[]byte)
			b, _ := json.Marshal(oauth2.Token{AccessToken: "stored-token"})
			*raw = b
		}).
		Return(nil)
	mmClient.On("KVGet", "mcp_oauth_needed_v1_"+testUserID+"_"+server.Name, mock.AnythingOfType("*mcp.OAuthNeededState")).Return(nil)

	oauthManager := mcp.NewOAuthManager(mmClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", &http.Client{}, func(serverID string) (mcp.ServerConfig, bool) {
		if serverID == server.Name {
			return server, true
		}
		return mcp.ServerConfig{}, false
	})

	e.api.mcpClientManager = &mockMCPClientManager{
		oauthManager: oauthManager,
	}

	response := getUserMCPToolsResponse(t, e.api)

	require.Len(t, response.Servers, 1)
	require.Equal(t, server.Name, response.Servers[0].Name)
	require.True(t, response.Servers[0].Authenticated)
	require.True(t, response.Servers[0].NeedsOAuth)
	require.Empty(t, response.Servers[0].AuthURL)
	require.Empty(t, response.Servers[0].Tools)
}

func TestHandleGetUserMCPToolsAuthErrorsOverrideStoredTokensForZeroToolServers(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := mcp.ServerConfig{
		Name:    "OAuth Server",
		Enabled: true,
		BaseURL: "https://oauth.example.com",
	}
	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{server},
	}

	mmClient := mmapimocks.NewMockClient(t)
	mmClient.On("KVGet", "mcp_oauth_token_v1_"+testUserID+"_"+server.Name, mock.AnythingOfType("*[]uint8")).
		Run(func(args mock.Arguments) {
			raw := args.Get(1).(*[]byte)
			b, _ := json.Marshal(oauth2.Token{AccessToken: "stored-token"})
			*raw = b
		}).
		Return(nil).
		Maybe()
	mmClient.On("KVGet", "mcp_oauth_needed_v1_"+testUserID+"_"+server.Name, mock.AnythingOfType("*mcp.OAuthNeededState")).Return(nil).Maybe()

	oauthManager := mcp.NewOAuthManager(mmClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", &http.Client{}, func(serverID string) (mcp.ServerConfig, bool) {
		if serverID == server.Name {
			return server, true
		}
		return mcp.ServerConfig{}, false
	})

	e.api.mcpClientManager = &mockMCPClientManager{
		oauthManager: oauthManager,
		mcpErrors: &mcp.Errors{
			ToolAuthErrors: []llm.ToolAuthError{
				{
					ServerName:   server.Name,
					ServerOrigin: server.BaseURL,
					AuthURL:      "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/OAuth%20Server/start",
					Error:        errors.New("oauth needed"),
				},
			},
		},
	}

	response := getUserMCPToolsResponse(t, e.api)

	require.Len(t, response.Servers, 1)
	require.Equal(t, server.Name, response.Servers[0].Name)
	require.False(t, response.Servers[0].Authenticated)
	require.Empty(t, response.Servers[0].Tools)
	require.True(t, response.Servers[0].NeedsOAuth)
	require.Equal(t, "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/OAuth%20Server/start", response.Servers[0].AuthURL)
}

func TestHandleGetUserMCPToolsIncludesEmbeddedZeroToolServer(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		EmbeddedServer: mcp.EmbeddedServerConfig{
			Enabled: true,
		},
	}
	e.api.mcpClientManager = &mockMCPClientManager{
		embeddedServer: &stubEmbeddedServer{},
	}

	response := getUserMCPToolsResponse(t, e.api)

	require.Len(t, response.Servers, 1)
	require.Equal(t, mcp.EmbeddedServerName, response.Servers[0].Name)
	require.Equal(t, mcp.EmbeddedClientKey, response.Servers[0].ServerOrigin)
	require.Equal(t, mcp.ServerKindEmbedded, response.Servers[0].Kind)
	require.False(t, response.Servers[0].ServiceAccountConfigured)
	require.True(t, response.Servers[0].Authenticated)
	require.Empty(t, response.Servers[0].Tools)
	require.False(t, response.Servers[0].NeedsOAuth)
	require.Empty(t, response.Servers[0].AuthURL)
}

func TestHandleGetUserMCPToolsIncludesPluginServers(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	pluginCfg := mcp.PluginServerConfig{
		PluginID: "com.example.mcp-demo",
		Name:     "MCP Demo",
		Path:     "/mcp",
		Enabled:  true,
	}
	disabledCfg := mcp.PluginServerConfig{
		PluginID: "com.example.disabled",
		Name:     "Disabled Plugin",
		Path:     "/mcp",
		Enabled:  false,
	}

	e.config.mcpConfig = mcp.Config{Enabled: true}
	e.api.mcpClientManager = &mockMCPClientManager{
		pluginServers: []mcp.PluginServerConfig{pluginCfg, disabledCfg},
		tools: []llm.Tool{
			{
				Name:         "echo",
				Description:  "echo back input",
				ServerOrigin: "plugin://" + pluginCfg.PluginID,
			},
			{
				Name:         "add",
				Description:  "add two numbers",
				ServerOrigin: "plugin://" + pluginCfg.PluginID,
			},
		},
	}

	response := getUserMCPToolsResponse(t, e.api)

	require.Len(t, response.Servers, 1)
	require.Equal(t, pluginCfg.Name, response.Servers[0].Name)
	require.Equal(t, "plugin://"+pluginCfg.PluginID, response.Servers[0].ServerOrigin)
	require.Equal(t, mcp.ServerKindPlugin, response.Servers[0].Kind)
	require.False(t, response.Servers[0].ServiceAccountConfigured)
	require.True(t, response.Servers[0].Authenticated)
	require.False(t, response.Servers[0].NeedsOAuth)
	require.Len(t, response.Servers[0].Tools, 2)
	require.Equal(t, "add", response.Servers[0].Tools[0].Name)
	require.Equal(t, "echo", response.Servers[0].Tools[1].Name)
	// Default-allow synthetic entries (filterToolsByConfig): every tool is enabled with "ask" policy.
	for _, tool := range response.Servers[0].Tools {
		require.True(t, tool.Enabled, "tool %q should default to enabled", tool.Name)
		require.Equal(t, "ask", tool.Policy, "tool %q should default to ask policy", tool.Name)
	}
}

func TestHandleGetUserMCPToolsAuthNeededStateOverridesDiscoveredTools(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	server := mcp.ServerConfig{
		Name:    "GitHub",
		Enabled: true,
		BaseURL: "https://api.githubcopilot.com/mcp",
	}
	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{server},
	}

	mmClient := mmapimocks.NewMockClient(t)
	mmClient.On("KVGet", "mcp_oauth_token_v1_"+testUserID+"_"+server.Name, mock.AnythingOfType("*[]uint8")).Return(mmapi.ErrKVNotFound)
	mmClient.On("KVGet", "mcp_oauth_needed_v1_"+testUserID+"_"+server.Name, mock.AnythingOfType("*mcp.OAuthNeededState")).
		Run(func(args mock.Arguments) {
			state := args.Get(1).(*mcp.OAuthNeededState)
			*state = mcp.OAuthNeededState{
				AuthURL: "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/GitHub/start?resource_metadata=https%3A%2F%2Fapi.githubcopilot.com%2F.well-known%2Foauth-protected-resource%2Fmcp",
			}
		}).
		Return(nil)

	oauthManager := mcp.NewOAuthManager(mmClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", &http.Client{}, func(serverID string) (mcp.ServerConfig, bool) {
		if serverID == server.Name {
			return server, true
		}
		return mcp.ServerConfig{}, false
	})

	e.api.mcpClientManager = &mockMCPClientManager{
		oauthManager: oauthManager,
		tools: []llm.Tool{
			{
				Name:         "get_me",
				Description:  "Get current user",
				ServerOrigin: server.BaseURL,
			},
		},
	}

	response := getUserMCPToolsResponse(t, e.api)

	require.Len(t, response.Servers, 1)
	require.Equal(t, server.Name, response.Servers[0].Name)
	require.False(t, response.Servers[0].Authenticated)
	require.True(t, response.Servers[0].NeedsOAuth)
	require.Equal(t, "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/GitHub/start?resource_metadata=https%3A%2F%2Fapi.githubcopilot.com%2F.well-known%2Foauth-protected-resource%2Fmcp", response.Servers[0].AuthURL)
	require.Len(t, response.Servers[0].Tools, 1)
}

func getUserMCPToolsResponse(t *testing.T, api *API) UserMCPToolsResponse {
	t.Helper()
	response, status := requestUserMCPTools(t, api, "")
	require.Equal(t, http.StatusOK, status)
	return response
}

func requestUserMCPTools(t *testing.T, api *API, rawQuery string) (UserMCPToolsResponse, int) {
	t.Helper()

	path := "/mcp/tools"
	if rawQuery != "" {
		path += "?" + rawQuery
	}
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	api.ServeHTTP(nil, recorder, request)

	var response UserMCPToolsResponse
	if recorder.Result().StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	}
	return response, recorder.Result().StatusCode
}

func TestHandleGetUserMCPToolsServiceAccountCatalog(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	n8nServer := mcp.ServerConfig{
		Name:                  "n8n",
		Enabled:               true,
		BaseURL:               "https://n8n.example.com/mcp",
		ServiceAccountHeaders: map[string]string{"X-API-KEY": "sa-pat"},
	}
	oauthOnlyServer := mcp.ServerConfig{
		Name:     "OAuth Only",
		Enabled:  true,
		BaseURL:  "https://oauth.example.com/mcp",
		ClientID: "client-id",
	}
	saTool := llm.Tool{
		Name:         "workflow_list",
		Description:  "List n8n workflows",
		ServerOrigin: n8nServer.BaseURL,
	}
	userTool := llm.Tool{
		Name:         "user_only_tool",
		Description:  "should not appear in SA catalog",
		ServerOrigin: oauthOnlyServer.BaseURL,
	}

	t.Run("uses the agent's bot user SA catalog and hides OAuth", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		e.agentStore.agents["agent-1"] = &llm.BotConfig{
			ID:                    "agent-1",
			CreatorID:             testUserID,
			BotUserID:             testBotUserID,
			UseServiceAccountAuth: true,
		}
		mcpMock := &mockMCPClientManager{
			tools:               []llm.Tool{userTool},
			serviceAccountTools: []llm.Tool{saTool},
		}
		e.api.mcpClientManager = mcpMock
		e.config.mcpConfig = mcp.Config{
			Enabled: true,
			Servers: []mcp.ServerConfig{n8nServer, oauthOnlyServer},
		}

		response, status := requestUserMCPTools(t, e.api, "catalog=service_account&agent_id=agent-1")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, []string{testBotUserID}, mcpMock.getServiceAccountCalls)
		require.Equal(t, []string{testUserID}, mcpMock.getServiceAccountInvokerCalls)
		require.Empty(t, mcpMock.getContexts)

		require.Len(t, response.Servers, 2)
		require.Equal(t, "n8n", response.Servers[0].Name)
		require.Equal(t, mcp.ServerKindRemote, response.Servers[0].Kind)
		require.True(t, response.Servers[0].Authenticated)
		require.False(t, response.Servers[0].NeedsOAuth)
		require.Empty(t, response.Servers[0].AuthURL)
		require.True(t, response.Servers[0].ServiceAccountConfigured)
		require.Len(t, response.Servers[0].Tools, 1)
		require.Equal(t, "workflow_list", response.Servers[0].Tools[0].Name)

		require.Equal(t, "OAuth Only", response.Servers[1].Name)
		require.Equal(t, mcp.ServerKindRemote, response.Servers[1].Kind)
		require.False(t, response.Servers[1].Authenticated)
		require.False(t, response.Servers[1].NeedsOAuth)
		require.Empty(t, response.Servers[1].AuthURL)
		require.False(t, response.Servers[1].ServiceAccountConfigured)
		require.Empty(t, response.Servers[1].Tools)
	})

	t.Run("rejects catalog=service_account without manage access", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		e.agentStore.agents["agent-1"] = &llm.BotConfig{
			ID:                    "agent-1",
			CreatorID:             testOtherUserID,
			BotUserID:             testBotUserID,
			UseServiceAccountAuth: true,
		}
		e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false).Maybe()
		e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false).Maybe()
		mcpMock := &mockMCPClientManager{}
		e.api.mcpClientManager = mcpMock

		_, status := requestUserMCPTools(t, e.api, "catalog=service_account&agent_id=agent-1")
		require.Equal(t, http.StatusForbidden, status)
		require.Empty(t, mcpMock.getServiceAccountCalls)
		require.Empty(t, mcpMock.getContexts)
	})

	t.Run("sysadmin can preview SA catalog before the flag is saved", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		e.agentStore.agents["agent-1"] = &llm.BotConfig{
			ID:                    "agent-1",
			CreatorID:             testUserID,
			BotUserID:             testBotUserID,
			UseServiceAccountAuth: false,
		}
		e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true)
		mcpMock := &mockMCPClientManager{serviceAccountTools: []llm.Tool{saTool}}
		e.api.mcpClientManager = mcpMock
		e.config.mcpConfig = mcp.Config{Enabled: true, Servers: []mcp.ServerConfig{n8nServer}}

		response, status := requestUserMCPTools(t, e.api, "catalog=service_account&agent_id=agent-1")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, []string{testBotUserID}, mcpMock.getServiceAccountCalls)
		require.Equal(t, []string{testUserID}, mcpMock.getServiceAccountInvokerCalls)
		require.True(t, response.Servers[0].Authenticated)
	})

	t.Run("empty BotUserID returns 500 and does not fetch SA tools", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		e.agentStore.agents["agent-1"] = &llm.BotConfig{
			ID:                    "agent-1",
			CreatorID:             testUserID,
			BotUserID:             "",
			UseServiceAccountAuth: true,
		}
		mcpMock := &mockMCPClientManager{}
		e.api.mcpClientManager = mcpMock

		_, status := requestUserMCPTools(t, e.api, "catalog=service_account&agent_id=agent-1")
		require.Equal(t, http.StatusInternalServerError, status)
		require.Empty(t, mcpMock.getServiceAccountCalls)
		require.Empty(t, mcpMock.getContexts)
	})

	t.Run("rejects unknown catalog values", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		mcpMock := &mockMCPClientManager{}
		e.api.mcpClientManager = mcpMock
		_, status := requestUserMCPTools(t, e.api, "catalog=user")
		require.Equal(t, http.StatusBadRequest, status)
		require.Empty(t, mcpMock.getServiceAccountCalls)
		require.Empty(t, mcpMock.getContexts)
	})

	t.Run("sysadmin unsaved-agent preview uses the viewer as remote owner and invoker", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true)
		mcpMock := &mockMCPClientManager{serviceAccountTools: []llm.Tool{saTool}}
		e.api.mcpClientManager = mcpMock
		e.config.mcpConfig = mcp.Config{Enabled: true, Servers: []mcp.ServerConfig{n8nServer}}

		response, status := requestUserMCPTools(t, e.api, "catalog=service_account")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, []string{testUserID}, mcpMock.getServiceAccountCalls)
		require.Equal(t, []string{testUserID}, mcpMock.getServiceAccountInvokerCalls)
		require.True(t, response.Servers[0].Authenticated)
		require.Equal(t, mcp.ServerKindRemote, response.Servers[0].Kind)
	})
}

func refreshUserMCPToolsResponse(t *testing.T, api *API, body io.Reader) UserMCPToolsResponse {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/mcp/tools/refresh", body)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var response UserMCPToolsResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	return response
}

func TestHandleDeleteUserMCPOAuth(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	mcpMock := &mockMCPClientManager{}
	e.api.mcpClientManager = mcpMock
	clusterNotifier := &fakeMCPOAuthClusterNotifier{}
	e.api.mcpOAuthNotifier = clusterNotifier

	const testServerOrigin = "https://mcp.test/"
	e.config.mcpConfig = mcp.Config{
		Servers: []mcp.ServerConfig{
			{Name: "TestServer", BaseURL: testServerOrigin, Enabled: true},
		},
	}

	mmClient := mmapimocks.NewMockClient(t)
	var gotEvent string
	var gotPayload map[string]any
	var gotBroadcast *model.WebsocketBroadcast
	mmClient.On("PublishWebSocketEvent", mock.AnythingOfType("string"), mock.AnythingOfType("map[string]interface {}"), mock.AnythingOfType("*model.WebsocketBroadcast")).
		Run(func(args mock.Arguments) {
			gotEvent = args.String(0)
			gotPayload, _ = args.Get(1).(map[string]any)
			gotBroadcast, _ = args.Get(2).(*model.WebsocketBroadcast)
		}).Return()
	e.api.mmClient = mmClient

	request := httptest.NewRequest(http.MethodDelete, "/mcp/oauth/TestServer", nil)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	require.Equal(t, []mcpDisconnectCall{{userID: testUserID, serverName: "TestServer"}}, mcpMock.disconnectCalls)
	require.Equal(t, []string{testUserID}, clusterNotifier.calls)
	require.Equal(t, WebsocketEventMCPConnectionUpdated, gotEvent)
	require.Equal(t, "disconnected", gotPayload["status"])
	require.Equal(t, "TestServer", gotPayload["serverName"])
	require.Equal(t, testServerOrigin, gotPayload["serverOrigin"])
	require.NotNil(t, gotBroadcast)
	require.Equal(t, testUserID, gotBroadcast.UserId)
}

func TestHandleDeleteUserMCPOAuthClusterPublishFailureStillSucceeds(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.api.mcpClientManager = &mockMCPClientManager{}
	clusterNotifier := &fakeMCPOAuthClusterNotifier{err: errors.New("cluster publish failed")}
	e.api.mcpOAuthNotifier = clusterNotifier
	e.mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	request := httptest.NewRequest(http.MethodDelete, "/mcp/oauth/TestServer", nil)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	require.Equal(t, []string{testUserID}, clusterNotifier.calls)
}

func TestHandleDeleteUserMCPOAuthDisconnectError(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	mcpMock := &mockMCPClientManager{disconnectErr: errors.New("oauth store unavailable")}
	e.api.mcpClientManager = mcpMock
	clusterNotifier := &fakeMCPOAuthClusterNotifier{}
	e.api.mcpOAuthNotifier = clusterNotifier

	request := httptest.NewRequest(http.MethodDelete, "/mcp/oauth/TestServer", nil)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusInternalServerError, recorder.Result().StatusCode)
	require.Equal(t, []mcpDisconnectCall{{userID: testUserID, serverName: "TestServer"}}, mcpMock.disconnectCalls)
	require.Empty(t, clusterNotifier.calls)
}

func TestHandleDeleteUserMCPOAuthDoesNotNotifyOnDisconnectFailure(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.api.mcpClientManager = &mockMCPClientManager{disconnectErr: errors.New("delete token failed")}
	clusterNotifier := &fakeMCPOAuthClusterNotifier{}
	e.api.mcpOAuthNotifier = clusterNotifier
	e.mockAPI.On("LogError", mock.Anything).Return().Maybe()

	request := httptest.NewRequest(http.MethodDelete, "/mcp/oauth/TestServer", nil)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusInternalServerError, recorder.Result().StatusCode)
	require.Empty(t, clusterNotifier.calls)
}

func TestHandleDeleteUserMCPOAuthMissingServerName(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.api.mcpClientManager = &mockMCPClientManager{}

	request := httptest.NewRequest(http.MethodDelete, "/mcp/oauth/", nil)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Result().StatusCode)
}

func TestHandleOAuthStartRedirectsToProviderAuthorizeURL(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	var authServer *httptest.Server
	authServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resource":"` + authServer.URL + `","authorization_servers":["` + authServer.URL + `"]}`))
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + authServer.URL + `","authorization_endpoint":"` + authServer.URL + `/authorize","token_endpoint":"` + authServer.URL + `/token","response_types_supported":["code"],"grant_types_supported":["authorization_code"],"code_challenge_methods_supported":["S256"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	server := mcp.ServerConfig{
		Name:         "OAuth Server",
		Enabled:      true,
		BaseURL:      authServer.URL,
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	}
	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{server},
	}

	mmClient := mmapimocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()
	mmClient.On("KVSetWithExpiry", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession"), mock.Anything).Return(nil)
	// Trust-on-first-use issuer pin for static credentials.
	mmClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.staticIssuerPin")).Return(mmapi.ErrKVNotFound).Maybe()
	mmClient.On("KVCompareAndSet", mock.AnythingOfType("string"), nil, mock.AnythingOfType("*mcp.staticIssuerPin")).Return(true, nil).Maybe()

	oauthManager := mcp.NewOAuthManager(mmClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", authServer.Client(), func(serverID string) (mcp.ServerConfig, bool) {
		if serverID == server.Name {
			return server, true
		}
		return mcp.ServerConfig{}, false
	})

	e.api.mcpClientManager = &mockMCPClientManager{
		oauthManager: oauthManager,
	}

	request := httptest.NewRequest(http.MethodGet, "/mcp/oauth/"+url.PathEscape(server.Name)+"/start", nil)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusFound, recorder.Result().StatusCode)

	redirectURL, err := url.Parse(recorder.Result().Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/authorize", redirectURL.Path)
	require.Equal(t, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", redirectURL.Query().Get("redirect_uri"))
	require.NotEmpty(t, redirectURL.Query().Get("state"))
	require.NotEmpty(t, redirectURL.Query().Get("code_challenge"))
	require.Equal(t, "S256", redirectURL.Query().Get("code_challenge_method"))
}

func TestHandleOAuthStartRejectsResourceMetadataWrongOrigin(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	var authServer *httptest.Server
	authServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resource":"` + authServer.URL + `","authorization_servers":["` + authServer.URL + `"]}`))
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + authServer.URL + `","authorization_endpoint":"` + authServer.URL + `/authorize","token_endpoint":"` + authServer.URL + `/token","response_types_supported":["code"],"grant_types_supported":["authorization_code"],"code_challenge_methods_supported":["S256"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	evilServer := httptest.NewServer(http.NotFoundHandler())
	defer evilServer.Close()

	server := mcp.ServerConfig{
		Name:         "OAuth Server",
		Enabled:      true,
		BaseURL:      authServer.URL,
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	}
	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{server},
	}

	mmClient := mmapimocks.NewMockClient(t)
	oauthManager := mcp.NewOAuthManager(mmClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", authServer.Client(), func(serverID string) (mcp.ServerConfig, bool) {
		if serverID == server.Name {
			return server, true
		}
		return mcp.ServerConfig{}, false
	})

	e.api.mcpClientManager = &mockMCPClientManager{
		oauthManager: oauthManager,
	}

	e.mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	startPath := "/mcp/oauth/" + url.PathEscape(server.Name) + "/start"
	metadata := evilServer.URL + "/.well-known/oauth-protected-resource"
	request := httptest.NewRequest(http.MethodGet, startPath+"?resource_metadata="+url.QueryEscape(metadata), nil)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
	require.Empty(t, recorder.Result().Header.Get("Location"))
}

func TestPublishMCPConnectionUpdatedEmitsUserScopedEvent(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	mmClient := mmapimocks.NewMockClient(t)
	var gotEvent string
	var gotPayload map[string]any
	var gotBroadcast *model.WebsocketBroadcast
	mmClient.On("PublishWebSocketEvent", mock.AnythingOfType("string"), mock.AnythingOfType("map[string]interface {}"), mock.AnythingOfType("*model.WebsocketBroadcast")).
		Run(func(args mock.Arguments) {
			gotEvent = args.String(0)
			gotPayload, _ = args.Get(1).(map[string]any)
			gotBroadcast, _ = args.Get(2).(*model.WebsocketBroadcast)
		}).Return()
	e.api.mmClient = mmClient

	session := &mcp.OAuthSession{
		UserID:    testUserID,
		ServerID:  "AtlassianMCP",
		ServerURL: "https://mcp.atlassian.com/v1/sse",
	}
	e.api.publishMCPConnectionUpdated(testUserID, session)

	require.Equal(t, WebsocketEventMCPConnectionUpdated, gotEvent)
	require.Equal(t, "connected", gotPayload["status"])
	require.Equal(t, "AtlassianMCP", gotPayload["serverName"])
	require.Equal(t, "https://mcp.atlassian.com/v1/sse", gotPayload["serverOrigin"])
	require.NotNil(t, gotBroadcast)
	require.Equal(t, testUserID, gotBroadcast.UserId)
}

func TestPublishMCPConnectionUpdatedNoOpWhenMMClientMissing(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.api.mmClient = nil
	session := &mcp.OAuthSession{
		UserID:    testUserID,
		ServerID:  "TestServer",
		ServerURL: "https://test.example.com",
	}
	e.api.publishMCPConnectionUpdated(testUserID, session)
}

func TestHandleOAuthStartAcceptsResourceMetadataMatchingOrigin(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	var authServer *httptest.Server
	authServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resource":"` + authServer.URL + `","authorization_servers":["` + authServer.URL + `"]}`))
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + authServer.URL + `","authorization_endpoint":"` + authServer.URL + `/authorize","token_endpoint":"` + authServer.URL + `/token","response_types_supported":["code"],"grant_types_supported":["authorization_code"],"code_challenge_methods_supported":["S256"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	server := mcp.ServerConfig{
		Name:         "OAuth Server",
		Enabled:      true,
		BaseURL:      authServer.URL,
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	}
	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{server},
	}

	mmClient := mmapimocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()
	mmClient.On("KVSetWithExpiry", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession"), mock.Anything).Return(nil)
	// Trust-on-first-use issuer pin for static credentials.
	mmClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.staticIssuerPin")).Return(mmapi.ErrKVNotFound).Maybe()
	mmClient.On("KVCompareAndSet", mock.AnythingOfType("string"), nil, mock.AnythingOfType("*mcp.staticIssuerPin")).Return(true, nil).Maybe()

	oauthManager := mcp.NewOAuthManager(mmClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", authServer.Client(), func(serverID string) (mcp.ServerConfig, bool) {
		if serverID == server.Name {
			return server, true
		}
		return mcp.ServerConfig{}, false
	})

	e.api.mcpClientManager = &mockMCPClientManager{
		oauthManager: oauthManager,
	}

	metadata := authServer.URL + "/.well-known/oauth-protected-resource"
	startPath := "/mcp/oauth/" + url.PathEscape(server.Name) + "/start"
	request := httptest.NewRequest(http.MethodGet, startPath+"?resource_metadata="+url.QueryEscape(metadata), nil)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusFound, recorder.Result().StatusCode)

	redirectURL, err := url.Parse(recorder.Result().Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/authorize", redirectURL.Path)
}

// setupAuditOAuthStartEnv wires a fake OAuth provider (protected-resource and
// authorization-server metadata endpoints) plus a matching enabled MCP server
// config so GET /mcp/oauth/:serverName/start reaches its 302 happy path.
func setupAuditOAuthStartEnv(t *testing.T, e *TestEnvironment) mcp.ServerConfig {
	t.Helper()

	var authServer *httptest.Server
	authServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resource":"` + authServer.URL + `","authorization_servers":["` + authServer.URL + `"]}`))
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + authServer.URL + `","authorization_endpoint":"` + authServer.URL + `/authorize","token_endpoint":"` + authServer.URL + `/token","response_types_supported":["code"],"grant_types_supported":["authorization_code"],"code_challenge_methods_supported":["S256"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(authServer.Close)

	server := mcp.ServerConfig{
		Name:         "audit-oauth-server",
		Enabled:      true,
		BaseURL:      authServer.URL,
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	}
	e.config.mcpConfig = mcp.Config{
		Enabled: true,
		Servers: []mcp.ServerConfig{server},
	}

	mmClient := mmapimocks.NewMockClient(t)
	// OAuth discovery emits step-by-step LogDebug/LogWarn diagnostics; they are
	// never asserted on here, so accept them.
	mmClient.On("LogDebug", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()
	mmClient.On("LogWarn", mock.AnythingOfType("string"), mock.Anything).Return().Maybe()
	// Static credentials are trust-on-first-use pinned to their issuer, which
	// reads and (first time) writes a pin record before the session is stored.
	mmClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.staticIssuerPin")).Return(mmapi.ErrKVNotFound).Maybe()
	mmClient.On("KVCompareAndSet", mock.AnythingOfType("string"), nil, mock.AnythingOfType("*mcp.staticIssuerPin")).Return(true, nil).Maybe()
	mmClient.On("KVSetWithExpiry", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession"), mock.Anything).Return(nil)

	oauthManager := mcp.NewOAuthManager(mmClient, "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback", authServer.Client(), func(serverID string) (mcp.ServerConfig, bool) {
		if serverID == server.Name {
			return server, true
		}
		return mcp.ServerConfig{}, false
	})

	e.api.mcpClientManager = &mockMCPClientManager{oauthManager: oauthManager}
	return server
}

func TestAuditMCPOAuthStart(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		setup          func(t *testing.T, e *TestEnvironment) (startPath, serverName string)
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord, res *http.Response)
	}{
		{
			name: "unknown server records a 404 fail carrying the requested server name",
			setup: func(t *testing.T, e *TestEnvironment) (string, string) {
				e.config.mcpConfig = mcp.Config{Enabled: true}
				return "/mcp/oauth/missing-server/start", "missing-server"
			},
			expectedStatus: http.StatusNotFound,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, _ *http.Response) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusNotFound, rec.Error.Code)
			},
		},
		{
			name: "redirect to the provider records success without the auth URL",
			setup: func(t *testing.T, e *TestEnvironment) (string, string) {
				server := setupAuditOAuthStartEnv(t, e)
				return "/mcp/oauth/" + url.PathEscape(server.Name) + "/start", server.Name
			},
			expectedStatus: http.StatusFound,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, res *http.Response) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status, "a 302 redirect is this handler's happy path")

				redirectURL, err := url.Parse(res.Header.Get("Location"))
				require.NoError(t, err)
				state := redirectURL.Query().Get("state")
				require.NotEmpty(t, state)
				raw, err := json.Marshal(rec)
				require.NoError(t, err)
				assert.NotContains(t, string(raw), state,
					"audit record must never carry the provider auth URL or its state")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			startPath, serverName := tt.setup(t, e)
			records := e.CaptureAuditRecords()

			request := httptest.NewRequest(http.MethodGet, startPath, nil)
			request.Header.Add("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventMCPOAuthStart, rec.EventName)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			assert.Equal(t, serverName, rec.EventData.Parameters[audit.KeyMCPServer])
			tt.validateRecord(t, rec, recorder.Result())
		})
	}
}

func TestAuditMCPOAuthCallback(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	// Planted OAuth transaction values that must never enter the audit record.
	const plantedState = "PLANTED-STATE-VALUE"
	const plantedCode = "PLANTED-CODE-VALUE"
	const plantedDescription = "PLANTED-DESCRIPTION"

	tests := []struct {
		name           string
		query          string
		session        *mcp.OAuthSession
		processErr     error
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord, raw string)
	}{
		{
			name:  "success records the granting server and never the state or code",
			query: "?state=" + plantedState + "&code=" + plantedCode,
			session: &mcp.OAuthSession{
				UserID:    testUserID,
				ServerID:  "someserver",
				ServerURL: "https://someserver.example.com",
			},
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, raw string) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, "someserver", rec.EventData.Parameters[audit.KeyMCPServer])
				assert.NotContains(t, raw, plantedState, "audit record must never carry the OAuth state")
				assert.NotContains(t, raw, plantedCode, "audit record must never carry the OAuth code")
			},
		},
		{
			name:           "provider error records only the spec-defined error code",
			query:          "?error=access_denied&error_description=" + plantedDescription,
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, raw string) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.Equal(t, "access_denied", rec.EventData.Parameters["provider_error"])
				assert.NotContains(t, raw, plantedDescription,
					"audit record must never carry provider-controlled error descriptions")
			},
		},
		{
			name:           "non-enum provider error is clamped so crafted redirects cannot inject text",
			query:          "?error=PLANTED-INJECTED-ERROR-TEXT",
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, raw string) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, "other", rec.EventData.Parameters["provider_error"])
				assert.NotContains(t, raw, "PLANTED-INJECTED-ERROR-TEXT",
					"redirect-controlled error text must never reach the audit record")
			},
		},
		{
			name:           "missing parameters record a 400 fail",
			query:          "",
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, _ string) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.NotContains(t, rec.EventData.Parameters, audit.KeyMCPServer)
			},
		},
		{
			name:           "callback processing failure records a fail without the state or code",
			query:          "?state=" + plantedState + "&code=" + plantedCode,
			processErr:     errors.New("token exchange failed"),
			expectedStatus: http.StatusInternalServerError,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, raw string) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusInternalServerError, rec.Error.Code)
				assert.NotContains(t, raw, plantedState, "audit record must never carry the OAuth state")
				assert.NotContains(t, raw, plantedCode, "audit record must never carry the OAuth code")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.mcp.processOAuthSession = tt.session
			e.mcp.processOAuthErr = tt.processErr
			records := e.CaptureAuditRecords()

			request := httptest.NewRequest(http.MethodGet, "/oauth/callback"+tt.query, nil)
			request.Header.Add("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventMCPOAuthCallback, rec.EventName)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			assert.Equal(t, "/oauth/callback", rec.Meta[model.AuditKeyAPIPath],
				"api_path must exclude the query string, which carries the state and code")
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			tt.validateRecord(t, rec, string(raw))
		})
	}
}

func TestAuditMCPOAuthDisconnect(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		disconnectErr  error
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name:           "successful disconnect records success with the server name",
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
			},
		},
		{
			name:           "disconnect failure records a fail with the server name",
			disconnectErr:  errors.New("oauth store unavailable"),
			expectedStatus: http.StatusInternalServerError,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusInternalServerError, rec.Error.Code)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.mcp.disconnectErr = tt.disconnectErr
			records := e.CaptureAuditRecords()

			request := httptest.NewRequest(http.MethodDelete, "/mcp/oauth/TestServer", nil)
			request.Header.Add("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventMCPOAuthDisconnect, rec.EventName)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			assert.Equal(t, "TestServer", rec.EventData.Parameters[audit.KeyMCPServer])
			tt.validateRecord(t, rec)
		})
	}
}
