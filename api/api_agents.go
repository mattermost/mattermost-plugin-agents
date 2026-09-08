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
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var validUsernameRe = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

// errServiceAccountAuthRequiresAdmin is returned when a caller without
// PermissionManageSystem tries to save an agent with the service account flag on.
var errServiceAccountAuthRequiresAdmin = errors.New("only system administrators can save an agent with service account authentication enabled; turn the setting off to make other changes")

// WebsocketEventBotsInvalidate is the event name for PublishWebSocketEvent (webapp: custom_mattermost-ai_<name>).
const WebsocketEventBotsInvalidate = "bots_invalidate"

// WebsocketEventMCPConnectionUpdated is the event name for user-scoped MCP OAuth connection updates (webapp: custom_mattermost-ai_<name>).
const WebsocketEventMCPConnectionUpdated = "mcp_connection_updated"

// MaxAgentRequestBodyBytes caps agent create/update JSON bodies. It must comfortably
// exceed the worst-case encoded size of llm.MaxCustomInstructionsRunes plus the
// remaining payload.
const MaxAgentRequestBodyBytes = 2 << 20 // 2 MiB

// agentErrorResponse is the JSON body returned for failed agent requests. The
// webapp surfaces Error directly to the user, so the message must be
// human-readable and actionable.
type agentErrorResponse struct {
	Error string `json:"error"`
}

// abortAgentRequest writes a JSON error response with the given status code so
// the webapp can surface the message instead of falling back to a generic
// "Failed to save agent. Please try again." The error is also recorded on the
// gin context so ginlogger captures it server-side.
func abortAgentRequest(c *gin.Context, status int, err error) {
	_ = c.Error(err)
	publicMsg := err.Error()
	if status >= http.StatusInternalServerError {
		publicMsg = "internal server error"
	}
	c.AbortWithStatusJSON(status, agentErrorResponse{Error: publicMsg})
}

// AgentRequestFields are the request fields common to agent create and update.
// Field values are stored as given (no server-side fill-in). MCP tool access is
// controlled by two independent fields:
//   - autoEnableNewMCPTools=true gives the agent every currently configured MCP tool and any added later.
//   - Otherwise, the agent gets only the tools listed in enabledMCPTools (empty/missing = no MCP tools).
type AgentRequestFields struct {
	DisplayName             string               `json:"displayName" binding:"required"`
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
	MCPDynamicToolLoading   bool                 `json:"mcpDynamicToolLoading"`
	UseServiceAccountAuth   bool                 `json:"useServiceAccountAuth"`
	Model                   string               `json:"model"`
	EnableVision            bool                 `json:"enableVision"`
	DisableTools            bool                 `json:"disableTools"`
	EnabledNativeTools      []string             `json:"enabledNativeTools"`
	ReasoningEnabled        bool                 `json:"reasoningEnabled"`
	ReasoningEffort         string               `json:"reasoningEffort"`
	ThinkingBudget          int                  `json:"thinkingBudget"`
	StructuredOutputEnabled bool                 `json:"structuredOutputEnabled"`
	MaxToolTurns            int                  `json:"maxToolTurns"`
}

// applyTo overwrites the request-controlled fields on cfg.
func (r AgentRequestFields) applyTo(cfg *llm.BotConfig) {
	cfg.DisplayName = r.DisplayName
	cfg.ServiceID = r.ServiceID
	cfg.CustomInstructions = r.CustomInstructions
	cfg.ChannelAccessLevel = llm.ChannelAccessLevel(r.ChannelAccessLevel)
	cfg.ChannelIDs = r.ChannelIDs
	cfg.UserAccessLevel = llm.UserAccessLevel(r.UserAccessLevel)
	cfg.UserIDs = r.UserIDs
	cfg.TeamIDs = r.TeamIDs
	cfg.AdminUserIDs = r.AdminUserIDs
	cfg.EnabledMCPTools = r.EnabledMCPTools
	cfg.AutoEnableNewMCPTools = r.AutoEnableNewMCPTools
	cfg.MCPDynamicToolLoading = r.MCPDynamicToolLoading
	cfg.UseServiceAccountAuth = r.UseServiceAccountAuth
	cfg.Model = r.Model
	cfg.EnableVision = r.EnableVision
	cfg.DisableTools = r.DisableTools
	cfg.EnabledNativeTools = r.EnabledNativeTools
	cfg.ReasoningEnabled = r.ReasoningEnabled
	cfg.ReasoningEffort = r.ReasoningEffort
	cfg.ThinkingBudget = r.ThinkingBudget
	cfg.StructuredOutputEnabled = r.StructuredOutputEnabled
	cfg.MaxToolTurns = r.MaxToolTurns
}

// CreateAgentRequest is the JSON body for POST /agents.
type CreateAgentRequest struct {
	AgentRequestFields
	Username string `json:"username" binding:"required"`
}

// UpdateAgentRequest is the JSON body for PUT /agents/:agentid (full document replace, same shape as create).
// Username cannot change after create (enforced in the handler).
type UpdateAgentRequest struct {
	AgentRequestFields
	Username string `json:"username"`

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

// AgentActiveCountHeader is returned on GET /agents for unlicensed servers so the
// webapp can gate creation against the server-wide count (not the access-filtered list).
const AgentActiveCountHeader = "X-Agent-Active-Count"

// checkAgentCreateQuota allows unlimited creation when multi-LLM licensed; otherwise
// enforces FreeTierAgentLimit across all self-service agents on the server. It writes
// the abort response and returns false when creation must be blocked.
func (a *API) checkAgentCreateQuota(c *gin.Context) bool {
	if a.licenseChecker.IsMultiLLMLicensed() {
		return true
	}
	count, err := a.agentStore.CountActiveAgents()
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to check agent quota: %w", err))
		return false
	}
	if count >= FreeTierAgentLimit {
		abortAgentRequest(c, http.StatusForbidden, fmt.Errorf("creating more than %d self-service agent(s) requires an E20 or Enterprise license", FreeTierAgentLimit))
		return false
	}
	return true
}

// loadPluginConfigForAgents loads plugin config; on failure it aborts with 500.
func (a *API) loadPluginConfigForAgents(c *gin.Context) (*config.Config, bool) {
	cfg, err := a.configStore.GetConfig()
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to read config: %w", err))
		return nil, false
	}
	if cfg == nil {
		abortAgentRequest(c, http.StatusInternalServerError, errors.New("no plugin configuration available"))
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
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("service %q not found in configuration", serviceID))
		return nil, false
	}
	return cfg, true
}

// buildAgentConfigForCreate builds a new llm.BotConfig from req and the new bot/user IDs.
func buildAgentConfigForCreate(req CreateAgentRequest, userID, botUserID string) *llm.BotConfig {
	cfg := &llm.BotConfig{
		BotUserID: botUserID,
		CreatorID: userID,
		Name:      req.Username,
	}
	req.applyTo(cfg)
	return cfg
}

// applyAgentUpdateRequest overwrites mutable fields on cfg from req; returns whether DisplayName changed.
func applyAgentUpdateRequest(cfg *llm.BotConfig, req UpdateAgentRequest) (displayNameChanged bool) {
	displayNameChanged = cfg.DisplayName != req.DisplayName
	req.applyTo(cfg)
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
		a.mmClient.PublishWebSocketEvent(WebsocketEventBotsInvalidate, map[string]any{}, &model.WebsocketBroadcast{})
	}
	return ensureErr
}

// handleCreateAgent handles POST /agents: creates the bot user and persisted agent config.
func (a *API) handleCreateAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	if !canCreateAgent(a.pluginAPI, userID) {
		abortAgentRequest(c, http.StatusForbidden, errors.New("user does not have permission to create agents"))
		return
	}

	if !a.checkAgentCreateQuota(c) {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxAgentRequestBodyBytes)

	var req CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			abortAgentRequest(c, http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large: %w", err))
			return
		}
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if req.UseServiceAccountAuth && !isSystemAdmin(a.pluginAPI, userID) {
		abortAgentRequest(c, http.StatusForbidden, errServiceAccountAuthRequiresAdmin)
		return
	}

	// Identify the requested agent as soon as the body is bound so
	// validation and persistence fail paths carry it too. Both values are
	// unvalidated request text at this point, so they are length-clamped.
	audit.AddParam(auditRec(c), audit.KeyAgentName, audit.TruncateID(req.Username))
	audit.AddParam(auditRec(c), "service_id", audit.TruncateID(req.ServiceID))

	if !validUsernameRe.MatchString(req.Username) {
		abortAgentRequest(c, http.StatusBadRequest, errors.New("invalid username: must start with a lowercase letter and contain only lowercase letters, numbers, dots, hyphens, or underscores"))
		return
	}

	if _, ok := a.validateAgentServiceID(c, req.ServiceID); !ok {
		return
	}

	// Validate the built config before creating the Mattermost bot account so an
	// invalid request does not leave an orphan bot user behind.
	if err := buildAgentConfigForCreate(req, userID, "").Validate(); err != nil {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid agent configuration: %w", err))
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
			abortAgentRequest(c, http.StatusConflict, fmt.Errorf("username %q is already taken", req.Username))
			return
		}
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to create bot account: %w", err))
		return
	}

	agent := buildAgentConfigForCreate(req, userID, mmBot.UserId)

	if err := a.agentStore.CreateAgent(agent); err != nil {
		if _, deactivateErr := a.pluginAPI.Bot.UpdateActive(mmBot.UserId, false); deactivateErr != nil {
			a.pluginAPI.Log.Error("Failed to deactivate bot after agent persist failure", "bot_user_id", mmBot.UserId, "error", deactivateErr.Error())
		}
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to persist agent: %w", err))
		return
	}

	// Both IDs exist only after the bot account and agent config are persisted.
	audit.AddParam(auditRec(c), audit.KeyAgentID, agent.ID)
	audit.AddParam(auditRec(c), "bot_user_id", mmBot.UserId)

	_ = a.refreshBotsAndNotify()
	c.JSON(http.StatusCreated, agent)
}

// handleListAgents handles GET /agents: agents the caller may access.
func (a *API) handleListAgents(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	agents, err := a.agentStore.ListAgents()
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to list agents: %w", err))
		return
	}

	accessible := make([]*llm.BotConfig, 0, len(agents))
	for _, cfg := range agents {
		if a.canUserAccessAgent(cfg, userID) {
			accessible = append(accessible, sanitizeAgentForUser(a.pluginAPI, cfg, userID))
		}
	}

	// Enrich (best-effort) with the server-wide count so the webapp can gate creation
	// against the real quota, not the access-filtered list. A failure here must not fail
	// the list request: just omit the header and let the create API enforce the limit.
	if !a.licenseChecker.IsMultiLLMLicensed() {
		count, err := a.agentStore.CountActiveAgents()
		if err != nil {
			a.pluginAPI.Log.Warn("Failed to count active agents for quota header", "error", err.Error())
		} else {
			c.Header(AgentActiveCountHeader, strconv.Itoa(count))
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
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
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

// loadManageableAgent loads agentID and verifies userID may manage it, aborting
// with 500/404/403 as appropriate. The agent name (plus any params added by
// enrichAudit, which may be nil) is recorded before the authorization check so
// 403 fail records identify the target.
func (a *API) loadManageableAgent(c *gin.Context, agentID, userID, forbiddenMsg string, enrichAudit func(*llm.BotConfig)) (*llm.BotConfig, bool) {
	cfg, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return nil, false
	}
	if cfg == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, false
	}

	audit.AddParam(auditRec(c), audit.KeyAgentName, cfg.Name)
	if enrichAudit != nil {
		enrichAudit(cfg)
	}

	if !canManageAgent(a.pluginAPI, cfg, userID) {
		abortAgentRequest(c, http.StatusForbidden, errors.New(forbiddenMsg))
		return nil, false
	}
	return cfg, true
}

// handleUpdateAgent handles PUT /agents/:agentid (full replace).
func (a *API) handleUpdateAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	// Identify the target early so 404/403 fail records carry it.
	audit.AddParam(auditRec(c), audit.KeyAgentID, audit.TruncateID(agentID))

	cfg, ok := a.loadManageableAgent(c, agentID, userID, "not authorized to modify this agent", nil)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxAgentRequestBodyBytes)

	var req UpdateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			abortAgentRequest(c, http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large: %w", err))
			return
		}
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Shallow copy is safe: applyAgentUpdateRequest replaces slice headers rather than mutating elements.
	proposed := *cfg
	displayNameChanged := applyAgentUpdateRequest(&proposed, req)

	if serviceAccountChangeNeedsAdmin(*cfg, proposed) && !isSystemAdmin(a.pluginAPI, userID) {
		abortAgentRequest(c, http.StatusForbidden, errServiceAccountAuthRequiresAdmin)
		return
	}

	if req.usernameProvided && req.Username != cfg.Name {
		abortAgentRequest(c, http.StatusBadRequest, errors.New("username cannot be changed after the agent is created"))
		return
	}
	if _, ok := a.validateAgentServiceID(c, req.ServiceID); !ok {
		return
	}

	// Snapshot the stored config for the audit field diff, then adopt the
	// already-applied proposed update (apply-then-compare ACL above).
	prev := *cfg
	cfg = &proposed

	// Audit which fields the update changed — never their values, since
	// customInstructions carries prompt content.
	// Field names only — the agent config carries prompt content
	// (customInstructions), so the record never says what values changed to.
	audit.AddParam(auditRec(c), "changed_fields", audit.ChangedJSONKeys(&prev, cfg))

	if err := cfg.Validate(); err != nil {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid agent configuration: %w", err))
		return
	}

	if err := a.agentStore.UpdateAgent(cfg); err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to update agent: %w", err))
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

	// Identify the target early so 404/403 fail records carry it.
	audit.AddParam(auditRec(c), audit.KeyAgentID, audit.TruncateID(agentID))

	cfg, ok := a.loadManageableAgent(c, agentID, userID, "not authorized to delete this agent", func(cfg *llm.BotConfig) {
		audit.AddParam(auditRec(c), "bot_user_id", cfg.BotUserID)
	})
	if !ok {
		return
	}

	if err := a.agentStore.DeleteAgent(agentID); err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to delete agent: %w", err))
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

	// Identify the target early so 404/403 fail records carry it. Nothing
	// about the image itself is ever recorded.
	audit.AddParam(auditRec(c), audit.KeyAgentID, audit.TruncateID(agentID))

	cfg, ok := a.loadManageableAgent(c, agentID, userID, "not authorized to modify this agent", nil)
	if !ok {
		return
	}

	file, _, err := c.Request.FormFile("image")
	if err != nil {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("missing or invalid image file: %w", err))
		return
	}
	defer file.Close()

	const maxAvatarSize = 10 << 20 // 10 MB
	limitedReader := io.LimitReader(file, maxAvatarSize+1)
	imageBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to read image: %w", err))
		return
	}
	if len(imageBytes) > maxAvatarSize {
		abortAgentRequest(c, http.StatusRequestEntityTooLarge, errors.New("image file too large (max 10MB)"))
		return
	}

	if err := a.pluginAPI.User.SetProfileImage(cfg.BotUserID, bytes.NewReader(imageBytes)); err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to set profile image: %w", err))
		return
	}

	c.Status(http.StatusOK)
}

// handleListServices handles GET /services (non-secret fields only).
func (a *API) handleListServices(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	if !canConfigureAgentServices(a.pluginAPI, userID) {
		abortAgentRequest(c, http.StatusForbidden, errors.New("user does not have permission to list services"))
		return
	}

	cfg, err := a.configStore.GetConfig()
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to read config: %w", err))
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
		abortAgentRequest(c, http.StatusForbidden, errors.New("user does not have permission to fetch models"))
		return
	}

	var req FetchModelsForServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	cfg, err := a.configStore.GetConfig()
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to read config: %w", err))
		return
	}
	if cfg == nil {
		abortAgentRequest(c, http.StatusBadRequest, errors.New("no plugin configuration"))
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
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("service %q not found in configuration", req.ServiceID))
		return
	}

	supportsModelFetching := svc.Type == llm.ServiceTypeAnthropic ||
		svc.Type == llm.ServiceTypeOpenAI ||
		svc.Type == llm.ServiceTypeAzure ||
		svc.Type == llm.ServiceTypeOpenAICompatible ||
		svc.Type == llm.ServiceTypeGemini ||
		svc.Type == llm.ServiceTypeVertex
	if !supportsModelFetching {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("model listing not supported for service type %q", svc.Type))
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
		abortAgentRequest(c, http.StatusBadRequest, errors.New("service is missing credentials required to list models"))
		return
	}

	models, err := bifrost.FetchModelsForService(*svc)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to fetch models: %w", err))
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
