// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentexport"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	jiraOrigin       = "https://jira.example.com/mcp"
	newTrackerOrigin = "https://new.example.com/mcp"
	oldTrackerOrigin = "https://old.example.com/mcp"

	plantedHeaderSecret = "planted-mcp-header-secret"
	plantedClientSecret = "planted-mcp-client-secret"
	plantedSASecret     = "planted-mcp-service-account-secret"
	plantedServiceKey   = "planted-llm-api-key"
)

func setupTransferTestEnvironment(t *testing.T) *TestEnvironment {
	t.Helper()
	e := setupAgentTestEnvironment(t)
	cfgStore := e.api.configStore.(*mockConfigStore)
	cfgStore.cfg.Services[0].APIKey = plantedServiceKey
	cfgStore.cfg.MCP = config.MCPConfig{
		Enabled: true,
		Servers: []config.MCPServerConfig{
			{
				ID: "jira-id", Name: "Jira", Enabled: true, BaseURL: jiraOrigin,
				Headers:               map[string]string{"X-Token": plantedHeaderSecret},
				ClientSecret:          plantedClientSecret,
				ServiceAccountHeaders: map[string]string{"Authorization": plantedSASecret},
			},
			{ID: "new-id", Name: "New Tracker", Enabled: true, BaseURL: newTrackerOrigin},
		},
		EmbeddedServer: config.MCPEmbeddedServerConfig{Enabled: true},
	}
	mockLicensed(e.mockAPI)
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	return e
}

func exportDocument(tools ...agentexport.MCPTool) agentexport.Document {
	return agentexport.Document{
		Kind:          agentexport.Kind,
		SchemaVersion: agentexport.SchemaVersion,
		ExportedAt:    1759600000000,
		AgentVersion:  7,
		Agent: agentexport.Agent{
			Name:                  "release-helper",
			DisplayName:           "Release Helper",
			CustomInstructions:    "imported instructions",
			DisableTools:          false,
			MaxToolTurns:          42,
			MCPDynamicToolLoading: false,
			MCPTools:              append([]agentexport.MCPTool{}, tools...),
		},
	}
}

func fullyConfiguredAgent() *llm.BotConfig {
	return &llm.BotConfig{
		ID:                    "agent-1",
		CreatorID:             testUserID,
		BotUserID:             "bot-user-1",
		DisplayName:           "My Agent",
		Name:                  "my-agent",
		ServiceID:             "svc-1",
		Model:                 "secret-model-name",
		CustomInstructions:    "exported instructions",
		ChannelAccessLevel:    llm.ChannelAccessLevelAllow,
		ChannelIDs:            []string{"channel-id-exported"},
		UserAccessLevel:       llm.UserAccessLevelAllow,
		UserIDs:               []string{"user-id-exported"},
		TeamIDs:               []string{"team-id-exported"},
		AdminUserIDs:          []string{"admin-id-exported"},
		EnableVision:          true,
		EnabledNativeTools:    []string{"code_interpreter"},
		ReasoningEnabled:      true,
		ReasoningEffort:       "high",
		ThinkingBudget:        4096,
		UseServiceAccountAuth: false,
		MaxToolTurns:          17,
		MCPDynamicToolLoading: true,
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: jiraOrigin + "/", ToolName: "create_issue"},
			{ServerOrigin: config.MCPEmbeddedServerOrigin, ToolName: "search_posts"},
		},
	}
}

func TestExportAgent(t *testing.T) {
	e := setupTransferTestEnvironment(t)
	defer e.Cleanup(t)
	seedVersionedAgent(t, e, fullyConfiguredAgent(), map[string]any{"customInstructions": "exported instructions v2"})

	recorder := doRequest(e.api, http.MethodGet, "/agents/agent-1/export", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())
	assert.Equal(t, `attachment; filename="my-agent-v2.agent.json"`, recorder.Header().Get("Content-Disposition"))
	assert.Contains(t, recorder.Header().Get("Content-Type"), "application/json")

	body := recorder.Body.String()
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(body), &raw))
	assert.ElementsMatch(t, []string{"kind", "schemaVersion", "exportedAt", "agentVersion", "agent"}, slices.Collect(maps.Keys(raw)))
	var rawAgent map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["agent"], &rawAgent))
	assert.ElementsMatch(t, []string{
		"name", "displayName", "customInstructions", "disableTools", "maxToolTurns",
		"mcpDynamicToolLoading", "autoEnableNewMCPTools", "mcpTools",
	}, slices.Collect(maps.Keys(rawAgent)), "only transferable fields are exported")

	var doc agentexport.Document
	require.NoError(t, json.Unmarshal([]byte(body), &doc))
	assert.Equal(t, agentexport.Kind, doc.Kind)
	assert.Equal(t, 1, doc.SchemaVersion)
	assert.Equal(t, 2, doc.AgentVersion)
	assert.NotZero(t, doc.ExportedAt)
	assert.Equal(t, "my-agent", doc.Agent.Name)
	assert.Equal(t, "My Agent", doc.Agent.DisplayName)
	assert.Equal(t, "exported instructions v2", doc.Agent.CustomInstructions)
	assert.Equal(t, 17, doc.Agent.MaxToolTurns)
	assert.True(t, doc.Agent.MCPDynamicToolLoading)
	assert.Equal(t, []agentexport.MCPTool{
		{ServerOrigin: jiraOrigin, ServerName: "Jira", ToolName: "create_issue"},
		{ServerOrigin: config.MCPEmbeddedServerOrigin, ServerName: "Mattermost", ToolName: "search_posts"},
	}, doc.Agent.MCPTools)

	for _, leaked := range []string{
		plantedHeaderSecret, plantedClientSecret, plantedSASecret, plantedServiceKey,
		"svc-1", "secret-model-name", "channel-id-exported", "user-id-exported", "team-id-exported",
		"admin-id-exported", "bot-user-1", testUserID, "code_interpreter", "agent-1", "useServiceAccountAuth",
	} {
		assert.NotContains(t, body, leaked)
	}
}

func TestPreviewAgentImport(t *testing.T) {
	doc := exportDocument(
		agentexport.MCPTool{ServerOrigin: jiraOrigin + "/", ServerName: "Jira", ToolName: "create_issue"},
		agentexport.MCPTool{ServerOrigin: oldTrackerOrigin, ServerName: "Old Tracker", ToolName: "search"},
		agentexport.MCPTool{ServerOrigin: config.MCPEmbeddedServerOrigin, ServerName: "Mattermost", ToolName: "search_posts"},
	)
	doc.Agent.Name = "my-agent"

	t.Run("maps same-origin servers and finds the existing agent", func(t *testing.T) {
		e := setupTransferTestEnvironment(t)
		defer e.Cleanup(t)
		e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
		e.agentStore.agents["agent-1"] = fullyConfiguredAgent()
		records := e.CaptureAuditRecords()

		recorder := doRequest(e.api, http.MethodPost, "/agents/import/preview", map[string]any{"document": doc}, testUserID)
		require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())

		var resp importAgentPreviewResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
		assert.Equal(t, jiraOrigin, resp.Document.Agent.MCPTools[0].ServerOrigin, "the document is returned normalized")
		assert.Equal(t, []agentexport.ServerGroup{
			{SourceOrigin: jiraOrigin, SourceName: "Jira", ToolNames: []string{"create_issue"}, AutoTargetOrigin: jiraOrigin},
			{SourceOrigin: oldTrackerOrigin, SourceName: "Old Tracker", ToolNames: []string{"search"}, AutoTargetOrigin: ""},
			{SourceOrigin: config.MCPEmbeddedServerOrigin, SourceName: "Mattermost", ToolNames: []string{"search_posts"}, AutoTargetOrigin: config.MCPEmbeddedServerOrigin},
		}, resp.MCPServers)
		assert.Equal(t, []agentexport.Server{
			{Origin: jiraOrigin, Name: "Jira"},
			{Origin: newTrackerOrigin, Name: "New Tracker"},
			{Origin: config.MCPEmbeddedServerOrigin, Name: "Mattermost"},
		}, resp.AvailableMCPServers)
		require.NotNil(t, resp.ExistingAgent)
		assert.Equal(t, importExistingAgent{ID: "agent-1", DisplayName: "My Agent", Username: "my-agent", CanManage: true}, *resp.ExistingAgent)

		assert.NotContains(t, recorder.Body.String(), plantedHeaderSecret)
		assert.Empty(t, *records, "preview is read-only and not audited")
	})

	t.Run("agent managers without create permission may preview", func(t *testing.T) {
		e := setupTransferTestEnvironment(t)
		defer e.Cleanup(t)
		e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(false)
		e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
		e.agentStore.agents["agent-1"] = fullyConfiguredAgent()

		other := exportDocument()
		other.Agent.Name = "unrelated"
		recorder := doRequest(e.api, http.MethodPost, "/agents/import/preview", map[string]any{"document": other}, testUserID)
		require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())
		var resp importAgentPreviewResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
		assert.Nil(t, resp.ExistingAgent)
		assert.Equal(t, []agentexport.ServerGroup{}, resp.MCPServers)
	})

	t.Run("users who can neither create nor manage agents are rejected", func(t *testing.T) {
		e := setupTransferTestEnvironment(t)
		defer e.Cleanup(t)
		e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false)
		agent := fullyConfiguredAgent()
		agent.CreatorID = "someone-else"
		e.agentStore.agents["agent-1"] = agent

		recorder := doRequest(e.api, http.MethodPost, "/agents/import/preview", map[string]any{"document": doc}, testUserID)
		require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
	})

	invalid := []struct {
		name          string
		document      any
		errorContains string
	}{
		{name: "wrong kind", document: map[string]any{"kind": "other", "schemaVersion": 1, "agent": map[string]any{}}, errorContains: "not a Mattermost agent export"},
		{name: "future schema", document: map[string]any{"kind": agentexport.Kind, "schemaVersion": 2, "agent": map[string]any{}}, errorContains: "unsupported agent export schema version 2"},
		{name: "max tool turns out of range", document: map[string]any{"kind": agentexport.Kind, "schemaVersion": 1, "agent": map[string]any{"maxToolTurns": 1000}}, errorContains: "maxToolTurns"},
		{name: "not an object", document: "nope", errorContains: "invalid request body"},
	}
	for _, tt := range invalid {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			e := setupTransferTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)

			recorder := doRequest(e.api, http.MethodPost, "/agents/import/preview", map[string]any{"document": tt.document}, testUserID)
			require.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode)
			var errResp agentErrorResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &errResp))
			assert.Contains(t, errResp.Error, tt.errorContains)
		})
	}
}

func TestImportAgentCreate(t *testing.T) {
	createBody := func(doc agentexport.Document, mappings ...agentexport.ServerMapping) map[string]any {
		return map[string]any{
			"document":          doc,
			"mode":              "create",
			"username":          "imported-agent",
			"displayName":       "Imported Agent",
			"serviceID":         "svc-1",
			"model":             "chosen-model",
			"mcpServerMappings": mappings,
		}
	}

	tests := []struct {
		name           string
		body           map[string]any
		expectedStatus int
		errorContains  string
		expectedTools  []llm.EnabledMCPTool
	}{
		{
			name: "same-origin servers map automatically",
			body: createBody(exportDocument(
				agentexport.MCPTool{ServerOrigin: jiraOrigin, ServerName: "Jira", ToolName: "create_issue"},
				agentexport.MCPTool{ServerOrigin: config.MCPEmbeddedServerOrigin, ServerName: "Mattermost", ToolName: "search_posts"},
			)),
			expectedStatus: http.StatusCreated,
			expectedTools: []llm.EnabledMCPTool{
				{ServerOrigin: jiraOrigin, ToolName: "create_issue"},
				{ServerOrigin: config.MCPEmbeddedServerOrigin, ToolName: "search_posts"},
			},
		},
		{
			name: "unknown server without a mapping is rejected",
			body: createBody(exportDocument(
				agentexport.MCPTool{ServerOrigin: oldTrackerOrigin, ServerName: "Old Tracker", ToolName: "search"},
			)),
			expectedStatus: http.StatusBadRequest,
			errorContains:  `MCP server "Old Tracker" (https://old.example.com/mcp) must be mapped to a server on this instance or removed`,
		},
		{
			name: "explicit mapping and removal",
			body: createBody(exportDocument(
				agentexport.MCPTool{ServerOrigin: oldTrackerOrigin, ServerName: "Old Tracker", ToolName: "search"},
				agentexport.MCPTool{ServerOrigin: "https://gone.example.com", ServerName: "Gone", ToolName: "x"},
				agentexport.MCPTool{ServerOrigin: jiraOrigin, ServerName: "Jira", ToolName: "search"},
			),
				agentexport.ServerMapping{SourceOrigin: oldTrackerOrigin, TargetOrigin: newTrackerOrigin},
				agentexport.ServerMapping{SourceOrigin: "https://gone.example.com", TargetOrigin: ""},
				agentexport.ServerMapping{SourceOrigin: jiraOrigin, TargetOrigin: newTrackerOrigin},
			),
			expectedStatus: http.StatusCreated,
			expectedTools:  []llm.EnabledMCPTool{{ServerOrigin: newTrackerOrigin, ToolName: "search"}},
		},
		{
			name: "mapping target must exist on this server",
			body: createBody(exportDocument(
				agentexport.MCPTool{ServerOrigin: oldTrackerOrigin, ServerName: "Old Tracker", ToolName: "search"},
			), agentexport.ServerMapping{SourceOrigin: oldTrackerOrigin, TargetOrigin: "https://nowhere.example.com"}),
			expectedStatus: http.StatusBadRequest,
			errorContains:  "is not configured on this server",
		},
		{
			name: "auto-enable ignores the tool list and needs no mapping",
			body: func() map[string]any {
				doc := exportDocument(agentexport.MCPTool{ServerOrigin: oldTrackerOrigin, ServerName: "Old Tracker", ToolName: "search"})
				doc.Agent.AutoEnableNewMCPTools = true
				return createBody(doc)
			}(),
			expectedStatus: http.StatusCreated,
			expectedTools:  nil,
		},
		{
			name: "create requires a service",
			body: func() map[string]any {
				body := createBody(exportDocument())
				delete(body, "serviceID")
				return body
			}(),
			expectedStatus: http.StatusBadRequest,
			errorContains:  "serviceID",
		},
		{
			name: "create validates the username like POST /agents",
			body: func() map[string]any {
				body := createBody(exportDocument())
				body["username"] = "Bad Name"
				return body
			}(),
			expectedStatus: http.StatusBadRequest,
			errorContains:  "invalid username",
		},
		{
			name: "unknown mode",
			body: func() map[string]any {
				body := createBody(exportDocument())
				body["mode"] = "merge"
				return body
			}(),
			expectedStatus: http.StatusBadRequest,
			errorContains:  "mode must be",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupTransferTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{UserId: "imported-bot-user"}, nil).Maybe()

			recorder := doRequest(e.api, http.MethodPost, "/agents/import", tt.body, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			if tt.errorContains != "" {
				var errResp agentErrorResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &errResp))
				assert.Contains(t, errResp.Error, tt.errorContains)
				assert.Empty(t, e.agentStore.agents, "a rejected import creates nothing")
				return
			}

			var created llm.BotConfig
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
			stored := e.agentStore.agents[created.ID]
			require.NotNil(t, stored)
			assert.Equal(t, "imported-agent", stored.Name)
			assert.Equal(t, "Imported Agent", stored.DisplayName)
			assert.Equal(t, "svc-1", stored.ServiceID)
			assert.Equal(t, "chosen-model", stored.Model)
			assert.Equal(t, "imported-bot-user", stored.BotUserID)
			assert.Equal(t, testUserID, stored.CreatorID)
			assert.Equal(t, "imported instructions", stored.CustomInstructions)
			assert.Equal(t, 42, stored.MaxToolTurns)
			assert.False(t, stored.MCPDynamicToolLoading)
			assert.Equal(t, tt.expectedTools, stored.EnabledMCPTools)

			// Everything not in the document gets the new-agent UI defaults.
			assert.Equal(t, llm.ChannelAccessLevelAll, stored.ChannelAccessLevel)
			assert.Equal(t, llm.UserAccessLevelAll, stored.UserAccessLevel)
			assert.Empty(t, stored.AdminUserIDs)
			assert.False(t, stored.UseServiceAccountAuth)
			assert.True(t, stored.EnableVision)
			assert.True(t, stored.ReasoningEnabled)
			assert.Equal(t, "medium", stored.ReasoningEffort)
			assert.Equal(t, []string{llm.NativeToolWebSearch}, stored.EnabledNativeTools)

			versions, err := e.agentStore.ListAgentVersions(created.ID)
			require.NoError(t, err)
			require.Len(t, versions, 1)
			assert.Equal(t, store.AgentVersionSourceImport, versions[0].Source)
			assert.Equal(t, testUserID, versions[0].CreatedBy)
		})
	}
}

func TestImportAgentCreatePermissions(t *testing.T) {
	e := setupTransferTestEnvironment(t)
	defer e.Cleanup(t)
	// Manages an agent (passes the import gate) but may not create agents.
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(false)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
	e.agentStore.agents["agent-1"] = fullyConfiguredAgent()

	recorder := doRequest(e.api, http.MethodPost, "/agents/import", map[string]any{
		"document": exportDocument(), "mode": "create",
		"username": "new-agent", "displayName": "New", "serviceID": "svc-1",
	}, testUserID)
	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
	assert.Len(t, e.agentStore.agents, 1)
}

func TestImportAgentUpdate(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(e *TestEnvironment)
		body           map[string]any
		expectedStatus int
		errorContains  string
	}{
		{
			name: "applies only the transferable fields",
			body: map[string]any{
				"mode":     "update",
				"agentID":  "agent-1",
				"username": "ignored-username", "displayName": "Ignored", "serviceID": "ignored", "model": "ignored",
				"document": exportDocument(
					agentexport.MCPTool{ServerOrigin: oldTrackerOrigin, ServerName: "Old Tracker", ToolName: "search"},
				),
				"mcpServerMappings": []agentexport.ServerMapping{{SourceOrigin: oldTrackerOrigin, TargetOrigin: newTrackerOrigin}},
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "agentID is required",
			body:           map[string]any{"mode": "update", "document": exportDocument()},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "agentID is required",
		},
		{
			name:           "unknown agent",
			body:           map[string]any{"mode": "update", "agentID": "missing", "document": exportDocument()},
			expectedStatus: http.StatusNotFound,
		},
		{
			name: "target must be manageable",
			setup: func(e *TestEnvironment) {
				other := fullyConfiguredAgent()
				other.ID = "agent-2"
				other.CreatorID = "someone-else"
				other.BotUserID = "bot-user-2"
				other.Name = "other-agent"
				e.agentStore.agents[other.ID] = other
			},
			body:           map[string]any{"mode": "update", "agentID": "agent-2", "document": exportDocument()},
			expectedStatus: http.StatusForbidden,
		},
		{
			name: "missing mapping is rejected before anything changes",
			body: map[string]any{
				"mode": "update", "agentID": "agent-1",
				"document": exportDocument(agentexport.MCPTool{ServerOrigin: oldTrackerOrigin, ToolName: "search"}),
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "must be mapped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupTransferTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true).Maybe()
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOthersAgent).Return(false).Maybe()
			original := fullyConfiguredAgent()
			e.agentStore.agents[original.ID] = original
			if tt.setup != nil {
				tt.setup(e)
			}

			recorder := doRequest(e.api, http.MethodPost, "/agents/import", tt.body, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			stored := e.agentStore.agents["agent-1"]
			if tt.expectedStatus != http.StatusOK {
				if tt.errorContains != "" {
					var errResp agentErrorResponse
					require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &errResp))
					assert.Contains(t, errResp.Error, tt.errorContains)
				}
				assert.Equal(t, "exported instructions", stored.CustomInstructions)
				assert.Empty(t, e.agentStore.versions["agent-1"])
				return
			}

			// Mission fields come from the document.
			assert.Equal(t, "imported instructions", stored.CustomInstructions)
			assert.Equal(t, 42, stored.MaxToolTurns)
			assert.False(t, stored.MCPDynamicToolLoading)
			assert.Equal(t, []llm.EnabledMCPTool{{ServerOrigin: newTrackerOrigin, ToolName: "search"}}, stored.EnabledMCPTools)

			// Identity, service, model, access, and admins stay as they are.
			want := fullyConfiguredAgent()
			assert.Equal(t, want.Name, stored.Name)
			assert.Equal(t, want.DisplayName, stored.DisplayName)
			assert.Equal(t, want.ServiceID, stored.ServiceID)
			assert.Equal(t, want.Model, stored.Model)
			assert.Equal(t, want.ChannelAccessLevel, stored.ChannelAccessLevel)
			assert.Equal(t, want.ChannelIDs, stored.ChannelIDs)
			assert.Equal(t, want.UserAccessLevel, stored.UserAccessLevel)
			assert.Equal(t, want.UserIDs, stored.UserIDs)
			assert.Equal(t, want.TeamIDs, stored.TeamIDs)
			assert.Equal(t, want.AdminUserIDs, stored.AdminUserIDs)
			assert.Equal(t, want.EnabledNativeTools, stored.EnabledNativeTools)
			assert.Equal(t, want.ReasoningEffort, stored.ReasoningEffort)
			assert.Equal(t, want.UseServiceAccountAuth, stored.UseServiceAccountAuth)

			versions, err := e.agentStore.ListAgentVersions("agent-1")
			require.NoError(t, err)
			require.NotEmpty(t, versions)
			assert.Equal(t, store.AgentVersionSourceImport, versions[0].Source)
			assert.Equal(t, testUserID, versions[0].CreatedBy)
		})
	}
}

func TestAuditImportAgent(t *testing.T) {
	const plantedInstructions = "planted-imported-instructions-that-must-never-leak"
	plantedDocument := func() agentexport.Document {
		doc := exportDocument()
		doc.Agent.CustomInstructions = plantedInstructions
		return doc
	}

	tests := []struct {
		name           string
		body           map[string]any
		expectedStatus int
		validateRecord func(t *testing.T, e *TestEnvironment, rec *model.AuditRecord)
	}{
		{
			name: "create records the new agent and mode",
			body: map[string]any{
				"document": plantedDocument(), "mode": "create",
				"username": "imported-agent", "displayName": "Imported", "serviceID": "svc-1",
			},
			expectedStatus: http.StatusCreated,
			validateRecord: func(t *testing.T, e *TestEnvironment, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				require.Len(t, e.agentStore.agents, 2)
				var createdID string
				for id := range e.agentStore.agents {
					if id != "agent-1" {
						createdID = id
					}
				}
				assert.Equal(t, createdID, rec.EventData.Parameters[audit.KeyAgentID])
				assert.Equal(t, "imported-agent", rec.EventData.Parameters[audit.KeyAgentName])
				assert.Equal(t, "create", rec.EventData.Parameters["mode"])
			},
		},
		{
			name:           "update records the target agent and mode",
			body:           map[string]any{"document": plantedDocument(), "mode": "update", "agentID": "agent-1"},
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, _ *TestEnvironment, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, "agent-1", rec.EventData.Parameters[audit.KeyAgentID])
				assert.Equal(t, "my-agent", rec.EventData.Parameters[audit.KeyAgentName])
				assert.Equal(t, "update", rec.EventData.Parameters["mode"])
				assert.Contains(t, rec.EventData.Parameters["changed_fields"], "customInstructions")
			},
		},
		{
			name: "rejected document records a 400 fail",
			body: map[string]any{
				"document": map[string]any{"kind": "other", "schemaVersion": 1, "agent": map[string]any{"customInstructions": plantedInstructions}},
				"mode":     "create",
			},
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, _ *TestEnvironment, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.Equal(t, "create", rec.EventData.Parameters["mode"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupTransferTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{UserId: "imported-bot-user"}, nil).Maybe()
			e.agentStore.agents["agent-1"] = fullyConfiguredAgent()
			records := e.CaptureAuditRecords()

			recorder := doRequest(e.api, http.MethodPost, "/agents/import", tt.body, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventImportAgent, rec.EventName)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			tt.validateRecord(t, e, rec)

			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), plantedInstructions, "audit record must never carry the document or instructions")
		})
	}
}
