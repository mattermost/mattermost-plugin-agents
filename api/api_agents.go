// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var validUsernameRe = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

// WebsocketEventBotsInvalidate is the short name passed to PublishWebSocketEvent; the webapp receives it as custom_mattermost-ai_<name>.
const WebsocketEventBotsInvalidate = "bots_invalidate"

// CreateAgentRequest is the JSON body for POST /agents.
// The UI is the sole source of truth for create-time defaults; the backend no longer
// substitutes hidden defaults for omitted fields. All fields below are taken verbatim
// from the request payload.
//
// `enabledMCPTools` is a required tri-state field:
//   - `null` means "allow all MCP tools"
//   - `[]`   means "allow no MCP tools"
//   - `[..]` means "allow only the listed tools"
//
// Omitting `enabledMCPTools` from the JSON is rejected as an invalid request.
type CreateAgentRequest struct {
	DisplayName             string               `json:"displayName" binding:"required"`
	Username                string               `json:"username" binding:"required"`
	ServiceID               string               `json:"serviceID" binding:"required"`
	CustomInstructions      string               `json:"customInstructions"`
	ChannelAccessLevel      int                  `json:"channelAccessLevel"`
	ChannelIDs              []string             `json:"channelIDs"`
	UserAccessLevel         int                  `json:"userAccessLevel"`
	UserIDs                 []string             `json:"userIDs"`
	TeamIDs                 []string             `json:"teamIDs"`
	AdminUserIDs            []string             `json:"adminUserIDs"`
	EnabledMCPTools         []llm.EnabledMCPTool `json:"enabledMCPTools"`
	Model                   string               `json:"model"`
	EnableVision            bool                 `json:"enableVision"`
	DisableTools            bool                 `json:"disableTools"`
	EnabledNativeTools      []string             `json:"enabledNativeTools"`
	ReasoningEnabled        bool                 `json:"reasoningEnabled"`
	ReasoningEffort         string               `json:"reasoningEffort"`
	ThinkingBudget          int                  `json:"thinkingBudget"`
	StructuredOutputEnabled bool                 `json:"structuredOutputEnabled"`

	enabledMCPToolsProvided bool
}

func (r *CreateAgentRequest) UnmarshalJSON(data []byte) error {
	type alias CreateAgentRequest
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	_, enabledMCPToolsProvided := raw["enabledMCPTools"]
	*r = CreateAgentRequest(decoded)
	r.enabledMCPToolsProvided = enabledMCPToolsProvided
	return nil
}

// UpdateAgentRequest is the JSON body for PUT /agents/:agentid.
// This endpoint is a full-object replacement, not a patch: every field the caller wants
// to keep must be sent on every save. Field names mirror CreateAgentRequest so clients
// can send the same document shape for both flows.
//
// `enabledMCPTools` is a required tri-state field with the same semantics as
// CreateAgentRequest: `null` = all, `[]` = none, `[..]` = allowlist. Omitting it is
// rejected as an invalid request.
//
// `username` is still validated against the stored agent's name and cannot be changed
// after creation; see handleUpdateAgent.
type UpdateAgentRequest struct {
	DisplayName             string               `json:"displayName" binding:"required"`
	Username                string               `json:"username"`
	ServiceID               string               `json:"serviceID" binding:"required"`
	CustomInstructions      string               `json:"customInstructions"`
	ChannelAccessLevel      int                  `json:"channelAccessLevel"`
	ChannelIDs              []string             `json:"channelIDs"`
	UserAccessLevel         int                  `json:"userAccessLevel"`
	UserIDs                 []string             `json:"userIDs"`
	TeamIDs                 []string             `json:"teamIDs"`
	AdminUserIDs            []string             `json:"adminUserIDs"`
	EnabledMCPTools         []llm.EnabledMCPTool `json:"enabledMCPTools"`
	Model                   string               `json:"model"`
	EnableVision            bool                 `json:"enableVision"`
	DisableTools            bool                 `json:"disableTools"`
	EnabledNativeTools      []string             `json:"enabledNativeTools"`
	ReasoningEnabled        bool                 `json:"reasoningEnabled"`
	ReasoningEffort         string               `json:"reasoningEffort"`
	ThinkingBudget          int                  `json:"thinkingBudget"`
	StructuredOutputEnabled bool                 `json:"structuredOutputEnabled"`

	usernameProvided        bool
	enabledMCPToolsProvided bool
}

func (r *UpdateAgentRequest) UnmarshalJSON(data []byte) error {
	type alias UpdateAgentRequest
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	_, usernameProvided := raw["username"]
	_, enabledMCPToolsProvided := raw["enabledMCPTools"]
	*r = UpdateAgentRequest(decoded)
	r.usernameProvided = usernameProvided
	r.enabledMCPToolsProvided = enabledMCPToolsProvided
	return nil
}

// ServiceInfo is a safe-to-expose subset of llm.ServiceConfig (no API keys or secrets).
type ServiceInfo struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	DefaultModel     string `json:"defaultModel"`
	OutputTokenLimit int    `json:"outputTokenLimit"`
	UseResponsesAPI  bool   `json:"useResponsesAPI"`
}

func serviceUsesResponsesAPIForUI(service llm.ServiceConfig) bool {
	return service.Type == llm.ServiceTypeOpenAI || service.UseResponsesAPI
}

// agentLicenseRequired is a gin middleware that gates agent endpoints behind an E20+ license.
func (a *API) agentLicenseRequired(c *gin.Context) {
	if !a.licenseChecker.IsMultiLLMLicensed() {
		c.AbortWithError(http.StatusForbidden, errors.New("self-service agents require an E20 or Enterprise license"))
		return
	}
}

// canManageAgent returns true if the user may update or delete the agent.
// Holders of PermissionManageOthersAgent may manage any agent (including others' agents
// and migrated legacy bots with no owner).
// Migrated legacy bots have no CreatorID; system admins (PermissionManageSystem) retain the
// same operational access they had via System Console before self-service agents.
func canManageAgent(client *pluginapi.Client, cfg *llm.BotConfig, userID string) bool {
	if cfg == nil {
		return false
	}
	if cfg.IsAdmin(userID) {
		return true
	}
	if client.User.HasPermissionTo(userID, model.PermissionManageOthersAgent) {
		return true
	}
	if cfg.CreatorID == "" && client.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		return true
	}
	return false
}

// canCreateAgent returns true if the user may create new agents via POST /agents.
func canCreateAgent(client *pluginapi.Client, userID string) bool {
	if client.User.HasPermissionTo(userID, model.PermissionManageOwnAgent) {
		return true
	}
	return client.User.HasPermissionTo(userID, model.PermissionManageSystem)
}

// canConfigureAgentServices returns true if the user may list services or fetch models
// for agent configuration (ManageOwn or ManageOthers, same union as non-owner admin access).
func canConfigureAgentServices(client *pluginapi.Client, userID string) bool {
	if client.User.HasPermissionTo(userID, model.PermissionManageOwnAgent) {
		return true
	}
	if client.User.HasPermissionTo(userID, model.PermissionManageOthersAgent) {
		return true
	}
	return client.User.HasPermissionTo(userID, model.PermissionManageSystem)
}

// loadPluginConfigForAgents loads the plugin configuration for agent create/update handlers.
// On failure it aborts the request with the same status codes as the previous inline logic.
func (a *API) loadPluginConfigForAgents(c *gin.Context) (*config.Config, bool) {
	cfg, err := a.configStore.GetConfig()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to read config: %w", err))
		return nil, false
	}
	if cfg == nil {
		c.AbortWithError(http.StatusInternalServerError, errors.New("no plugin configuration available"))
		return nil, false
	}
	return cfg, true
}

func serviceIDExistsInConfig(cfg *config.Config, serviceID string) bool {
	for _, svc := range cfg.Services {
		if svc.ID == serviceID {
			return true
		}
	}
	return false
}

func (a *API) validateAgentServiceID(c *gin.Context, serviceID string) (*config.Config, bool) {
	cfg, ok := a.loadPluginConfigForAgents(c)
	if !ok {
		return nil, false
	}
	if !serviceIDExistsInConfig(cfg, serviceID) {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("service %q not found in configuration", serviceID))
		return nil, false
	}
	return cfg, true
}

// buildAgentConfigForCreate maps an explicit CreateAgentRequest into a new llm.BotConfig.
// No hidden server-side defaults are applied: the caller (the UI) is the sole source of
// truth for create-time defaults.
func buildAgentConfigForCreate(req CreateAgentRequest, userID, botUserID string) *llm.BotConfig {
	return &llm.BotConfig{
		BotUserID:               botUserID,
		CreatorID:               userID,
		DisplayName:             req.DisplayName,
		Name:                    req.Username,
		ServiceID:               req.ServiceID,
		CustomInstructions:      req.CustomInstructions,
		ChannelAccessLevel:      llm.ChannelAccessLevel(req.ChannelAccessLevel),
		ChannelIDs:              req.ChannelIDs,
		UserAccessLevel:         llm.UserAccessLevel(req.UserAccessLevel),
		UserIDs:                 req.UserIDs,
		TeamIDs:                 req.TeamIDs,
		AdminUserIDs:            req.AdminUserIDs,
		EnabledMCPTools:         req.EnabledMCPTools,
		Model:                   req.Model,
		EnableVision:            req.EnableVision,
		DisableTools:            req.DisableTools,
		EnabledNativeTools:      req.EnabledNativeTools,
		ReasoningEnabled:        req.ReasoningEnabled,
		ReasoningEffort:         req.ReasoningEffort,
		ThinkingBudget:          req.ThinkingBudget,
		StructuredOutputEnabled: req.StructuredOutputEnabled,
	}
}

// applyAgentUpdateRequest replaces mutable fields on cfg with the values from req.
// This is an intentional full-object replacement: every mutable field on the stored
// agent is overwritten with the request payload. Immutable fields (BotUserID,
// CreatorID, Name, CreateAt, etc.) are preserved by not being touched here.
func applyAgentUpdateRequest(cfg *llm.BotConfig, req UpdateAgentRequest) (displayNameChanged bool) {
	displayNameChanged = cfg.DisplayName != req.DisplayName
	cfg.DisplayName = req.DisplayName
	cfg.ServiceID = req.ServiceID
	cfg.CustomInstructions = req.CustomInstructions
	cfg.ChannelAccessLevel = llm.ChannelAccessLevel(req.ChannelAccessLevel)
	cfg.ChannelIDs = req.ChannelIDs
	cfg.UserAccessLevel = llm.UserAccessLevel(req.UserAccessLevel)
	cfg.UserIDs = req.UserIDs
	cfg.TeamIDs = req.TeamIDs
	cfg.AdminUserIDs = req.AdminUserIDs
	cfg.EnabledMCPTools = req.EnabledMCPTools
	cfg.Model = req.Model
	cfg.EnableVision = req.EnableVision
	cfg.DisableTools = req.DisableTools
	cfg.EnabledNativeTools = req.EnabledNativeTools
	cfg.ReasoningEnabled = req.ReasoningEnabled
	cfg.ReasoningEffort = req.ReasoningEffort
	cfg.ThinkingBudget = req.ThinkingBudget
	cfg.StructuredOutputEnabled = req.StructuredOutputEnabled
	return displayNameChanged
}

// refreshBotsAndNotify forces the bot registry to re-read DB-backed agents,
// re-runs EnsureBots on this node, publishes a cluster event so other
// nodes do the same, and tells connected web clients to drop their cached bot list
// (same idea as the core config_changed handler).
func (a *API) refreshBotsAndNotify() {
	if a.bots != nil {
		a.bots.ForceRefreshOnNextEnsure()
		if err := a.bots.EnsureBots(); err != nil {
			a.pluginAPI.Log.Error("Failed to refresh bots after agent change", "error", err.Error())
		}
	}
	if a.clusterAgentNotifier != nil {
		if err := a.clusterAgentNotifier.PublishAgentUpdate(); err != nil {
			a.pluginAPI.Log.Error("Failed to publish agent update cluster event", "error", err.Error())
		}
	}
	if a.mmClient != nil {
		// Non-nil broadcast required: server Publish path dereferences the pointer (nil panics).
		a.mmClient.PublishWebSocketEvent(WebsocketEventBotsInvalidate, map[string]interface{}{}, &model.WebsocketBroadcast{})
	}
}

// handleCreateAgent creates a new user agent with its backing bot account.
// POST /agents
func (a *API) handleCreateAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	if !canCreateAgent(a.pluginAPI, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("user does not have permission to create agents"))
		return
	}

	var req CreateAgentRequest
	if err := c.BindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if !req.enabledMCPToolsProvided {
		c.AbortWithError(http.StatusBadRequest, errors.New("enabledMCPTools is required: send null to allow all tools, [] to allow none, or an array of {server_origin, tool_name} to allow specific tools"))
		return
	}

	// Validate username format before hitting the server
	if !validUsernameRe.MatchString(req.Username) {
		c.AbortWithError(http.StatusBadRequest, errors.New("invalid username: must start with a lowercase letter and contain only lowercase letters, numbers, dots, hyphens, or underscores"))
		return
	}

	// Validate that the referenced service exists in the config
	if _, ok := a.validateAgentServiceID(c, req.ServiceID); !ok {
		return
	}

	// Create the backing Mattermost bot account.
	mmBot := &model.Bot{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Description: "User-created AI agent",
	}
	if err := a.pluginAPI.Bot.Create(mmBot); err != nil {
		var appErr *model.AppError
		if errors.As(err, &appErr) && appErr.Id == "app.user.save.username_exists.app_error" {
			c.AbortWithError(http.StatusConflict, fmt.Errorf("username %q is already taken", req.Username))
			return
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to create bot account: %w", err))
		return
	}

	agent := buildAgentConfigForCreate(req, userID, mmBot.UserId)

	if err := a.agentStore.CreateAgent(agent); err != nil {
		// Best effort: deactivate the bot we just created since the DB insert failed
		if _, deactivateErr := a.pluginAPI.Bot.UpdateActive(mmBot.UserId, false); deactivateErr != nil {
			a.pluginAPI.Log.Error("Failed to deactivate bot after agent persist failure", "bot_user_id", mmBot.UserId, "error", deactivateErr.Error())
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to persist agent: %w", err))
		return
	}

	a.refreshBotsAndNotify()
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

	accessible := make([]*llm.BotConfig, 0, len(agents))
	for _, cfg := range agents {
		if a.canUserAccessAgent(cfg, userID) {
			accessible = append(accessible, cfg)
		}
	}

	c.JSON(http.StatusOK, accessible)
}

// handleGetAgent returns a single agent by ID.
// GET /agents/:agentid
func (a *API) handleGetAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	cfg, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return
	}
	if cfg == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	if !a.canUserAccessAgent(cfg, userID) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, cfg)
}

// handleUpdateAgent updates a user agent's mutable fields.
// PUT /agents/:agentid
func (a *API) handleUpdateAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	cfg, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return
	}
	if cfg == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	if !canManageAgent(a.pluginAPI, cfg, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("not authorized to modify this agent"))
		return
	}

	var req UpdateAgentRequest
	if err := c.BindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if !req.enabledMCPToolsProvided {
		c.AbortWithError(http.StatusBadRequest, errors.New("enabledMCPTools is required: send null to allow all tools, [] to allow none, or an array of {server_origin, tool_name} to allow specific tools"))
		return
	}

	if req.usernameProvided && req.Username != cfg.Name {
		c.AbortWithError(http.StatusBadRequest, errors.New("username cannot be changed after the agent is created"))
		return
	}
	if _, ok := a.validateAgentServiceID(c, req.ServiceID); !ok {
		return
	}
	displayNameChanged := applyAgentUpdateRequest(cfg, req)

	if err := a.agentStore.UpdateAgent(cfg); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to update agent: %w", err))
		return
	}

	a.refreshBotsAndNotify()

	// Sync display name change to the underlying Mattermost bot account
	if displayNameChanged {
		if _, err := a.pluginAPI.Bot.Patch(cfg.BotUserID, &model.BotPatch{
			DisplayName: &cfg.DisplayName,
		}); err != nil {
			// Non-fatal: the DB is already updated, log and continue
			_ = c.Error(fmt.Errorf("failed to patch bot display name: %w", err))
		}
	}

	c.JSON(http.StatusOK, cfg)
}

// handleDeleteAgent soft-deletes an agent and deactivates its bot account.
// DELETE /agents/:agentid
func (a *API) handleDeleteAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	cfg, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return
	}
	if cfg == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	if !canManageAgent(a.pluginAPI, cfg, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("not authorized to delete this agent"))
		return
	}

	if err := a.agentStore.DeleteAgent(agentID); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to delete agent: %w", err))
		return
	}

	a.refreshBotsAndNotify()

	// Deactivate the backing bot account
	if _, err := a.pluginAPI.Bot.UpdateActive(cfg.BotUserID, false); err != nil {
		// Non-fatal: the DB record is already soft-deleted
		_ = c.Error(fmt.Errorf("failed to deactivate bot: %w", err))
	}

	c.Status(http.StatusOK)
}

// handleUploadAgentAvatar sets a custom profile image on the agent's bot account.
// POST /agents/:agentid/avatar
func (a *API) handleUploadAgentAvatar(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	cfg, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return
	}
	if cfg == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	if !canManageAgent(a.pluginAPI, cfg, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("not authorized to modify this agent"))
		return
	}

	file, _, err := c.Request.FormFile("image")
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("missing or invalid image file: %w", err))
		return
	}
	defer file.Close()

	const maxAvatarSize = 10 << 20 // 10 MB
	limitedReader := io.LimitReader(file, maxAvatarSize+1)
	imageBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to read image: %w", err))
		return
	}
	if len(imageBytes) > maxAvatarSize {
		c.AbortWithError(http.StatusRequestEntityTooLarge, errors.New("image file too large (max 10MB)"))
		return
	}

	if err := a.pluginAPI.User.SetProfileImage(cfg.BotUserID, bytes.NewReader(imageBytes)); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to set profile image: %w", err))
		return
	}

	c.Status(http.StatusOK)
}

// handleListServices returns a list of configured AI services without exposing secrets.
// GET /services
func (a *API) handleListServices(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	if !canConfigureAgentServices(a.pluginAPI, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("user does not have permission to list services"))
		return
	}

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
			ID:               svc.ID,
			Name:             svc.Name,
			Type:             svc.Type,
			DefaultModel:     svc.DefaultModel,
			OutputTokenLimit: svc.OutputTokenLimit,
			UseResponsesAPI:  serviceUsesResponsesAPIForUI(svc),
		})
	}

	c.JSON(http.StatusOK, services)
}

// FetchModelsForServiceRequest is the JSON body for POST /agents/models/fetch.
type FetchModelsForServiceRequest struct {
	ServiceID string `json:"serviceID" binding:"required"`
}

// handleFetchModelsForService lists models for a configured service using stored credentials (non-admin).
func (a *API) handleFetchModelsForService(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	if !canConfigureAgentServices(a.pluginAPI, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("user does not have permission to fetch models"))
		return
	}

	var req FetchModelsForServiceRequest
	if err := c.BindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	cfg, err := a.configStore.GetConfig()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to read config: %w", err))
		return
	}
	if cfg == nil {
		c.AbortWithError(http.StatusBadRequest, errors.New("no plugin configuration"))
		return
	}

	var svc *llm.ServiceConfig
	for i := range cfg.Services {
		if cfg.Services[i].ID == req.ServiceID {
			svc = &cfg.Services[i]
			break
		}
	}
	if svc == nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("service %q not found in configuration", req.ServiceID))
		return
	}

	supportsModelFetching := svc.Type == "anthropic" ||
		svc.Type == "openai" ||
		svc.Type == "azure" ||
		svc.Type == "openaicompatible"
	if !supportsModelFetching {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("model listing not supported for service type %q", svc.Type))
		return
	}

	hasRequiredCredentials := svc.APIKey != ""
	switch svc.Type {
	case "openaicompatible":
		hasRequiredCredentials = svc.APIKey != "" || svc.APIURL != ""
	case "azure":
		hasRequiredCredentials = svc.APIKey != "" && svc.APIURL != ""
	}
	if !hasRequiredCredentials {
		c.AbortWithError(http.StatusBadRequest, errors.New("service is missing credentials required to list models"))
		return
	}

	models, err := bifrost.FetchModelsForServiceType(svc.Type, svc.APIKey, svc.APIURL, svc.OrgID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to fetch models: %w", err))
		return
	}

	c.JSON(http.StatusOK, models)
}

// canUserAccessAgent reports whether userID is allowed to see/use the given agent.
// Creators and admins always have access; otherwise the shared
// bots.CheckUsageRestrictionsForUserConfig rules apply.
func (a *API) canUserAccessAgent(cfg *llm.BotConfig, userID string) bool {
	if cfg == nil {
		return false
	}
	if cfg.IsAdmin(userID) {
		return true
	}
	return a.bots.CheckUsageRestrictionsForUserConfig(*cfg, userID) == nil
}
