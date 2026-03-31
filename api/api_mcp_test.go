// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	mmapimocks "github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type stubEmbeddedServer struct{}

func (s *stubEmbeddedServer) CreateClientTransport(string, string, *pluginapi.Client) (*gomcp.InMemoryTransport, error) {
	return nil, nil
}

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
	require.False(t, response.Servers[0].Authenticated)
	require.Empty(t, response.Servers[0].Tools)

	require.Equal(t, toolServer.Name, response.Servers[1].Name)
	require.Equal(t, toolServer.BaseURL, response.Servers[1].ServerOrigin)
	require.True(t, response.Servers[1].Authenticated)
	require.Len(t, response.Servers[1].Tools, 2)
	require.Equal(t, "a_tool", response.Servers[1].Tools[0].Name)
	require.Equal(t, "z_tool", response.Servers[1].Tools[1].Name)
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
	mmClient.On("KVGet", "mcp_oauth_token_v1_"+testUserID+"_"+server.Name, mock.AnythingOfType("*oauth2.Token")).
		Run(func(args mock.Arguments) {
			token := args.Get(1).(*oauth2.Token)
			*token = oauth2.Token{AccessToken: "stored-token"}
		}).
		Return(nil).
		Once()

	oauthManager := mcp.NewOAuthManager(mmClient, "https://mattermost.example.com/plugins/oauth/callback", &http.Client{}, func(serverID string) (mcp.ServerConfig, bool) {
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
	mmClient.On("KVGet", "mcp_oauth_token_v1_"+testUserID+"_"+server.Name, mock.AnythingOfType("*oauth2.Token")).
		Run(func(args mock.Arguments) {
			token := args.Get(1).(*oauth2.Token)
			*token = oauth2.Token{AccessToken: "stored-token"}
		}).
		Return(nil).
		Maybe()

	oauthManager := mcp.NewOAuthManager(mmClient, "https://mattermost.example.com/plugins/oauth/callback", &http.Client{}, func(serverID string) (mcp.ServerConfig, bool) {
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
					AuthURL:      "https://oauth.example.com/authorize",
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
	require.True(t, response.Servers[0].Authenticated)
	require.Empty(t, response.Servers[0].Tools)
}

func getUserMCPToolsResponse(t *testing.T, api *API) UserMCPToolsResponse {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/mcp/tools", nil)
	request.Header.Add("Mattermost-User-Id", testUserID)

	recorder := httptest.NewRecorder()
	api.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var response UserMCPToolsResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	return response
}
