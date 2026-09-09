// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAdminConfigDoesNotMintIDs(t *testing.T) {
	result := normalizeAdminConfig(config.Config{
		Services: []llm.ServiceConfig{
			{Name: "no-id", Type: llm.ServiceTypeOpenAI},
		},
		MCP: config.MCPConfig{
			Servers: []config.MCPServerConfig{
				{Name: "srv-no-id", BaseURL: "https://one.example.com"},
			},
			PluginServers: []config.PluginServerConfig{
				{PluginID: "com.example.a", Name: "A", Path: "/mcp"},
			},
		},
	})

	assert.Empty(t, result.Services[0].ID)
	assert.True(t, result.Services[0].UseResponsesAPI)
	assert.Empty(t, result.MCP.Servers[0].ID)
	assert.Empty(t, result.MCP.EmbeddedServer.ID)
	assert.Empty(t, result.MCP.PluginServers[0].ID)
	assert.True(t, result.MCP.Enabled)
	assert.True(t, result.MCP.EmbeddedServer.Enabled)
}

func TestMintEmptyAdminIDsAssignsStableIDs(t *testing.T) {
	tests := []struct {
		name     string
		cfg      config.Config
		validate func(t *testing.T, result config.Config)
	}{
		{
			name: "empty service and MCP server IDs get fresh valid IDs",
			cfg: config.Config{
				Services: []llm.ServiceConfig{
					{Name: "no-id"},
				},
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{Name: "srv-no-id", BaseURL: "https://one.example.com"},
					},
					PluginServers: []config.PluginServerConfig{
						{PluginID: "com.example.a", Name: "A", Path: "/mcp"},
					},
				},
			},
			validate: func(t *testing.T, result config.Config) {
				assert.True(t, model.IsValidId(result.Services[0].ID))
				assert.True(t, model.IsValidId(result.MCP.Servers[0].ID))
				assert.True(t, model.IsValidId(result.MCP.EmbeddedServer.ID))
				assert.True(t, model.IsValidId(result.MCP.PluginServers[0].ID))
			},
		},
		{
			name: "non-empty IDs untouched",
			cfg: config.Config{
				Services: []llm.ServiceConfig{
					{ID: "svc-existing", Name: "has-id"},
				},
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "mcp-existing", Name: "srv-has-id", BaseURL: "https://one.example.com"},
					},
					EmbeddedServer: config.MCPEmbeddedServerConfig{ID: "embedded-existing", Enabled: true},
					PluginServers: []config.PluginServerConfig{
						{ID: "plugin-existing", PluginID: "com.example.a", Name: "A", Path: "/mcp"},
					},
				},
			},
			validate: func(t *testing.T, result config.Config) {
				assert.Equal(t, "svc-existing", result.Services[0].ID)
				assert.Equal(t, "mcp-existing", result.MCP.Servers[0].ID)
				assert.Equal(t, "embedded-existing", result.MCP.EmbeddedServer.ID)
				assert.Equal(t, "plugin-existing", result.MCP.PluginServers[0].ID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(t, mintEmptyAdminIDs(tt.cfg))
		})
	}
}

// TestHandleSaveConfigMCPServerIDs: incoming uniqueness, then plugin overlay +
// embedded copy, then mint empty IDs. Duplicate incoming IDs return 409.
func TestHandleSaveConfigMCPServerIDs(t *testing.T) {
	tests := []struct {
		name           string
		storedCfg      *config.Config
		payloadCfg     config.Config
		expectedStatus int
		validate       func(t *testing.T, store *testConfigStore)
	}{
		{
			name: "payload without id mints a new remote server ID",
			storedCfg: &config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "stable-id", Name: "Jira", BaseURL: "https://jira.example.com"},
					},
					EmbeddedServer: config.MCPEmbeddedServerConfig{ID: "embedded-stable", Enabled: true},
					PluginServers: []config.PluginServerConfig{
						{ID: "plugin-stable", PluginID: "com.example.a", Name: "A", Path: "/mcp"},
					},
				},
			},
			payloadCfg: config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{Name: "Jira", BaseURL: "https://jira.example.com"},
					},
					EmbeddedServer: config.MCPEmbeddedServerConfig{Enabled: true},
					PluginServers: []config.PluginServerConfig{
						{PluginID: "com.example.a", Name: "A", Path: "/mcp/v2"},
					},
				},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 1)
				assert.True(t, model.IsValidId(store.cfg.MCP.Servers[0].ID))
				assert.NotEqual(t, "stable-id", store.cfg.MCP.Servers[0].ID)
				assert.Equal(t, "embedded-stable", store.cfg.MCP.EmbeddedServer.ID)
				require.Len(t, store.cfg.MCP.PluginServers, 1)
				assert.Equal(t, "plugin-stable", store.cfg.MCP.PluginServers[0].ID)
			},
		},
		{
			name: "renamed ID-less server is treated as create",
			storedCfg: &config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "stable-id", Name: "Old Name", BaseURL: "https://jira.example.com"},
					},
				},
			},
			payloadCfg: config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{Name: "New Name", BaseURL: "https://jira.example.com"},
					},
				},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 1)
				assert.True(t, model.IsValidId(store.cfg.MCP.Servers[0].ID))
				assert.NotEqual(t, "stable-id", store.cfg.MCP.Servers[0].ID)
			},
		},
		{
			name: "add flow: existing ID kept and ID-less sibling minted",
			storedCfg: &config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "stable-id", Name: "Jira", BaseURL: "https://jira.example.com"},
					},
				},
			},
			payloadCfg: config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "stable-id", Name: "Jira", BaseURL: "https://jira.example.com"},
						{Name: "Brand New", BaseURL: "https://new.example.com"},
					},
				},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 2)
				assert.Equal(t, "stable-id", store.cfg.MCP.Servers[0].ID)
				assert.True(t, model.IsValidId(store.cfg.MCP.Servers[1].ID))
				assert.NotEqual(t, "stable-id", store.cfg.MCP.Servers[1].ID)
			},
		},
		{
			name: "duplicate incoming remote IDs return 409",
			storedCfg: &config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "stable-id", Name: "Jira", BaseURL: "https://jira.example.com"},
					},
				},
			},
			payloadCfg: config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "stable-id", Name: "Jira", BaseURL: "https://jira.example.com"},
						{ID: "stable-id", Name: "Copy", BaseURL: "https://copy.example.com"},
					},
				},
			},
			expectedStatus: http.StatusConflict,
			validate: func(t *testing.T, store *testConfigStore) {
				require.NotNil(t, store.cfg)
				require.Len(t, store.cfg.MCP.Servers, 1)
				assert.Equal(t, "stable-id", store.cfg.MCP.Servers[0].ID, "stored config must be untouched on rejection")
			},
		},
		{
			name:      "no stored config still assigns ids",
			storedCfg: nil,
			payloadCfg: config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{Name: "Jira", BaseURL: "https://jira.example.com"},
					},
				},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 1)
				assert.True(t, model.IsValidId(store.cfg.MCP.Servers[0].ID))
			},
		},
		{
			name: "caller-chosen ID for a same-named stored server is kept",
			storedCfg: &config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "stable-id", Name: "Jira", BaseURL: "https://jira.example.com"},
					},
				},
			},
			payloadCfg: config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "invented-by-client", Name: "Jira", BaseURL: "https://jira.example.com"},
					},
				},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 1)
				assert.Equal(t, "invented-by-client", store.cfg.MCP.Servers[0].ID)
			},
		},
		{
			name:      "caller-chosen ID on first write is kept",
			storedCfg: nil,
			payloadCfg: config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "seeded-by-client", Name: "Jira", BaseURL: "https://jira.example.com"},
					},
				},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 1)
				assert.Equal(t, "seeded-by-client", store.cfg.MCP.Servers[0].ID)
			},
		},
		{
			name:      "duplicate IDs on first write return 409",
			storedCfg: nil,
			payloadCfg: config.Config{
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{ID: "seeded-by-client", Name: "Jira", BaseURL: "https://jira.example.com"},
						{ID: "seeded-by-client", Name: "Copy", BaseURL: "https://copy.example.com"},
					},
				},
			},
			expectedStatus: http.StatusConflict,
			validate: func(t *testing.T, store *testConfigStore) {
				assert.Nil(t, store.cfg, "nothing must be persisted on rejection")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &testConfigStore{cfg: tt.storedCfg}
			updater := &testConfigUpdater{}
			notifier := &testClusterNotifier{}
			router := setupTestRouter(store, updater, notifier)

			body, err := json.Marshal(tt.payloadCfg)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedStatus == http.StatusConflict {
				assert.Equal(t, 0, updater.callCount, "in-memory config must not be updated on rejection")
				assert.Equal(t, 0, notifier.callCount, "cluster must not be notified on rejection")
			}
			if tt.validate != nil {
				tt.validate(t, store)
			}
		})
	}
}

// TestHandleSaveConfigPreservesOmittedPluginServers: full config save never
// accepts client-provided plugin_servers — omitted and stale non-empty lists
// both leave persisted plugin rows (and their IDs) untouched.
func TestHandleSaveConfigPreservesOmittedPluginServers(t *testing.T) {
	pluginA := "abcdefghijklmnopqrstuvwx0a"
	pluginB := "abcdefghijklmnopqrstuvwx0b"
	embeddedID := "abcdefghijklmnopqrstuvwx0e"
	remoteID := "abcdefghijklmnopqrstuvwx0r"

	storedPlugins := []config.PluginServerConfig{
		{ID: pluginA, PluginID: "com.example.a", Name: "A", Path: "/mcp", Enabled: true},
		{ID: pluginB, PluginID: "com.example.b", Name: "B", Path: "/other", Enabled: false},
	}

	tests := []struct {
		name    string
		mcp     map[string]any
		wantIDs []string
	}{
		{
			name: "omitted plugin_servers preserves prev",
			mcp: map[string]any{
				"enabled": true,
				"servers": []map[string]any{
					{"id": remoteID, "name": "Remote", "baseURL": "https://mcp.example.com", "enabled": true},
				},
				"embeddedServer": map[string]any{"enabled": true},
			},
			wantIDs: []string{pluginA, pluginB},
		},
		{
			name: "stale non-empty plugin_servers ignored",
			mcp: map[string]any{
				"enabled": true,
				"servers": []map[string]any{
					{"id": remoteID, "name": "Remote", "baseURL": "https://mcp.example.com", "enabled": true},
				},
				"embeddedServer": map[string]any{"enabled": true},
				"plugin_servers": []map[string]any{
					{
						"id": "attackeridattackeridattack", "plugin_id": "com.example.a",
						"name": "Hacked", "path": "/evil", "enabled": false,
					},
				},
			},
			wantIDs: []string{pluginA, pluginB},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &testConfigStore{cfg: &config.Config{
				MCP: config.MCPConfig{
					Enabled: true,
					Servers: []config.MCPServerConfig{
						{ID: remoteID, Name: "Remote", BaseURL: "https://mcp.example.com", Enabled: true},
					},
					EmbeddedServer: config.MCPEmbeddedServerConfig{ID: embeddedID, Enabled: true},
					PluginServers:  append([]config.PluginServerConfig(nil), storedPlugins...),
				},
			}}
			router := setupTestRouter(store, &testConfigUpdater{}, &testClusterNotifier{})

			body, err := json.Marshal(map[string]any{"mcp": tt.mcp})
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)

			require.Len(t, store.cfg.MCP.PluginServers, len(tt.wantIDs))
			for i, id := range tt.wantIDs {
				assert.Equal(t, id, store.cfg.MCP.PluginServers[i].ID)
				assert.Equal(t, storedPlugins[i].PluginID, store.cfg.MCP.PluginServers[i].PluginID)
				assert.Equal(t, storedPlugins[i].Name, store.cfg.MCP.PluginServers[i].Name)
				assert.Equal(t, storedPlugins[i].Path, store.cfg.MCP.PluginServers[i].Path)
				assert.Equal(t, storedPlugins[i].Enabled, store.cfg.MCP.PluginServers[i].Enabled)
			}
			assert.Equal(t, remoteID, store.cfg.MCP.Servers[0].ID)
			assert.Equal(t, embeddedID, store.cfg.MCP.EmbeddedServer.ID)
		})
	}
}

func TestHandleSaveConfigRejectsCrossKindMCPIDConflict(t *testing.T) {
	sharedID := "abcdefghijklmnopqrstuvwxzz"
	store := &testConfigStore{cfg: &config.Config{
		MCP: config.MCPConfig{
			EmbeddedServer: config.MCPEmbeddedServerConfig{ID: sharedID, Enabled: true},
		},
	}}
	router := setupTestRouter(store, &testConfigUpdater{}, &testClusterNotifier{})

	payload := config.Config{
		MCP: config.MCPConfig{
			Servers: []config.MCPServerConfig{
				{ID: sharedID, Name: "Remote", BaseURL: "https://mcp.example.com", Enabled: true},
			},
			EmbeddedServer: config.MCPEmbeddedServerConfig{ID: sharedID, Enabled: true},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusConflict, w.Code)
}

// TestHandleSaveConfigMintsServiceIDs: empty service ID means create
// (mintEmptyAdminIDs); explicit IDs are kept, including a new ID for
// a same-named stored service. Duplicate incoming IDs return 409.
func TestHandleSaveConfigMintsServiceIDs(t *testing.T) {
	stored := func() *config.Config {
		return &config.Config{
			Services: []llm.ServiceConfig{
				{ID: "stable-svc-id", Name: "OpenAI", Type: "openai"},
			},
		}
	}

	tests := []struct {
		name            string
		storedCfg       *config.Config
		payloadServices []llm.ServiceConfig
		expectedStatus  int
		validate        func(t *testing.T, store *testConfigStore)
	}{
		{
			name:      "payload without id mints a new service ID",
			storedCfg: stored(),
			payloadServices: []llm.ServiceConfig{
				{Name: "OpenAI", Type: "openai"},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.Services, 1)
				assert.True(t, model.IsValidId(store.cfg.Services[0].ID))
				assert.NotEqual(t, "stable-svc-id", store.cfg.Services[0].ID)
			},
		},
		{
			name:      "ID-less payload with field edits is treated as create",
			storedCfg: stored(),
			payloadServices: []llm.ServiceConfig{
				{Name: "OpenAI", Type: "anthropic", APIURL: "https://edited.example.com"},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.Services, 1)
				assert.True(t, model.IsValidId(store.cfg.Services[0].ID))
				assert.NotEqual(t, "stable-svc-id", store.cfg.Services[0].ID)
			},
		},
		{
			name:      "add flow: ID-less sibling is minted",
			storedCfg: stored(),
			payloadServices: []llm.ServiceConfig{
				{ID: "stable-svc-id", Name: "OpenAI", Type: "openai"},
				{Name: "Brand New", Type: "anthropic"},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.Services, 2)
				assert.Equal(t, "stable-svc-id", store.cfg.Services[0].ID)
				assert.True(t, model.IsValidId(store.cfg.Services[1].ID))
			},
		},
		{
			name:      "caller-chosen ID for a same-named service is kept",
			storedCfg: stored(),
			payloadServices: []llm.ServiceConfig{
				{ID: "invented-by-client", Name: "OpenAI", Type: "openai"},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.Services, 1)
				assert.Equal(t, "invented-by-client", store.cfg.Services[0].ID)
			},
		},
		{
			name:      "duplicate incoming IDs return 409",
			storedCfg: stored(),
			payloadServices: []llm.ServiceConfig{
				{ID: "stable-svc-id", Name: "OpenAI", Type: "openai"},
				{ID: "stable-svc-id", Name: "Copy", Type: "openai"},
			},
			expectedStatus: http.StatusConflict,
		},
		{
			name:      "no stored config still assigns ids",
			storedCfg: nil,
			payloadServices: []llm.ServiceConfig{
				{Name: "OpenAI", Type: "openai"},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.Services, 1)
				assert.True(t, model.IsValidId(store.cfg.Services[0].ID))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &testConfigStore{cfg: tt.storedCfg}
			router := setupTestRouter(store, &testConfigUpdater{}, &testClusterNotifier{})

			body, err := json.Marshal(config.Config{Services: tt.payloadServices})
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, tt.expectedStatus, w.Code)
			if tt.validate != nil {
				tt.validate(t, store)
			}
		})
	}
}

// TestHandleSaveConfigRejectsLegacyUUIDServiceIDs covers a save that carries
// dashed UUID service IDs after the ABAC ID migration: the save must fail with
// 400 and leave the migrated config untouched, while a valid payload is accepted.
func TestHandleSaveConfigRejectsLegacyUUIDServiceIDs(t *testing.T) {
	migrated := &config.Config{
		Services: []llm.ServiceConfig{
			{ID: "migrated26charidmigrated26", Name: "OpenAI", Type: "openai"},
		},
	}
	store := &testConfigStore{cfg: migrated, serviceIDMigrationDone: true}
	updater := &testConfigUpdater{}
	notifier := &testClusterNotifier{}
	router := setupTestRouter(store, updater, notifier)

	put := func(cfg config.Config) *httptest.ResponseRecorder {
		body, err := json.Marshal(cfg)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// Payload with a dashed UUID service ID (invalid post-migration format).
	stale := config.Config{
		Services: []llm.ServiceConfig{
			{ID: "550e8400-e29b-41d4-a716-446655440000", Name: "OpenAI", Type: "openai"},
		},
	}
	w := put(stale)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "migrated26charidmigrated26", store.cfg.Services[0].ID, "migrated ID must survive the rejected save")
	assert.Equal(t, 0, updater.callCount, "in-memory config must not be updated on rejection")
	assert.Equal(t, 0, notifier.callCount, "cluster must not be notified on rejection")

	// Fresh payload with the migrated ID is accepted.
	fresh := config.Config{
		Services: []llm.ServiceConfig{
			{ID: "migrated26charidmigrated26", Name: "OpenAI Renamed", Type: "openai"},
		},
	}
	w = put(fresh)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OpenAI Renamed", store.cfg.Services[0].Name)
	assert.Equal(t, 1, updater.callCount)
	assert.Equal(t, 1, notifier.callCount)
}
