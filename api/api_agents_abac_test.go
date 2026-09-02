// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestUpdateAgentPolicyAutoDeleteOnSwitchAway(t *testing.T) {
	tests := []struct {
		name               string
		prevAccessLevel    llm.UserAccessLevel
		newAccessLevel     llm.UserAccessLevel
		setupMock          func(mockAPI *plugintest.API, agentID string)
		expectedStatus     int
		expectedStoreLevel llm.UserAccessLevel
		expectAuditParams  bool
	}{
		{
			name:            "switch from attribute-based to all deletes policy and enriches audit",
			prevAccessLevel: llm.UserAccessLevelAttributeBased,
			newAccessLevel:  llm.UserAccessLevelAll,
			setupMock: func(mockAPI *plugintest.API, agentID string) {
				mockAPI.On("DeleteAccessControlPolicy", testUserID, accesscontrol.ResourceTypeAgent, agentID).Return(nil).Once()
			},
			expectedStatus:     http.StatusOK,
			expectedStoreLevel: llm.UserAccessLevelAll,
			expectAuditParams:  true,
		},
		{
			name:            "switch from attribute-based to allow deletes policy",
			prevAccessLevel: llm.UserAccessLevelAttributeBased,
			newAccessLevel:  llm.UserAccessLevelAllow,
			setupMock: func(mockAPI *plugintest.API, agentID string) {
				mockAPI.On("DeleteAccessControlPolicy", testUserID, accesscontrol.ResourceTypeAgent, agentID).Return(nil).Once()
			},
			expectedStatus:     http.StatusOK,
			expectedStoreLevel: llm.UserAccessLevelAllow,
			expectAuditParams:  true,
		},
		{
			name:            "switch from attribute-based to block deletes policy",
			prevAccessLevel: llm.UserAccessLevelAttributeBased,
			newAccessLevel:  llm.UserAccessLevelBlock,
			setupMock: func(mockAPI *plugintest.API, agentID string) {
				mockAPI.On("DeleteAccessControlPolicy", testUserID, accesscontrol.ResourceTypeAgent, agentID).Return(nil).Once()
			},
			expectedStatus:     http.StatusOK,
			expectedStoreLevel: llm.UserAccessLevelBlock,
			expectAuditParams:  true,
		},
		{
			name:            "staying on attribute-based does not delete policy",
			prevAccessLevel: llm.UserAccessLevelAttributeBased,
			newAccessLevel:  llm.UserAccessLevelAttributeBased,
			setupMock: func(mockAPI *plugintest.API, agentID string) {
				mockAPI.On("GetAccessControlVisualAST", testUserID, accesscontrol.ResourceTypeAgent, mock.Anything).
					Return(&model.VisualExpression{Conditions: []model.Condition{}}, nil).Maybe()
			},
			expectedStatus:     http.StatusOK,
			expectedStoreLevel: llm.UserAccessLevelAttributeBased,
		},
		{
			name:               "switching between legacy levels does not delete policy",
			prevAccessLevel:    llm.UserAccessLevelAll,
			newAccessLevel:     llm.UserAccessLevelAllow,
			expectedStatus:     http.StatusOK,
			expectedStoreLevel: llm.UserAccessLevelAllow,
		},
		{
			name:            "switching from legacy to attribute-based does not delete policy",
			prevAccessLevel: llm.UserAccessLevelAll,
			newAccessLevel:  llm.UserAccessLevelAttributeBased,
			setupMock: func(mockAPI *plugintest.API, agentID string) {
				mockAPI.On("GetAccessControlVisualAST", testUserID, accesscontrol.ResourceTypeAgent, mock.Anything).
					Return(&model.VisualExpression{Conditions: []model.Condition{}}, nil).Maybe()
			},
			expectedStatus:     http.StatusOK,
			expectedStoreLevel: llm.UserAccessLevelAttributeBased,
		},
		{
			name:            "switch away succeeds even if policy is not found (404)",
			prevAccessLevel: llm.UserAccessLevelAttributeBased,
			newAccessLevel:  llm.UserAccessLevelAll,
			setupMock: func(mockAPI *plugintest.API, agentID string) {
				mockAPI.On("DeleteAccessControlPolicy", testUserID, accesscontrol.ResourceTypeAgent, agentID).
					Return(model.NewAppError("DeleteAccessControlPolicy", "app.access_control.policy_not_found", nil, "policy not found", http.StatusNotFound)).Once()
			},
			expectedStatus:     http.StatusOK,
			expectedStoreLevel: llm.UserAccessLevelAll,
			expectAuditParams:  true,
		},
		{
			name:            "switch away aborts atomically and rolls back if policy delete returns an unexpected error",
			prevAccessLevel: llm.UserAccessLevelAttributeBased,
			newAccessLevel:  llm.UserAccessLevelAll,
			setupMock: func(mockAPI *plugintest.API, agentID string) {
				mockAPI.On("DeleteAccessControlPolicy", testUserID, accesscontrol.ResourceTypeAgent, agentID).
					Return(model.NewAppError("DeleteAccessControlPolicy", "app.access_control.delete_failed", nil, "backend policy storage unreachable", http.StatusInternalServerError)).Once()
			},
			expectedStatus:     http.StatusInternalServerError,
			expectedStoreLevel: llm.UserAccessLevelAttributeBased, // must remain unmodified (rolled back)
			expectAuditParams:  true,                              // audit record still captures ABAC resource identifiers on failure
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)

			records := e.CaptureAuditRecords()

			mockLicensed(e.mockAPI)
			e.api.accessChecker = accesscontrol.New(accesscontrol.PassthroughClient{}, e.mockAPI, accesscontrol.NoMCPServerIDs, nil)
			e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			agentID := "agent-abac-1"
			stored := &llm.BotConfig{
				ID:              agentID,
				CreatorID:       testUserID,
				BotUserID:       "bot-1",
				DisplayName:     "Test ABAC Agent",
				Name:            "test-abac-agent",
				ServiceID:       "svc-1",
				UserAccessLevel: tt.prevAccessLevel,
			}
			e.agentStore.agents[agentID] = stored

			if tt.setupMock != nil {
				tt.setupMock(e.mockAPI, agentID)
			}

			body := updateAgentBodyFromStored(stored, map[string]any{
				"userAccessLevel": int(tt.newAccessLevel),
			})

			recorder := doRequest(e.api, http.MethodPut, "/agents/"+agentID, body, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)

			updated := e.agentStore.agents[agentID]
			require.NotNil(t, updated)
			assert.Equal(t, tt.expectedStoreLevel, updated.UserAccessLevel)

			if tt.expectAuditParams {
				require.NotEmpty(t, *records)
				rec := (*records)[0]
				assert.Equal(t, accesscontrol.ResourceTypeAgent, rec.EventData.Parameters[audit.KeyABACResourceType])
				assert.Equal(t, agentID, rec.EventData.Parameters[audit.KeyABACResourceID])

				raw, err := json.Marshal(rec)
				require.NoError(t, err)
				assert.Contains(t, string(raw), agentID)
			}
		})
	}
}

func TestUpdateAgentPolicyNotDeletedIfStoreUpdateFails(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	mockLicensed(e.mockAPI)
	e.api.accessChecker = accesscontrol.New(accesscontrol.PassthroughClient{}, e.mockAPI, accesscontrol.NoMCPServerIDs, nil)
	e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	agentID := "agent-abac-store-fail"
	stored := &llm.BotConfig{
		ID:              agentID,
		CreatorID:       testUserID,
		BotUserID:       "bot-1",
		DisplayName:     "Test ABAC Agent",
		Name:            "test-abac-agent",
		ServiceID:       "svc-1",
		UserAccessLevel: llm.UserAccessLevelAttributeBased,
	}
	e.agentStore.agents[agentID] = stored
	e.agentStore.updateErr = errors.New("database disk full")

	body := updateAgentBodyFromStored(stored, map[string]any{
		"userAccessLevel": int(llm.UserAccessLevelAll),
	})

	recorder := doRequest(e.api, http.MethodPut, "/agents/"+agentID, body, testUserID)
	require.Equal(t, http.StatusInternalServerError, recorder.Result().StatusCode)

	// DeleteAccessControlPolicy must NOT have been called on mockAPI
	e.mockAPI.AssertNotCalled(t, "DeleteAccessControlPolicy", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpdateAgentPolicyDeleteFailsAndRollbackFails(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)

	records := e.CaptureAuditRecords()

	mockLicensed(e.mockAPI)
	e.api.accessChecker = accesscontrol.New(accesscontrol.PassthroughClient{}, e.mockAPI, accesscontrol.NoMCPServerIDs, nil)
	e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
	// First UpdateAgent succeeds (nil), second UpdateAgent (rollback) fails
	e.agentStore.updateErrs = []error{nil, errors.New("rollback database failure")}

	agentID := "agent-abac-rollback-fail"
	stored := &llm.BotConfig{
		ID:              agentID,
		CreatorID:       testUserID,
		BotUserID:       "bot-1",
		DisplayName:     "Test ABAC Agent",
		Name:            "test-abac-agent",
		ServiceID:       "svc-1",
		UserAccessLevel: llm.UserAccessLevelAttributeBased,
	}
	e.agentStore.agents[agentID] = stored

	e.mockAPI.On("DeleteAccessControlPolicy", testUserID, accesscontrol.ResourceTypeAgent, agentID).
		Return(model.NewAppError("DeleteAccessControlPolicy", "app.access_control.delete_failed", nil, "backend policy storage unreachable", http.StatusInternalServerError)).Once()

	body := updateAgentBodyFromStored(stored, map[string]any{
		"userAccessLevel": int(llm.UserAccessLevelAll),
	})

	recorder := doRequest(e.api, http.MethodPut, "/agents/"+agentID, body, testUserID)
	require.Equal(t, http.StatusInternalServerError, recorder.Result().StatusCode)

	e.mockAPI.AssertCalled(t, "LogError", "Failed to rollback agent after access policy deletion failure", "agent_id", agentID, "rollback_error", "rollback database failure", "delete_error", mock.Anything)

	require.NotEmpty(t, *records)
	rec := (*records)[0]
	assert.Equal(t, accesscontrol.ResourceTypeAgent, rec.EventData.Parameters[audit.KeyABACResourceType])
	assert.Equal(t, agentID, rec.EventData.Parameters[audit.KeyABACResourceID])
}
