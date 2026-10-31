// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// kvKeysPluginClient is a plugin API client whose KV store holds keys and
// records the keys deleted from it. A key in failKeys fails to delete.
func kvKeysPluginClient(keys []string, failKeys ...string) (*pluginapi.Client, func() []string) {
	var mu sync.Mutex
	var deleted []string
	mockAPI := &plugintest.API{}
	mockAPI.On("KVList", 0, 1000).Return(keys, nil)
	// pluginapi deletes by setting a nil value.
	mockAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), []byte(nil), model.PluginKVSetOptions{}).
		Return(func(key string, _ []byte, _ model.PluginKVSetOptions) (bool, *model.AppError) {
			if slices.Contains(failKeys, key) {
				return false, model.NewAppError("KVSetWithOptions", "kv.unavailable", nil, "", http.StatusInternalServerError)
			}
			mu.Lock()
			defer mu.Unlock()
			deleted = append(deleted, key)
			return true, nil
		})
	mockAPI.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	return pluginapi.NewClient(mockAPI, nil), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), deleted...)
	}
}

// A configuration save removes users' saved MCP sign-ins that the change
// would otherwise orphan or send to another server, and only those.
func TestHandleSaveConfigClearsMCPServerSignIns(t *testing.T) {
	const alice = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	const bob = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const jiraID = "jirajirajirajirajirajira00"
	const docsID = "docsdocsdocsdocsdocsdocs00"
	jira := config.MCPServerConfig{ID: jiraID, Name: "Jira", Enabled: true, BaseURL: "https://jira.example.com/mcp"}
	docs := config.MCPServerConfig{ID: docsID, Name: "my_docs", Enabled: true, BaseURL: "https://docs.example.com/mcp"}
	token := func(user, name string) string { return "mcp_oauth_token_v1_" + user + "_" + name }
	needed := func(user, name string) string { return "mcp_oauth_needed_v1_" + user + "_" + name }

	stored := []string{
		token(alice, "Jira"), token(bob, "Jira"), needed(bob, "Jira"),
		token(alice, "my_docs"), token(alice, "my_docs_v2"), token(alice, "Jira Cloud"), token(bob, "Wiki"),
		"mcp_oauth_refresh_lease_v1_" + alice + "_Jira", "mcp_tools_cache_v1_Jira", "unrelated",
	}
	with := func(s config.MCPServerConfig, change func(*config.MCPServerConfig)) config.MCPServerConfig {
		change(&s)
		return s
	}

	tests := []struct {
		name        string
		next        []config.MCPServerConfig
		wantDeleted []string
	}{
		{
			name: "unchanged servers keep sign-ins",
			next: []config.MCPServerConfig{with(jira, func(s *config.MCPServerConfig) { s.Description = "Issues" }), docs},
		},
		{
			name: "a path-only URL change keeps sign-ins",
			next: []config.MCPServerConfig{with(jira, func(s *config.MCPServerConfig) { s.BaseURL = "https://jira.example.com/v2/mcp" }), docs},
		},
		{
			name:        "deleting a server removes its sign-ins",
			next:        []config.MCPServerConfig{jira},
			wantDeleted: []string{token(alice, "my_docs")},
		},
		{
			name:        "renaming a server removes sign-ins under its old and new names",
			next:        []config.MCPServerConfig{with(jira, func(s *config.MCPServerConfig) { s.Name = "Jira Cloud" }), docs},
			wantDeleted: []string{token(alice, "Jira"), token(bob, "Jira"), needed(bob, "Jira"), token(alice, "Jira Cloud")},
		},
		{
			name:        "moving a server to another origin removes its sign-ins",
			next:        []config.MCPServerConfig{with(jira, func(s *config.MCPServerConfig) { s.BaseURL = "https://other.example.com/mcp" }), docs},
			wantDeleted: []string{token(alice, "Jira"), token(bob, "Jira"), needed(bob, "Jira")},
		},
		{
			name: "adding a server removes sign-ins an earlier server left under its name",
			next: []config.MCPServerConfig{jira, docs, {Name: "Wiki", Enabled: true, BaseURL: "https://wiki.example.com/mcp"}},
			// The new server's minted ID is not known in advance.
			wantDeleted: []string{token(bob, "Wiki")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgStore := &testConfigStore{cfg: &config.Config{MCP: config.MCPConfig{Servers: []config.MCPServerConfig{jira, docs}}}}
			updater := &testConfigUpdater{}
			pluginClient, deleted := kvKeysPluginClient(stored)
			router := setupConfigTestRouter(&API{
				configStore:     cfgStore,
				configUpdater:   updater,
				clusterNotifier: &testClusterNotifier{},
				licenseChecker:  enterprisetest.CheckerAt(enterprise.LevelEnterpriseAdvanced),
				pluginAPI:       pluginClient,
			})

			body, err := json.Marshal(config.Config{MCP: config.MCPConfig{Servers: tt.next}})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.ElementsMatch(t, tt.wantDeleted, deleted())
			assert.Equal(t, 1, updater.callCount)
		})
	}
}

// A failure to remove sign-ins is reported, but the saved configuration
// still takes effect, and the audit record names the affected server.
func TestHandleSaveConfigReportsSignInRemovalFailure(t *testing.T) {
	const alice = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	const jiraID = "jirajirajirajirajirajira00"
	jira := config.MCPServerConfig{ID: jiraID, Name: "Jira", Enabled: true, BaseURL: "https://jira.example.com/mcp"}
	failing := "mcp_oauth_token_v1_" + alice + "_Jira"

	e, records := setupAdminAuditTest(t)
	defer e.Cleanup(t)
	e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(true)
	cfgStore := &testConfigStore{cfg: &config.Config{MCP: config.MCPConfig{Servers: []config.MCPServerConfig{jira}}}}
	updater := &testConfigUpdater{}
	notifier := &testClusterNotifier{}
	e.api.configStore = cfgStore
	e.api.configUpdater = updater
	e.api.clusterNotifier = notifier
	e.api.licenseChecker = enterprisetest.CheckerAt(enterprise.LevelEnterpriseAdvanced)
	e.mockAPI.On("KVList", 0, 1000).Return([]string{failing}, nil)
	e.mockAPI.On("KVSetWithOptions", failing, []byte(nil), model.PluginKVSetOptions{}).
		Return(false, model.NewAppError("KVSetWithOptions", "kv.unavailable", nil, "", http.StatusInternalServerError))
	e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	body, err := json.Marshal(config.Config{MCP: config.MCPConfig{Servers: []config.MCPServerConfig{}}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
	req.Header.Set("Mattermost-User-Id", "userid")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.api.ServeHTTP(nil, w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Empty(t, cfgStore.cfg.MCP.Servers, "the configuration change is saved")
	assert.Equal(t, 1, updater.callCount, "the saved configuration is applied on this node")
	assert.Equal(t, 1, notifier.callCount, "other nodes are told to reload")

	require.Len(t, *records, 1)
	params := (*records)[0].EventData.Parameters
	assert.Equal(t, []string{jiraID}, params["mcp_oauth_cleared_server_ids"])
	assert.Equal(t, true, params["mcp_oauth_clear_failed"])
	assert.Equal(t, true, params["persisted"])
	raw, err := json.Marshal((*records)[0])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), failing, "stored keys never enter audit records")
}
