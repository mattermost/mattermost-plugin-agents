// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testConfigStore is a simple in-memory implementation of ConfigStore for testing.
type testConfigStore struct {
	cfg     *config.Config
	getErr  error
	saveErr error

	// serviceIDMigrationDone mirrors the store's migration marker driving the
	// stale legacy UUID rejection in UpdateConfig.
	serviceIDMigrationDone bool
}

func (s *testConfigStore) GetConfig() (*config.Config, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.cfg, nil
}

func (s *testConfigStore) SaveConfig(cfg config.Config) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	clone := cfg
	s.cfg = &clone
	return nil
}

func (s *testConfigStore) UpdateConfig(transform func(prev *config.Config) (config.Config, error)) (config.Config, error) {
	if s.getErr != nil {
		return config.Config{}, s.getErr
	}
	next, err := transform(s.cfg)
	if err != nil {
		return config.Config{}, err
	}
	if s.serviceIDMigrationDone {
		for i := range next.Services {
			if len(next.Services[i].ID) == 36 {
				return next, store.ErrStaleLegacyServiceIDs
			}
		}
	}
	if err := s.SaveConfig(next); err != nil {
		return next, err
	}
	return next, nil
}

// testConfigUpdater tracks whether Update was called and with what config.
type testConfigUpdater struct {
	lastUpdate *config.Config
	callCount  int
}

func (u *testConfigUpdater) Update(cfg *config.Config) {
	u.lastUpdate = cfg
	u.callCount++
}

// testClusterNotifier tracks whether PublishConfigUpdate was called.
type testClusterNotifier struct {
	callCount int
	err       error
}

func (n *testClusterNotifier) PublishConfigUpdate() error {
	n.callCount++
	return n.err
}

func setupTestRouter(store ConfigStore, updater ConfigUpdater, notifier ClusterNotifier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	a := &API{
		configStore:     store,
		configUpdater:   updater,
		clusterNotifier: notifier,
	}

	adminRouter := router.Group("/admin")
	adminRouter.GET("/config", a.handleGetConfig)
	adminRouter.PUT("/config", a.handleSaveConfig)

	return router
}

func TestHandleGetConfig(t *testing.T) {
	tests := []struct {
		name           string
		storedConfig   *config.Config
		expectedStatus int
		validateBody   func(t *testing.T, body []byte)
	}{
		{
			name:           "returns empty config when store has nil",
			storedConfig:   nil,
			expectedStatus: http.StatusOK,
			validateBody: func(t *testing.T, body []byte) {
				var raw map[string]any
				err := json.Unmarshal(body, &raw)
				require.NoError(t, err)

				services, ok := raw["services"].([]any)
				require.True(t, ok, "services should marshal as an empty array")
				assert.Empty(t, services)

				bots, ok := raw["bots"].([]any)
				require.True(t, ok, "bots should marshal as an empty array")
				assert.Empty(t, bots)

				mcpConfig, ok := raw["mcp"].(map[string]any)
				require.True(t, ok, "mcp should be present in response")
				assert.Equal(t, true, mcpConfig["enabled"])
				embeddedServer, ok := mcpConfig["embeddedServer"].(map[string]any)
				require.True(t, ok, "mcp.embeddedServer should be present in response")
				assert.Equal(t, true, embeddedServer["enabled"])
				servers, ok := mcpConfig["servers"].([]any)
				require.True(t, ok, "mcp.servers should marshal as an empty array")
				assert.Empty(t, servers)

				webSearchConfig, ok := raw["webSearch"].(map[string]any)
				require.True(t, ok, "webSearch should be present in response")
				domainDenylist, ok := webSearchConfig["domainDenylist"].([]any)
				require.True(t, ok, "webSearch.domainDenylist should marshal as an empty array")
				assert.Empty(t, domainDenylist)

				var cfg config.Config
				err = json.Unmarshal(body, &cfg)
				require.NoError(t, err)
				assert.Empty(t, cfg.Services)
				assert.Empty(t, cfg.Bots)
				assert.Empty(t, cfg.DefaultBotName)
				assert.True(t, cfg.MCP.Enabled)
				assert.True(t, cfg.MCP.EmbeddedServer.Enabled)
			},
		},
		{
			name: "returns stored config",
			storedConfig: &config.Config{
				DefaultBotName: "ai",
				Services: []llm.ServiceConfig{
					{
						ID:   "svc-1",
						Name: "OpenAI",
						Type: "openai",
					},
				},
				Bots: []llm.BotConfig{
					{
						ID:        "bot-1",
						Name:      "ai",
						ServiceID: "svc-1",
					},
				},
			},
			expectedStatus: http.StatusOK,
			validateBody: func(t *testing.T, body []byte) {
				var cfg config.Config
				err := json.Unmarshal(body, &cfg)
				require.NoError(t, err)
				assert.Equal(t, "ai", cfg.DefaultBotName)
				require.Len(t, cfg.Services, 1)
				assert.Equal(t, "svc-1", cfg.Services[0].ID)
				assert.Equal(t, "openai", cfg.Services[0].Type)
				require.Len(t, cfg.Bots, 1)
				assert.Equal(t, "bot-1", cfg.Bots[0].ID)
				assert.Equal(t, "svc-1", cfg.Bots[0].ServiceID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &testConfigStore{cfg: tt.storedConfig}
			updater := &testConfigUpdater{}
			notifier := &testClusterNotifier{}

			router := setupTestRouter(store, updater, notifier)

			req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.validateBody != nil {
				tt.validateBody(t, w.Body.Bytes())
			}
		})
	}
}

func TestHandleGetConfigDoesNotMutateStoredServices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stored := &config.Config{
		Services: []llm.ServiceConfig{
			{ID: "svc-1", Type: llm.ServiceTypeOpenAI, UseResponsesAPI: false},
		},
	}
	store := &testConfigStore{cfg: stored}
	router := setupTestRouter(store, &testConfigUpdater{}, &testClusterNotifier{})

	req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.False(t, store.cfg.Services[0].UseResponsesAPI, "GET must not mutate stored config backing array")

	var out config.Config
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Services, 1)
	assert.True(t, out.Services[0].UseResponsesAPI)
}

func TestHandleSaveConfig(t *testing.T) {
	tests := []struct {
		name                  string
		storedCfg             *config.Config
		requestBody           any
		clusterErr            error
		expectedStatus        int
		validateStore         func(t *testing.T, store *testConfigStore)
		validateUpdater       func(t *testing.T, updater *testConfigUpdater)
		validateClusterNotify func(t *testing.T, notifier *testClusterNotifier)
	}{
		{
			name: "returns error when cluster notify fails after successful save",
			storedCfg: &config.Config{
				Services: []llm.ServiceConfig{
					{ID: "svc-1", Name: "OpenAI", Type: "openai"},
				},
			},
			requestBody: config.Config{
				DefaultBotName: "ai",
				Services: []llm.ServiceConfig{
					{ID: "svc-1", Name: "OpenAI", Type: "openai"},
				},
				Bots: []llm.BotConfig{
					{ID: "bot-1", Name: "ai", ServiceID: "svc-1"},
				},
			},
			clusterErr:     errors.New("cluster publish failed"),
			expectedStatus: http.StatusInternalServerError,
			validateStore: func(t *testing.T, store *testConfigStore) {
				require.NotNil(t, store.cfg)
				assert.Equal(t, "ai", store.cfg.DefaultBotName)
				assert.True(t, store.cfg.MCP.Enabled)
				assert.True(t, store.cfg.MCP.EmbeddedServer.Enabled)
			},
			validateUpdater: func(t *testing.T, updater *testConfigUpdater) {
				assert.Equal(t, 1, updater.callCount)
			},
			validateClusterNotify: func(t *testing.T, notifier *testClusterNotifier) {
				assert.Equal(t, 1, notifier.callCount)
			},
		},
		{
			name: "saves valid config",
			storedCfg: &config.Config{
				Services: []llm.ServiceConfig{
					{ID: "svc-1", Name: "OpenAI", Type: "openai"},
				},
			},
			requestBody: config.Config{
				DefaultBotName: "ai",
				Services: []llm.ServiceConfig{
					{
						ID:   "svc-1",
						Name: "OpenAI",
						Type: "openai",
					},
				},
				Bots: []llm.BotConfig{
					{
						ID:        "bot-1",
						Name:      "ai",
						ServiceID: "svc-1",
					},
				},
			},
			expectedStatus: http.StatusOK,
			validateStore: func(t *testing.T, store *testConfigStore) {
				require.NotNil(t, store.cfg)
				assert.Equal(t, "ai", store.cfg.DefaultBotName)
				require.Len(t, store.cfg.Services, 1)
				assert.Equal(t, "svc-1", store.cfg.Services[0].ID)
				assert.True(t, store.cfg.Services[0].UseResponsesAPI)
				require.Len(t, store.cfg.Bots, 1)
				assert.Equal(t, "bot-1", store.cfg.Bots[0].ID)
				assert.True(t, store.cfg.MCP.Enabled)
				assert.True(t, store.cfg.MCP.EmbeddedServer.Enabled)
			},
			validateUpdater: func(t *testing.T, updater *testConfigUpdater) {
				assert.Equal(t, 1, updater.callCount)
				require.NotNil(t, updater.lastUpdate)
				assert.Equal(t, "ai", updater.lastUpdate.DefaultBotName)
				assert.True(t, updater.lastUpdate.Services[0].UseResponsesAPI)
			},
			validateClusterNotify: func(t *testing.T, notifier *testClusterNotifier) {
				assert.Equal(t, 1, notifier.callCount)
			},
		},
		{
			name:           "saves empty config",
			requestBody:    config.Config{},
			expectedStatus: http.StatusOK,
			validateStore: func(t *testing.T, store *testConfigStore) {
				require.NotNil(t, store.cfg)
				assert.Empty(t, store.cfg.DefaultBotName)
				assert.Empty(t, store.cfg.Services)
				assert.Empty(t, store.cfg.Bots)
				assert.True(t, store.cfg.MCP.Enabled)
				assert.True(t, store.cfg.MCP.EmbeddedServer.Enabled)
			},
			validateUpdater: func(t *testing.T, updater *testConfigUpdater) {
				assert.Equal(t, 1, updater.callCount)
			},
			validateClusterNotify: func(t *testing.T, notifier *testClusterNotifier) {
				assert.Equal(t, 1, notifier.callCount)
			},
		},
		{
			name:           "rejects invalid JSON",
			requestBody:    "not-json",
			expectedStatus: http.StatusBadRequest,
			validateStore: func(t *testing.T, store *testConfigStore) {
				assert.Nil(t, store.cfg, "store should not be modified on bad request")
			},
			validateUpdater: func(t *testing.T, updater *testConfigUpdater) {
				assert.Equal(t, 0, updater.callCount, "updater should not be called on bad request")
			},
			validateClusterNotify: func(t *testing.T, notifier *testClusterNotifier) {
				assert.Equal(t, 0, notifier.callCount, "cluster notify should not be called on bad request")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &testConfigStore{cfg: tt.storedCfg}
			updater := &testConfigUpdater{}
			notifier := &testClusterNotifier{err: tt.clusterErr}

			router := setupTestRouter(store, updater, notifier)

			var body []byte
			var err error
			switch v := tt.requestBody.(type) {
			case string:
				body = []byte(v)
			default:
				body, err = json.Marshal(v)
				require.NoError(t, err)
			}

			req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.validateStore != nil {
				tt.validateStore(t, store)
			}
			if tt.validateUpdater != nil {
				tt.validateUpdater(t, updater)
			}
			if tt.validateClusterNotify != nil {
				tt.validateClusterNotify(t, notifier)
			}
		})
	}
}

func TestNormalizeAdminConfigAssignsStableIDs(t *testing.T) {
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
			tt.validate(t, normalizeAdminConfig(tt.cfg))
		})
	}
}

func TestHandleSaveConfigCarriesForwardMCPServerIDs(t *testing.T) {
	tests := []struct {
		name       string
		storedCfg  *config.Config
		payloadCfg config.Config
		validate   func(t *testing.T, store *testConfigStore)
	}{
		{
			name: "payload without id keeps stored id (stale bundle)",
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
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 1)
				assert.Equal(t, "stable-id", store.cfg.MCP.Servers[0].ID)
				assert.Equal(t, "embedded-stable", store.cfg.MCP.EmbeddedServer.ID)
				require.Len(t, store.cfg.MCP.PluginServers, 1)
				assert.Equal(t, "plugin-stable", store.cfg.MCP.PluginServers[0].ID)
			},
		},
		{
			name: "renamed server without id matched by BaseURL",
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
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 1)
				assert.Equal(t, "stable-id", store.cfg.MCP.Servers[0].ID)
			},
		},
		{
			name: "brand-new server without match gets a fresh id from the backstop",
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
						{Name: "Jira", BaseURL: "https://jira.example.com"},
						{Name: "Brand New", BaseURL: "https://new.example.com"},
					},
				},
			},
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 2)
				assert.Equal(t, "stable-id", store.cfg.MCP.Servers[0].ID)
				assert.True(t, model.IsValidId(store.cfg.MCP.Servers[1].ID))
				assert.NotEqual(t, "stable-id", store.cfg.MCP.Servers[1].ID)
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
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 1)
				assert.True(t, model.IsValidId(store.cfg.MCP.Servers[0].ID))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &testConfigStore{cfg: tt.storedCfg}
			router := setupTestRouter(store, &testConfigUpdater{}, &testClusterNotifier{})

			body, err := json.Marshal(tt.payloadCfg)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			tt.validate(t, store)
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
					{"name": "Remote", "baseURL": "https://mcp.example.com", "enabled": true},
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
					{"name": "Remote", "baseURL": "https://mcp.example.com", "enabled": true},
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

// TestHandleSaveConfigCarriesForwardServiceIDs mirrors the MCP server ID
// reconciliation for LLM services: service IDs are ABAC policy identities, so
// an ID-less payload from a stale client must reclaim the stored ID instead
// of rotating it (which would silently detach the service's policy), and
// unresolvable identities must fail with 409.
func TestHandleSaveConfigCarriesForwardServiceIDs(t *testing.T) {
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
			name:      "payload without id keeps stored id (stale client)",
			storedCfg: stored(),
			payloadServices: []llm.ServiceConfig{
				{Name: "OpenAI", Type: "openai"},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.Services, 1)
				assert.Equal(t, "stable-svc-id", store.cfg.Services[0].ID)
			},
		},
		{
			name:      "repeated ID-less saves never rotate the id",
			storedCfg: stored(),
			payloadServices: []llm.ServiceConfig{
				{Name: "OpenAI", Type: "anthropic", APIURL: "https://edited.example.com"},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.Services, 1)
				assert.Equal(t, "stable-svc-id", store.cfg.Services[0].ID)
			},
		},
		{
			name:      "brand-new service without match gets a fresh id from the backstop",
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
			name:      "fabricated incoming ID returns 409",
			storedCfg: stored(),
			payloadServices: []llm.ServiceConfig{
				{ID: "invented-by-client", Name: "OpenAI", Type: "openai"},
			},
			expectedStatus: http.StatusConflict,
			validate: func(t *testing.T, store *testConfigStore) {
				assert.Equal(t, "stable-svc-id", store.cfg.Services[0].ID, "stored config must be untouched on rejection")
			},
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

// TestHandleSaveConfigRejectsMCPServerIDConflicts covers stale or corrupt
// admin payloads whose MCP server identities cannot be reconciled with the
// stored config: the save must fail with 409 (never silently re-mint IDs,
// which would detach existing policies), while legitimate add/edit flows
// still succeed.
func TestHandleSaveConfigRejectsMCPServerIDConflicts(t *testing.T) {
	storedServer := config.MCPServerConfig{ID: "stable-id", Name: "Jira", BaseURL: "https://jira.example.com"}
	storedOther := config.MCPServerConfig{ID: "other-id", Name: "Linear", BaseURL: "https://linear.example.com"}

	tests := []struct {
		name           string
		payloadServers []config.MCPServerConfig
		expectedStatus int
		validate       func(t *testing.T, store *testConfigStore)
	}{
		{
			name: "fabricated incoming ID returns 409",
			payloadServers: []config.MCPServerConfig{
				{ID: "invented-by-client", Name: "Jira", BaseURL: "https://jira.example.com"},
			},
			expectedStatus: http.StatusConflict,
		},
		{
			name: "duplicate incoming IDs return 409",
			payloadServers: []config.MCPServerConfig{
				{ID: "stable-id", Name: "Jira", BaseURL: "https://jira.example.com"},
				{ID: "stable-id", Name: "Copy", BaseURL: "https://copy.example.com"},
			},
			expectedStatus: http.StatusConflict,
		},
		{
			name: "ambiguous ID-less identity match returns 409",
			payloadServers: []config.MCPServerConfig{
				// Name matches Linear, BaseURL matches Jira: never guess.
				{Name: "Linear", BaseURL: "https://jira.example.com"},
			},
			expectedStatus: http.StatusConflict,
		},
		{
			name: "add flow: new ID-less server minted server-side",
			payloadServers: []config.MCPServerConfig{
				{ID: "stable-id", Name: "Jira", BaseURL: "https://jira.example.com"},
				{ID: "other-id", Name: "Linear", BaseURL: "https://linear.example.com"},
				{Name: "Brand New", BaseURL: "https://new.example.com"},
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, store *testConfigStore) {
				require.Len(t, store.cfg.MCP.Servers, 3)
				assert.Equal(t, "stable-id", store.cfg.MCP.Servers[0].ID)
				assert.Equal(t, "other-id", store.cfg.MCP.Servers[1].ID)
				assert.True(t, model.IsValidId(store.cfg.MCP.Servers[2].ID), "new server gets a server-minted ID")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := &config.Config{
				MCP: config.MCPConfig{Servers: []config.MCPServerConfig{storedServer, storedOther}},
			}
			store := &testConfigStore{cfg: stored}
			updater := &testConfigUpdater{}
			notifier := &testClusterNotifier{}
			router := setupTestRouter(store, updater, notifier)

			body, err := json.Marshal(config.Config{
				MCP: config.MCPConfig{Servers: tt.payloadServers},
			})
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedStatus == http.StatusConflict {
				assert.Equal(t, "stable-id", store.cfg.MCP.Servers[0].ID, "stored config must be untouched on rejection")
				assert.Equal(t, 0, updater.callCount, "in-memory config must not be updated on rejection")
				assert.Equal(t, 0, notifier.callCount, "cluster must not be notified on rejection")
			}
			if tt.validate != nil {
				tt.validate(t, store)
			}
		})
	}
}

// TestHandleSaveConfigFirstWriteIncomingMCPIDs covers the first-write path
// (no stored config yet): reconciliation must still run against an empty
// previous list, so duplicate incoming MCP server IDs are rejected, while
// caller-chosen IDs for genuinely new servers (API automation seeding a
// fresh install) are kept.
func TestHandleSaveConfigFirstWriteIncomingMCPIDs(t *testing.T) {
	tests := []struct {
		name           string
		payloadServers []config.MCPServerConfig
		expectedStatus int
	}{
		{
			name: "caller-chosen ID on first write is kept",
			payloadServers: []config.MCPServerConfig{
				{ID: "seeded-by-client", Name: "Jira", BaseURL: "https://jira.example.com"},
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "duplicate IDs on first write",
			payloadServers: []config.MCPServerConfig{
				{ID: "seeded-by-client", Name: "Jira", BaseURL: "https://jira.example.com"},
				{ID: "seeded-by-client", Name: "Copy", BaseURL: "https://copy.example.com"},
			},
			expectedStatus: http.StatusConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &testConfigStore{cfg: nil}
			updater := &testConfigUpdater{}
			notifier := &testClusterNotifier{}
			router := setupTestRouter(store, updater, notifier)

			body, err := json.Marshal(config.Config{
				MCP: config.MCPConfig{Servers: tt.payloadServers},
			})
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedStatus == http.StatusConflict {
				assert.Nil(t, store.cfg, "nothing must be persisted on rejection")
				assert.Equal(t, 0, updater.callCount)
				assert.Equal(t, 0, notifier.callCount)
			} else {
				require.NotNil(t, store.cfg)
				require.Len(t, store.cfg.MCP.Servers, len(tt.payloadServers))
				assert.Equal(t, tt.payloadServers[0].ID, store.cfg.MCP.Servers[0].ID)
			}
		})
	}
}

// TestHandleSaveConfigReturnsNormalizedConfig verifies the PUT response body
// carries the normalized saved config, so the webapp can adopt server-minted
// service and MCP server IDs immediately instead of waiting for a reload.
func TestHandleSaveConfigReturnsNormalizedConfig(t *testing.T) {
	store := &testConfigStore{}
	router := setupTestRouter(store, &testConfigUpdater{}, &testClusterNotifier{})

	payload := config.Config{
		Services: []llm.ServiceConfig{{Name: "OpenAI", Type: "openai"}},
		MCP: config.MCPConfig{
			Servers: []config.MCPServerConfig{{Name: "Jira", BaseURL: "https://jira.example.com"}},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp config.Config
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp.Services, 1)
	assert.True(t, model.IsValidId(resp.Services[0].ID), "response must carry the server-minted service ID")
	assert.Equal(t, store.cfg.Services[0].ID, resp.Services[0].ID, "response ID must match the persisted one")

	require.Len(t, resp.MCP.Servers, 1)
	assert.True(t, model.IsValidId(resp.MCP.Servers[0].ID), "response must carry the server-minted MCP server ID")
	assert.Equal(t, store.cfg.MCP.Servers[0].ID, resp.MCP.Servers[0].ID, "response ID must match the persisted one")

	assert.True(t, resp.Services[0].UseResponsesAPI, "response must reflect normalization")
}

// TestHandleSaveConfigRejectsStaleLegacyServiceIDs covers the interleaving
// where a pre-upgrade webapp bundle loaded the config before the ID migration
// ran and then saves UUID service IDs back: the save must fail with 409 and
// leave the migrated config untouched, while a fresh payload is accepted.
func TestHandleSaveConfigRejectsStaleLegacyServiceIDs(t *testing.T) {
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

	// Stale payload echoing a pre-migration UUID service ID.
	stale := config.Config{
		Services: []llm.ServiceConfig{
			{ID: "550e8400-e29b-41d4-a716-446655440000", Name: "OpenAI", Type: "openai"},
		},
	}
	w := put(stale)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "migrated26charidmigrated26", store.cfg.Services[0].ID, "migrated ID must survive the stale save")
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

func TestSaveAndGetConfigRoundTrip(t *testing.T) {
	store := &testConfigStore{}
	updater := &testConfigUpdater{}
	notifier := &testClusterNotifier{}
	router := setupTestRouter(store, updater, notifier)

	// Step 1: GET returns empty config
	req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var emptyCfg config.Config
	err := json.Unmarshal(w.Body.Bytes(), &emptyCfg)
	require.NoError(t, err)
	assert.Empty(t, emptyCfg.Services)

	// Step 2: PUT a config. The new service arrives ID-less (the backend
	// mints the stable ID); an unknown incoming ID would be rejected.
	saveCfg := config.Config{
		DefaultBotName: "ai",
		Services: []llm.ServiceConfig{
			{Name: "OpenAI", Type: "openai", APIKey: "sk-test"},
		},
		Bots: []llm.BotConfig{
			{ID: "bot-1", Name: "ai", ServiceID: "svc-1"},
		},
	}
	body, err := json.Marshal(saveCfg)
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Step 3: GET returns the saved config
	req = httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var loadedCfg config.Config
	err = json.Unmarshal(w.Body.Bytes(), &loadedCfg)
	require.NoError(t, err)
	assert.Equal(t, "ai", loadedCfg.DefaultBotName)
	require.Len(t, loadedCfg.Services, 1)
	assert.Equal(t, "sk-test", loadedCfg.Services[0].APIKey)
	assert.True(t, loadedCfg.Services[0].UseResponsesAPI)
	require.Len(t, loadedCfg.Bots, 1)
	assert.Equal(t, "bot-1", loadedCfg.Bots[0].ID)
	assert.True(t, loadedCfg.MCP.Enabled)
	assert.True(t, loadedCfg.MCP.EmbeddedServer.Enabled)

	// Step 4: Verify side effects
	assert.Equal(t, 1, updater.callCount)
	assert.Equal(t, 1, notifier.callCount)
}

func TestAdminConfigRoundTripsMCPRetrievalOverride(t *testing.T) {
	store := &testConfigStore{}
	updater := &testConfigUpdater{}
	notifier := &testClusterNotifier{}
	router := setupTestRouter(store, updater, notifier)

	saveCfg := config.Config{
		MCP: config.MCPConfig{
			Servers: []config.MCPServerConfig{
				{
					Name:    "Jira",
					Enabled: true,
					BaseURL: "https://jira.example.com",
					ToolConfigs: []config.MCPToolConfig{
						{
							Name:                         "get_issue",
							Policy:                       config.MCPToolPolicyAsk,
							Enabled:                      true,
							RetrievalDescriptionOverride: "Find Jira issues by incident context",
						},
					},
				},
			},
			EmbeddedServer: config.MCPEmbeddedServerConfig{
				ToolConfigs: []config.MCPToolConfig{
					{
						Name:                         "search_users",
						Policy:                       config.MCPToolPolicyAutoRunInDM,
						Enabled:                      true,
						RetrievalDescriptionOverride: "Find Mattermost people",
					},
				},
			},
		},
	}
	body, err := json.Marshal(saveCfg)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var loadedCfg config.Config
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &loadedCfg))
	require.Len(t, loadedCfg.MCP.Servers, 1)
	require.Len(t, loadedCfg.MCP.Servers[0].ToolConfigs, 1)
	require.Equal(t, "Find Jira issues by incident context", loadedCfg.MCP.Servers[0].ToolConfigs[0].RetrievalDescriptionOverride)
	require.Len(t, loadedCfg.MCP.EmbeddedServer.ToolConfigs, 1)
	require.Equal(t, "Find Mattermost people", loadedCfg.MCP.EmbeddedServer.ToolConfigs[0].RetrievalDescriptionOverride)
	require.Equal(t, 1, updater.callCount)
	require.Equal(t, 1, notifier.callCount)
}
