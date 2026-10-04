// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

// agentVersionListResponse is the body of GET /agents/:agentid/versions.
type agentVersionListResponse struct {
	CurrentVersion int                  `json:"currentVersion"`
	Versions       []store.AgentVersion `json:"versions"`
}

// parseAgentVersionParam reads the :version path parameter, aborting with
// 400 when it is not a positive integer.
func parseAgentVersionParam(c *gin.Context) (int, bool) {
	raw := c.Param("version")
	version, err := strconv.Atoi(raw)
	if err != nil || version < 1 {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid version %q: must be a positive integer", raw))
		return 0, false
	}
	return version, true
}

// handleListAgentVersions handles GET /agents/:agentid/versions (newest first).
// Snapshots carry custom instructions, so only agent managers may read them.
func (a *API) handleListAgentVersions(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	if _, ok := a.loadManageableAgent(c, agentID, userID, "not authorized to view this agent's history", nil); !ok {
		return
	}

	versions, err := a.agentStore.ListAgentVersions(agentID)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to list agent versions: %w", err))
		return
	}

	resp := agentVersionListResponse{Versions: versions}
	if len(versions) > 0 {
		resp.CurrentVersion = versions[0].Version
	}
	c.JSON(http.StatusOK, resp)
}

// handleGetAgentVersion handles GET /agents/:agentid/versions/:version.
func (a *API) handleGetAgentVersion(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	if _, ok := a.loadManageableAgent(c, agentID, userID, "not authorized to view this agent's history", nil); !ok {
		return
	}
	version, ok := parseAgentVersionParam(c)
	if !ok {
		return
	}

	detail, err := a.agentStore.GetAgentVersion(agentID, version)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to get agent version: %w", err))
		return
	}
	if detail == nil {
		abortAgentRequest(c, http.StatusNotFound, fmt.Errorf("version %d of this agent does not exist", version))
		return
	}
	c.JSON(http.StatusOK, detail)
}

// handleRestoreAgentVersion handles POST /agents/:agentid/versions/:version/restore:
// the version's configuration is saved through the same path as PUT
// /agents/:agentid and becomes a new latest version. The username never changes.
func (a *API) handleRestoreAgentVersion(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	// Identify the target early so 404/403 fail records carry it.
	audit.AddParam(auditRec(c), audit.KeyAgentID, audit.TruncateID(agentID))

	cfg, ok := a.loadManageableAgent(c, agentID, userID, "not authorized to modify this agent", nil)
	if !ok {
		return
	}
	version, ok := parseAgentVersionParam(c)
	if !ok {
		return
	}
	audit.AddParam(auditRec(c), "version", version)

	detail, err := a.agentStore.GetAgentVersion(agentID, version)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to get agent version: %w", err))
		return
	}
	if detail == nil {
		abortAgentRequest(c, http.StatusNotFound, fmt.Errorf("version %d of this agent does not exist", version))
		return
	}

	pluginCfg, ok := a.loadPluginConfigForAgents(c)
	if !ok {
		return
	}
	if !serviceIDExistsInConfig(pluginCfg, detail.Config.ServiceID) {
		abortAgentRequest(c, http.StatusBadRequest, errors.New("this version uses an AI service that no longer exists; restore a different version or edit the agent to choose a service"))
		return
	}

	req := UpdateAgentRequest{AgentRequestFields: requestFieldsFromConfig(&detail.Config)}
	updated, ok := a.updateAgent(c, userID, cfg, req, store.AgentVersionMeta{
		ActorID:             userID,
		Source:              store.AgentVersionSourceRestore,
		RestoredFromVersion: version,
	})
	if !ok {
		return
	}
	c.JSON(http.StatusOK, updated)
}
