// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	appResourceErrInvalidRequest      = "invalid_request"
	appResourceErrNotFound            = "not_found"
	appResourceErrForbidden           = "forbidden"
	appResourceErrAuthRequired        = "mcp_auth_required"
	appResourceErrUpstreamUnreachable = "upstream_unreachable"
	appResourceErrInvalidResourceMime = "invalid_resource_mime"
)

// AppResourceResponse is the JSON shape returned by GET /mcp/app-resource.
// Phase 1c feeds Html/UIMeta directly into @mcp-ui/client's AppRenderer.
type AppResourceResponse struct {
	ServerOrigin string                 `json:"server_origin"`
	URI          string                 `json:"uri"`
	MIMEType     string                 `json:"mime_type"`
	HTML         string                 `json:"html"`
	UIMeta       *mcp.AppResourceUIMeta `json:"ui_meta,omitempty"`
}

// AppResourceErrorResponse is the JSON error shape for GET /mcp/app-resource.
// ErrorCode drives webapp behavior: mcp_auth_required renders "Connect to
// view" with AuthURL; forbidden renders the no-access popover.
type AppResourceErrorResponse struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	AuthURL   string `json:"auth_url,omitempty"`
}

func (a *API) writeAppResourceError(c *gin.Context, status int, code, message, authURL string) {
	c.JSON(status, AppResourceErrorResponse{
		ErrorCode: code,
		Message:   message,
		AuthURL:   authURL,
	})
}

func (a *API) isKnownMCPServerOrigin(origin string) bool {
	normalized := llm.NormalizeMCPServerOrigin(origin)
	if normalized == "" {
		return false
	}
	if normalized == llm.NormalizeMCPServerOrigin(mcp.EmbeddedClientKey) {
		return true
	}
	for _, server := range a.config.MCP().Servers {
		if !server.Enabled {
			continue
		}
		if llm.NormalizeMCPServerOrigin(server.BaseURL) == normalized {
			return true
		}
	}
	if a.mcpClientManager != nil {
		for _, cfg := range a.mcpClientManager.ListPluginServers() {
			if !cfg.Enabled {
				continue
			}
			if llm.NormalizeMCPServerOrigin("plugin://"+cfg.PluginID) == normalized {
				return true
			}
		}
	}
	return false
}

func (a *API) handleGetMCPAppResource(c *gin.Context) {
	// Explicit non-checks: no license gate (viewing an already-executed tool's
	// UI is a read, consistent with un-gated GET /conversations/:id; execution
	// was already license-gated at handleToolCall), no EnableChannelMentionToolCalling
	// gate (same reason), no tool-status gate (D3's Success/AutoApproved gating
	// is a render-time webapp concern; the resource template itself is not
	// result data).
	userID := c.GetHeader("Mattermost-User-Id")
	postID := c.Query("post_id")
	toolCallID := c.Query("tool_call_id")
	if postID == "" || toolCallID == "" || !model.IsValidId(postID) {
		a.writeAppResourceError(c, http.StatusBadRequest, appResourceErrInvalidRequest, "post_id and tool_call_id are required", "")
		return
	}

	post, err := a.pluginAPI.Post.GetPost(postID)
	if err != nil {
		a.writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "post not found", "")
		return
	}

	if !a.pluginAPI.User.HasPermissionToChannel(userID, post.ChannelId, model.PermissionReadChannel) {
		a.writeAppResourceError(c, http.StatusForbidden, appResourceErrForbidden, "permission denied", "")
		return
	}

	turn, err := a.conversationStore.GetTurnByPostID(postID)
	if err != nil || turn == nil {
		a.writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "turn not found", "")
		return
	}

	conv, err := a.conversationStore.GetConversation(turn.ConversationID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get conversation: %w", err))
		return
	}
	requesterID := conv.UserID

	turns, err := a.conversationStore.GetTurnsForConversation(conv.ID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get turns: %w", err))
		return
	}

	var toolUse *conversation.ContentBlock
	var toolResult *conversation.ContentBlock
	for _, t := range turns {
		var blocks []conversation.ContentBlock
		if unmarshalErr := json.Unmarshal(t.Content, &blocks); unmarshalErr != nil {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to unmarshal turn content: %w", unmarshalErr))
			return
		}
		for i := range blocks {
			block := &blocks[i]
			if block.Type == conversation.BlockTypeToolUse && block.ID == toolCallID {
				// Copy so the pointer outlives the loop body.
				copied := *block
				toolUse = &copied
			}
			if block.Type == conversation.BlockTypeToolResult && block.ToolUseID == toolCallID {
				copied := *block
				toolResult = &copied
			}
		}
	}
	if toolUse == nil {
		a.writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "tool call not found", "")
		return
	}
	if toolUse.UIMeta == nil || toolUse.UIMeta.ResourceURI == "" {
		a.writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "tool call has no app resource", "")
		return
	}

	if userID != requesterID {
		if toolResult == nil || toolResult.Shared == nil || !*toolResult.Shared {
			a.writeAppResourceError(c, http.StatusForbidden, appResourceErrForbidden, "tool result is not shared", "")
			return
		}
	}

	if !a.isKnownMCPServerOrigin(toolUse.ServerOrigin) {
		a.writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "server no longer configured", "")
		return
	}

	res, err := a.mcpClientManager.ReadUserAppResource(c.Request.Context(), userID, toolUse.ServerOrigin, toolUse.UIMeta.ResourceURI)
	if err != nil {
		var oauthErr *mcp.OAuthNeededError
		if errors.As(err, &oauthErr) {
			a.writeAppResourceError(c, http.StatusUnauthorized, appResourceErrAuthRequired, "MCP authentication required", oauthErr.AuthURL())
			return
		}
		var invalidErr *mcp.InvalidAppResourceError
		if errors.As(err, &invalidErr) {
			a.writeAppResourceError(c, http.StatusBadGateway, appResourceErrInvalidResourceMime, invalidErr.Error(), "")
			return
		}
		a.writeAppResourceError(c, http.StatusBadGateway, appResourceErrUpstreamUnreachable, err.Error(), "")
		return
	}

	c.JSON(http.StatusOK, AppResourceResponse{
		ServerOrigin: toolUse.ServerOrigin,
		URI:          res.URI,
		MIMEType:     res.MIMEType,
		HTML:         res.HTML,
		UIMeta:       res.UIMeta,
	})
}
