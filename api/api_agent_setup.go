// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/agentsetup"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// AgentSetup starts agent auto-setup conversations (agentsetup.Service).
type AgentSetup interface {
	// Available reports whether actorID could set up agent now.
	Available(ctx context.Context, actorID string, agent *llm.BotConfig) bool
	Start(ctx context.Context, actor *model.User, agentID string) (*conversations.AgentThread, error)
}

// SetAgentSetup installs the auto-setup service. Without it the setup route
// answers 404 and no agent is listed as available to set up.
func (a *API) SetAgentSetup(setup AgentSetup) {
	a.agentSetup = setup
}

// agentSetupResponse is the JSON body of POST /agents/:agentid/setup: the
// setup thread, in the caller's DM with the agent.
type agentSetupResponse struct {
	ChannelID  string `json:"channelId"`
	RootPostID string `json:"rootPostId"`
	BotUserID  string `json:"botUserId"`
}

// handleStartAgentSetup handles POST /agents/:agentid/setup: it opens a setup
// thread in the caller's DM with the agent, where the agent interviews them
// and configures itself with the setup tools.
func (a *API) handleStartAgentSetup(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	// Identify the target early so 404/403 fail records carry it.
	audit.AddParam(auditRec(c), audit.KeyAgentID, audit.TruncateID(agentID))

	if a.agentSetup == nil {
		abortAgentRequest(c, http.StatusNotFound, errors.New("agent auto-setup is not available"))
		return
	}

	user, err := a.pluginAPI.User.Get(userID)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to get user: %w", err))
		return
	}

	thread, err := a.agentSetup.Start(c.Request.Context(), user, agentID)
	switch {
	case errors.Is(err, agentsetup.ErrDisabled):
		abortAgentRequest(c, http.StatusForbidden, err)
		return
	case errors.Is(err, agentsetup.ErrAgentNotRunning):
		abortAgentRequest(c, http.StatusConflict, err)
		return
	case errors.Is(err, bots.ErrUsageRestriction):
		abortAgentRequest(c, http.StatusForbidden, errors.New("you do not have access to this agent"))
		return
	case err != nil:
		abortAgentConfigError(c, err)
		return
	}

	root := thread.RootPost
	audit.AddParam(auditRec(c), audit.KeyChannelID, root.ChannelId)
	audit.AddParam(auditRec(c), audit.KeyThreadRootPostID, root.Id)
	c.JSON(http.StatusCreated, agentSetupResponse{
		ChannelID:  root.ChannelId,
		RootPostID: root.Id,
		BotUserID:  thread.BotUserID,
	})
}
