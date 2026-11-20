// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleGetAgentScopedAccess reports what an agent's scoped channel runs can
// access. It is read-only and system-admin only: it lists teams and
// channels of any visibility, and the catalog it builds connects to MCP
// servers with the agent's service account.
func (a *API) handleGetAgentScopedAccess(c *gin.Context) {
	agentID := c.Param("agentid")
	cfg, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return
	}
	if cfg == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if cfg.BotUserID == "" {
		abortAgentRequest(c, http.StatusConflict, errors.New("agent has no bot user"))
		return
	}
	bot := a.bots.GetBotByID(cfg.BotUserID)
	if bot == nil {
		abortAgentRequest(c, http.StatusConflict, errors.New("agent is not active"))
		return
	}

	report, err := a.contextBuilder.ScopedAccessReport(c.Request.Context(), bot, c.GetHeader("Mattermost-User-Id"))
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to build scoped access report: %w", err))
		return
	}
	c.JSON(http.StatusOK, report)
}
