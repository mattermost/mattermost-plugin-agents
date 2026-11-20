// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/projects"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// Projects runs project channels (projects.Service).
type Projects interface {
	CreateProject(ctx context.Context, actorID string, req projects.CreateRequest) (*store.Project, error)
	GetProject(ctx context.Context, viewerID, channelID string) (*store.Project, error)
	ListProjects(ctx context.Context, viewerID, teamID string) ([]*store.Project, error)
	EndProject(ctx context.Context, actorID, channelID string, skipWrapUp bool) (*store.Project, error)
	TransferLead(ctx context.Context, actorID, channelID, leadUserID string) (*store.Project, error)
	UpdateSchedule(ctx context.Context, actorID, channelID, cron, timezone string) (*store.Project, error)
	GetChecklistSummary(ctx context.Context, viewerID, channelID string) (*projects.ChecklistSummary, error)
	ProjectResponse(project *store.Project) projects.Response
	Available() bool
	// AgentRetired reports whether agent no longer leads a live project:
	// its project ended, or never finished being created.
	AgentRetired(agent *llm.BotConfig) (bool, error)
}

// SetProjects installs the projects service. Without it every project route
// answers 404.
func (a *API) SetProjects(p Projects) {
	a.projects = p
}

func (a *API) projectsAvailable(c *gin.Context) bool {
	if a.projects == nil || !a.projects.Available() {
		abortAgentRequest(c, http.StatusNotFound, projects.ErrDisabled)
		return false
	}
	return true
}

func abortProjectError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, projects.ErrDisabled), errors.Is(err, store.ErrProjectNotFound):
		abortAgentRequest(c, http.StatusNotFound, err)
	case errors.Is(err, projects.ErrForbidden):
		abortAgentRequest(c, http.StatusForbidden, err)
	case errors.Is(err, projects.ErrInvalidRequest):
		abortAgentRequest(c, http.StatusBadRequest, err)
	case errors.Is(err, projects.ErrNotActive), errors.Is(err, store.ErrProjectExists), errors.Is(err, store.ErrProjectStateChanged):
		abortAgentRequest(c, http.StatusConflict, err)
	default:
		abortAgentRequest(c, http.StatusInternalServerError, err)
	}
}

type createProjectRequest struct {
	TeamID      string `json:"team_id"`
	ChannelID   string `json:"channel_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Purpose     string `json:"purpose"`
	Header      string `json:"header"`
	Private     bool   `json:"private"`
}

// handleCreateProject handles POST /projects.
func (a *API) handleCreateProject(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	var req createProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortAgentRequest(c, http.StatusBadRequest, projects.ErrInvalidRequest)
		return
	}
	audit.AddParam(auditRec(c), "team_id", audit.TruncateID(req.TeamID))
	if req.ChannelID != "" {
		audit.AddParam(auditRec(c), audit.KeyChannelID, audit.TruncateID(req.ChannelID))
	}
	if !a.projectsAvailable(c) {
		return
	}
	project, err := a.projects.CreateProject(context.WithoutCancel(c.Request.Context()), userID, projects.CreateRequest{
		TeamID:      req.TeamID,
		ChannelID:   req.ChannelID,
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Purpose:     req.Purpose,
		Header:      req.Header,
		Private:     req.Private,
		CreatedFrom: store.ProjectCreatedFromModal,
	})
	if err != nil {
		abortProjectError(c, err)
		return
	}
	audit.AddParam(auditRec(c), audit.KeyChannelID, project.ChannelID)
	audit.AddParam(auditRec(c), audit.KeyAgentID, project.AgentID)
	c.JSON(http.StatusCreated, a.projects.ProjectResponse(project))
}

// handleListProjects handles GET /projects?team_id=.
func (a *API) handleListProjects(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	teamID := c.Query("team_id")
	if !model.IsValidId(teamID) {
		abortAgentRequest(c, http.StatusBadRequest, errors.New("team_id is required"))
		return
	}
	if !a.projectsAvailable(c) {
		return
	}
	list, err := a.projects.ListProjects(c.Request.Context(), userID, teamID)
	if err != nil {
		abortProjectError(c, err)
		return
	}
	out := make([]projects.Response, 0, len(list))
	for _, p := range list {
		out = append(out, a.projects.ProjectResponse(p))
	}
	c.JSON(http.StatusOK, gin.H{"projects": out})
}

// handleGetProject handles GET /projects/:channelid.
func (a *API) handleGetProject(c *gin.Context) {
	if !a.projectsAvailable(c) {
		return
	}
	project, err := a.projects.GetProject(c.Request.Context(), c.GetHeader("Mattermost-User-Id"), c.Param("channelid"))
	if err != nil {
		abortProjectError(c, err)
		return
	}
	c.JSON(http.StatusOK, a.projects.ProjectResponse(project))
}

type endProjectRequest struct {
	SkipWrapUp bool `json:"skip_wrap_up"`
}

// handleEndProject handles POST /projects/:channelid/end.
func (a *API) handleEndProject(c *gin.Context) {
	channelID := c.Param("channelid")
	audit.AddParam(auditRec(c), audit.KeyChannelID, audit.TruncateID(channelID))
	var req endProjectRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			abortAgentRequest(c, http.StatusBadRequest, projects.ErrInvalidRequest)
			return
		}
	}
	if !a.projectsAvailable(c) {
		return
	}
	project, err := a.projects.EndProject(context.WithoutCancel(c.Request.Context()), c.GetHeader("Mattermost-User-Id"), channelID, req.SkipWrapUp)
	if err != nil {
		abortProjectError(c, err)
		return
	}
	audit.AddParam(auditRec(c), audit.KeyAgentID, project.AgentID)
	c.JSON(http.StatusOK, a.projects.ProjectResponse(project))
}

type transferLeadRequest struct {
	LeadUserID string `json:"lead_user_id"`
}

// handleTransferProjectLead handles POST /projects/:channelid/transfer-lead.
func (a *API) handleTransferProjectLead(c *gin.Context) {
	channelID := c.Param("channelid")
	audit.AddParam(auditRec(c), audit.KeyChannelID, audit.TruncateID(channelID))
	var req transferLeadRequest
	if err := c.ShouldBindJSON(&req); err != nil || !model.IsValidId(req.LeadUserID) {
		abortAgentRequest(c, http.StatusBadRequest, projects.ErrInvalidRequest)
		return
	}
	audit.AddParam(auditRec(c), audit.KeyUserID, req.LeadUserID)
	if !a.projectsAvailable(c) {
		return
	}
	project, err := a.projects.TransferLead(c.Request.Context(), c.GetHeader("Mattermost-User-Id"), channelID, req.LeadUserID)
	if err != nil {
		abortProjectError(c, err)
		return
	}
	c.JSON(http.StatusOK, a.projects.ProjectResponse(project))
}

type projectScheduleRequest struct {
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
}

// handleUpdateProjectSchedule handles PUT /projects/:channelid/schedule.
func (a *API) handleUpdateProjectSchedule(c *gin.Context) {
	channelID := c.Param("channelid")
	audit.AddParam(auditRec(c), audit.KeyChannelID, audit.TruncateID(channelID))
	var req projectScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortAgentRequest(c, http.StatusBadRequest, projects.ErrInvalidRequest)
		return
	}
	if !a.projectsAvailable(c) {
		return
	}
	project, err := a.projects.UpdateSchedule(c.Request.Context(), c.GetHeader("Mattermost-User-Id"), channelID, req.Cron, req.Timezone)
	if err != nil {
		abortProjectError(c, err)
		return
	}
	c.JSON(http.StatusOK, a.projects.ProjectResponse(project))
}

// handleGetProjectChecklist handles GET /projects/:channelid/checklist.
func (a *API) handleGetProjectChecklist(c *gin.Context) {
	if !a.projectsAvailable(c) {
		return
	}
	summary, err := a.projects.GetChecklistSummary(c.Request.Context(), c.GetHeader("Mattermost-User-Id"), c.Param("channelid"))
	if err != nil {
		abortProjectError(c, err)
		return
	}
	c.JSON(http.StatusOK, summary)
}
