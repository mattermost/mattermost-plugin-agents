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
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testConfigStore is a simple in-memory implementation of ConfigStore for testing.
type testConfigStore struct {
	cfg *config.Config
}

func (s *testConfigStore) GetConfig() (*config.Config, error) {
	return s.cfg, nil
}

func (s *testConfigStore) SaveConfig(cfg config.Config) error {
	clone := cfg
	s.cfg = &clone
	return nil
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

func TestHandleSaveConfig(t *testing.T) {
	tests := []struct {
		name                  string
		requestBody           any
		clusterErr            error
		expectedStatus        int
		validateStore         func(t *testing.T, store *testConfigStore)
		validateUpdater       func(t *testing.T, updater *testConfigUpdater)
		validateClusterNotify func(t *testing.T, notifier *testClusterNotifier)
	}{
		{
			name: "returns error when cluster notify fails after successful save",
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
				require.Len(t, store.cfg.Bots, 1)
				assert.Equal(t, "bot-1", store.cfg.Bots[0].ID)
			},
			validateUpdater: func(t *testing.T, updater *testConfigUpdater) {
				assert.Equal(t, 1, updater.callCount)
				require.NotNil(t, updater.lastUpdate)
				assert.Equal(t, "ai", updater.lastUpdate.DefaultBotName)
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
			store := &testConfigStore{}
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

	// Step 2: PUT a config
	saveCfg := config.Config{
		DefaultBotName: "ai",
		Services: []llm.ServiceConfig{
			{ID: "svc-1", Name: "OpenAI", Type: "openai", APIKey: "sk-test"},
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
	require.Len(t, loadedCfg.Bots, 1)
	assert.Equal(t, "bot-1", loadedCfg.Bots[0].ID)

	// Step 4: Verify side effects
	assert.Equal(t, 1, updater.callCount)
	assert.Equal(t, 1, notifier.callCount)
}
