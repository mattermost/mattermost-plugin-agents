// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const webhookSecret = "supersecrettoken1234567890"

// setupWebhookEnv builds a test environment with the webhook feature configured
// and two bots: the default "ai" bot and a "support" bot, each backed by a fake
// LLM with a distinct reply so routing can be asserted.
func setupWebhookEnv(t *testing.T, enabled bool, configuredSecret string, defaultLLMErr bool) *TestEnvironment {
	t.Helper()
	e := SetupTestEnvironment(t)
	e.config.webhookConfig = config.WebhookConfig{Enabled: enabled, Secret: configuredSecret}
	e.mockAPI.On("LogError", mock.Anything, mock.Anything).Maybe()

	var defaultLLM llm.LanguageModel
	if defaultLLMErr {
		defaultLLM = NewFakeLLMWithError(fmt.Errorf("provider unavailable"))
	} else {
		defaultLLM = NewFakeLLM("default reply")
	}

	defaultBot := bots.NewBot(
		llm.BotConfig{Name: "ai", DisplayName: "AI"},
		llm.ServiceConfig{},
		&model.Bot{UserId: testBotUserID, Username: "ai", DisplayName: "AI"},
		defaultLLM,
	)
	supportBot := bots.NewBot(
		llm.BotConfig{Name: "support", DisplayName: "Support"},
		llm.ServiceConfig{},
		&model.Bot{UserId: testOtherUserID, Username: "support", DisplayName: "Support"},
		NewFakeLLM("support reply"),
	)
	e.bots.SetBotsForTesting([]*bots.Bot{defaultBot, supportBot})
	return e
}

func TestWebhookAgent(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	formType := "application/x-www-form-urlencoded"
	jsonType := "application/json"

	tests := []struct {
		name        string
		enabled     bool
		emptySecret bool
		path        string
		contentType string
		body        string
		authHeader  string
		wantStatus  int
		wantText    string
	}{
		{
			name:        "urlencoded happy path returns the reply",
			enabled:     true,
			path:        "/webhooks/agent",
			contentType: formType,
			body:        "token=" + webhookSecret + "&text=hello&channel_name=town-square",
			wantStatus:  http.StatusOK,
			wantText:    "default reply",
		},
		{
			name:        "json happy path returns the reply",
			enabled:     true,
			path:        "/webhooks/agent",
			contentType: jsonType,
			body:        `{"message":"hello"}`,
			authHeader:  "Bearer " + webhookSecret,
			wantStatus:  http.StatusOK,
			wantText:    "default reply",
		},
		{
			name:        "json agent field selects the bot",
			enabled:     true,
			path:        "/webhooks/agent",
			contentType: jsonType,
			body:        `{"message":"hello","agent":"support"}`,
			authHeader:  "Bearer " + webhookSecret,
			wantStatus:  http.StatusOK,
			wantText:    "support reply",
		},
		{
			name:        "path param selects the bot",
			enabled:     true,
			path:        "/webhooks/agent/support",
			contentType: formType,
			body:        "token=" + webhookSecret + "&text=hello",
			wantStatus:  http.StatusOK,
			wantText:    "support reply",
		},
		{
			name:        "wrong token is rejected",
			enabled:     true,
			path:        "/webhooks/agent",
			contentType: formType,
			body:        "token=wrong&text=hello",
			wantStatus:  http.StatusUnauthorized,
		},
		{
			name:        "missing token is rejected",
			enabled:     true,
			path:        "/webhooks/agent",
			contentType: formType,
			body:        "text=hello",
			wantStatus:  http.StatusUnauthorized,
		},
		{
			name:        "feature disabled is not exposed",
			enabled:     false,
			path:        "/webhooks/agent",
			contentType: formType,
			body:        "token=" + webhookSecret + "&text=hello",
			wantStatus:  http.StatusNotFound,
		},
		{
			name:        "empty configured secret is not exposed",
			enabled:     true,
			emptySecret: true,
			path:        "/webhooks/agent",
			contentType: formType,
			body:        "token=&text=hello",
			wantStatus:  http.StatusNotFound,
		},
		{
			name:        "empty message is rejected",
			enabled:     true,
			path:        "/webhooks/agent",
			contentType: formType,
			body:        "token=" + webhookSecret + "&text=",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "unknown agent is not found",
			enabled:     true,
			path:        "/webhooks/agent/nonexistent",
			contentType: formType,
			body:        "token=" + webhookSecret + "&text=hello",
			wantStatus:  http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configuredSecret := webhookSecret
			if tc.emptySecret {
				configuredSecret = ""
			}
			e := setupWebhookEnv(t, tc.enabled, configuredSecret, false)
			defer e.Cleanup(t)

			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			resp := serveAndReturn(e, req)
			require.Equal(t, tc.wantStatus, resp.StatusCode)

			if tc.wantStatus == http.StatusOK {
				defer resp.Body.Close()
				var out webhookAgentResponse
				require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
				require.Equal(t, tc.wantText, out.Text)
				require.Equal(t, "comment", out.ResponseType)
			}
		})
	}
}

func TestWebhookAgentAgentError(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := setupWebhookEnv(t, true, webhookSecret, true)
	defer e.Cleanup(t)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/agent",
		strings.NewReader("token="+webhookSecret+"&text=hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	// The provider error detail must not leak to the caller.
	require.NotContains(t, string(body), "provider unavailable")
}

func TestWebhookAgentStripsTriggerWord(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := setupWebhookEnv(t, true, webhookSecret, false)
	defer e.Cleanup(t)

	// Replace the default bot's LLM with a fake we can inspect.
	fake := NewFakeLLM("ok")
	e.bots.GetBotByUsername("ai").SetLLMForTest(fake)

	body := url.Values{}
	body.Set("token", webhookSecret)
	body.Set("trigger_word", "!ai")
	body.Set("text", "!ai what is the weather")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/agent", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	last := fake.LastRequest()
	require.Len(t, last.Posts, 1)
	require.Equal(t, "what is the weather", last.Posts[0].Message)
	require.Equal(t, llm.PostRoleUser, last.Posts[0].Role)
	// Tools must be disabled for an unattended invoker.
	require.True(t, fake.LastConfig.ToolsDisabled)
}

func TestWebhookAgentAuditRecord(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const sentinelMessage = "SENTINEL_WEBHOOK_MESSAGE_CONTENT"

	e := setupWebhookEnv(t, true, webhookSecret, false)
	defer e.Cleanup(t)

	records := e.CaptureAuditRecords()

	body := url.Values{}
	body.Set("token", webhookSecret)
	body.Set("text", sentinelMessage)
	body.Set("channel_name", "town-square")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/agent", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp := serveAndReturn(e, req)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, *records, 1)
	rec := (*records)[0]
	require.Equal(t, AuditEventWebhookAgent, rec.EventName)
	require.Equal(t, model.AuditStatusSuccess, rec.Status)
	require.Equal(t, testBotUserID, rec.EventData.Parameters[audit.KeyAgentID])
	require.Equal(t, "ai", rec.EventData.Parameters[audit.KeyAgentName])
	require.Equal(t, "town-square", rec.EventData.Parameters["channel_name"])

	// The message content and the shared secret must never enter the record.
	marshaled, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NotContains(t, string(marshaled), sentinelMessage)
	require.NotContains(t, string(marshaled), webhookSecret)
}
