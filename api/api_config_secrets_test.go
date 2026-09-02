// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Sentinel values stand in for stored third-party credentials. None of them may
// appear in the body of GET /admin/config.
const (
	sentinelServiceAPIKey        = "sentinel-service-api-key"
	sentinelServiceAWSSecret     = "sentinel-aws-secret"
	sentinelServiceVertexJSON    = "sentinel-vertex-json"
	sentinelBotServiceAPIKey     = "sentinel-bot-service-api-key"    // #nosec G101 -- test fixture value
	sentinelBotServiceAWSSecret  = "sentinel-bot-service-aws-secret" // #nosec G101 -- test fixture value
	sentinelBotServiceVertexJSON = "sentinel-bot-service-vertex-json"
	sentinelMCPClientSecret      = "sentinel-mcp-client-secret" // #nosec G101 -- test fixture value
	sentinelMCPHeaderToken       = "sentinel-mcp-header-token"
	sentinelMCPServiceAccount    = "sentinel-mcp-sa-header-token"
	sentinelGoogleAPIKey         = "sentinel-google-key"
	sentinelBraveAPIKey          = "sentinel-brave-key"
	sentinelEmbeddingAPIKey      = "sentinel-embedding-key"
)

// Identifiers are not credentials: the read response must keep returning them
// verbatim so the admin console can render the configuration.
const (
	visibleAWSAccessKeyID     = "AKIAVISIBLEACCESSID"
	visibleBotAWSAccessKeyID  = "AKIALEGACYACCESSID"
	visibleOrgID              = "org-visible-1"
	visibleServiceAPIURL      = "https://service.example.com/v1"
	visibleServiceRegion      = "us-east-1"
	visibleVertexProjectID    = "vertex-project-visible"
	visibleVertexProjectNum   = "1234567890"
	visibleMCPClientID        = "mcp-client-id-visible"
	visibleMCPBaseURL         = "https://mcp.example.com"
	visibleMCPHeaderName      = "Authorization"
	visibleMCPOtherHeaderName = "X-Tenant"
	visibleSearchEngineID     = "search-engine-id-visible"
	visibleEmbeddingModel     = "text-embedding-3-small"
	visibleEmbeddingAPIURL    = "https://embeddings.example.com/v1"
)

// storedCredentialConfig is a configuration in which every credential-bearing
// field holds a distinct sentinel and every identifier holds a distinct
// recognizable value.
func storedCredentialConfig() *config.Config {
	return &config.Config{
		DefaultBotName: "ai",
		Services: []llm.ServiceConfig{
			{
				ID:                    "svc-1",
				Name:                  "OpenAI",
				Type:                  llm.ServiceTypeOpenAI,
				APIKey:                sentinelServiceAPIKey,
				OrgID:                 visibleOrgID,
				APIURL:                visibleServiceAPIURL,
				Region:                visibleServiceRegion,
				AWSAccessKeyID:        visibleAWSAccessKeyID,
				AWSSecretAccessKey:    sentinelServiceAWSSecret,
				VertexProjectID:       visibleVertexProjectID,
				VertexProjectNumber:   visibleVertexProjectNum,
				VertexAuthCredentials: sentinelServiceVertexJSON,
			},
		},
		Bots: []llm.BotConfig{
			{
				ID:        "bot-1",
				Name:      "ai",
				ServiceID: "svc-1",
				// Deprecated inline service kept by not-yet-migrated installs.
				Service: &llm.ServiceConfig{
					ID:                    "svc-legacy",
					Type:                  llm.ServiceTypeBedrock,
					APIKey:                sentinelBotServiceAPIKey,
					AWSAccessKeyID:        visibleBotAWSAccessKeyID,
					AWSSecretAccessKey:    sentinelBotServiceAWSSecret,
					VertexAuthCredentials: sentinelBotServiceVertexJSON,
				},
			},
		},
		MCP: config.MCPConfig{
			Servers: []config.MCPServerConfig{
				{
					Name:         "Jira",
					Enabled:      true,
					BaseURL:      visibleMCPBaseURL,
					ClientID:     visibleMCPClientID,
					ClientSecret: sentinelMCPClientSecret,
					Headers: map[string]string{
						visibleMCPHeaderName:      "Bearer " + sentinelMCPHeaderToken,
						visibleMCPOtherHeaderName: "tenant-visible",
					},
					ServiceAccountHeaders: map[string]string{
						visibleMCPHeaderName: "Bearer " + sentinelMCPServiceAccount,
					},
				},
			},
		},
		WebSearch: config.WebSearchConfig{
			Enabled:  true,
			Provider: "google",
			Google: config.WebSearchGoogleConfig{
				APIKey:         sentinelGoogleAPIKey,
				SearchEngineID: visibleSearchEngineID,
			},
			Brave: config.WebSearchBraveConfig{
				APIKey: sentinelBraveAPIKey,
			},
		},
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			Type: "composite",
			EmbeddingProvider: embeddings.UpstreamConfig{
				Type: "openai",
				Parameters: json.RawMessage(`{"apiKey":"` + sentinelEmbeddingAPIKey +
					`","embeddingModel":"` + visibleEmbeddingModel +
					`","apiURL":"` + visibleEmbeddingAPIURL + `"}`),
			},
		},
	}
}

// credentialField addresses a single credential-bearing field of the plugin
// configuration.
type credentialField struct {
	name string
	get  func(cfg config.Config) string
	set  func(cfg *config.Config, value string)
}

func embeddingProviderParam(cfg config.Config, key string) string {
	raw := cfg.EmbeddingSearchConfig.EmbeddingProvider.Parameters
	if len(raw) == 0 {
		return ""
	}
	params := map[string]any{}
	if err := json.Unmarshal(raw, &params); err != nil {
		return ""
	}
	value, _ := params[key].(string)
	return value
}

func setEmbeddingProviderParam(cfg *config.Config, key, value string) {
	params := map[string]any{}
	if raw := cfg.EmbeddingSearchConfig.EmbeddingProvider.Parameters; len(raw) > 0 {
		_ = json.Unmarshal(raw, &params)
	}
	params[key] = value
	encoded, err := json.Marshal(params)
	if err != nil {
		return
	}
	cfg.EmbeddingSearchConfig.EmbeddingProvider.Parameters = encoded
}

// credentialFields lists every credential-bearing field of storedCredentialConfig.
func credentialFields() []credentialField {
	return []credentialField{
		{
			name: "services[0].apiKey",
			get:  func(cfg config.Config) string { return cfg.Services[0].APIKey },
			set:  func(cfg *config.Config, v string) { cfg.Services[0].APIKey = v },
		},
		{
			name: "services[0].awsSecretAccessKey",
			get:  func(cfg config.Config) string { return cfg.Services[0].AWSSecretAccessKey },
			set:  func(cfg *config.Config, v string) { cfg.Services[0].AWSSecretAccessKey = v },
		},
		{
			name: "services[0].vertexAuthCredentials",
			get:  func(cfg config.Config) string { return cfg.Services[0].VertexAuthCredentials },
			set:  func(cfg *config.Config, v string) { cfg.Services[0].VertexAuthCredentials = v },
		},
		{
			name: "bots[0].service.apiKey",
			get: func(cfg config.Config) string {
				if cfg.Bots[0].Service == nil {
					return ""
				}
				return cfg.Bots[0].Service.APIKey
			},
			set: func(cfg *config.Config, v string) { cfg.Bots[0].Service.APIKey = v },
		},
		{
			name: "bots[0].service.awsSecretAccessKey",
			get: func(cfg config.Config) string {
				if cfg.Bots[0].Service == nil {
					return ""
				}
				return cfg.Bots[0].Service.AWSSecretAccessKey
			},
			set: func(cfg *config.Config, v string) { cfg.Bots[0].Service.AWSSecretAccessKey = v },
		},
		{
			name: "bots[0].service.vertexAuthCredentials",
			get: func(cfg config.Config) string {
				if cfg.Bots[0].Service == nil {
					return ""
				}
				return cfg.Bots[0].Service.VertexAuthCredentials
			},
			set: func(cfg *config.Config, v string) { cfg.Bots[0].Service.VertexAuthCredentials = v },
		},
		{
			name: "mcp.servers[0].clientSecret",
			get:  func(cfg config.Config) string { return cfg.MCP.Servers[0].ClientSecret },
			set:  func(cfg *config.Config, v string) { cfg.MCP.Servers[0].ClientSecret = v },
		},
		{
			name: "mcp.servers[0].headers[Authorization]",
			get:  func(cfg config.Config) string { return cfg.MCP.Servers[0].Headers[visibleMCPHeaderName] },
			set: func(cfg *config.Config, v string) {
				cfg.MCP.Servers[0].Headers[visibleMCPHeaderName] = v
			},
		},
		{
			name: "mcp.servers[0].serviceAccountHeaders[Authorization]",
			get:  func(cfg config.Config) string { return cfg.MCP.Servers[0].ServiceAccountHeaders[visibleMCPHeaderName] },
			set: func(cfg *config.Config, v string) {
				cfg.MCP.Servers[0].ServiceAccountHeaders[visibleMCPHeaderName] = v
			},
		},
		{
			name: "webSearch.google.apiKey",
			get:  func(cfg config.Config) string { return cfg.WebSearch.Google.APIKey },
			set:  func(cfg *config.Config, v string) { cfg.WebSearch.Google.APIKey = v },
		},
		{
			name: "webSearch.brave.apiKey",
			get:  func(cfg config.Config) string { return cfg.WebSearch.Brave.APIKey },
			set:  func(cfg *config.Config, v string) { cfg.WebSearch.Brave.APIKey = v },
		},
		{
			name: "embeddingSearchConfig.embeddingProvider.parameters.apiKey",
			get:  func(cfg config.Config) string { return embeddingProviderParam(cfg, "apiKey") },
			set: func(cfg *config.Config, v string) {
				setEmbeddingProviderParam(cfg, "apiKey", v)
			},
		},
	}
}

// getAdminConfig issues GET /admin/config and returns the raw response body.
func getAdminConfig(t *testing.T, store *testConfigStore) []byte {
	t.Helper()

	router := setupTestRouter(store, &testConfigUpdater{}, &testClusterNotifier{})
	req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	return w.Body.Bytes()
}

// TestGetConfigDoesNotReturnStoredCredentials asserts the read response never
// carries a stored credential.
func TestGetConfigDoesNotReturnStoredCredentials(t *testing.T) {
	body := string(getAdminConfig(t, &testConfigStore{cfg: storedCredentialConfig()}))

	tests := []struct {
		field    string
		sentinel string
	}{
		{field: "services[0].apiKey", sentinel: sentinelServiceAPIKey},
		{field: "services[0].awsSecretAccessKey", sentinel: sentinelServiceAWSSecret},
		{field: "services[0].vertexAuthCredentials", sentinel: sentinelServiceVertexJSON},
		{field: "bots[0].service.apiKey", sentinel: sentinelBotServiceAPIKey},
		{field: "bots[0].service.awsSecretAccessKey", sentinel: sentinelBotServiceAWSSecret},
		{field: "bots[0].service.vertexAuthCredentials", sentinel: sentinelBotServiceVertexJSON},
		{field: "mcp.servers[0].clientSecret", sentinel: sentinelMCPClientSecret},
		{field: "mcp.servers[0].headers[Authorization]", sentinel: sentinelMCPHeaderToken},
		{field: "mcp.servers[0].serviceAccountHeaders[Authorization]", sentinel: sentinelMCPServiceAccount},
		{field: "webSearch.google.apiKey", sentinel: sentinelGoogleAPIKey},
		{field: "webSearch.brave.apiKey", sentinel: sentinelBraveAPIKey},
		{field: "embeddingSearchConfig.embeddingProvider.parameters.apiKey", sentinel: sentinelEmbeddingAPIKey},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			assert.NotContains(t, body, tt.sentinel,
				"the stored value of %s must not appear in the response of GET /admin/config", tt.field)
		})
	}
}

// TestGetConfigPreservesNonCredentialFields asserts that hiding credentials
// does not blank out identifiers the admin console needs to render.
func TestGetConfigPreservesNonCredentialFields(t *testing.T) {
	rawBody := getAdminConfig(t, &testConfigStore{cfg: storedCredentialConfig()})
	body := string(rawBody)

	tests := []struct {
		field string
		value string
	}{
		{field: "services[0].awsAccessKeyID", value: visibleAWSAccessKeyID},
		{field: "services[0].orgId", value: visibleOrgID},
		{field: "services[0].apiURL", value: visibleServiceAPIURL},
		{field: "services[0].region", value: visibleServiceRegion},
		{field: "services[0].vertexProjectID", value: visibleVertexProjectID},
		{field: "services[0].vertexProjectNumber", value: visibleVertexProjectNum},
		{field: "bots[0].service.awsAccessKeyID", value: visibleBotAWSAccessKeyID},
		{field: "mcp.servers[0].clientID", value: visibleMCPClientID},
		{field: "mcp.servers[0].baseURL", value: visibleMCPBaseURL},
		{field: "mcp.servers[0].headers key", value: visibleMCPHeaderName},
		{field: "mcp.servers[0].headers second key", value: visibleMCPOtherHeaderName},
		{field: "webSearch.google.searchEngineId", value: visibleSearchEngineID},
		{field: "embeddingSearchConfig.embeddingProvider.parameters.embeddingModel", value: visibleEmbeddingModel},
		{field: "embeddingSearchConfig.embeddingProvider.parameters.apiURL", value: visibleEmbeddingAPIURL},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			assert.Contains(t, body, tt.value, "%s must be returned verbatim", tt.field)
		})
	}

	var got config.Config
	require.NoError(t, json.Unmarshal(rawBody, &got))
	require.Len(t, got.Services, 1)
	assert.Equal(t, "svc-1", got.Services[0].ID)
	require.Len(t, got.Bots, 1)
	assert.Equal(t, "bot-1", got.Bots[0].ID)
	require.Len(t, got.MCP.Servers, 1)
	assert.Equal(t, visibleEmbeddingModel, embeddingProviderParam(got, "embeddingModel"))
}

// TestGetConfigDistinguishesStoredFromUnsetCredentials asserts the read
// response tells a configured credential apart from an unconfigured one: a
// stored credential comes back as some non-empty value other than the stored
// one, an unset credential comes back empty.
func TestGetConfigDistinguishesStoredFromUnsetCredentials(t *testing.T) {
	tests := []struct {
		name       string
		configured bool
	}{
		{name: "stored", configured: true},
		{name: "unset", configured: false},
	}

	for _, field := range credentialFields() {
		for _, tt := range tests {
			t.Run(field.name+"/"+tt.name, func(t *testing.T) {
				stored := storedCredentialConfig()
				if !tt.configured {
					field.set(stored, "")
				}
				storedValue := field.get(*stored)

				var got config.Config
				require.NoError(t, json.Unmarshal(getAdminConfig(t, &testConfigStore{cfg: stored}), &got))
				returned := field.get(got)

				if !tt.configured {
					assert.Empty(t, returned, "%s is not configured and must come back empty", field.name)
					return
				}

				assert.NotEmpty(t, returned, "%s is configured and must come back as a non-empty value", field.name)
				assert.NotEqual(t, storedValue, returned, "%s must not come back as its stored value", field.name)
			})
		}
	}
}

// TestSaveConfigCredentialRoundTrip drives the client flow (GET then PUT) and
// asserts what the save leaves in the store: an unmodified read response keeps
// every stored credential, an explicit value replaces one, and an empty value
// clears one. In all three cases the credentials the admin did not touch must
// survive the save.
func TestSaveConfigCredentialRoundTrip(t *testing.T) {
	const replacementValue = "replacement-credential-value"

	tests := []struct {
		name string
		// apply mutates the decoded read response for the field under test.
		// nil sends the read response back verbatim.
		apply func(cfg *config.Config, field credentialField)
		// expect asserts the value the save left in the store for that field.
		expect func(t *testing.T, field credentialField, originalValue, savedValue string)
	}{
		{
			name:  "unmodified read response preserves the stored credential",
			apply: nil,
			expect: func(t *testing.T, field credentialField, originalValue, savedValue string) {
				assert.Equal(t, originalValue, savedValue,
					"saving back an unmodified read response must preserve the stored value of %s", field.name)
			},
		},
		{
			name: "explicit value replaces the stored credential",
			apply: func(cfg *config.Config, field credentialField) {
				field.set(cfg, replacementValue)
			},
			expect: func(t *testing.T, field credentialField, _, savedValue string) {
				assert.Equal(t, replacementValue, savedValue,
					"an explicit value must replace the stored value of %s", field.name)
			},
		},
		{
			name: "empty value clears the stored credential",
			apply: func(cfg *config.Config, field credentialField) {
				field.set(cfg, "")
			},
			expect: func(t *testing.T, field credentialField, _, savedValue string) {
				assert.Empty(t, savedValue,
					"an empty value must clear the stored value of %s", field.name)
			},
		},
	}

	for _, tt := range tests {
		for _, field := range credentialFields() {
			t.Run(tt.name+"/"+field.name, func(t *testing.T) {
				original := storedCredentialConfig()
				store := &testConfigStore{cfg: storedCredentialConfig()}
				router := setupTestRouter(store, &testConfigUpdater{}, &testClusterNotifier{})

				req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				require.Equal(t, http.StatusOK, w.Code)

				body := w.Body.Bytes()
				if tt.apply != nil {
					var decoded config.Config
					require.NoError(t, json.Unmarshal(body, &decoded))
					tt.apply(&decoded, field)

					var err error
					body, err = json.Marshal(decoded)
					require.NoError(t, err)
				}

				req = httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				w = httptest.NewRecorder()
				router.ServeHTTP(w, req)
				require.Equal(t, http.StatusOK, w.Code)

				require.NotNil(t, store.cfg)
				saved := *store.cfg

				tt.expect(t, field, field.get(*original), field.get(saved))

				for _, other := range credentialFields() {
					if other.name == field.name {
						continue
					}
					assert.Equal(t, other.get(*original), other.get(saved),
						"saving a change to %s must not alter the stored value of %s", field.name, other.name)
				}
			})
		}
	}
}

// TestFetchModelsRejectsPlaceholderAsCredential asserts a masked credential is
// never forwarded to a provider as if it were a real one. Without a service
// reference the request carries nothing that could be resolved to a stored
// credential, so it must be rejected.
func TestFetchModelsRejectsPlaceholderAsCredential(t *testing.T) {
	const maskedCredential = "**********"

	var upstreamCalls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upstreamCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o","object":"model"}]}`))
	}))
	defer upstream.Close()

	api, mockAPI, _ := setupAdminTestEnvironment(t)
	defer mockAPI.AssertExpectations(t)

	mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
	mockAPI.On("LogError", mock.Anything).Return().Maybe()
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogWarn", mock.Anything).Return().Maybe()
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogInfo", mock.Anything).Return().Maybe()
	mockAPI.On("LogDebug", mock.Anything).Return().Maybe()

	raw, err := json.Marshal(map[string]any{
		"serviceType": llm.ServiceTypeOpenAICompatible,
		"apiKey":      maskedCredential,
		"apiURL":      upstream.URL + "/v1",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/admin/models/fetch", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mattermost-User-Id", "admin-user")

	recorder := httptest.NewRecorder()
	api.ServeHTTP(&plugin.Context{}, recorder, req)

	assert.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode,
		"a masked credential with no service reference must be rejected")
	assert.Zero(t, atomic.LoadInt32(&upstreamCalls),
		"a masked credential must never be sent to a provider")
}

// recordingUpstream is a provider stand-in that answers a model listing and
// records every request header it was given.
type recordingUpstream struct {
	server *httptest.Server

	mu      sync.Mutex
	headers []http.Header
}

func newRecordingUpstream() *recordingUpstream {
	u := &recordingUpstream{}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.headers = append(u.headers, r.Header.Clone())
		u.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o","object":"model"}]}`))
	}))
	return u
}

func (u *recordingUpstream) apiURL() string {
	return u.server.URL + "/v1"
}

// sawValue reports whether any recorded request carried value in a header.
func (u *recordingUpstream) sawValue(value string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	for _, header := range u.headers {
		for _, values := range header {
			for _, v := range values {
				if strings.Contains(v, value) {
					return true
				}
			}
		}
	}
	return false
}

// TestFetchModelsSendsStoredCredentialOnlyToStoredEndpoint asserts that the
// credentials of a saved service reach the endpoint that service is saved with,
// and no other. The admin console names a saved service instead of repeating its
// credentials, so the endpoint the request carries is what decides whether the
// stored credentials apply to it.
func TestFetchModelsSendsStoredCredentialOnlyToStoredEndpoint(t *testing.T) {
	const storedServiceID = "svc-saved"

	tests := []struct {
		name        string
		serviceType string
		// storedURL is the endpoint held by the saved service. A provider reached
		// at a fixed address holds none.
		storedURL func(savedURL string) string
		// requestURL is the endpoint carried by the request.
		requestURL func(savedURL, otherURL string) string
		// wantAtSaved is whether the saved endpoint must receive the credentials.
		wantAtSaved bool
	}{
		{
			name:        "request naming the endpoint the service is saved with",
			serviceType: llm.ServiceTypeOpenAICompatible,
			storedURL:   func(savedURL string) string { return savedURL },
			requestURL:  func(savedURL, _ string) string { return savedURL },
			wantAtSaved: true,
		},
		{
			name:        "request naming an unrelated endpoint",
			serviceType: llm.ServiceTypeOpenAICompatible,
			storedURL:   func(savedURL string) string { return savedURL },
			requestURL:  func(_, otherURL string) string { return otherURL },
		},
		{
			name:        "request naming an endpoint for a service saved without one",
			serviceType: llm.ServiceTypeOpenAI,
			storedURL:   func(string) string { return "" },
			requestURL:  func(_, otherURL string) string { return otherURL },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saved := newRecordingUpstream()
			defer saved.server.Close()
			other := newRecordingUpstream()
			defer other.server.Close()

			api, mockAPI, stores := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()
			mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
			mockAPI.On("LogWarn", mock.Anything).Return().Maybe()
			mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
			mockAPI.On("LogInfo", mock.Anything).Return().Maybe()
			mockAPI.On("LogDebug", mock.Anything).Return().Maybe()

			stores.configStore.cfg = &config.Config{
				Services: []llm.ServiceConfig{{
					ID:     storedServiceID,
					Type:   tt.serviceType,
					APIKey: sentinelServiceAPIKey,
					APIURL: tt.storedURL(saved.apiURL()),
				}},
			}

			raw, err := json.Marshal(map[string]any{
				"serviceType": tt.serviceType,
				"serviceID":   storedServiceID,
				"apiURL":      tt.requestURL(saved.apiURL(), other.apiURL()),
			})
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/admin/models/fetch", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Mattermost-User-Id", "admin-user")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			assert.False(t, other.sawValue(sentinelServiceAPIKey),
				"the credentials of a saved service must not be sent to an endpoint the service is not saved with")

			if tt.wantAtSaved {
				assert.Equal(t, http.StatusOK, recorder.Result().StatusCode,
					"naming the endpoint a service is saved with must list its models")
				assert.True(t, saved.sawValue(sentinelServiceAPIKey),
					"the endpoint a service is saved with must receive that service's credentials")
			}
		})
	}
}
