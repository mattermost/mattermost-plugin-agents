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
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// erroringDecisionClient fails every evaluation; the decision tables deny.
type erroringDecisionClient struct{}

func (erroringDecisionClient) EvaluateAccessRequest(_ context.Context, _, _, _, _ string) (*model.AccessDecision, error) {
	return nil, errors.New("access control evaluation unavailable")
}

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

func TestAgentPolicyWriteRequiresAdminWhenServiceAccountOn(t *testing.T) {
	const plantedCEL = `user.attributes.department == "planted-sa-policy-secret"`

	managerID := model.NewId()
	sysadminID := model.NewId()
	agentID := model.NewId()

	policyBody := map[string]any{
		"rules": []map[string]any{{"actions": []string{"use"}, "expression": plantedCEL}},
	}

	tests := []struct {
		name          string
		userID        string
		saOn          bool
		method        string
		body          any
		wantStatus    int
		wantEvent     string
		wantAuditFail bool
		expectSave    bool
		expectDelete  bool
		expectGet     bool
	}{
		{
			name:   "SA on manager PUT is forbidden",
			userID: managerID, saOn: true, method: http.MethodPut, body: policyBody,
			wantStatus: http.StatusForbidden, wantEvent: AuditEventPutAgentPolicy, wantAuditFail: true,
		},
		{
			name:   "SA on manager DELETE is forbidden",
			userID: managerID, saOn: true, method: http.MethodDelete,
			wantStatus: http.StatusForbidden, wantEvent: AuditEventDeleteAgentPolicy, wantAuditFail: true,
		},
		{
			name:   "SA on manager GET still allowed",
			userID: managerID, saOn: true, method: http.MethodGet,
			wantStatus: http.StatusOK, expectGet: true,
		},
		{
			name:   "SA on sysadmin PUT succeeds",
			userID: sysadminID, saOn: true, method: http.MethodPut, body: policyBody,
			wantStatus: http.StatusOK, wantEvent: AuditEventPutAgentPolicy, expectSave: true,
		},
		{
			name:   "SA on sysadmin DELETE succeeds",
			userID: sysadminID, saOn: true, method: http.MethodDelete,
			wantStatus: http.StatusOK, wantEvent: AuditEventDeleteAgentPolicy, expectDelete: true,
		},
		{
			name:   "SA off manager PUT still allowed",
			userID: managerID, method: http.MethodPut, body: policyBody,
			wantStatus: http.StatusOK, wantEvent: AuditEventPutAgentPolicy, expectSave: true,
		},
		{
			name:   "SA off manager DELETE still allowed",
			userID: managerID, method: http.MethodDelete,
			wantStatus: http.StatusOK, wantEvent: AuditEventDeleteAgentPolicy, expectDelete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)

			e.agentStore.agents[agentID] = &llm.BotConfig{
				ID: agentID, Name: "policyagent", DisplayName: "Policy Agent",
				ServiceID: "svc-1", CreatorID: managerID, AdminUserIDs: []string{sysadminID},
				UseServiceAccountAuth: tt.saOn,
			}

			e.mockAPI.On("HasPermissionTo", sysadminID, model.PermissionManageSystem).Return(true).Maybe()
			e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(false).Maybe()
			e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOthersAgent).Return(false).Maybe()

			if tt.expectGet {
				e.mockAPI.On("GetAccessControlPolicy", agentID).Return(&model.AccessControlPolicy{ID: agentID}, nil).Once()
			}
			if tt.expectSave {
				e.mockAPI.On("SaveAccessControlPolicy", tt.userID, mock.AnythingOfType("*model.AccessControlPolicy")).
					Return(&model.AccessControlPolicy{ID: agentID}, nil).Once()
			}
			if tt.expectDelete {
				e.mockAPI.On("DeleteAccessControlPolicy", tt.userID, accesscontrol.ResourceTypeAgent, agentID).Return(nil).Once()
			}

			records := e.CaptureAuditRecords()
			recorder := doRequest(e.api, tt.method, "/agents/"+agentID+"/access_policy", tt.body, tt.userID)
			require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)

			if tt.wantEvent == "" {
				assert.Empty(t, *records, "read-only policy GET must not emit an audit record")
				return
			}

			require.Len(t, *records, 1)
			rec := (*records)[0]
			assert.Equal(t, tt.wantEvent, rec.EventName)
			if tt.wantAuditFail {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusForbidden, rec.Error.Code)
			} else {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
			}

			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), plantedCEL, "audit record must never carry policy expression content")
		})
	}
}

func TestAuditAccessPolicyMutations(t *testing.T) {
	const plantedCEL = `user.attributes.department == "planted-secret-cel-expr"`

	creatorID := model.NewId()
	adminID := model.NewId()
	agentID := model.NewId()
	serviceID := model.NewId()
	serverID := model.NewId()

	policyBody := map[string]any{
		"rules": []map[string]any{{"actions": []string{"use"}, "expression": plantedCEL}},
	}

	tests := []struct {
		name           string
		method         string
		path           string
		body           any
		userID         string
		event          string
		resourceType   string
		resourceID     string
		expectedStatus int
	}{
		{
			name:           "agent PUT records identifiers and never policy content",
			method:         http.MethodPut,
			path:           "/agents/" + agentID + "/access_policy",
			body:           policyBody,
			userID:         creatorID,
			event:          AuditEventPutAgentPolicy,
			resourceType:   accesscontrol.ResourceTypeAgent,
			resourceID:     agentID,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "agent DELETE records identifiers",
			method:         http.MethodDelete,
			path:           "/agents/" + agentID + "/access_policy",
			userID:         creatorID,
			event:          AuditEventDeleteAgentPolicy,
			resourceType:   accesscontrol.ResourceTypeAgent,
			resourceID:     agentID,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "service PUT records identifiers and never policy content",
			method:         http.MethodPut,
			path:           "/admin/services/" + serviceID + "/access_policy",
			body:           policyBody,
			userID:         adminID,
			event:          AuditEventPutServicePolicy,
			resourceType:   accesscontrol.ResourceTypeService,
			resourceID:     serviceID,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "service DELETE records identifiers",
			method:         http.MethodDelete,
			path:           "/admin/services/" + serviceID + "/access_policy",
			userID:         adminID,
			event:          AuditEventDeleteServicePolicy,
			resourceType:   accesscontrol.ResourceTypeService,
			resourceID:     serviceID,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "mcp PUT records identifiers and never policy content",
			method:         http.MethodPut,
			path:           "/admin/mcp/" + serverID + "/access_policy",
			body:           policyBody,
			userID:         adminID,
			event:          AuditEventPutMCPPolicy,
			resourceType:   accesscontrol.ResourceTypeMCP,
			resourceID:     serverID,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "mcp DELETE records identifiers",
			method:         http.MethodDelete,
			path:           "/admin/mcp/" + serverID + "/access_policy",
			userID:         adminID,
			event:          AuditEventDeleteMCPPolicy,
			resourceType:   accesscontrol.ResourceTypeMCP,
			resourceID:     serverID,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)

			e.agentStore.agents[agentID] = &llm.BotConfig{
				ID: agentID, Name: "policyagent", DisplayName: "Policy Agent",
				ServiceID: serviceID, CreatorID: creatorID,
			}
			e.api.configStore = &mockConfigStore{
				cfg: &config.Config{
					Services: []llm.ServiceConfig{{ID: serviceID, Name: "Svc", Type: "openai"}},
					MCP: config.MCPConfig{
						Servers: []config.MCPServerConfig{{ID: serverID, Name: "Ext", Enabled: true, BaseURL: "https://mcp.example.com"}},
					},
				},
			}

			e.mockAPI.On("HasPermissionTo", adminID, model.PermissionManageSystem).Return(true).Maybe()
			e.mockAPI.On("SaveAccessControlPolicy", mock.Anything, mock.AnythingOfType("*model.AccessControlPolicy")).
				Return(&model.AccessControlPolicy{ID: tt.resourceID}, nil).Maybe()
			e.mockAPI.On("DeleteAccessControlPolicy", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

			records := e.CaptureAuditRecords()
			recorder := doRequest(e.api, tt.method, tt.path, tt.body, tt.userID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1)
			rec := (*records)[0]
			assert.Equal(t, tt.event, rec.EventName)
			assert.Equal(t, model.AuditStatusSuccess, rec.Status)
			assert.Equal(t, tt.resourceType, rec.EventData.Parameters[audit.KeyABACResourceType])
			assert.Equal(t, tt.resourceID, rec.EventData.Parameters[audit.KeyABACResourceID])

			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), plantedCEL, "audit record must never carry policy expression content")
		})
	}
}

func TestAuditAccessPolicyGetsEmitNothing(t *testing.T) {
	creatorID := model.NewId()
	adminID := model.NewId()
	agentID := model.NewId()
	serviceID := model.NewId()
	serverID := model.NewId()

	tests := []struct {
		name   string
		userID string
		path   string
	}{
		{name: "agent GET", userID: creatorID, path: "/agents/" + agentID + "/access_policy"},
		{name: "service GET", userID: adminID, path: "/admin/services/" + serviceID + "/access_policy"},
		{name: "mcp GET", userID: adminID, path: "/admin/mcp/" + serverID + "/access_policy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)

			e.agentStore.agents[agentID] = &llm.BotConfig{
				ID: agentID, Name: "policyagent", DisplayName: "Policy Agent",
				ServiceID: serviceID, CreatorID: creatorID,
			}
			e.api.configStore = &mockConfigStore{
				cfg: &config.Config{
					Services: []llm.ServiceConfig{{ID: serviceID, Name: "Svc", Type: "openai"}},
					MCP: config.MCPConfig{
						Servers: []config.MCPServerConfig{{ID: serverID, Name: "Ext", Enabled: true, BaseURL: "https://mcp.example.com"}},
					},
				},
			}
			e.mockAPI.On("HasPermissionTo", adminID, model.PermissionManageSystem).Return(true).Maybe()
			e.mockAPI.On("GetAccessControlPolicy", mock.AnythingOfType("string")).
				Return(&model.AccessControlPolicy{ID: agentID}, nil).Maybe()

			records := e.CaptureAuditRecords()
			recorder := doRequest(e.api, http.MethodGet, tt.path, nil, tt.userID)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
			assert.Empty(t, *records, "read-only policy GET must not emit an audit record")
		})
	}
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

func TestEmbeddedAndPluginMCPPolicyRoutes(t *testing.T) {
	adminID := model.NewId()
	embeddedID := model.NewId()
	pluginID := model.NewId()
	unknownID := model.NewId()
	const pluginServerPluginID = "com.example.mcp"

	policyBody := map[string]any{
		"rules": []map[string]any{{"actions": []string{"use"}, "expression": `user.attributes.department == "eng"`}},
	}

	seedConfig := func(e *TestEnvironment) {
		e.api.configStore = &mockConfigStore{
			cfg: &config.Config{
				MCP: config.MCPConfig{
					EmbeddedServer: config.MCPEmbeddedServerConfig{ID: embeddedID, Enabled: true},
					PluginServers: []config.PluginServerConfig{{
						ID: pluginID, PluginID: pluginServerPluginID, Name: "Plugin MCP", Enabled: true, Path: "/mcp",
					}},
				},
			},
		}
	}

	tests := []struct {
		name       string
		method     string
		serverID   string
		body       any
		wantStatus int
		wantName   string // non-empty: assert SaveAccessControlPolicy received this Name
	}{
		{name: "GET embedded policy", method: http.MethodGet, serverID: embeddedID, wantStatus: http.StatusOK},
		{name: "PUT embedded policy", method: http.MethodPut, serverID: embeddedID, body: policyBody, wantStatus: http.StatusOK, wantName: "Mattermost"},
		{name: "DELETE embedded policy", method: http.MethodDelete, serverID: embeddedID, wantStatus: http.StatusOK},
		{name: "GET plugin policy", method: http.MethodGet, serverID: pluginID, wantStatus: http.StatusOK},
		{name: "PUT plugin policy", method: http.MethodPut, serverID: pluginID, body: policyBody, wantStatus: http.StatusOK, wantName: "Plugin MCP"},
		{name: "DELETE plugin policy", method: http.MethodDelete, serverID: pluginID, wantStatus: http.StatusOK},
		{name: "unknown MCP server is not found", method: http.MethodGet, serverID: unknownID, wantStatus: http.StatusNotFound},
		{name: "PUT unknown MCP server is not found", method: http.MethodPut, serverID: unknownID, body: policyBody, wantStatus: http.StatusNotFound},
		{name: "DELETE unknown MCP server is not found", method: http.MethodDelete, serverID: unknownID, wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)
			seedConfig(e)

			e.mockAPI.On("HasPermissionTo", adminID, model.PermissionManageSystem).Return(true).Maybe()
			e.mockAPI.On("GetAccessControlPolicy", mock.AnythingOfType("string")).
				Return(&model.AccessControlPolicy{ID: tt.serverID}, nil).Maybe()
			e.mockAPI.On("DeleteAccessControlPolicy", adminID, accesscontrol.ResourceTypeMCP, tt.serverID).
				Return(nil).Maybe()

			var savedPolicy *model.AccessControlPolicy
			e.mockAPI.On("SaveAccessControlPolicy", adminID, mock.AnythingOfType("*model.AccessControlPolicy")).
				Run(func(args mock.Arguments) {
					savedPolicy = args.Get(1).(*model.AccessControlPolicy)
				}).
				Return(&model.AccessControlPolicy{ID: tt.serverID}, nil).Maybe()

			path := "/admin/mcp/" + tt.serverID + "/access_policy"
			recorder := doRequest(e.api, tt.method, path, tt.body, adminID)
			require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)

			if tt.wantName != "" {
				require.NotNil(t, savedPolicy)
				assert.Equal(t, tt.wantName, savedPolicy.Name)
				assert.Equal(t, tt.serverID, savedPolicy.ID)
				assert.Equal(t, accesscontrol.ResourceTypeMCP, savedPolicy.Type)
			}
		})
	}
}

func TestPluginMCPPolicyPutFallsBackNameToPluginID(t *testing.T) {
	adminID := model.NewId()
	pluginServerID := model.NewId()
	const pluginServerPluginID = "com.example.unnamed"

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)
	e.api.configStore = &mockConfigStore{
		cfg: &config.Config{
			MCP: config.MCPConfig{
				PluginServers: []config.PluginServerConfig{{
					ID: pluginServerID, PluginID: pluginServerPluginID, Enabled: true, Path: "/mcp",
				}},
			},
		},
	}

	e.mockAPI.On("HasPermissionTo", adminID, model.PermissionManageSystem).Return(true).Maybe()
	var savedPolicy *model.AccessControlPolicy
	e.mockAPI.On("SaveAccessControlPolicy", adminID, mock.AnythingOfType("*model.AccessControlPolicy")).
		Run(func(args mock.Arguments) {
			savedPolicy = args.Get(1).(*model.AccessControlPolicy)
		}).
		Return(&model.AccessControlPolicy{ID: pluginServerID}, nil).Once()

	body := map[string]any{
		"rules": []map[string]any{{"actions": []string{"use"}, "expression": "true"}},
	}
	recorder := doRequest(e.api, http.MethodPut, "/admin/mcp/"+pluginServerID+"/access_policy", body, adminID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	require.NotNil(t, savedPolicy)
	assert.Equal(t, pluginServerPluginID, savedPolicy.Name)
}

// Legacy IDs can never carry a policy. GET reports absent (404) instead of the
// upstream "Invalid identifier" 400; writes fail with an explicit 400.
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

func mockCELPermissions(e *TestEnvironment, userID string, ownAgent, sysAdmin bool) {
	e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageOwnAgent).Return(ownAgent).Maybe()
	e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOwnAgent).Return(false).Maybe()
	e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOthersAgent).Return(false).Maybe()
	e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageSystem).Return(sysAdmin).Maybe()
	e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(false).Maybe()
}

func TestCELRouteAuthMatrix(t *testing.T) {
	managerID := model.NewId()    // has ManageOwnAgent
	agentAdminID := model.NewId() // manages one agent via AdminUserIDs only
	plainID := model.NewId()
	agentID := model.NewId()

	checkBody := map[string]any{
		"resource_type": accesscontrol.ResourceTypeAgent,
		"expression":    `user.attributes.department == "eng"`,
	}

	routes := []struct {
		name   string
		method string
		path   string
		body   any
		setup  func(e *TestEnvironment, userID string)
	}{
		{
			name: "check", method: http.MethodPost, path: "/access_control/cel/check", body: checkBody,
			setup: func(e *TestEnvironment, userID string) {
				e.mockAPI.On("CheckAccessControlExpression", userID, accesscontrol.ResourceTypeAgent, mock.AnythingOfType("string")).
					Return([]model.CELExpressionError{}, nil).Maybe()
			},
		},
		{
			name: "autocomplete", method: http.MethodGet, path: "/access_control/cel/autocomplete/fields",
			setup: func(e *TestEnvironment, userID string) {
				e.mockAPI.On("GetAccessControlFieldsAutocomplete", userID, "", 0).
					Return([]*model.PropertyField{}, nil).Maybe()
			},
		},
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

	for _, route := range routes {
		for _, tt := range tests {
			t.Run(route.name+"/"+tt.name, func(t *testing.T) {
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
				route.setup(e, tt.userID)

				recorder := doRequest(e.api, route.method, route.path+tt.query, route.body, tt.userID)
				require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)
			})
		}
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

func TestCELTestAuthAndSelfInclusion(t *testing.T) {
	const (
		username         = "cel-tester"
		sentinelEmail    = "sentinel-email@example.test"
		sentinelAuthData = "uid=sentinel,dc=example,dc=com"
	)
	expression := `user.attributes.department == "eng"`

	type setupKind int
	const (
		setupNone setupKind = iota
		setupSysadminQuery
		setupSelfCheckMiss
		setupSelfCheckHit
	)

	tests := []struct {
		name         string
		ownAgent     bool
		sysAdmin     bool
		resourceType string
		setup        setupKind
		wantStatus   int
		wantEmpty    bool
		wantUserIDs  int // expected number of users when wantStatus is 200 and not empty
	}{
		{
			name:         "sysadmin agent test returns the unrestricted list",
			sysAdmin:     true,
			resourceType: accesscontrol.ResourceTypeAgent,
			setup:        setupSysadminQuery,
			wantStatus:   http.StatusOK,
			wantUserIDs:  2,
		},
		{
			name:         "sysadmin service test is allowed",
			sysAdmin:     true,
			resourceType: accesscontrol.ResourceTypeService,
			setup:        setupSysadminQuery,
			wantStatus:   http.StatusOK,
			wantUserIDs:  2,
		},
		{
			name:         "sysadmin mcp test is allowed",
			sysAdmin:     true,
			resourceType: accesscontrol.ResourceTypeMCP,
			setup:        setupSysadminQuery,
			wantStatus:   http.StatusOK,
			wantUserIDs:  2,
		},
		{
			name:         "manage_own_agent service test is forbidden",
			ownAgent:     true,
			resourceType: accesscontrol.ResourceTypeService,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "manage_own_agent mcp test is forbidden",
			ownAgent:     true,
			resourceType: accesscontrol.ResourceTypeMCP,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "plain user agent test is forbidden",
			resourceType: accesscontrol.ResourceTypeAgent,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "manage_own_agent self-check miss returns empty not 403",
			ownAgent:     true,
			resourceType: accesscontrol.ResourceTypeAgent,
			setup:        setupSelfCheckMiss,
			wantStatus:   http.StatusOK,
			wantEmpty:    true,
		},
		{
			name:         "manage_own_agent self-check hit returns the full sanitized list",
			ownAgent:     true,
			resourceType: accesscontrol.ResourceTypeAgent,
			setup:        setupSelfCheckHit,
			wantStatus:   http.StatusOK,
			wantUserIDs:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := model.NewId()
			otherID := model.NewId()
			matchedSelf := &model.User{Id: userID, Username: username}
			matchedOther := &model.User{
				Id:       otherID,
				Username: "other-user",
				Email:    sentinelEmail,
				AuthData: model.NewPointer(sentinelAuthData),
			}

			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)

			mockCELPermissions(e, userID, tt.ownAgent, tt.sysAdmin)

			switch tt.setup {
			case setupSysadminQuery:
				e.mockAPI.On("QueryUsersForAccessControlExpression", userID, tt.resourceType, mock.AnythingOfType("string"), "", "", 0).
					Return(&model.AccessControlPolicyTestResponse{
						Users: []*model.User{matchedSelf, matchedOther},
						Total: 2,
					}, nil).Once()
			case setupSelfCheckMiss:
				e.mockAPI.On("GetUser", userID).Return(&model.User{Id: userID, Username: username}, nil).Once()
				e.mockAPI.On("QueryUsersForAccessControlExpression", userID, tt.resourceType, mock.AnythingOfType("string"), username, "", 10).
					Return(&model.AccessControlPolicyTestResponse{
						Users: []*model.User{matchedOther},
						Total: 1,
					}, nil).Once()
			case setupSelfCheckHit:
				e.mockAPI.On("GetUser", userID).Return(&model.User{Id: userID, Username: username}, nil).Once()
				e.mockAPI.On("QueryUsersForAccessControlExpression", userID, tt.resourceType, mock.AnythingOfType("string"), username, "", 10).
					Return(&model.AccessControlPolicyTestResponse{
						Users: []*model.User{matchedSelf},
						Total: 1,
					}, nil).Once()
				e.mockAPI.On("QueryUsersForAccessControlExpression", userID, tt.resourceType, mock.AnythingOfType("string"), "", "", 0).
					Return(&model.AccessControlPolicyTestResponse{
						Users: []*model.User{matchedSelf, matchedOther},
						Total: 2,
					}, nil).Once()
			}

			records := e.CaptureAuditRecords()
			body := map[string]any{
				"resource_type": tt.resourceType,
				"expression":    expression,
				"term":          "",
				"after":         "",
				"limit":         0,
			}
			recorder := doRequest(e.api, http.MethodPost, "/access_control/cel/test", body, userID)
			require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)
			assert.Empty(t, *records, "CEL test is not a mutating route and must not emit an audit record")

			if tt.wantStatus != http.StatusOK {
				return
			}

			raw := recorder.Body.String()
			assert.NotContains(t, raw, sentinelAuthData)

			var parsed model.AccessControlPolicyTestResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &parsed))
			require.NotNil(t, parsed.Users)

			if tt.wantEmpty {
				assert.Empty(t, parsed.Users)
				assert.Equal(t, int64(0), parsed.Total)
				assert.NotContains(t, raw, otherID)
				assert.NotContains(t, raw, sentinelEmail)
				return
			}

			require.Len(t, parsed.Users, tt.wantUserIDs)
			assert.Equal(t, int64(tt.wantUserIDs), parsed.Total)
			gotIDs := make([]string, 0, len(parsed.Users))
			for _, u := range parsed.Users {
				gotIDs = append(gotIDs, u.Id)
			}
			assert.ElementsMatch(t, []string{userID, otherID}, gotIDs)
			if !tt.sysAdmin {
				assert.NotContains(t, raw, sentinelEmail)
			}
		})
	}
}

// Empty matching sets come back from the PAP with Users == nil after gob RPC.
// The JSON body must still emit "users": [] so the host TestResultsModal can
// spread the list without throwing.
func TestCELTestNormalizesNilUsers(t *testing.T) {
	userID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)

	mockCELPermissions(e, userID, false, true)
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

func TestCELTestSanitizesReturnedUsers(t *testing.T) {
	const (
		sentinelEmail    = "sentinel-email@example.test"
		sentinelAuthData = "uid=sentinel,dc=example,dc=com"
		sentinelFirst    = "SentinelFirst"
		sentinelLast     = "SentinelLast"
		sentinelAuthSvc  = "sentinel-authservice"
		keepUsername     = "keep-username"
		testerUsername   = "cel-tester"
	)

	tests := []struct {
		name            string
		isAdmin         bool
		showEmail       bool
		showFullName    bool
		wantEmail       bool
		wantNames       bool
		wantAuthService bool
	}{
		{
			name: "non-admin privacy off hides email names and auth",
		},
		{
			name:         "non-admin privacy on keeps email and names not auth",
			showEmail:    true,
			showFullName: true,
			wantEmail:    true,
			wantNames:    true,
		},
		{
			name:      "non-admin show email only",
			showEmail: true,
			wantEmail: true,
		},
		{
			name:         "non-admin show full name only",
			showFullName: true,
			wantNames:    true,
		},
		{
			name:            "system admin sees email names authservice despite privacy off",
			isAdmin:         true,
			wantEmail:       true,
			wantNames:       true,
			wantAuthService: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := model.NewId()
			matched := &model.User{
				Id:          model.NewId(),
				Username:    keepUsername,
				Email:       sentinelEmail,
				FirstName:   sentinelFirst,
				LastName:    sentinelLast,
				AuthService: sentinelAuthSvc,
				AuthData:    model.NewPointer(sentinelAuthData),
			}

			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)

			e.OverrideConfig(&model.Config{
				PrivacySettings: model.PrivacySettings{
					ShowEmailAddress: model.NewPointer(tt.showEmail),
					ShowFullName:     model.NewPointer(tt.showFullName),
				},
			})
			mockCELPermissions(e, userID, !tt.isAdmin, tt.isAdmin)
			if !tt.isAdmin {
				e.mockAPI.On("GetUser", userID).Return(&model.User{Id: userID, Username: testerUsername}, nil).Once()
				e.mockAPI.On("QueryUsersForAccessControlExpression", userID, accesscontrol.ResourceTypeAgent, mock.AnythingOfType("string"), testerUsername, "", 10).
					Return(&model.AccessControlPolicyTestResponse{
						Users: []*model.User{{Id: userID, Username: testerUsername}},
						Total: 1,
					}, nil).Once()
			}
			e.mockAPI.On("QueryUsersForAccessControlExpression", userID, accesscontrol.ResourceTypeAgent, mock.AnythingOfType("string"), "", "", 0).
				Return(&model.AccessControlPolicyTestResponse{
					Users: []*model.User{matched},
					Total: 1,
				}, nil).Once()

			body := map[string]any{
				"resource_type": accesscontrol.ResourceTypeAgent,
				"expression":    `user.attributes.department == "eng"`,
				"term":          "",
				"after":         "",
				"limit":         0,
			}
			recorder := doRequest(e.api, http.MethodPost, "/access_control/cel/test", body, userID)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

			raw := recorder.Body.String()
			assert.Contains(t, raw, matched.Id)
			assert.Contains(t, raw, keepUsername)
			assert.NotContains(t, raw, sentinelAuthData)

			if tt.wantEmail {
				assert.Contains(t, raw, sentinelEmail)
			} else {
				assert.NotContains(t, raw, sentinelEmail)
			}
			if tt.wantNames {
				assert.Contains(t, raw, sentinelFirst)
				assert.Contains(t, raw, sentinelLast)
			} else {
				assert.NotContains(t, raw, sentinelFirst)
				assert.NotContains(t, raw, sentinelLast)
			}
			if tt.wantAuthService {
				assert.Contains(t, raw, sentinelAuthSvc)
			} else {
				assert.NotContains(t, raw, sentinelAuthSvc)
			}
		})
	}
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

// Unchanged pre-existing service assignments are not re-checked on write.
// List/get still hide the agent from non-sysadmins when CanUseService denies.
func TestUpdateAgentUnchangedDeniedServiceSucceeds(t *testing.T) {
	serviceID := model.NewId()
	userID := model.NewId()
	agentID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)
	seedServiceConfig(e, serviceID)
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

// GET /agents: non-sysadmins need agent access and CanUseService; sysadmins
// still see agents whose service policy would deny them.
func TestListAgentsAppliesServicePolicies(t *testing.T) {
	deniedServiceID := model.NewId()
	allowedServiceID := model.NewId()
	agentOnDeniedSvc := model.NewId()
	agentOnAllowedSvc := model.NewId()
	agentPolicyDenied := model.NewId()
	creatorID := model.NewId()

	tests := []struct {
		name      string
		isAdmin   bool
		deniedIDs map[string]bool
		wantIDs   []string
	}{
		{
			name:      "non-sysadmin loses agent whose service is denied",
			deniedIDs: map[string]bool{deniedServiceID: true, agentPolicyDenied: true},
			wantIDs:   []string{agentOnAllowedSvc},
		},
		{
			name:      "system admin keeps agent whose service is denied",
			isAdmin:   true,
			deniedIDs: map[string]bool{deniedServiceID: true, agentPolicyDenied: true},
			wantIDs:   []string{agentOnDeniedSvc, agentOnAllowedSvc},
		},
		{
			// agentPolicyDenied stays in deniedIDs so wantIDs also assert that
			// an agent-policy deny is filtered even when its service is allowed.
			name:      "non-sysadmin keeps agent when service is allowed",
			deniedIDs: map[string]bool{agentPolicyDenied: true},
			wantIDs:   []string{agentOnDeniedSvc, agentOnAllowedSvc},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := model.NewId()
			e := setupAccessControlTestEnvironment(t)
			defer e.Cleanup(t)
			seedTwoServiceConfig(e, allowedServiceID, deniedServiceID)
			e.api.accessChecker = accesscontrol.New(perIDDecisionClient{denied: tt.deniedIDs}, nil, accesscontrol.NoMCPServerIDs, nil)

			mockLicensed(e.mockAPI)
			e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOthersAgent).Return(false).Maybe()
			e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageSystem).Return(tt.isAdmin).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			e.agentStore.agents[agentOnDeniedSvc] = &llm.BotConfig{
				ID: agentOnDeniedSvc, Name: "denied-svc", DisplayName: "Denied Service Agent",
				ServiceID: deniedServiceID, CreatorID: creatorID, UserAccessLevel: llm.UserAccessLevelAll,
			}
			e.agentStore.agents[agentOnAllowedSvc] = &llm.BotConfig{
				ID: agentOnAllowedSvc, Name: "allowed-svc", DisplayName: "Allowed Service Agent",
				ServiceID: allowedServiceID, CreatorID: creatorID, UserAccessLevel: llm.UserAccessLevelAll,
			}
			e.agentStore.agents[agentPolicyDenied] = &llm.BotConfig{
				ID: agentPolicyDenied, Name: "policy-denied", DisplayName: "Policy Denied Agent",
				ServiceID: allowedServiceID, CreatorID: creatorID, UserAccessLevel: llm.UserAccessLevelAll,
			}

			recorder := doRequest(e.api, http.MethodGet, "/agents", nil, userID)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

			var agents []llm.BotConfig
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agents))
			gotIDs := make([]string, 0, len(agents))
			for _, agent := range agents {
				gotIDs = append(gotIDs, agent.ID)
			}
			assert.ElementsMatch(t, tt.wantIDs, gotIDs)
		})
	}
}

func TestListAgentsKeepsAgentWhenFallbackServiceDenied(t *testing.T) {
	primaryID := model.NewId()
	fallbackID := model.NewId()
	plainServiceID := model.NewId()
	agentWithDeniedFallback := model.NewId()
	agentOnPlainService := model.NewId()
	creatorID := model.NewId()
	userID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)
	e.api.configStore = &mockConfigStore{
		cfg: &config.Config{
			Services: []llm.ServiceConfig{
				{ID: primaryID, Name: "Primary", Type: llm.ServiceTypeOpenAI, APIKey: "sk-primary", FallbackServiceID: fallbackID},
				{ID: fallbackID, Name: "Fallback", Type: llm.ServiceTypeOpenAI, APIKey: "sk-fallback"},
				{ID: plainServiceID, Name: "Plain", Type: llm.ServiceTypeOpenAI, APIKey: "sk-plain"},
			},
		},
	}
	e.api.accessChecker = accesscontrol.New(perIDDecisionClient{denied: map[string]bool{fallbackID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOthersAgent).Return(false).Maybe()
	e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageSystem).Return(false).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.agents[agentWithDeniedFallback] = &llm.BotConfig{
		ID: agentWithDeniedFallback, Name: "fallback-gated", DisplayName: "Fallback Gated Agent",
		ServiceID: primaryID, CreatorID: creatorID, UserAccessLevel: llm.UserAccessLevelAll,
	}
	e.agentStore.agents[agentOnPlainService] = &llm.BotConfig{
		ID: agentOnPlainService, Name: "plain", DisplayName: "Plain Agent",
		ServiceID: plainServiceID, CreatorID: creatorID, UserAccessLevel: llm.UserAccessLevelAll,
	}

	recorder := doRequest(e.api, http.MethodGet, "/agents", nil, userID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var agents []llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agents))
	gotIDs := make([]string, 0, len(agents))
	for _, agent := range agents {
		gotIDs = append(gotIDs, agent.ID)
	}
	assert.ElementsMatch(t, []string{agentWithDeniedFallback, agentOnPlainService}, gotIDs)
}

// Per-agent admins (and creators) do not bypass service ABAC on list/get —
// only PermissionManageSystem does.
func TestListAgentsHidesDeniedServiceFromAgentAdmin(t *testing.T) {
	serviceID := model.NewId()
	agentID := model.NewId()
	userID := model.NewId()

	e := setupAccessControlTestEnvironment(t)
	defer e.Cleanup(t)
	seedServiceConfig(e, serviceID)
	e.api.accessChecker = accesscontrol.New(perIDDecisionClient{denied: map[string]bool{serviceID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)

	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageOthersAgent).Return(false).Maybe()
	e.mockAPI.On("HasPermissionTo", userID, model.PermissionManageSystem).Return(false).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	e.agentStore.agents[agentID] = &llm.BotConfig{
		ID: agentID, Name: "mine", DisplayName: "Mine",
		ServiceID: serviceID, CreatorID: userID, UserAccessLevel: llm.UserAccessLevelNone,
	}

	recorder := doRequest(e.api, http.MethodGet, "/agents", nil, userID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var agents []llm.BotConfig
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&agents))
	assert.Empty(t, agents)
}

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
