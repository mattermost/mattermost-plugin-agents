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
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleGetConfigWithInvalidEmbeddingParameters(t *testing.T) {
	stored := &config.Config{
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			EmbeddingProvider: embeddings.UpstreamConfig{
				Parameters: json.RawMessage(`{"apiKey":"stored-value`),
			},
		},
	}
	store := &testConfigStore{cfg: stored}
	router := setupTestRouter(store, &testConfigUpdater{}, &testClusterNotifier{})

	req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "stored-value")

	var out config.Config
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.JSONEq(t, "null", string(out.EmbeddingSearchConfig.EmbeddingProvider.Parameters))
	assert.Equal(t, `{"apiKey":"stored-value`, string(store.cfg.EmbeddingSearchConfig.EmbeddingProvider.Parameters))
}

func TestHandleSaveConfigRestoresRedactedValues(t *testing.T) {
	store := &testConfigStore{}
	router := setupTestRouter(store, &testConfigUpdater{}, &testClusterNotifier{})

	putConfig := func(cfg config.Config) {
		t.Helper()
		body, err := json.Marshal(cfg)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}
	getConfig := func() config.Config {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)

		var cfg config.Config
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cfg))
		return cfg
	}

	putConfig(config.Config{
		Services: []llm.ServiceConfig{{
			ID:     "service-1",
			Type:   llm.ServiceTypeOpenAI,
			APIKey: "initial-service-value",
		}},
	})

	redacted := getConfig()
	require.Len(t, redacted.Services, 1)
	require.Equal(t, config.FakeSetting, redacted.Services[0].APIKey)

	putConfig(redacted)
	require.NotNil(t, store.cfg)
	require.Equal(t, "initial-service-value", store.cfg.Services[0].APIKey)

	redacted.Services[0].APIKey = ""
	putConfig(redacted)
	require.Empty(t, store.cfg.Services[0].APIKey)

	redacted.Services[0].APIKey = "replacement-service-value"
	putConfig(redacted)
	require.Equal(t, "replacement-service-value", store.cfg.Services[0].APIKey)
}
