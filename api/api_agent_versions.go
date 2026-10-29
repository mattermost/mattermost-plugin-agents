// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

const (
	defaultAgentVersionPageSize = 50
	maxAgentVersionPageSize     = 200
)

// parseVersionNumber parses a positive version number. Versions are stored
// as 32-bit INT, so larger values are rejected rather than sent to the database.
func parseVersionNumber(raw string) (int, bool) {
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value < 1 {
		return 0, false
	}
	return int(value), true
}

// parsePositiveIntQuery reads an optional positive integer query parameter,
// returning fallback when it is absent and aborting with 400 when it is invalid.
func parsePositiveIntQuery(c *gin.Context, name string, fallback int) (int, bool) {
	raw, present := c.GetQuery(name)
	if !present {
		return fallback, true
	}
	value, ok := parseVersionNumber(raw)
	if !ok {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid %s %q: must be a positive integer", name, raw))
		return 0, false
	}
	return value, true
}

// parseAgentVersionParam reads the :version path parameter, aborting with
// 400 when it is not a positive integer.
func parseAgentVersionParam(c *gin.Context) (int, bool) {
	raw := c.Param("version")
	version, ok := parseVersionNumber(raw)
	if !ok {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid version %q: must be a positive integer", raw))
		return 0, false
	}
	return version, true
}

// handleListAgentVersions handles GET /agents/:agentid/versions: one page of
// versions, newest first, older than the optional ?before= version and at most
// ?limit= long. Snapshots carry custom instructions, so only agent managers
// may read them.
func (a *API) handleListAgentVersions(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	if _, ok := a.loadManageableAgent(c, agentID, userID, "not authorized to view this agent's history", nil); !ok {
		return
	}
	before, ok := parsePositiveIntQuery(c, "before", 0)
	if !ok {
		return
	}
	limit, ok := parsePositiveIntQuery(c, "limit", defaultAgentVersionPageSize)
	if !ok {
		return
	}

	page, err := a.agentStore.ListAgentVersions(agentID, before, min(limit, maxAgentVersionPageSize))
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to list agent versions: %w", err))
		return
	}
	c.JSON(http.StatusOK, page)
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
	// Leaving attribute-based access deletes the agent's policy, which versions
	// do not store, so only an explicit access edit may do it.
	if cfg.UserAccessLevel == llm.UserAccessLevelAttributeBased && detail.Config.UserAccessLevel != llm.UserAccessLevelAttributeBased {
		abortAgentRequest(c, http.StatusBadRequest, errors.New("this version uses a different user access level; change user access on the Access tab before restoring"))
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

// maxDeleteAgentVersions bounds one delete request; the History tab selects
// from the versions it has loaded, at most a few pages.
const maxDeleteAgentVersions = 1000

// DeleteAgentVersionsRequest is the JSON body for DELETE /agents/:agentid/versions:
// either the versions to delete, or all to delete every version but the current one.
type DeleteAgentVersionsRequest struct {
	Versions []int `json:"versions"`
	All      bool  `json:"all"`
}

type deleteAgentVersionsResponse struct {
	Deleted int64 `json:"deleted"`
}

// handleDeleteAgentVersions handles DELETE /agents/:agentid/versions: permanently
// deletes versions from the agent's history. The current version is the live
// configuration and is never deleted.
func (a *API) handleDeleteAgentVersions(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	audit.AddParam(auditRec(c), audit.KeyAgentID, audit.TruncateID(agentID))

	if _, ok := a.loadManageableAgent(c, agentID, userID, "not authorized to modify this agent", nil); !ok {
		return
	}

	var req DeleteAgentVersionsRequest
	if !bindAgentRequestJSON(c, &req) {
		return
	}
	audit.AddParam(auditRec(c), "all", req.All)

	var deleted int64
	var err error
	switch {
	case req.All && len(req.Versions) > 0:
		abortAgentRequest(c, http.StatusBadRequest, errors.New("send either versions or all, not both"))
		return
	case req.All:
		deleted, err = a.agentStore.DeletePreviousAgentVersions(agentID)
	case len(req.Versions) == 0:
		abortAgentRequest(c, http.StatusBadRequest, errors.New("versions must list at least one version, or set all"))
		return
	case len(req.Versions) > maxDeleteAgentVersions:
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("at most %d versions can be deleted at once", maxDeleteAgentVersions))
		return
	default:
		versions := make([]string, 0, len(req.Versions))
		for _, v := range req.Versions {
			if v < 1 || v > math.MaxInt32 {
				abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid version %d: must be a positive integer", v))
				return
			}
			versions = append(versions, strconv.Itoa(v))
		}
		audit.AddParam(auditRec(c), "versions", versions)
		deleted, err = a.agentStore.DeleteAgentVersions(agentID, req.Versions)
	}
	if errors.Is(err, store.ErrCurrentAgentVersion) {
		abortAgentRequest(c, http.StatusBadRequest, err)
		return
	}
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to delete agent versions: %w", err))
		return
	}
	audit.AddParam(auditRec(c), "deleted", deleted)
	c.JSON(http.StatusOK, deleteAgentVersionsResponse{Deleted: deleted})
}
