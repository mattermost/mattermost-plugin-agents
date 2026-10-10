// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// webhookAgentRequest is the JSON request body accepted by the webhook agent
// endpoint for external (non-Mattermost-outgoing-webhook) callers. It is a
// local DTO on purpose: the public/bridgeclient package is a frozen public API
// and must not grow webhook-specific shapes.
type webhookAgentRequest struct {
	Message string `json:"message"`
	Agent   string `json:"agent"`
}

// webhookAgentResponse is the Mattermost outgoing-webhook response shape. The
// same shape is returned to external JSON callers.
type webhookAgentResponse struct {
	Text         string `json:"text"`
	ResponseType string `json:"response_type"`
}

// webhookTokenAuthRequired is gin middleware for the session-less webhook
// endpoint. It is modeled on mcpAuthMiddleware but authenticates with a shared
// secret instead of a Mattermost session, because the Mattermost server strips
// the Mattermost-User-Id header from external callers before proxying to the
// plugin.
//
// When the feature is disabled or no secret is configured the route is not
// exposed: the middleware returns 404 rather than revealing that an endpoint
// exists. A present-but-wrong secret is a 401.
func (a *API) webhookTokenAuthRequired(c *gin.Context) {
	cfg := a.config.Webhook()
	if !cfg.Enabled || cfg.Secret == "" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	presented := webhookPresentedToken(c)
	// ConstantTimeCompare returns 0 when the lengths differ, so an empty or
	// short token is rejected without leaking timing information about the
	// configured secret.
	if subtle.ConstantTimeCompare([]byte(presented), []byte(cfg.Secret)) != 1 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
}

// webhookPresentedToken extracts the caller's token from either an
// Authorization: Bearer <token> header (external JSON callers) or the token
// form field (Mattermost outgoing-webhook style).
func webhookPresentedToken(c *gin.Context) string {
	if authHeader := c.GetHeader("Authorization"); authHeader != "" {
		const prefix = "Bearer "
		if len(authHeader) > len(prefix) && strings.EqualFold(authHeader[:len(prefix)], prefix) {
			return strings.TrimSpace(authHeader[len(prefix):])
		}
	}
	return c.PostForm("token")
}

// handleWebhookAgent routes an incoming webhook message to a configured agent
// and returns the agent's reply in the Mattermost outgoing-webhook response
// shape. Tools are disabled: the caller is an unattended, session-less invoker,
// so no tool call could be reviewed or approved by a human.
func (a *API) handleWebhookAgent(c *gin.Context) {
	ctx := c.Request.Context()

	var (
		message   string
		agentName string
	)

	if isWebhookFormContentType(c.ContentType()) {
		// Mattermost outgoing webhook: application/x-www-form-urlencoded.
		message = strings.TrimSpace(c.PostForm("text"))
		if triggerWord := c.PostForm("trigger_word"); triggerWord != "" {
			message = strings.TrimSpace(strings.TrimPrefix(message, triggerWord))
		}
	} else {
		var req webhookAgentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		message = strings.TrimSpace(req.Message)
		agentName = req.Agent
	}

	// channel_name is attribution only (outgoing webhooks send it); it never
	// reaches the LLM request.
	channelName := c.PostForm("channel_name")

	// The path parameter, when present, overrides the JSON agent field.
	if param := c.Param("botusername"); param != "" {
		agentName = param
	}

	bot := a.resolveWebhookBot(agentName)
	if bot == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	// Enrich the audit record with object identifiers only — never the message
	// text or the shared secret.
	rec := auditRec(c)
	audit.AddParam(rec, audit.KeyAgentID, bot.BotUserID())
	audit.AddParam(rec, audit.KeyAgentName, bot.GetConfig().Name)
	if channelName != "" {
		audit.AddParam(rec, "channel_name", audit.TruncateID(channelName))
	}

	if message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message cannot be empty"})
		return
	}

	llmContext := llm.NewContext()
	if a.contextBuilder != nil {
		a.contextBuilder.WithLLMContextBot(bot)(llmContext)
	}

	llmRequest := llm.CompletionRequest{
		Posts:            []llm.Post{{Role: llm.PostRoleUser, Message: message}},
		Context:          llmContext,
		Operation:        llm.OperationWebhookAgent,
		OperationSubType: llm.SubTypeNoStream,
	}

	reply, err := bot.LLM().ChatCompletionNoStream(ctx, llmRequest, llm.WithToolsDisabled())
	if err != nil {
		// Rich detail to the server log; a generic body to the caller.
		a.pluginAPI.Log.Error("webhook agent completion failed",
			"agent_id", bot.BotUserID(),
			"agent", bot.GetConfig().Name,
			"error", err,
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate a reply"})
		return
	}

	c.JSON(http.StatusOK, webhookAgentResponse{
		Text:         reply,
		ResponseType: "comment",
	})
}

// resolveWebhookBot resolves the target agent. A named agent (path param or
// JSON field) must match an existing bot username; an unspecified agent falls
// back to the configured default bot, then the first bot, mirroring
// aiBotRequired.
func (a *API) resolveWebhookBot(agentName string) *bots.Bot {
	if agentName != "" {
		return a.bots.GetBotByUsername(agentName)
	}
	return a.bots.GetBotByUsernameOrFirst(a.config.GetDefaultBotName())
}

// isWebhookFormContentType reports whether the request body is a form payload
// (Mattermost outgoing webhooks post application/x-www-form-urlencoded).
func isWebhookFormContentType(contentType string) bool {
	return contentType == "application/x-www-form-urlencoded" || contentType == "multipart/form-data"
}
