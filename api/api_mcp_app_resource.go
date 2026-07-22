// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
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

// AppResourceContents is one contents[] entry in the MCP ReadResourceResult
// wire shape returned by GET /mcp/app-resource on success.
type AppResourceContents struct {
	URI      string           `json:"uri"`
	MIMEType string           `json:"mimeType"`
	Text     string           `json:"text"`
	Meta     *AppResourceMeta `json:"_meta,omitempty"`
}

// AppResourceMeta is the MCP `_meta` object on a resource contents item.
type AppResourceMeta struct {
	UI *mcp.AppResourceUIMeta `json:"ui,omitempty"`
}

// AppResourceResponse is the success JSON shape for GET /mcp/app-resource.
// It mirrors MCP ReadResourceResult so Phase 1c's onReadResource callback can
// return response.json() directly to @mcp-ui/client.
type AppResourceResponse struct {
	Contents []AppResourceContents `json:"contents"`
}

// AppResourceErrorResponse is the JSON error shape for GET /mcp/app-resource.
// ErrorCode drives webapp behavior: mcp_auth_required renders "Connect to
// view" with AuthURL; forbidden renders the no-access popover.
//
// D5 state 3 ("can never gain access") is approximated client-side: after a
// shared-result onlooker receives repeated mcp_auth_required responses and
// OAuth fails/denies, the webapp renders the state-3 popover. The server has
// no distinct definitive-rejection code beyond that flow.
type AppResourceErrorResponse struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	AuthURL   string `json:"auth_url,omitempty"`
}

func writeAppResourceError(c *gin.Context, status int, code, message, authURL string) {
	c.JSON(status, AppResourceErrorResponse{
		ErrorCode: code,
		Message:   message,
		AuthURL:   authURL,
	})
}

func (a *API) handleGetMCPAppResource(c *gin.Context) {
	// Explicit non-checks: no license gate (viewing an already-executed tool's
	// UI is a read, consistent with un-gated GET /conversations/:id; execution
	// was already license-gated at handleToolCall), no EnableChannelMentionToolCalling
	// gate (same reason), no tool-status gate (D3's Success/AutoApproved gating
	// is a render-time webapp concern; the resource template itself is not
	// result data).
	//
	// D6(b) uses the tool_use block's Shared flag — the same flag
	// FilterForNonRequester uses when exposing ui_meta — so the invariant is
	// exact: if GET /conversations/:id shows ui_meta to the caller, this
	// fetch will not 403 for the share check.
	userID := c.GetHeader("Mattermost-User-Id")
	postID := c.Query("post_id")
	toolCallID := c.Query("tool_call_id")
	if postID == "" || toolCallID == "" || !model.IsValidId(postID) {
		writeAppResourceError(c, http.StatusBadRequest, appResourceErrInvalidRequest, "post_id and tool_call_id are required", "")
		return
	}

	post, err := a.pluginAPI.Post.GetPost(postID)
	if err != nil {
		writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "post not found", "")
		return
	}

	if !a.pluginAPI.User.HasPermissionToChannel(userID, post.ChannelId, model.PermissionReadChannel) {
		writeAppResourceError(c, http.StatusForbidden, appResourceErrForbidden, "permission denied", "")
		return
	}

	turn, err := a.conversationStore.GetTurnByPostID(postID)
	if err != nil || turn == nil {
		writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "turn not found", "")
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

	toolUse, _, findErr := conversation.FindToolCallBlocks(turns, postID, toolCallID)
	if findErr != nil {
		if errors.Is(findErr, conversation.ErrAmbiguousToolCallID) {
			writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "ambiguous tool call", "")
			return
		}
		writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "tool call not found", "")
		return
	}
	if toolUse.UIMeta == nil || toolUse.UIMeta.ResourceURI == "" {
		writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "tool call has no app resource", "")
		return
	}

	if userID != requesterID {
		if toolUse.Shared == nil || !*toolUse.Shared {
			writeAppResourceError(c, http.StatusForbidden, appResourceErrForbidden, "tool result is not shared", "")
			return
		}
	}

	res, err := a.mcpClientManager.ReadUserAppResource(c.Request.Context(), userID, toolUse.ServerOrigin, toolUse.UIMeta.ResourceURI)
	if err != nil {
		var oauthErr *mcp.OAuthNeededError
		if errors.As(err, &oauthErr) {
			writeAppResourceError(c, http.StatusUnauthorized, appResourceErrAuthRequired, "MCP authentication required", oauthErr.AuthURL())
			return
		}
		if errors.Is(err, mcp.ErrServerNotConfigured) {
			writeAppResourceError(c, http.StatusNotFound, appResourceErrNotFound, "server no longer configured", "")
			return
		}
		var invalidErr *mcp.InvalidAppResourceError
		if errors.As(err, &invalidErr) {
			a.pluginAPI.Log.Error("Invalid MCP app resource",
				"userID", userID, "serverOrigin", toolUse.ServerOrigin, "uri", toolUse.UIMeta.ResourceURI, "error", invalidErr)
			writeAppResourceError(c, http.StatusBadGateway, appResourceErrInvalidResourceMime, "invalid app resource", "")
			return
		}
		a.pluginAPI.Log.Error("MCP app resource upstream failure",
			"userID", userID, "serverOrigin", toolUse.ServerOrigin, "uri", toolUse.UIMeta.ResourceURI, "error", err)
		writeAppResourceError(c, http.StatusBadGateway, appResourceErrUpstreamUnreachable, "MCP server unreachable", "")
		return
	}

	var meta *AppResourceMeta
	if res.UIMeta != nil {
		meta = &AppResourceMeta{UI: res.UIMeta}
	}
	c.JSON(http.StatusOK, AppResourceResponse{
		Contents: []AppResourceContents{{
			URI:      res.URI,
			MIMEType: res.MIMEType,
			Text:     res.HTML,
			Meta:     meta,
		}},
	})
}
