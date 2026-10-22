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

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/plugin"
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
