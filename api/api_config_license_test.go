// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/require"
)

func TestHandleSaveConfigLicenseGates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       config.Config
		minLevel   enterprise.Level
		wantStatus int
	}{
		{
			name:       "single service is available at every level",
			body:       config.Config{Services: []llm.ServiceConfig{{ID: "s1", Name: "OpenAI", Type: "openai"}}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "second service is available at Enterprise and above",
			body:       config.Config{Services: []llm.ServiceConfig{{ID: "s1", Type: "openai"}, {ID: "s2", Type: "openai"}}},
			minLevel:   enterprise.LevelEnterprise,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "token accounting is available at Professional and above",
			body:       config.Config{EnableTokenUsageLogging: true},
			minLevel:   enterprise.LevelProfessional,
			wantStatus: http.StatusForbidden,
		},
		{
			name: "turning token accounting off is accepted",
			body: config.Config{EnableTokenUsageLogging: false},
			// prev is seeded with logging on in the test body below
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, level := range enterprisetest.AllLevels {
				t.Run(level.String(), func(t *testing.T) {
					prev := &config.Config{}
					if tc.name == "turning token accounting off is accepted" {
						prev.EnableTokenUsageLogging = true
					}
					store := &testConfigStore{cfg: prev}
					router := gin.New()
					a := &API{
						configStore:     store,
						configUpdater:   &testConfigUpdater{},
						clusterNotifier: &testClusterNotifier{},
						licenseChecker:  enterprisetest.CheckerAt(level),
					}
					router.PUT("/admin/config", a.handleSaveConfig)

					body, err := json.Marshal(tc.body)
					require.NoError(t, err)
					req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)

					denied := tc.minLevel != 0 && level < tc.minLevel
					if denied {
						require.Equal(t, http.StatusForbidden, w.Code)
						return
					}
					require.Equal(t, http.StatusOK, w.Code)
				})
			}
		})
	}
}

func TestHandleSaveConfigUnchangedOverLimitAccepted(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prev := &config.Config{
		Services: []llm.ServiceConfig{{ID: "s1", Type: "openai"}, {ID: "s2", Type: "openai"}},
		Bots: []llm.BotConfig{
			{ID: "b1", Name: "one", DisplayName: "One", ServiceID: "s1"},
			{ID: "b2", Name: "two", DisplayName: "Two", ServiceID: "s1"},
		},
	}
	store := &testConfigStore{cfg: prev}
	router := gin.New()
	a := &API{
		configStore:     store,
		configUpdater:   &testConfigUpdater{},
		clusterNotifier: &testClusterNotifier{},
		licenseChecker:  enterprisetest.CheckerAt(enterprise.LevelUnlicensed),
	}
	router.PUT("/admin/config", a.handleSaveConfig)

	body, err := json.Marshal(*prev)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}
