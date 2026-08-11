// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// erroringDecisionClient fails every evaluation, as an unlicensed, disabled, or
// unreachable PDP does. The decision tables translate that to an unconditional
// deny.
type erroringDecisionClient struct{}

func (erroringDecisionClient) EvaluateAccessRequest(_ context.Context, _, _, _, _ string) (*model.AccessDecision, error) {
	return nil, errors.New("access control evaluation unavailable")
}

// setupAccessControlTestEnvironment builds an API whose accessChecker proxies
// PAP calls to e.mockAPI.
func setupAccessControlTestEnvironment(t *testing.T) *TestEnvironment {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.api.accessChecker = accesscontrol.New(accesscontrol.PassthroughClient{}, e.mockAPI, accesscontrol.NoMCPServerIDs, nil)
	return e
}

func TestAgentPolicyRouteAuthMatrix(t *testing.T) {
	creatorID := model.NewId()
	agentAdminID := model.NewId()
	othersManagerID := model.NewId()
	unrelatedID := model.NewId()
	agentID := model.NewId()
	missingAgentID := model.NewId()

	storedPolicy := &model.AccessControlPolicy{ID: agentID, Type: accesscontrol.ResourceTypeAgent}

	tests := []struct {
		name       string
		userID     string
		agentID    string
		wantStatus int
	}{
		{name: "creator can read", userID: creatorID, agentID: agentID, wantStatus: http.StatusOK},
		{name: "agent admin can read", userID: agentAdminID, agentID: agentID, wantStatus: http.StatusOK},
		{name: "manage-others-agent can read", userID: othersManagerID, agentID: agentID, wantStatus: http.StatusOK},
		{name: "unrelated user is forbidden", userID: unrelatedID, agentID: agentID, wantStatus: http.StatusForbidden},
		{name: "unauthenticated is unauthorized", userID: "", agentID: agentID, wantStatus: http.StatusUnauthorized},
		{name: "missing agent is not found", userID: creatorID, agentID: missingAgentID, wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)

			e.agentStore.agents[agentID] = &llm.BotConfig{
				ID: agentID, Name: "policyagent", DisplayName: "Policy Agent",
				ServiceID: "svc-1", CreatorID: creatorID, AdminUserIDs: []string{agentAdminID},
			}

			e.mockAPI.On("HasPermissionTo", othersManagerID, model.PermissionManageOthersAgent).Return(true).Maybe()
			e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOthersAgent).Return(false).Maybe()
			e.mockAPI.On("GetAccessControlPolicy", agentID).Return(storedPolicy, nil).Maybe()

			recorder := doRequest(e.api, http.MethodGet, "/agents/"+tt.agentID+"/access_policy", nil, tt.userID)
			require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)

			if tt.wantStatus == http.StatusOK {
				var policy model.AccessControlPolicy
				require.NoError(t, json.NewDecoder(recorder.Body).Decode(&policy))
				assert.Equal(t, agentID, policy.ID)
			}
		})
	}
}

func TestAgentPolicyPutOverwritesIdentity(t *testing.T) {
	creatorID := model.NewId()
	agentID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)

	e.agentStore.agents[agentID] = &llm.BotConfig{
		ID: agentID, Name: "policyagent", DisplayName: "Policy Agent",
		ServiceID: "svc-1", CreatorID: creatorID,
	}

	var savedPolicy *model.AccessControlPolicy
	e.mockAPI.On("SaveAccessControlPolicy", creatorID, mock.AnythingOfType("*model.AccessControlPolicy")).
		Run(func(args mock.Arguments) {
			savedPolicy = args.Get(1).(*model.AccessControlPolicy)
		}).
		Return(&model.AccessControlPolicy{ID: agentID}, nil).Once()

	// Spoofed identity fields must be replaced by route-derived values.
	body := map[string]any{
		"id":      "spoofed-id",
		"type":    "channel",
		"version": "v99",
		"active":  false,
		"rules":   []map[string]any{{"actions": []string{"use"}, "expression": `user.attributes.department == "eng"`}},
	}

	recorder := doRequest(e.api, http.MethodPut, "/agents/"+agentID+"/access_policy", body, creatorID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	require.NotNil(t, savedPolicy)
	assert.Equal(t, agentID, savedPolicy.ID)
	assert.Equal(t, accesscontrol.ResourceTypeAgent, savedPolicy.Type)
	assert.Equal(t, model.AccessControlPolicyVersionV0_5, savedPolicy.Version)
	assert.True(t, savedPolicy.Active)
	assert.Equal(t, "Policy Agent", savedPolicy.Name, "empty name defaults to the agent display name")
}

func TestAgentPolicyDelete(t *testing.T) {
	creatorID := model.NewId()
	agentID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)

	e.agentStore.agents[agentID] = &llm.BotConfig{
		ID: agentID, Name: "policyagent", DisplayName: "Policy Agent",
		ServiceID: "svc-1", CreatorID: creatorID,
	}

	e.mockAPI.On("DeleteAccessControlPolicy", creatorID, accesscontrol.ResourceTypeAgent, agentID).Return(nil).Once()

	recorder := doRequest(e.api, http.MethodDelete, "/agents/"+agentID+"/access_policy", nil, creatorID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
}

func TestServiceAndMCPPolicyRouteAuthMatrix(t *testing.T) {
	adminID := model.NewId()
	nonAdminID := model.NewId()
	serviceID := model.NewId()
	serverID := model.NewId()
	unknownID := model.NewId()

	seedConfig := func(e *TestEnvironment) {
		e.api.configStore = &mockConfigStore{
			cfg: &config.Config{
				Services: []llm.ServiceConfig{{ID: serviceID, Name: "Svc", Type: "openai"}},
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{{ID: serverID, Name: "Ext", Enabled: true, BaseURL: "https://mcp.example.com"}},
				},
			},
		}
	}

	tests := []struct {
		name       string
		userID     string
		path       string
		wantStatus int
	}{
		{name: "admin reads service policy", userID: adminID, path: "/admin/services/" + serviceID + "/access_policy", wantStatus: http.StatusOK},
		{name: "non-admin forbidden on service policy", userID: nonAdminID, path: "/admin/services/" + serviceID + "/access_policy", wantStatus: http.StatusForbidden},
		{name: "unknown service is not found", userID: adminID, path: "/admin/services/" + unknownID + "/access_policy", wantStatus: http.StatusNotFound},
		{name: "admin reads mcp policy", userID: adminID, path: "/admin/mcp/" + serverID + "/access_policy", wantStatus: http.StatusOK},
		{name: "non-admin forbidden on mcp policy", userID: nonAdminID, path: "/admin/mcp/" + serverID + "/access_policy", wantStatus: http.StatusForbidden},
		{name: "unknown mcp server is not found", userID: adminID, path: "/admin/mcp/" + unknownID + "/access_policy", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)
			seedConfig(e)

			e.mockAPI.On("HasPermissionTo", adminID, model.PermissionManageSystem).Return(true).Maybe()
			e.mockAPI.On("HasPermissionTo", nonAdminID, model.PermissionManageSystem).Return(false).Maybe()
			e.mockAPI.On("GetAccessControlPolicy", mock.AnythingOfType("string")).
				Return(&model.AccessControlPolicy{ID: serviceID}, nil).Maybe()

			recorder := doRequest(e.api, http.MethodGet, tt.path, nil, tt.userID)
			require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)
		})
	}
}

// TestLegacyIDPolicyRoutes: resources whose stored ID is a hand-crafted
// legacy string (set via a raw config PUT before server-side minting) can
// never carry a policy — the PDP short-circuits them to no_policy. GET must
// report the policy absent (404) instead of surfacing the upstream "Invalid
// identifier" 400, and writes must fail with an explicit 400.
func TestLegacyIDPolicyRoutes(t *testing.T) {
	adminID := model.NewId()
	const legacyServiceID = "mock-openai"
	const legacyServerID = "my-mcp"

	policyBody := map[string]any{
		"rules": []map[string]any{{"actions": []string{"use"}, "expression": "true"}},
	}

	tests := []struct {
		name       string
		method     string
		path       string
		body       any
		wantStatus int
	}{
		{name: "service GET maps to policy absent", method: http.MethodGet, path: "/admin/services/" + legacyServiceID + "/access_policy", wantStatus: http.StatusNotFound},
		{name: "service PUT rejected explicitly", method: http.MethodPut, path: "/admin/services/" + legacyServiceID + "/access_policy", body: policyBody, wantStatus: http.StatusBadRequest},
		{name: "service DELETE rejected explicitly", method: http.MethodDelete, path: "/admin/services/" + legacyServiceID + "/access_policy", wantStatus: http.StatusBadRequest},
		{name: "mcp GET maps to policy absent", method: http.MethodGet, path: "/admin/mcp/" + legacyServerID + "/access_policy", wantStatus: http.StatusNotFound},
		{name: "mcp PUT rejected explicitly", method: http.MethodPut, path: "/admin/mcp/" + legacyServerID + "/access_policy", body: policyBody, wantStatus: http.StatusBadRequest},
		{name: "mcp DELETE rejected explicitly", method: http.MethodDelete, path: "/admin/mcp/" + legacyServerID + "/access_policy", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.configStore = &mockConfigStore{
				cfg: &config.Config{
					Services: []llm.ServiceConfig{{ID: legacyServiceID, Name: "Mock OpenAI", Type: "openaicompatible"}},
					MCP: config.MCPConfig{
						Servers: []config.MCPServerConfig{{ID: legacyServerID, Name: "Legacy", Enabled: true, BaseURL: "https://mcp.example.com"}},
					},
				},
			}

			e.mockAPI.On("HasPermissionTo", adminID, model.PermissionManageSystem).Return(true).Maybe()
			// No GetAccessControlPolicy / Save / Delete expectations: the
			// gate must short-circuit before any upstream policy call.

			recorder := doRequest(e.api, tt.method, tt.path, tt.body, adminID)
			require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)
		})
	}
}

func TestCELRouteAuthMatrix(t *testing.T) {
	managerID := model.NewId()    // has ManageOwnAgent
	agentAdminID := model.NewId() // manages one agent via AdminUserIDs only
	plainID := model.NewId()
	agentID := model.NewId()

	body := map[string]any{
		"resource_type": accesscontrol.ResourceTypeAgent,
		"expression":    `user.attributes.department == "eng"`,
	}

	tests := []struct {
		name       string
		userID     string
		query      string
		wantStatus int
	}{
		{name: "agent manager allowed", userID: managerID, wantStatus: http.StatusOK},
		{name: "per-agent admin with agent_id allowed", userID: agentAdminID, query: "?agent_id=" + agentID, wantStatus: http.StatusOK},
		{name: "per-agent admin without agent_id forbidden", userID: agentAdminID, wantStatus: http.StatusForbidden},
		{name: "plain user forbidden", userID: plainID, wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)

			e.agentStore.agents[agentID] = &llm.BotConfig{
				ID: agentID, Name: "celagent", DisplayName: "CEL Agent",
				ServiceID: "svc-1", CreatorID: model.NewId(), AdminUserIDs: []string{agentAdminID},
			}

			e.mockAPI.On("HasPermissionTo", managerID, model.PermissionManageOwnAgent).Return(true).Maybe()
			e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOwnAgent).Return(false).Maybe()
			e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOthersAgent).Return(false).Maybe()
			e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(false).Maybe()
			e.mockAPI.On("CheckAccessControlExpression", tt.userID, accesscontrol.ResourceTypeAgent, mock.AnythingOfType("string")).
				Return([]model.CELExpressionError{}, nil).Maybe()

			recorder := doRequest(e.api, http.MethodPost, "/access_control/cel/check"+tt.query, body, tt.userID)
			require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)
		})
	}
}

func TestCELCheckRejectsForeignResourceType(t *testing.T) {
	userID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)

	e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageOwnAgent).Return(true).Maybe()

	body := map[string]any{
		"resource_type": "channel", // core type, not plugin-addressable here
		"expression":    "true",
	}
	recorder := doRequest(e.api, http.MethodPost, "/access_control/cel/check", body, userID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

// Empty matching sets come back from the PAP with Users == nil after gob RPC.
// The JSON body must still emit "users": [] so the host TestResultsModal can
// spread the list without throwing.
func TestCELTestNormalizesNilUsers(t *testing.T) {
	userID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)

	e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageOwnAgent).Return(true).Maybe()
	e.mockAPI.On("QueryUsersForAccessControlExpression", userID, accesscontrol.ResourceTypeService, mock.AnythingOfType("string"), "", "", 0).
		Return(&model.AccessControlPolicyTestResponse{Users: nil, Total: 0}, nil).Once()

	body := map[string]any{
		"resource_type": accesscontrol.ResourceTypeService,
		"expression":    `user.attributes.is_a_cool_guy == "true"`,
		"term":          "",
		"after":         "",
		"limit":         0,
	}
	recorder := doRequest(e.api, http.MethodPost, "/access_control/cel/test", body, userID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var raw map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&raw))
	require.Equal(t, json.RawMessage("[]"), raw["users"])
}

// perIDDecisionClient denies specific resource IDs; everything else is no_policy.
type perIDDecisionClient struct {
	denied map[string]bool
}

func (c perIDDecisionClient) EvaluateAccessRequest(_ context.Context, _, _, resourceID, _ string) (*model.AccessDecision, error) {
	if c.denied[resourceID] {
		return &model.AccessDecision{Decision: false}, nil
	}
	noPolicy := model.NewNoPolicyAccessDecision()
	return &noPolicy, nil
}

// seedServiceConfig registers a service with a valid stable ID so ABAC
// evaluation actually runs (invalid IDs short-circuit to no_policy).
func seedServiceConfig(e *TestEnvironment, serviceID string) {
	e.api.configStore = &mockConfigStore{
		cfg: &config.Config{
			Services: []llm.ServiceConfig{{ID: serviceID, Name: "Gated Service", Type: "openai"}},
		},
	}
}

func TestCreateAgentDeniedServiceReturns403(t *testing.T) {
	serviceID := model.NewId()
	userID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)
	seedServiceConfig(e, serviceID)
	e.api.accessChecker = accesscontrol.New(perIDDecisionClient{denied: map[string]bool{serviceID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := createAgentBody(map[string]any{"serviceID": serviceID})
	recorder := doRequest(e.api, http.MethodPost, "/agents", body, userID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)

	var errResp agentErrorResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&errResp))
	assert.Contains(t, errResp.Error, "service")
}

func TestCreateAgentAttributeBasedWhileUnavailableReturns400(t *testing.T) {
	serviceID := model.NewId()
	userID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)
	seedServiceConfig(e, serviceID)
	// nil plugin API: the PAP availability probe has nothing to ask, so
	// IsAvailable reports false and the attribute-based save is rejected.
	e.api.accessChecker = accesscontrol.New(accesscontrol.PassthroughClient{}, nil, accesscontrol.NoMCPServerIDs, nil)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	body := createAgentBody(map[string]any{
		"serviceID":       serviceID,
		"userAccessLevel": int(llm.UserAccessLevelAttributeBased),
	})
	recorder := doRequest(e.api, http.MethodPost, "/agents", body, userID)
	require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
}

func TestUpdateAgentUnchangedDeniedServiceSucceeds(t *testing.T) {
	serviceID := model.NewId()
	userID := model.NewId()
	agentID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)
	seedServiceConfig(e, serviceID)
	// The service is denied to the editor, but it is a pre-existing
	// assignment: unrelated edits must not be blocked.
	e.api.accessChecker = accesscontrol.New(perIDDecisionClient{denied: map[string]bool{serviceID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	stored := &llm.BotConfig{
		ID: agentID, CreatorID: userID, BotUserID: "bot-1",
		DisplayName: "Original", Name: "original", ServiceID: serviceID,
	}
	e.agentStore.agents[agentID] = stored

	body := updateAgentBodyFromStored(stored, map[string]any{"displayName": "Renamed"})
	recorder := doRequest(e.api, http.MethodPut, "/agents/"+agentID, body, userID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
}

func TestListAgentsFiltersPolicyDeniedAgents(t *testing.T) {
	userID := model.NewId()
	deniedAgentID := model.NewId()
	allowedAgentID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)
	e.api.accessChecker = accesscontrol.New(perIDDecisionClient{denied: map[string]bool{deniedAgentID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOthersAgent).Return(false).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	creatorID := model.NewId() // not the requesting user: no admin bypass
	e.agentStore.agents[deniedAgentID] = &llm.BotConfig{
		ID: deniedAgentID, Name: "denied", DisplayName: "Denied", ServiceID: "svc-1", CreatorID: creatorID,
	}
	e.agentStore.agents[allowedAgentID] = &llm.BotConfig{
		ID: allowedAgentID, Name: "allowed", DisplayName: "Allowed", ServiceID: "svc-1", CreatorID: creatorID,
	}

	recorder := doRequest(e.api, http.MethodGet, "/agents", nil, userID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var agents []llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agents))
	require.Len(t, agents, 1)
	assert.Equal(t, allowedAgentID, agents[0].ID)
}

// TestABACStatusRoute drives the status endpoint through the availability probe,
// which asks the server to render a trivial CEL expression: an AST means the
// server is past its ABAC readiness gate, a readiness error means it is not.
func TestABACStatusRoute(t *testing.T) {
	unlicensed := model.NewAppError("Init", "app.pap.init.app_error", nil, "enterprise advanced license required", http.StatusNotImplemented)
	abacDisabled := model.NewAppError("isReady", "app.pap.is_ready.app_error", nil, "access control is disabled", http.StatusNotAcceptable)

	tests := []struct {
		name          string
		probeAST      *model.VisualExpression
		probeErr      *model.AppError
		wantAvailable bool
	}{
		{name: "ready", probeAST: &model.VisualExpression{Conditions: []model.Condition{}}, wantAvailable: true},
		{name: "unlicensed", probeErr: unlicensed, wantAvailable: false},
		{name: "ABAC disabled", probeErr: abacDisabled, wantAvailable: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("GetAccessControlVisualAST", mock.AnythingOfType("string"), accesscontrol.ResourceTypeAgent, mock.AnythingOfType("string")).
				Return(tt.probeAST, tt.probeErr).Once()

			recorder := doRequest(e.api, http.MethodGet, "/access_control/status", nil, model.NewId())
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

			var status ABACStatusResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&status))
			assert.Equal(t, tt.wantAvailable, status.Available)
		})
	}
}

// seedTwoServiceConfig registers two services with valid stable IDs.
func seedTwoServiceConfig(e *TestEnvironment, firstID, secondID string) {
	e.api.configStore = &mockConfigStore{
		cfg: &config.Config{
			Services: []llm.ServiceConfig{
				{ID: firstID, Name: "Allowed Service", Type: "openai"},
				{ID: secondID, Name: "Gated Service", Type: "openai"},
			},
		},
	}
}

// System admins get the full catalog; everyone else is filtered through
// CanUseService — including the evaluation-error row, which fails closed.
func TestListServicesAppliesServicePolicies(t *testing.T) {
	allowedID := model.NewId()
	gatedID := model.NewId()

	tests := []struct {
		name    string
		isAdmin bool
		checker func() *accesscontrol.Checker
		wantIDs []string
	}{
		{
			name: "non-admin loses denied service",
			checker: func() *accesscontrol.Checker {
				return accesscontrol.New(perIDDecisionClient{denied: map[string]bool{gatedID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)
			},
			wantIDs: []string{allowedID},
		},
		{
			name:    "system admin keeps the full catalog",
			isAdmin: true,
			checker: func() *accesscontrol.Checker {
				return accesscontrol.New(perIDDecisionClient{denied: map[string]bool{gatedID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)
			},
			wantIDs: []string{allowedID, gatedID},
		},
		{
			name: "an evaluation error fails closed for every service",
			checker: func() *accesscontrol.Checker {
				return accesscontrol.New(erroringDecisionClient{}, nil, accesscontrol.NoMCPServerIDs, nil)
			},
			wantIDs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := model.NewId()
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)
			seedTwoServiceConfig(e, allowedID, gatedID)
			e.api.accessChecker = tt.checker()

			mockLicensed(e.mockAPI)
			e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageSystem).Return(tt.isAdmin).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			recorder := doRequest(e.api, http.MethodGet, "/services", nil, userID)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

			var services []ServiceInfo
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&services))
			var gotIDs []string
			for _, svc := range services {
				gotIDs = append(gotIDs, svc.ID)
			}
			assert.ElementsMatch(t, tt.wantIDs, gotIDs)
		})
	}
}

// On POST /agents/models/fetch a non-admin probing a denied service gets 403;
// admins bypass the policy (and then fail later on missing credentials, 400).
func TestFetchModelsForServiceAppliesServicePolicies(t *testing.T) {
	serviceID := model.NewId()

	tests := []struct {
		name       string
		isAdmin    bool
		checker    func() *accesscontrol.Checker
		wantStatus int
	}{
		{
			name: "non-admin denied service is 403",
			checker: func() *accesscontrol.Checker {
				return accesscontrol.New(perIDDecisionClient{denied: map[string]bool{serviceID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:    "admin bypasses the policy",
			isAdmin: true,
			checker: func() *accesscontrol.Checker {
				return accesscontrol.New(perIDDecisionClient{denied: map[string]bool{serviceID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)
			},
			wantStatus: http.StatusBadRequest, // missing credentials, past the policy gate
		},
		{
			name: "an evaluation error fails closed",
			checker: func() *accesscontrol.Checker {
				return accesscontrol.New(erroringDecisionClient{}, nil, accesscontrol.NoMCPServerIDs, nil)
			},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := model.NewId()
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)
			seedServiceConfig(e, serviceID)
			e.api.accessChecker = tt.checker()

			mockLicensed(e.mockAPI)
			e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageSystem).Return(tt.isAdmin).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			body := map[string]string{"serviceID": serviceID}
			recorder := doRequest(e.api, http.MethodPost, "/agents/models/fetch", body, userID)
			require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)
		})
	}
}
