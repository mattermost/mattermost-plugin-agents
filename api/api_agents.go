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
	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var validUsernameRe = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

// WebsocketEventBotsInvalidate is the event name for PublishWebSocketEvent (webapp: custom_mattermost-ai_<name>).
const WebsocketEventBotsInvalidate = "bots_invalidate"

// WebsocketEventMCPConnectionUpdated is the event name for user-scoped MCP OAuth connection updates (webapp: custom_mattermost-ai_<name>).
const WebsocketEventMCPConnectionUpdated = "mcp_connection_updated"

// MaxAgentRequestBodyBytes caps the JSON body size for agent create/update requests
// to protect against oversized payloads in the various ID slices and MCP tool lists.
const MaxAgentRequestBodyBytes = 512 << 10 // 512 KiB

// CreateAgentRequest is the JSON body for POST /agents. Field values are stored as given (no server-side fill-in).
// MCP tool access is controlled by two independent fields:
//   - autoEnableNewMCPTools=true gives the agent every currently configured MCP tool and any added later.
//   - Otherwise, the agent gets only the tools listed in enabledMCPTools (empty/missing = no MCP tools).
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
	AutoEnableNewMCPTools   bool                 `json:"autoEnableNewMCPTools"`
	Model                   string               `json:"model"`
	EnableVision            bool                 `json:"enableVision"`
	DisableTools            bool                 `json:"disableTools"`
	EnabledNativeTools      []string             `json:"enabledNativeTools"`
	ReasoningEnabled        bool                 `json:"reasoningEnabled"`
	ReasoningEffort         string               `json:"reasoningEffort"`
	ThinkingBudget          int                  `json:"thinkingBudget"`
	StructuredOutputEnabled bool                 `json:"structuredOutputEnabled"`
}

// UpdateAgentRequest is the JSON body for PUT /agents/:agentid (full document replace, same shape as create).
// Username cannot change after create (enforced in the handler).
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
	AutoEnableNewMCPTools   bool                 `json:"autoEnableNewMCPTools"`
	Model                   string               `json:"model"`
	EnableVision            bool                 `json:"enableVision"`
	DisableTools            bool                 `json:"disableTools"`
	EnabledNativeTools      []string             `json:"enabledNativeTools"`
	ReasoningEnabled        bool                 `json:"reasoningEnabled"`
	ReasoningEffort         string               `json:"reasoningEffort"`
	ThinkingBudget          int                  `json:"thinkingBudget"`
	StructuredOutputEnabled bool                 `json:"structuredOutputEnabled"`

	usernameProvided bool
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
	*r = UpdateAgentRequest(decoded)
	r.usernameProvided = usernameProvided
	return nil
}

// ServiceInfo is a client-safe view of an AI service (no secrets).
type ServiceInfo struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	DefaultModel     string `json:"defaultModel"`
	OutputTokenLimit int    `json:"outputTokenLimit"`
	UseResponsesAPI  bool   `json:"useResponsesAPI"`
}

// FreeTierAgentLimit is the maximum number of self-service agents allowed when
// the server does not have a multi-LLM (E20+) license.
const FreeTierAgentLimit = 1

// checkAgentCreateQuota allows unlimited creation when multi-LLM licensed; otherwise
// enforces FreeTierAgentLimit across all self-service agents on the server. It writes
// the abort response and returns false when creation must be blocked.
func (a *API) checkAgentCreateQuota(c *gin.Context) bool {
	if a.licenseChecker.IsMultiLLMLicensed() {
		return true
	}
	count, err := a.agentStore.CountActiveAgents()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to check agent quota: %w", err))
		return false
	}
	if count >= FreeTierAgentLimit {
		c.AbortWithError(http.StatusForbidden, fmt.Errorf("creating more than %d self-service agent(s) requires an E20 or Enterprise license", FreeTierAgentLimit))
		return false
	}
	return true
}

// canManageAgent reports whether userID may update or delete cfg: agent admin, PermissionManageOthersAgent,
// or (agent with empty CreatorID) PermissionManageSystem for migrated legacy bots.
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

// canConfigureAgentServices reports whether userID may list services or fetch models (ManageOwnAgent, ManageOthersAgent, or ManageSystem).
func canConfigureAgentServices(client *pluginapi.Client, userID string) bool {
	if client.User.HasPermissionTo(userID, model.PermissionManageOwnAgent) {
		return true
	}
	if client.User.HasPermissionTo(userID, model.PermissionManageOthersAgent) {
		return true
	}
	return client.User.HasPermissionTo(userID, model.PermissionManageSystem)
}

// loadPluginConfigForAgents loads plugin config; on failure it aborts with 500.
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

// buildAgentConfigForCreate builds a new llm.BotConfig from req and the new bot/user IDs.
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
		AutoEnableNewMCPTools:   req.AutoEnableNewMCPTools,
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

// applyAgentUpdateRequest overwrites mutable fields on cfg from req; returns whether DisplayName changed.
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
	cfg.AutoEnableNewMCPTools = req.AutoEnableNewMCPTools
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

// refreshBotsAndNotify reloads bots on this node, notifies the cluster, and broadcasts so clients refresh bot lists.
// It returns the error from EnsureBots when a.bots is non-nil, or nil when a.bots is nil; cluster and websocket
// steps always run regardless.
func (a *API) refreshBotsAndNotify() error {
	var ensureErr error
	if a.bots != nil {
		a.bots.ForceRefreshOnNextEnsure()
		ensureErr = a.bots.EnsureBots()
		if ensureErr != nil {
			a.pluginAPI.Log.Error("Failed to refresh bots after agent change", "error", ensureErr.Error())
		}
	}
	if a.clusterAgentNotifier != nil {
		if err := a.clusterAgentNotifier.PublishAgentUpdate(); err != nil {
			a.pluginAPI.Log.Error("Failed to publish agent update cluster event", "error", err.Error())
		}
	}
	if a.mmClient != nil {
		// PublishWebSocketEvent requires a non-nil broadcast (server dereferences it).
		a.mmClient.PublishWebSocketEvent(WebsocketEventBotsInvalidate, map[string]interface{}{}, &model.WebsocketBroadcast{})
	}
	return ensureErr
}

// handleCreateAgent handles POST /agents: creates the bot user and persisted agent config.
func (a *API) handleCreateAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	if !canCreateAgent(a.pluginAPI, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("user does not have permission to create agents"))
		return
	}

	if !a.checkAgentCreateQuota(c) {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxAgentRequestBodyBytes)

	var req CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.AbortWithError(http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large: %w", err))
			return
		}
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if !validUsernameRe.MatchString(req.Username) {
		c.AbortWithError(http.StatusBadRequest, errors.New("invalid username: must start with a lowercase letter and contain only lowercase letters, numbers, dots, hyphens, or underscores"))
		return
	}

	if _, ok := a.validateAgentServiceID(c, req.ServiceID); !ok {
		return
	}

	// Validate the built config before creating the Mattermost bot account so an
	// invalid request does not leave an orphan bot user behind.
	if err := buildAgentConfigForCreate(req, userID, "").Validate(); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid agent configuration: %w", err))
		return
	}

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
		if _, deactivateErr := a.pluginAPI.Bot.UpdateActive(mmBot.UserId, false); deactivateErr != nil {
			a.pluginAPI.Log.Error("Failed to deactivate bot after agent persist failure", "bot_user_id", mmBot.UserId, "error", deactivateErr.Error())
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to persist agent: %w", err))
		return
	}

	_ = a.refreshBotsAndNotify()
	c.JSON(http.StatusCreated, agent)
}

// handleListAgents handles GET /agents: agents the caller may access.
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
			accessible = append(accessible, sanitizeAgentForUser(a.pluginAPI, cfg, userID))
		}
	}

	c.JSON(http.StatusOK, accessible)
}

// handleGetAgent handles GET /agents/:agentid.
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

	c.JSON(http.StatusOK, sanitizeAgentForUser(a.pluginAPI, cfg, userID))
}

// handleUpdateAgent handles PUT /agents/:agentid (full replace).
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

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxAgentRequestBodyBytes)

	var req UpdateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.AbortWithError(http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large: %w", err))
			return
		}
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
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

	if err := cfg.Validate(); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid agent configuration: %w", err))
		return
	}

	if err := a.agentStore.UpdateAgent(cfg); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to update agent: %w", err))
		return
	}

	ensureErr := a.refreshBotsAndNotify()

	// EnsureBots patches display name on success; when it did not run (no bot registry) or failed, sync explicitly.
	if displayNameChanged && (ensureErr != nil || a.bots == nil) {
		if _, err := a.pluginAPI.Bot.Patch(cfg.BotUserID, &model.BotPatch{
			DisplayName: &cfg.DisplayName,
		}); err != nil {
			_ = c.Error(fmt.Errorf("failed to patch bot display name: %w", err))
		}
	}

	c.JSON(http.StatusOK, cfg)
}

// handleDeleteAgent handles DELETE /agents/:agentid (soft-delete and deactivate bot).
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

	ensureErr := a.refreshBotsAndNotify()

	// EnsureBots deactivates removed bots on success; when it did not run or failed, deactivate explicitly.
	if ensureErr != nil || a.bots == nil {
		if _, err := a.pluginAPI.Bot.UpdateActive(cfg.BotUserID, false); err != nil {
			_ = c.Error(fmt.Errorf("failed to deactivate bot: %w", err))
		}
	}

	c.Status(http.StatusOK)
}

// handleUploadAgentAvatar handles POST /agents/:agentid/avatar.
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

// handleListServices handles GET /services (non-secret fields only).
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
			UseResponsesAPI:  llm.ServiceUsesResponsesAPI(svc),
		})
	}

	c.JSON(http.StatusOK, services)
}

// FetchModelsForServiceRequest is the JSON body for POST /agents/models/fetch.
type FetchModelsForServiceRequest struct {
	ServiceID string `json:"serviceID" binding:"required"`
}

// handleFetchModelsForService handles POST /agents/models/fetch using stored service credentials.
func (a *API) handleFetchModelsForService(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	if !canConfigureAgentServices(a.pluginAPI, userID) {
		c.AbortWithError(http.StatusForbidden, errors.New("user does not have permission to fetch models"))
		return
	}

	var req FetchModelsForServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
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

	supportsModelFetching := svc.Type == llm.ServiceTypeAnthropic ||
		svc.Type == llm.ServiceTypeOpenAI ||
		svc.Type == llm.ServiceTypeAzure ||
		svc.Type == llm.ServiceTypeOpenAICompatible ||
		svc.Type == llm.ServiceTypeGemini ||
		svc.Type == llm.ServiceTypeVertex
	if !supportsModelFetching {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("model listing not supported for service type %q", svc.Type))
		return
	}

	hasRequiredCredentials := svc.APIKey != ""
	switch svc.Type {
	case llm.ServiceTypeOpenAICompatible:
		hasRequiredCredentials = svc.APIKey != "" || svc.APIURL != ""
	case llm.ServiceTypeAzure:
		hasRequiredCredentials = svc.APIKey != "" && svc.APIURL != ""
	case llm.ServiceTypeVertex:
		// Vertex uses GCP project + region; service-account JSON is optional (ADC).
		hasRequiredCredentials = svc.VertexProjectID != "" && svc.Region != ""
	}
	if !hasRequiredCredentials {
		c.AbortWithError(http.StatusBadRequest, errors.New("service is missing credentials required to list models"))
		return
	}

	models, err := bifrost.FetchModelsForService(*svc)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to fetch models: %w", err))
		return
	}

	c.JSON(http.StatusOK, models)
}

// canUserAccessAgent reports whether userID may view or use the agent (admin, then usage restrictions).
func (a *API) canUserAccessAgent(cfg *llm.BotConfig, userID string) bool {
	if cfg == nil || a.pluginAPI == nil {
		return false
	}
	if cfg.IsAdmin(userID) {
		return true
	}
	// Do not use a.bots here: agent list/get routes are not bot-middleware-gated and a.bots may be nil.
	return bots.UsageRestrictionsForUserConfig(a.pluginAPI, *cfg, userID) == nil
}

// sanitizeAgentForUser returns cfg unchanged for users who can manage the agent
// (creator / agent admin / PermissionManageOthersAgent / legacy bot ManageSystem).
// For everyone else, it returns a shallow copy with CustomInstructions stripped
// since that field can contain sensitive organizational procedures.
func sanitizeAgentForUser(client *pluginapi.Client, cfg *llm.BotConfig, userID string) *llm.BotConfig {
	if cfg == nil {
		return nil
	}
	if canManageAgent(client, cfg, userID) {
		return cfg
	}
	redacted := *cfg
	redacted.CustomInstructions = ""
	return &redacted
}
