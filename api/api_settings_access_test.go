// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type settingsAccessUsers struct {
	admin     string
	readOnly  string
	readWrite string
	other     string
}

// setupSettingsAccessEnv wires a config store holding stored and an in-memory
// delegation list granting users.readOnly and users.readWrite, with
// users.admin the only system admin.
func setupSettingsAccessEnv(t *testing.T, stored *config.Config) (*TestEnvironment, settingsAccessUsers, *testConfigStore, *testConfigUpdater) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	users := settingsAccessUsers{
		admin:     model.NewId(),
		readOnly:  model.NewId(),
		readWrite: model.NewId(),
		other:     model.NewId(),
	}

	e := SetupTestEnvironment(t)
	e.mockAPI.On("HasPermissionTo", users.admin, model.PermissionManageSystem).Return(true).Maybe()
	e.mockAPI.On("HasPermissionTo", mock.Anything, mock.Anything).Return(false).Maybe()

	access := config.SettingsAccess{
		ReadOnlyUserIDs:  []string{users.readOnly},
		ReadWriteUserIDs: []string{users.readWrite},
	}
	e.config.settingsAccess = access
	if stored != nil {
		stored.SettingsAccess = &access
	}

	store := &testConfigStore{cfg: stored}
	updater := &testConfigUpdater{}
	e.api.configStore = store
	e.api.configUpdater = updater
	e.api.clusterNotifier = &testClusterNotifier{}
	return e, users, store, updater
}

func TestSettingsRoutesAuthorization(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		body    any
		allowed map[string]bool // keyed by role
	}{
		{
			name:    "read the configuration",
			method:  http.MethodGet,
			path:    "/admin/config",
			allowed: map[string]bool{"admin": true, "readOnly": true, "readWrite": true},
		},
		{
			name:    "save the configuration",
			method:  http.MethodPut,
			path:    "/admin/config",
			body:    config.Config{},
			allowed: map[string]bool{"admin": true, "readWrite": true},
		},
		{
			name:    "read the MCP tools",
			method:  http.MethodGet,
			path:    "/admin/mcp/tools",
			allowed: map[string]bool{"admin": true, "readOnly": true, "readWrite": true},
		},
		{
			name:    "clear the MCP tools cache",
			method:  http.MethodPost,
			path:    "/admin/mcp/tools/cache/clear",
			allowed: map[string]bool{"admin": true, "readWrite": true},
		},
		{
			name:    "fetch models with credentials",
			method:  http.MethodPost,
			path:    "/admin/models/fetch",
			body:    map[string]any{},
			allowed: map[string]bool{"admin": true, "readWrite": true},
		},
		{
			name:    "read the delegation lists",
			method:  http.MethodGet,
			path:    "/admin/settings-access",
			allowed: map[string]bool{"admin": true},
		},
		{
			name:    "save the delegation lists",
			method:  http.MethodPut,
			path:    "/admin/settings-access",
			body:    config.SettingsAccess{},
			allowed: map[string]bool{"admin": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, users, _, _ := setupSettingsAccessEnv(t, &config.Config{})
			defer e.Cleanup(t)

			for role, userID := range map[string]string{
				"admin":     users.admin,
				"readOnly":  users.readOnly,
				"readWrite": users.readWrite,
				"other":     users.other,
			} {
				recorder := doRequest(e.api, tt.method, tt.path, tt.body, userID)
				if tt.allowed[role] {
					assert.NotEqual(t, http.StatusForbidden, recorder.Code, "%s should reach %s %s", role, tt.method, tt.path)
				} else {
					assert.Equal(t, http.StatusForbidden, recorder.Code, "%s must not reach %s %s", role, tt.method, tt.path)
				}
			}
		})
	}
}

func TestGetMySettingsAccess(t *testing.T) {
	e, users, _, _ := setupSettingsAccessEnv(t, &config.Config{})
	defer e.Cleanup(t)

	tests := []struct {
		name   string
		userID string
		level  string
	}{
		{name: "system admin", userID: users.admin, level: "write"},
		{name: "read/write delegate", userID: users.readWrite, level: "write"},
		{name: "read-only delegate", userID: users.readOnly, level: "read"},
		{name: "anyone else", userID: users.other, level: "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := doRequest(e.api, http.MethodGet, "/settings/access", nil, tt.userID)
			require.Equal(t, http.StatusOK, recorder.Code)
			assert.JSONEq(t, `{"level":"`+tt.level+`"}`, recorder.Body.String())
		})
	}
}

func configWithEverySecret() *config.Config {
	return &config.Config{
		Services: []llm.ServiceConfig{{
			ID:                    "service1",
			Name:                  "Primary",
			Type:                  llm.ServiceTypeAnthropic,
			APIKey:                "sentinel-service-api-key",
			AWSAccessKeyID:        "sentinel-aws-access-key-id",
			AWSSecretAccessKey:    "sentinel-aws-secret",
			VertexAuthCredentials: "sentinel-vertex-credentials",
		}},
		MCP: config.MCPConfig{
			Servers: []config.MCPServerConfig{{
				ID:                    "server1",
				Name:                  "Tracker",
				BaseURL:               "https://mcp.example.com",
				Headers:               map[string]string{"X-Api-Key": "sentinel-mcp-header"},
				ServiceAccountHeaders: map[string]string{"Authorization": "sentinel-mcp-service-account"},
				ClientID:              "client-id",
				ClientSecret:          "sentinel-mcp-client-secret",
			}},
		},
		WebSearch: config.WebSearchConfig{
			Google: config.WebSearchGoogleConfig{APIKey: "sentinel-google-key"},
			Brave:  config.WebSearchBraveConfig{APIKey: "sentinel-brave-key"},
		},
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			EmbeddingProvider: embeddings.UpstreamConfig{
				Type:       "openai",
				Parameters: json.RawMessage(`{"apiKey":"sentinel-embedding-key","embeddingModel":"text-embedding-3-small"}`),
			},
		},
		OpenTelemetryHeaders: "Authorization=sentinel-otlp-header",
	}
}

var everySecret = []string{
	"sentinel-service-api-key",
	"sentinel-aws-access-key-id",
	"sentinel-aws-secret",
	"sentinel-vertex-credentials",
	"sentinel-mcp-header",
	"sentinel-mcp-service-account",
	"sentinel-mcp-client-secret",
	"sentinel-google-key",
	"sentinel-brave-key",
	"sentinel-embedding-key",
	"sentinel-otlp-header",
}

func TestGetConfigSecretsByAccessLevel(t *testing.T) {
	tests := []struct {
		name        string
		role        string
		wantSecrets bool
	}{
		{name: "system admin sees credentials", role: "admin", wantSecrets: true},
		{name: "read/write delegate sees credentials", role: "readWrite", wantSecrets: true},
		{name: "read-only delegate does not", role: "readOnly", wantSecrets: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, users, _, _ := setupSettingsAccessEnv(t, configWithEverySecret())
			defer e.Cleanup(t)
			userID := map[string]string{"admin": users.admin, "readWrite": users.readWrite, "readOnly": users.readOnly}[tt.role]

			recorder := doRequest(e.api, http.MethodGet, "/admin/config", nil, userID)
			require.Equal(t, http.StatusOK, recorder.Code)
			body := recorder.Body.String()

			for _, secret := range everySecret {
				if tt.wantSecrets {
					assert.Contains(t, body, secret)
				} else {
					assert.NotContains(t, body, secret)
				}
			}

			// The rest of the configuration is still there for the viewer, and
			// nobody gets the delegation lists from this route.
			assert.Contains(t, body, "Primary")
			assert.Contains(t, body, "text-embedding-3-small")
			assert.Contains(t, body, "X-Api-Key")
			assert.NotContains(t, body, "settingsAccess")
			assert.NotContains(t, body, users.readOnly)
		})
	}
}

func TestSaveConfigKeepsDelegationLists(t *testing.T) {
	e, users, store, _ := setupSettingsAccessEnv(t, &config.Config{})
	defer e.Cleanup(t)

	// A read/write delegate tries to promote someone through the config save.
	body := map[string]any{
		"services": []any{},
		"bots":     []any{},
		"settingsAccess": map[string]any{
			"readOnlyUserIDs":  []string{},
			"readWriteUserIDs": []string{users.readWrite, users.other},
		},
	}
	recorder := doRequest(e.api, http.MethodPut, "/admin/config", body, users.readWrite)
	require.Equal(t, http.StatusOK, recorder.Code)

	require.NotNil(t, store.cfg.SettingsAccess)
	assert.Equal(t, []string{users.readOnly}, store.cfg.SettingsAccess.ReadOnlyUserIDs)
	assert.Equal(t, []string{users.readWrite}, store.cfg.SettingsAccess.ReadWriteUserIDs)
	assert.NotContains(t, recorder.Body.String(), "settingsAccess")
}

func TestPutSettingsAccess(t *testing.T) {
	newReadOnly := model.NewId()
	newReadWrite := model.NewId()

	tests := []struct {
		name           string
		body           any
		expectedStatus int
		expected       *config.SettingsAccess
	}{
		{
			name: "replaces both lists",
			body: config.SettingsAccess{
				ReadOnlyUserIDs:  []string{newReadOnly},
				ReadWriteUserIDs: []string{newReadWrite},
			},
			expectedStatus: http.StatusOK,
			expected: &config.SettingsAccess{
				ReadOnlyUserIDs:  []string{newReadOnly},
				ReadWriteUserIDs: []string{newReadWrite},
			},
		},
		{
			name: "a user on both lists keeps read/write only",
			body: config.SettingsAccess{
				ReadOnlyUserIDs:  []string{newReadOnly, newReadWrite},
				ReadWriteUserIDs: []string{newReadWrite, newReadWrite},
			},
			expectedStatus: http.StatusOK,
			expected: &config.SettingsAccess{
				ReadOnlyUserIDs:  []string{newReadOnly},
				ReadWriteUserIDs: []string{newReadWrite},
			},
		},
		{
			name:           "clears both lists",
			body:           map[string]any{},
			expectedStatus: http.StatusOK,
			expected:       &config.SettingsAccess{ReadOnlyUserIDs: []string{}, ReadWriteUserIDs: []string{}},
		},
		{
			name:           "rejects something that is not a user ID",
			body:           config.SettingsAccess{ReadWriteUserIDs: []string{"alice"}},
			expectedStatus: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := &config.Config{DefaultBotUserID: "keepme"}
			e, users, store, updater := setupSettingsAccessEnv(t, stored)
			defer e.Cleanup(t)
			before := *store.cfg.SettingsAccess

			recorder := doRequest(e.api, http.MethodPut, "/admin/settings-access", tt.body, users.admin)
			require.Equal(t, tt.expectedStatus, recorder.Code)

			if tt.expected == nil {
				assert.Equal(t, before, *store.cfg.SettingsAccess)
				assert.Zero(t, updater.callCount)
				return
			}
			assert.Equal(t, *tt.expected, *store.cfg.SettingsAccess)
			assert.Equal(t, "keepme", store.cfg.DefaultBotUserID, "the rest of the configuration is untouched")
			assert.Equal(t, 1, updater.callCount, "this node's in-memory configuration is updated")

			var got config.SettingsAccess
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
			assert.Equal(t, *tt.expected, got)

			recorder = doRequest(e.api, http.MethodGet, "/admin/settings-access", nil, users.admin)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
			assert.Equal(t, *tt.expected, got)
		})
	}
}

func TestGetSettingsAccessEncodesEmptyLists(t *testing.T) {
	e, users, store, _ := setupSettingsAccessEnv(t, &config.Config{})
	defer e.Cleanup(t)
	store.cfg.SettingsAccess = nil

	recorder := doRequest(e.api, http.MethodGet, "/admin/settings-access", nil, users.admin)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"readOnlyUserIDs":[],"readWriteUserIDs":[]}`, recorder.Body.String())
}

func TestPutSettingsAccessAuditRecord(t *testing.T) {
	e, users, _, _ := setupSettingsAccessEnv(t, &config.Config{})
	defer e.Cleanup(t)
	records := e.CaptureAuditRecords()

	granted := model.NewId()
	recorder := doRequest(e.api, http.MethodPut, "/admin/settings-access", config.SettingsAccess{ReadWriteUserIDs: []string{granted}}, users.admin)
	require.Equal(t, http.StatusOK, recorder.Code)

	require.Len(t, *records, 1)
	rec := (*records)[0]
	assert.Equal(t, AuditEventSaveSettingsAccess, rec.EventName)
	assert.Equal(t, model.AuditStatusSuccess, rec.Status)
	assert.Equal(t, users.admin, rec.Actor.UserId)
	assert.Equal(t, []string{granted}, rec.EventData.Parameters["read_write_user_ids"])
}

func TestCELTestForServicePoliciesFollowsSettingsAccess(t *testing.T) {
	tests := []struct {
		name    string
		role    string
		allowed bool
	}{
		{name: "read/write delegate may test", role: "readWrite", allowed: true},
		{name: "read-only delegate may not", role: "readOnly", allowed: false},
		{name: "anyone else may not", role: "other", allowed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, users, _, _ := setupSettingsAccessEnv(t, &config.Config{})
			defer e.Cleanup(t)
			e.api.accessChecker = accesscontrol.New(accesscontrol.PassthroughClient{}, e.mockAPI, accesscontrol.NoMCPServerIDs, nil)
			userID := map[string]string{"readWrite": users.readWrite, "readOnly": users.readOnly, "other": users.other}[tt.role]

			if tt.allowed {
				// Delegates must match the expression themselves, as other
				// non-admin policy authors do.
				e.mockAPI.On("GetUser", userID).Return(&model.User{Id: userID, Username: "delegate"}, nil).Maybe()
				e.mockAPI.On("QueryUsersForAccessControlExpression", userID, accesscontrol.ResourceTypeService, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&model.AccessControlPolicyTestResponse{Users: []*model.User{{Id: userID}}, Total: 1}, nil).Maybe()
			}

			body := map[string]any{
				"resource_type": accesscontrol.ResourceTypeService,
				"expression":    `user.attributes.team == "eng"`,
			}
			recorder := doRequest(e.api, http.MethodPost, "/access_control/cel/test", body, userID)
			if tt.allowed {
				assert.Equal(t, http.StatusOK, recorder.Code)
			} else {
				assert.Equal(t, http.StatusForbidden, recorder.Code)
			}
		})
	}
}
