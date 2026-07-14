// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// Access-control authoring routes (contract §7). Request/response bodies are
// model.AccessControlPolicy JSON verbatim; identity fields (ID, Type, Version,
// Active) are always overwritten from the route, never trusted from the body.

// abortPolicyRequest maps PAP errors onto HTTP statuses using the
// abortAgentRequest response conventions.
func abortPolicyRequest(c *gin.Context, err error) {
	var appErr *model.AppError
	switch {
	case errors.Is(err, accesscontrol.ErrPolicyNotFound):
		abortAgentRequest(c, http.StatusNotFound, err)
	case errors.Is(err, accesscontrol.ErrAccessDenied):
		abortAgentRequest(c, http.StatusForbidden, err)
	case errors.As(err, &appErr) && appErr.StatusCode >= http.StatusBadRequest && appErr.StatusCode < http.StatusInternalServerError:
		abortAgentRequest(c, appErr.StatusCode, err)
	default:
		abortAgentRequest(c, http.StatusInternalServerError, err)
	}
}

// validPolicyResourceType reports whether t is one of the three plugin
// resource types accepted by the CEL proxy routes.
func validPolicyResourceType(t string) bool {
	switch t {
	case accesscontrol.ResourceTypeAgent, accesscontrol.ResourceTypeService, accesscontrol.ResourceTypeMCP:
		return true
	}
	return false
}

// bindPolicyBody binds the request body into an AccessControlPolicy with the
// same size cap as agent writes. Returns nil after aborting on bind failure.
func bindPolicyBody(c *gin.Context) *model.AccessControlPolicy {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxAgentRequestBodyBytes)
	var policy model.AccessControlPolicy
	if err := c.ShouldBindJSON(&policy); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			abortAgentRequest(c, http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large: %w", err))
			return nil
		}
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid policy body: %w", err))
		return nil
	}
	return &policy
}

// loadManagedAgent loads the :agentid agent and authorizes the caller as an
// agent manager (creator / agent admin / ManageOthersAgent / legacy
// ManageSystem). Returns nil after writing the response on failure.
func (a *API) loadManagedAgent(c *gin.Context) *llm.BotConfig {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	cfg, err := a.agentStore.GetAgent(agentID)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to get agent: %w", err))
		return nil
	}
	if cfg == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil
	}
	if !canManageAgent(a.pluginAPI, cfg, userID) {
		abortAgentRequest(c, http.StatusForbidden, errors.New("not authorized to manage this agent's access policy"))
		return nil
	}
	return cfg
}

func (a *API) handleGetAgentPolicy(c *gin.Context) {
	cfg := a.loadManagedAgent(c)
	if cfg == nil {
		return
	}
	policy, err := a.accessChecker.GetPolicy(c.Request.Context(), cfg.ID)
	if err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, policy)
}

func (a *API) handlePutAgentPolicy(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	cfg := a.loadManagedAgent(c)
	if cfg == nil {
		return
	}
	policy := bindPolicyBody(c)
	if policy == nil {
		return
	}
	saved, err := a.accessChecker.SavePolicy(c.Request.Context(), userID, accesscontrol.ResourceTypeAgent, cfg.ID, cfg.DisplayName, policy)
	if err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, saved)
}

func (a *API) handleDeleteAgentPolicy(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	cfg := a.loadManagedAgent(c)
	if cfg == nil {
		return
	}
	if err := a.accessChecker.DeletePolicy(c.Request.Context(), userID, accesscontrol.ResourceTypeAgent, cfg.ID); err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.Status(http.StatusOK)
}

// resolveServiceForPolicy resolves :serviceid to a configured service. The
// existence check doubles as the ID-validity gate: policies can never be
// written against nonexistent resources. Returns nil after aborting.
func (a *API) resolveServiceForPolicy(c *gin.Context) *llm.ServiceConfig {
	serviceID := c.Param("serviceid")
	cfg, ok := a.loadPluginConfigForAgents(c)
	if !ok {
		return nil
	}
	for i := range cfg.Services {
		if cfg.Services[i].ID == serviceID {
			return &cfg.Services[i]
		}
	}
	c.AbortWithStatus(http.StatusNotFound)
	return nil
}

func (a *API) handleGetServicePolicy(c *gin.Context) {
	svc := a.resolveServiceForPolicy(c)
	if svc == nil {
		return
	}
	policy, err := a.accessChecker.GetPolicy(c.Request.Context(), svc.ID)
	if err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, policy)
}

func (a *API) handlePutServicePolicy(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	svc := a.resolveServiceForPolicy(c)
	if svc == nil {
		return
	}
	policy := bindPolicyBody(c)
	if policy == nil {
		return
	}
	saved, err := a.accessChecker.SavePolicy(c.Request.Context(), userID, accesscontrol.ResourceTypeService, svc.ID, svc.Name, policy)
	if err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, saved)
}

func (a *API) handleDeleteServicePolicy(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	svc := a.resolveServiceForPolicy(c)
	if svc == nil {
		return
	}
	if err := a.accessChecker.DeletePolicy(c.Request.Context(), userID, accesscontrol.ResourceTypeService, svc.ID); err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.Status(http.StatusOK)
}

// resolveMCPServerForPolicy resolves :serverid to a configured external MCP
// server (external only, contract §8). Returns nil after aborting.
func (a *API) resolveMCPServerForPolicy(c *gin.Context) *config.MCPServerConfig {
	serverID := c.Param("serverid")
	cfg, ok := a.loadPluginConfigForAgents(c)
	if !ok {
		return nil
	}
	for i := range cfg.MCP.Servers {
		if cfg.MCP.Servers[i].ID == serverID {
			return &cfg.MCP.Servers[i]
		}
	}
	c.AbortWithStatus(http.StatusNotFound)
	return nil
}

func (a *API) handleGetMCPPolicy(c *gin.Context) {
	server := a.resolveMCPServerForPolicy(c)
	if server == nil {
		return
	}
	policy, err := a.accessChecker.GetPolicy(c.Request.Context(), server.ID)
	if err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, policy)
}

func (a *API) handlePutMCPPolicy(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	server := a.resolveMCPServerForPolicy(c)
	if server == nil {
		return
	}
	policy := bindPolicyBody(c)
	if policy == nil {
		return
	}
	saved, err := a.accessChecker.SavePolicy(c.Request.Context(), userID, accesscontrol.ResourceTypeMCP, server.ID, server.Name, policy)
	if err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, saved)
}

func (a *API) handleDeleteMCPPolicy(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	server := a.resolveMCPServerForPolicy(c)
	if server == nil {
		return
	}
	if err := a.accessChecker.DeletePolicy(c.Request.Context(), userID, accesscontrol.ResourceTypeMCP, server.ID); err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.Status(http.StatusOK)
}

// celRouteAuthzRequired gates the CEL proxy routes (contract §7.1): system
// admins and agent managers (canConfigureAgentServices) pass outright;
// otherwise a per-agent admin passes when the agent_id query param names an
// agent they manage.
func (a *API) celRouteAuthzRequired(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	if canConfigureAgentServices(a.pluginAPI, userID) {
		c.Next()
		return
	}
	if agentID := c.Query("agent_id"); agentID != "" {
		cfg, err := a.agentStore.GetAgent(agentID)
		if err == nil && cfg != nil && canManageAgent(a.pluginAPI, cfg, userID) {
			c.Next()
			return
		}
	}
	abortAgentRequest(c, http.StatusForbidden, errors.New("not authorized to use the access policy editor"))
}

// celExpressionRequest is the JSON body shared by the check/visual_ast routes.
type celExpressionRequest struct {
	ResourceType string `json:"resource_type"`
	Expression   string `json:"expression"`
}

func (a *API) bindCELExpressionRequest(c *gin.Context) *celExpressionRequest {
	var req celExpressionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return nil
	}
	if !validPolicyResourceType(req.ResourceType) {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid resource_type %q", req.ResourceType))
		return nil
	}
	return &req
}

func (a *API) handleCELCheck(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	req := a.bindCELExpressionRequest(c)
	if req == nil {
		return
	}
	celErrors, err := a.accessChecker.CheckExpression(c.Request.Context(), userID, req.ResourceType, req.Expression)
	if err != nil {
		abortPolicyRequest(c, err)
		return
	}
	if celErrors == nil {
		celErrors = []model.CELExpressionError{}
	}
	c.JSON(http.StatusOK, celErrors)
}

// celTestRequest is the JSON body for POST /access_control/cel/test.
type celTestRequest struct {
	ResourceType string `json:"resource_type"`
	Expression   string `json:"expression"`
	Term         string `json:"term"`
	After        string `json:"after"`
	Limit        int    `json:"limit"`
}

func (a *API) handleCELTest(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	var req celTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if !validPolicyResourceType(req.ResourceType) {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid resource_type %q", req.ResourceType))
		return
	}
	result, err := a.accessChecker.TestExpression(c.Request.Context(), userID, req.ResourceType, req.Expression, req.Term, req.After, req.Limit)
	if err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (a *API) handleCELAutocompleteFields(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	after := c.Query("after")
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("invalid limit %q", raw))
			return
		}
		limit = parsed
	}
	fields, err := a.accessChecker.FieldsAutocomplete(c.Request.Context(), userID, after, limit)
	if err != nil {
		abortPolicyRequest(c, err)
		return
	}
	if fields == nil {
		fields = []*model.PropertyField{}
	}
	c.JSON(http.StatusOK, fields)
}

func (a *API) handleCELVisualAST(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	req := a.bindCELExpressionRequest(c)
	if req == nil {
		return
	}
	ast, err := a.accessChecker.VisualAST(c.Request.Context(), userID, req.ResourceType, req.Expression)
	if err != nil {
		abortPolicyRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, ast)
}

// ABACStatusResponse is the JSON body of GET /access_control/status.
type ABACStatusResponse struct {
	Available bool `json:"available"`
}

// handleABACStatus reports whether the server-side ABAC engine is usable;
// the webapp hides all policy UI when it is not.
func (a *API) handleABACStatus(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	c.JSON(http.StatusOK, ABACStatusResponse{
		Available: a.accessChecker.IsAvailable(c.Request.Context(), userID),
	})
}
