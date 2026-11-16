// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Servers with context-aware plugin HTTP cancel a request's context as soon as
// the request ends, while the response is still being produced on it.
func TestServeHTTPWorkOutlivesTheServerRequest(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.setupTestBot(llm.BotConfig{Name: "testbot", DisplayName: "Test Bot", UserAccessLevel: llm.UserAccessLevelAll})
	fakeLLM := NewFakeLLM("still streaming")
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}

	ended, endRequest := context.WithCancel(context.Background())
	endRequest()
	req := httptest.NewRequestWithContext(ended, http.MethodPost, "/bridge/v1/completion/agent/"+testBotUserID,
		strings.NewReader(`{"posts":[{"role":"user","message":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mattermost-Plugin-ID", "test-plugin")
	recorder := httptest.NewRecorder()

	e.api.ServeHTTP(&plugin.Context{}, recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, fakeLLM.LastContextErr, "the LLM call must not inherit the server request's cancellation")
}

// The plugin MCP server does its work within the request, so a client that
// disconnects from it must still end the request.
func TestServeHTTPMCPServerRequestEndsWithTheServerRequest(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.config.mcpConfig.EnablePluginServer = true
	e.mockAPI.On("KVGet", mock.Anything).Return(([]byte)(nil), (*model.AppError)(nil)).Maybe()
	e.mockAPI.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(true, (*model.AppError)(nil)).Maybe()

	var handlerCtxErr error
	e.api.mcpHandlers = &mcpserver.PluginMCPHandlers{
		MCPHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
				handlerCtxErr = r.Context().Err()
			case <-time.After(5 * time.Second):
			}
			w.WriteHeader(http.StatusOK)
		}),
	}

	ended, endRequest := context.WithCancel(context.Background())
	endRequest()
	req := httptest.NewRequestWithContext(ended, http.MethodGet, "/mcp-server/mcp", nil)
	req.Header.Set("Mattermost-User-Id", "uzr1234567890123456789012X")
	recorder := httptest.NewRecorder()

	e.api.ServeHTTP(&plugin.Context{}, recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.ErrorIs(t, handlerCtxErr, context.Canceled)
}
