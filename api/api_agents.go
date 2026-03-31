// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/useragents"
	"github.com/mattermost/mattermost/server/public/model"
)

// --- Request/Response types ---

// CreateAgentRequest is the JSON body for POST /agents.
type CreateAgentRequest struct {
	DisplayName        string                  `json:"display_name" binding:"required"`
	Username           string                  `json:"username" binding:"required"`
	ServiceID          string                  `json:"service_id" binding:"required"`
	CustomInstructions string                  `json:"custom_instructions"`
	ChannelAccessLevel int                     `json:"channel_access_level"`
	ChannelIDs         []string                `json:"channel_ids"`
	UserAccessLevel    int                     `json:"user_access_level"`
	UserIDs            []string                `json:"user_ids"`
	TeamIDs            []string                `json:"team_ids"`
	AdminUserIDs       []string                `json:"admin_user_ids"`
	EnabledTools       []useragents.EnabledTool `json:"enabled_tools"`
}

// UpdateAgentRequest is the JSON body for PUT /agents/:agentid.
// All fields are optional — only provided fields are applied via read-modify-write.
type UpdateAgentRequest struct {
	DisplayName        *string                  `json:"display_name"`
	Username           *string                  `json:"username"`
	ServiceID          *string                  `json:"service_id"`
	CustomInstructions *string                  `json:"custom_instructions"`
	ChannelAccessLevel *int                     `json:"channel_access_level"`
	ChannelIDs         *[]string                `json:"channel_ids"`
	UserAccessLevel    *int                     `json:"user_access_level"`
	UserIDs            *[]string                `json:"user_ids"`
	TeamIDs            *[]string                `json:"team_ids"`
	AdminUserIDs       *[]string                `json:"admin_user_ids"`
	EnabledTools       *[]useragents.EnabledTool `json:"enabled_tools"`
}

// ServiceInfo is a safe-to-expose subset of llm.ServiceConfig (no API keys or secrets).
type ServiceInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	DefaultModel string `json:"default_model"`
}

// --- Middleware ---

// agentLicenseRequired is a gin middleware that gates agent endpoints behind an E20+ license.
func (a *API) agentLicenseRequired(c *gin.Context) {
	if !a.licenseChecker.IsMultiLLMLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("self-service agents require an E20 or Enterprise license"))
		return
	}
}

// --- Permission helpers ---

// PermissionCreateAgent is defined here because the core MM model package may not
// yet export this constant. Once mattermost/mattermost merges the permission definition,
// replace this with model.PermissionCreateAgent.
var PermissionCreateAgent = &model.Permission{Id: "create_agent", Name: "", Description: "", Scope: ""}

// isAgentAdmin returns true if userID is the creator or an explicit admin of the agent.
func isAgentAdmin(agent *useragents.UserAgent, userID string) bool {
	return agent.CreatorID == userID || slices.Contains(agent.AdminUserIDs, userID)
}

// --- Handlers ---

// handleCreateAgent creates a new user agent with its backing bot account.
// POST /agents
func (a *API) handleCreateAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	// Permission check: user must have create_agent permission
	if !a.pluginAPI.User.HasPermissionTo(userID, PermissionCreateAgent) {
		c.AbortWithError(http.StatusForbidden, errors.New("user does not have permission to create agents"))
		return
	}

	var req CreateAgentRequest
	if err := c.BindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Validate that the referenced service exists in the config
	cfg, err := a.configStore.GetConfig()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to read config: %w", err))
		return
	}
	if cfg != nil {
		found := false
		for _, svc := range cfg.Services {
			if svc.ID == req.ServiceID {
				found = true
				break
			}
		}
		if !found {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("service %q not found in configuration", req.ServiceID))
			return
		}
	}

	// Create the backing Mattermost bot account.
	mmBot := &model.Bot{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Description: "User-created AI agent",
	}
	if err := a.pluginAPI.Bot.Create(mmBot); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to create bot account: %w", err))
		return
	}

	// Build the UserAgent record
	agent := &useragents.UserAgent{
		BotUserID:          mmBot.UserId,
		CreatorID:          userID,
		DisplayName:        req.DisplayName,
		Username:           req.Username,
		ServiceID:          req.ServiceID,
		CustomInstructions: req.CustomInstructions,
		ChannelAccessLevel: req.ChannelAccessLevel,
		ChannelIDs:         req.ChannelIDs,
		UserAccessLevel:    req.UserAccessLevel,
		UserIDs:            req.UserIDs,
		TeamIDs:            req.TeamIDs,
		AdminUserIDs:       req.AdminUserIDs,
		EnabledTools:       req.EnabledTools,
	}

	if err := a.agentStore.CreateAgent(agent); err != nil {
		// Best effort: deactivate the bot we just created since the DB insert failed
		_, _ = a.pluginAPI.Bot.UpdateActive(mmBot.UserId, false)
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to persist agent: %w", err))
		return
	}

	c.JSON(http.StatusCreated, agent)
}

// handleListAgents returns all agents the requesting user has access to.
// GET /agents
func (a *API) handleListAgents(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	agents, err := a.agentStore.ListAgents()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to list agents: %w", err))
		return
	}

	accessible := make([]*useragents.UserAgent, 0, len(agents))
	for _, agent := range agents {
		if canUserAccessAgent(agent, userID) {
			accessible = append(accessible, agent)
		}
	}

	c.JSON(http.StatusOK, accessible)
}

// handleGetAgent returns a single agent by ID.
// GET /agents/:agentid
func (a *API) handleGetAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	agent, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return
	}
	if agent == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	if !canUserAccessAgent(agent, userID) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, agent)
}

// handleUpdateAgent updates a user agent's mutable fields.
// PUT /agents/:agentid
func (a *API) handleUpdateAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	agent, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return
	}
	if agent == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	// Only creator or explicit admin can modify
	if !isAgentAdmin(agent, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("not authorized to modify this agent"))
		return
	}

	var req UpdateAgentRequest
	if err := c.BindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Apply partial update (read-modify-write)
	displayNameChanged := false
	if req.DisplayName != nil {
		displayNameChanged = agent.DisplayName != *req.DisplayName
		agent.DisplayName = *req.DisplayName
	}
	if req.Username != nil {
		agent.Username = *req.Username
	}
	if req.ServiceID != nil {
		agent.ServiceID = *req.ServiceID
	}
	if req.CustomInstructions != nil {
		agent.CustomInstructions = *req.CustomInstructions
	}
	if req.ChannelAccessLevel != nil {
		agent.ChannelAccessLevel = *req.ChannelAccessLevel
	}
	if req.ChannelIDs != nil {
		agent.ChannelIDs = *req.ChannelIDs
	}
	if req.UserAccessLevel != nil {
		agent.UserAccessLevel = *req.UserAccessLevel
	}
	if req.UserIDs != nil {
		agent.UserIDs = *req.UserIDs
	}
	if req.TeamIDs != nil {
		agent.TeamIDs = *req.TeamIDs
	}
	if req.AdminUserIDs != nil {
		agent.AdminUserIDs = *req.AdminUserIDs
	}
	if req.EnabledTools != nil {
		agent.EnabledTools = *req.EnabledTools
	}

	if err := a.agentStore.UpdateAgent(agent); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to update agent: %w", err))
		return
	}

	// Sync display name change to the underlying Mattermost bot account
	if displayNameChanged {
		if _, err := a.pluginAPI.Bot.Patch(agent.BotUserID, &model.BotPatch{
			DisplayName: &agent.DisplayName,
		}); err != nil {
			// Non-fatal: the DB is already updated, log and continue
			c.Error(fmt.Errorf("failed to patch bot display name: %w", err))
		}
	}

	c.JSON(http.StatusOK, agent)
}

// handleDeleteAgent soft-deletes an agent and deactivates its bot account.
// DELETE /agents/:agentid
func (a *API) handleDeleteAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	agent, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return
	}
	if agent == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	// Only creator or explicit admin can delete
	if !isAgentAdmin(agent, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("not authorized to delete this agent"))
		return
	}

	if err := a.agentStore.DeleteAgent(agentID); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to delete agent: %w", err))
		return
	}

	// Deactivate the backing bot account
	if _, err := a.pluginAPI.Bot.UpdateActive(agent.BotUserID, false); err != nil {
		// Non-fatal: the DB record is already soft-deleted
		c.Error(fmt.Errorf("failed to deactivate bot: %w", err))
	}

	c.Status(http.StatusOK)
}

// handleUploadAgentAvatar sets a custom profile image on the agent's bot account.
// POST /agents/:agentid/avatar
func (a *API) handleUploadAgentAvatar(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	agent, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return
	}
	if agent == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	if !isAgentAdmin(agent, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("not authorized to modify this agent"))
		return
	}

	file, _, err := c.Request.FormFile("image")
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("missing or invalid image file: %w", err))
		return
	}
	defer file.Close()

	imageBytes, err := io.ReadAll(file)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to read image: %w", err))
		return
	}

	if err := a.pluginAPI.User.SetProfileImage(agent.BotUserID, bytes.NewReader(imageBytes)); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to set profile image: %w", err))
		return
	}

	c.Status(http.StatusOK)
}

// handleListServices returns a list of configured AI services without exposing secrets.
// GET /services
func (a *API) handleListServices(c *gin.Context) {
	cfg, err := a.configStore.GetConfig()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to read config: %w", err))
		return
	}

	if cfg == nil {
		c.JSON(http.StatusOK, []ServiceInfo{})
		return
	}

	services := make([]ServiceInfo, 0, len(cfg.Services))
	for _, svc := range cfg.Services {
		services = append(services, ServiceInfo{
			ID:           svc.ID,
			Name:         svc.Name,
			Type:         svc.Type,
			DefaultModel: svc.DefaultModel,
		})
	}

	c.JSON(http.StatusOK, services)
}

// --- Access control helper ---

// canUserAccessAgent checks whether the given user can see/use the agent,
// based on the agent's UserAccessLevel and UserIDs/TeamIDs.
func canUserAccessAgent(agent *useragents.UserAgent, userID string) bool {
	// Creators and admins always have access
	if isAgentAdmin(agent, userID) {
		return true
	}

	switch agent.UserAccessLevel {
	case 0: // UserAccessLevelAll
		return true
	case 1: // UserAccessLevelAllow
		return slices.Contains(agent.UserIDs, userID)
	case 2: // UserAccessLevelBlock
		return !slices.Contains(agent.UserIDs, userID)
	case 3: // UserAccessLevelNone
		return false
	default:
		return false
	}
}
