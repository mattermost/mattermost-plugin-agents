// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
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

// A failure to remove sign-ins does not fail the save: the sign-ins can no
// longer be used with the changed server anyway. The audit record names the
// affected server and marks the failure.
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
	e.mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	body, err := json.Marshal(config.Config{MCP: config.MCPConfig{Servers: []config.MCPServerConfig{}}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
	req.Header.Set("Mattermost-User-Id", "userid")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.api.ServeHTTP(nil, w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Empty(t, cfgStore.cfg.MCP.Servers, "the configuration change is saved")
	assert.Equal(t, 1, updater.callCount, "the saved configuration is applied on this node")
	assert.Equal(t, 1, notifier.callCount, "other nodes are told to reload")

	require.Len(t, *records, 1)
	params := (*records)[0].EventData.Parameters
	assert.Equal(t, []string{jiraID}, params[audit.KeyMCPSignInsClearedServerIDs])
	assert.Equal(t, true, params[audit.KeyMCPSignInsClearFailed])
	assert.Equal(t, true, params["persisted"])
	raw, err := json.Marshal((*records)[0])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), failing, "stored keys never enter audit records")
}

// The sweep is not tied to the request: an admin closing the page right after
// saving does not stop users' sign-ins from being removed.
func TestHandleSaveConfigClearsSignInsAfterRequestCancelled(t *testing.T) {
	const alice = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	jira := config.MCPServerConfig{ID: "jirajirajirajirajirajira00", Name: "Jira", Enabled: true, BaseURL: "https://jira.example.com/mcp"}
	signIn := "mcp_oauth_token_v1_" + alice + "_Jira"

	pluginClient, deleted := kvKeysPluginClient([]string{signIn})
	router := setupConfigTestRouter(&API{
		configStore:     &testConfigStore{cfg: &config.Config{MCP: config.MCPConfig{Servers: []config.MCPServerConfig{jira}}}},
		configUpdater:   &testConfigUpdater{},
		clusterNotifier: &testClusterNotifier{},
		licenseChecker:  enterprisetest.CheckerAt(enterprise.LevelEnterpriseAdvanced),
		pluginAPI:       pluginClient,
	})

	body, err := json.Marshal(config.Config{MCP: config.MCPConfig{Servers: []config.MCPServerConfig{}}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, []string{signIn}, deleted())
}

type orderRecordingUpdater struct {
	deleted      func() []string
	deletedAtRun []string
}

func (u *orderRecordingUpdater) Update(*config.Config) { u.deletedAtRun = u.deleted() }

// The saved configuration is applied before the sign-in sweep starts, so a
// slow sweep never delays a deleted or moved server going out of use.
func TestHandleSaveConfigAppliesConfigBeforeSignInSweep(t *testing.T) {
	const alice = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	jira := config.MCPServerConfig{ID: "jirajirajirajirajirajira00", Name: "Jira", Enabled: true, BaseURL: "https://jira.example.com/mcp"}
	signIn := "mcp_oauth_token_v1_" + alice + "_Jira"

	pluginClient, deleted := kvKeysPluginClient([]string{signIn})
	updater := &orderRecordingUpdater{deleted: deleted}
	router := setupConfigTestRouter(&API{
		configStore:     &testConfigStore{cfg: &config.Config{MCP: config.MCPConfig{Servers: []config.MCPServerConfig{jira}}}},
		configUpdater:   updater,
		clusterNotifier: &testClusterNotifier{},
		licenseChecker:  enterprisetest.CheckerAt(enterprise.LevelEnterpriseAdvanced),
		pluginAPI:       pluginClient,
	})

	body, err := json.Marshal(config.Config{MCP: config.MCPConfig{Servers: []config.MCPServerConfig{}}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Empty(t, updater.deletedAtRun, "the configuration was applied before any sign-in was removed")
	assert.Equal(t, []string{signIn}, deleted())
}

// inMemoryKVPluginClient returns a *pluginapi.Client backed by a full in-memory
// KV store supporting Get, Set, CompareAndSet, Delete, and ListKeys, so
// end-to-end tests can exercise real OAuthManager, ToolsCache, and config save
// flows without mocking individual KV keys.
func inMemoryKVPluginClient(t *testing.T) (*pluginapi.Client, *sync.Mutex, map[string][]byte) {
	t.Helper()
	var mu sync.Mutex
	kv := make(map[string][]byte)

	mockAPI := &plugintest.API{}
	mockAPI.On("KVGet", mock.AnythingOfType("string")).Return(
		func(key string) []byte {
			mu.Lock()
			defer mu.Unlock()
			raw, ok := kv[key]
			if !ok {
				return nil
			}
			return append([]byte(nil), raw...)
		},
		func(string) *model.AppError { return nil },
	)
	mockAPI.On("KVSet", mock.AnythingOfType("string"), mock.Anything).Return(
		func(key string, val []byte) *model.AppError {
			mu.Lock()
			defer mu.Unlock()
			if val == nil {
				delete(kv, key)
			} else {
				kv[key] = append([]byte(nil), val...)
			}
			return nil
		},
	)
	mockAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.Anything, mock.Anything).Return(
		func(key string, val []byte, opts model.PluginKVSetOptions) bool {
			mu.Lock()
			defer mu.Unlock()
			if opts.Atomic {
				cur, exists := kv[key]
				if opts.OldValue == nil {
					if exists {
						return false
					}
				} else if !exists || !bytes.Equal(cur, opts.OldValue) {
					return false
				}
			}
			if val == nil {
				delete(kv, key)
			} else {
				kv[key] = append([]byte(nil), val...)
			}
			return true
		},
		func(string, []byte, model.PluginKVSetOptions) *model.AppError { return nil },
	)
	mockAPI.On("KVDelete", mock.AnythingOfType("string")).Return(
		func(key string) *model.AppError {
			mu.Lock()
			defer mu.Unlock()
			delete(kv, key)
			return nil
		},
	)
	mockAPI.On("KVList", mock.AnythingOfType("int"), mock.AnythingOfType("int")).Return(
		func(page, perPage int) []string {
			mu.Lock()
			defer mu.Unlock()
			keys := slices.Sorted(maps.Keys(kv))
			start := min(page*perPage, len(keys))
			end := min(start+perPage, len(keys))
			return keys[start:end]
		},
		func(int, int) *model.AppError { return nil },
	)
	for _, method := range []string{"LogDebug", "LogInfo", "LogWarn", "LogError"} {
		for argc := 1; argc <= 13; argc += 2 {
			args := make([]any, argc)
			for i := range args {
				args[i] = mock.Anything
			}
			mockAPI.On(method, args...).Maybe()
		}
	}
	mockAPI.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Maybe()

	return pluginapi.NewClient(mockAPI, nil), &mu, kv
}

// TestMCPServerQueryEditEndToEndThroughAdminConfigSave drives the real admin
// config save route (PUT /admin/config), real OAuth start/callback routes,
// real mcp.ClientManager + ToolsCache, real agent allowlists, and per-user
// provider preferences in both directions (adding ?tools=all and removing it).
// It verifies that:
//   - existing users keep their stored OAuth grant across the URL edit without
//     being forced to re-authenticate;
//   - a forced token refresh sends the query-less PRM resource;
//   - MCP traffic reaches the updated URL (query included/removed) and the
//     shared tools cache does not serve stale tools from the old URL;
//   - an agent's enabled tool on that server, tool retrieval overrides, and
//     another user's disabled-server preference all survive the URL edit.
func TestMCPServerQueryEditEndToEndThroughAdminConfigSave(t *testing.T) {
	tests := []struct {
		name             string
		initialQuery     string
		editedQuery      string
		wantInitialExtra string
		wantEditedExtra  string
	}{
		{
			name:             "adding query string (?tools=all) keeps sign-in, refreshes with query-less resource, and preserves agent tools",
			initialQuery:     "",
			editedQuery:      "tools=all",
			wantInitialExtra: "discovery_only",
			wantEditedExtra:  "all_tools_only",
		},
		{
			name:             "removing query string keeps sign-in, refreshes with query-less resource, and preserves agent tools",
			initialQuery:     "tools=all",
			editedQuery:      "",
			wantInitialExtra: "all_tools_only",
			wantEditedExtra:  "discovery_only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const (
				aliceID    = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
				bobID      = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
				serverID   = "atlassian00000000000000000"
				serverName = "Atlassian"
			)

			// --- Real OAuth v2 authorization server ---
			var (
				asMu                sync.Mutex
				v2ResourceURL       string
				clientID            string
				clientSecret        string
				authCodes           = map[string]string{} // code -> code_challenge
				accessTokens        = map[string]time.Time{}
				refreshToken        string
				tokenSeq            int
				refreshCalls        int
				lastRefreshResource string
			)
			issueTokensLocked := func(w http.ResponseWriter) {
				tokenSeq++
				access := fmt.Sprintf("access-token-%d", tokenSeq)
				refresh := fmt.Sprintf("refresh-token-%d", tokenSeq)
				accessTokens[access] = time.Now().Add(time.Hour)
				refreshToken = refresh
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  access,
					"refresh_token": refresh,
					"token_type":    "Bearer",
					"expires_in":    3600,
				})
			}

			var asBaseURL string
			asMux := http.NewServeMux()
			asServer := httptest.NewServer(asMux)
			t.Cleanup(asServer.Close)
			asBaseURL = asServer.URL

			asMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
					Issuer:                                     asBaseURL,
					AuthorizationEndpoint:                      asBaseURL + "/v2/authorize",
					TokenEndpoint:                              asBaseURL + "/v2/token",
					RegistrationEndpoint:                       asBaseURL + "/v2/register",
					CodeChallengeMethodsSupported:              []string{"S256"},
					TokenEndpointAuthMethodsSupported:          []string{"client_secret_basic"},
					AuthorizationResponseIssParameterSupported: true,
				})
			})
			asMux.HandleFunc("/v2/register", func(w http.ResponseWriter, r *http.Request) {
				var meta map[string]any
				_ = json.NewDecoder(r.Body).Decode(&meta)
				asMu.Lock()
				clientID = "v2-client"
				clientSecret = "v2-secret"
				asMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"client_id":                  "v2-client",
					"client_secret":              "v2-secret",
					"redirect_uris":              meta["redirect_uris"],
					"token_endpoint_auth_method": "client_secret_basic",
				})
			})
			asMux.HandleFunc("/v2/authorize", func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				asMu.Lock()
				defer asMu.Unlock()
				if q.Get("resource") != v2ResourceURL {
					http.Error(w, "wrong resource on authorize: "+q.Get("resource"), http.StatusBadRequest)
					return
				}
				code := fmt.Sprintf("code-%d", len(authCodes)+1)
				authCodes[code] = q.Get("code_challenge")
				redir, err := url.Parse(q.Get("redirect_uri"))
				if err != nil {
					http.Error(w, "bad redirect", http.StatusBadRequest)
					return
				}
				vals := redir.Query()
				vals.Set("code", code)
				vals.Set("state", q.Get("state"))
				vals.Set("iss", asBaseURL)
				redir.RawQuery = vals.Encode()
				http.Redirect(w, r, redir.String(), http.StatusFound)
			})
			asMux.HandleFunc("/v2/token", func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				cid, csec, _ := r.BasicAuth()
				asMu.Lock()
				defer asMu.Unlock()
				if cid != clientID || csec != clientSecret {
					w.WriteHeader(http.StatusUnauthorized)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
					return
				}
				switch r.PostFormValue("grant_type") {
				case "authorization_code":
					if r.PostFormValue("resource") != v2ResourceURL {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_target"})
						return
					}
					code := r.PostFormValue("code")
					challenge, ok := authCodes[code]
					if !ok {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
						return
					}
					delete(authCodes, code)
					sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
					if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
						return
					}
					issueTokensLocked(w)
				case "refresh_token":
					lastRefreshResource = r.PostFormValue("resource")
					if lastRefreshResource != v2ResourceURL {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_target"})
						return
					}
					if r.PostFormValue("refresh_token") != refreshToken {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
						return
					}
					refreshCalls++
					accessTokens = map[string]time.Time{}
					issueTokensLocked(w)
				default:
					w.WriteHeader(http.StatusBadRequest)
				}
			})

			// --- Real MCP resource server whose tool set depends on ?tools=all ---
			type whoamiIn struct{}
			type whoamiOut struct {
				Token string `json:"token"`
			}
			newModeMCPServer := func(extraTool string) *sdkmcp.Server {
				srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "atlassian-v2", Version: "1.0"}, nil)
				sdkmcp.AddTool(srv, &sdkmcp.Tool{Name: "whoami", Description: "Return bearer token"},
					func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ whoamiIn) (*sdkmcp.CallToolResult, whoamiOut, error) {
						tok := ""
						if ti := auth.TokenInfoFromContext(ctx); ti != nil {
							tok, _ = ti.Extra["token"].(string)
						}
						return nil, whoamiOut{Token: tok}, nil
					})
				sdkmcp.AddTool(srv, &sdkmcp.Tool{Name: extraTool, Description: "Mode-specific tool"},
					func(_ context.Context, _ *sdkmcp.CallToolRequest, _ whoamiIn) (*sdkmcp.CallToolResult, whoamiOut, error) {
						return nil, whoamiOut{Token: extraTool}, nil
					})
				return srv
			}
			allToolsMCP := newModeMCPServer("all_tools_only")
			discoveryMCP := newModeMCPServer("discovery_only")
			streamable := sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server {
				if r.URL.Query().Get("tools") == "all" {
					return allToolsMCP
				}
				return discoveryMCP
			}, &sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

			var (
				resMu         sync.Mutex
				resOrigin     string
				lastMCPQuery  string
				mcpQueryCalls []string
			)
			resMux := http.NewServeMux()
			resServer := httptest.NewServer(resMux)
			t.Cleanup(resServer.Close)
			resOrigin = resServer.URL
			v2ResourceURL = resOrigin + "/v2/mcp"

			verifier := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
				asMu.Lock()
				defer asMu.Unlock()
				exp, ok := accessTokens[token]
				if !ok {
					return nil, auth.ErrInvalidToken
				}
				return &auth.TokenInfo{Expiration: exp, Extra: map[string]any{"token": token}}, nil
			}
			resMux.Handle("/v2/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				resMu.Lock()
				lastMCPQuery = r.URL.RawQuery
				mcpQueryCalls = append(mcpQueryCalls, r.URL.RawQuery)
				resMu.Unlock()
				auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
					ResourceMetadataURL: resOrigin + "/.well-known/oauth-protected-resource/v2/mcp",
				})(streamable).ServeHTTP(w, r)
			}))
			resMux.HandleFunc("/.well-known/oauth-protected-resource/v2/mcp", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"resource":              v2ResourceURL,
					"authorization_servers": []string{asBaseURL},
				})
			})
			// Decoy v1 AS metadata at the resource origin to guarantee PRM is used.
			resMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
					Issuer:                        resOrigin,
					AuthorizationEndpoint:         resOrigin + "/v1/authorize",
					TokenEndpoint:                 resOrigin + "/v1/token",
					RegistrationEndpoint:          resOrigin + "/v1/register",
					CodeChallengeMethodsSupported: []string{"S256"},
				})
			})

			buildURL := func(q string) string {
				if q == "" {
					return v2ResourceURL
				}
				return v2ResourceURL + "?" + q
			}
			initialURL := buildURL(tt.initialQuery)
			editedURL := buildURL(tt.editedQuery)

			// --- Real plugin wiring: config.Container, OAuthManager, ClientManager, API routes ---
			pluginClient, kvMu, kvMap := inMemoryKVPluginClient(t)
			mmClient := mmapi.NewClient(pluginClient)

			initialServerCfg := config.MCPServerConfig{
				ID:      serverID,
				Name:    serverName,
				Enabled: true,
				BaseURL: initialURL,
				ToolConfigs: []config.MCPToolConfig{
					{
						Name:                         "whoami",
						Policy:                       mcp.ToolPolicyAutoRunInDM,
						Enabled:                      true,
						RetrievalDescriptionOverride: "Identify caller",
					},
				},
			}
			initialCfg := config.Config{
				MCP: config.MCPConfig{
					Enabled: true,
					Servers: []config.MCPServerConfig{initialServerCfg},
				},
				Bots: []llm.BotConfig{
					{
						ID:   "legacy-bot",
						Name: "legacy",
						EnabledMCPTools: []llm.EnabledMCPTool{
							{ServerOrigin: initialURL, ToolName: "whoami"},
						},
					},
				},
			}
			agentStore := newMockAgentStore()
			cfgStore := &testConfigStore{cfg: &initialCfg, agentStore: agentStore}
			cfgContainer := &config.Container{}
			cfgContainer.Update(&initialCfg)

			httpClient := &http.Client{Transport: http.DefaultTransport}
			const callbackURL = "http://localhost:3333/plugins/mattermost-ai/oauth/callback"
			oauthManager := mcp.NewOAuthManager(mmClient, callbackURL, httpClient, func(name string) (mcp.ServerConfig, bool) {
				for _, s := range cfgContainer.MCP().Servers {
					if s.Name == name {
						return s, true
					}
				}
				return mcp.ServerConfig{}, false
			})
			clientManager := mcp.NewClientManager(cfgContainer.MCP(), pluginClient.Log, pluginClient, oauthManager, nil, httpClient, mmClient, mcp.RemoteMCPAlwaysAllowed)
			t.Cleanup(clientManager.Close)
			cfgContainer.RegisterUpdateListener(func() {
				clientManager.ReInit(cfgContainer.MCP(), nil)
			})

			agent := &llm.BotConfig{
				BotUserID:             "bot-user-1",
				CreatorID:             aliceID,
				DisplayName:           "Jira Helper",
				Name:                  "jira-helper",
				ServiceID:             "svc-1",
				AutoEnableNewMCPTools: false,
				EnabledMCPTools: []llm.EnabledMCPTool{
					{ServerOrigin: initialURL, ToolName: "whoami"},
				},
			}
			require.NoError(t, agentStore.CreateAgent(agent, store.SystemAgentVersionMeta()))

			// Bob disabled this server in his personal tool preferences.
			_, err := mcp.SaveUserPreferences(mmClient, bobID, &mcp.UserToolProviderPreferences{
				DisabledServers: []string{initialURL},
			})
			require.NoError(t, err)

			agentNotifier := &recordingAgentNotifier{}
			apiInstance := &API{
				config:               cfgContainer,
				configStore:          cfgStore,
				configUpdater:        cfgContainer,
				clusterNotifier:      &testClusterNotifier{},
				clusterAgentNotifier: agentNotifier,
				agentStore:           agentStore,
				mcpClientManager:     clientManager,
				licenseChecker:       enterprisetest.CheckerAt(enterprise.LevelEnterpriseAdvanced),
				pluginAPI:            pluginClient,
				mmClient:             mmClient,
			}

			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(settingsAccessSystemAdminKey, true)
				c.Next()
			})
			router.PUT("/admin/config", apiInstance.handleSaveConfig)
			router.GET("/mcp/oauth/:serverName/start", apiInstance.handleOAuthStart)
			router.GET("/oauth/callback", apiInstance.handleOAuthCallback)

			ctx := context.Background()

			// --- Step 1: Unauthenticated connect yields OAuth start link ---
			_, mcpErrs := clientManager.GetToolsWithSelection(ctx, mcp.UserCatalogRequest(aliceID), mcp.ToolSelection{
				AllowedOrigins: []string{initialURL},
			})
			require.NotNil(t, mcpErrs)
			require.Len(t, mcpErrs.ToolAuthErrors, 1)
			startLink, err := url.Parse(mcpErrs.ToolAuthErrors[0].AuthURL)
			require.NoError(t, err)
			startPath := "/mcp/oauth/" + serverName + "/start?" + startLink.RawQuery

			// --- Step 2: Sign in via real /mcp/oauth/:serverName/start and /oauth/callback ---
			startReq := httptest.NewRequest(http.MethodGet, startPath, nil)
			startReq.Header.Set("Mattermost-User-Id", aliceID)
			startW := httptest.NewRecorder()
			router.ServeHTTP(startW, startReq)
			require.Equal(t, http.StatusFound, startW.Code, startW.Body.String())

			authRedirectURL := startW.Header().Get("Location")
			parsedAuthRedirect, err := url.Parse(authRedirectURL)
			require.NoError(t, err)
			require.Equal(t, "/v2/authorize", parsedAuthRedirect.Path, "must use v2 PRM authorization server")
			require.Equal(t, v2ResourceURL, parsedAuthRedirect.Query().Get("resource"))

			noRedirect := &http.Client{
				Timeout:       10 * time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}
			consentResp, err := noRedirect.Get(authRedirectURL)
			require.NoError(t, err)
			defer consentResp.Body.Close()
			require.Equal(t, http.StatusFound, consentResp.StatusCode)
			callbackLoc, err := consentResp.Location()
			require.NoError(t, err)

			cbReq := httptest.NewRequest(http.MethodGet, "/oauth/callback?"+callbackLoc.RawQuery, nil)
			cbReq.Header.Set("Mattermost-User-Id", aliceID)
			cbW := httptest.NewRecorder()
			router.ServeHTTP(cbW, cbReq)
			require.Equal(t, http.StatusOK, cbW.Code, cbW.Body.String())

			// --- Step 3: Authenticated call before the URL edit ---
			tools, mcpErrs := clientManager.GetToolsWithSelection(ctx, mcp.UserCatalogRequest(aliceID), mcp.ToolSelection{
				AllowedOrigins: []string{agent.EnabledMCPTools[0].ServerOrigin},
			})
			require.Nil(t, mcpErrs)
			toolNames := make([]string, 0, len(tools))
			for _, tool := range tools {
				toolNames = append(toolNames, llm.BareMCPToolName(tool.Name))
			}
			require.ElementsMatch(t, []string{"whoami", tt.wantInitialExtra}, toolNames)
			allowedTools := llm.FilterMCPToolsByEnabledAllowlist(tools, agent.EnabledMCPTools)
			require.Len(t, allowedTools, 1)
			resText, err := allowedTools[0].Resolver(ctx, &llm.Context{}, func(v any) error { return nil })
			require.NoError(t, err)
			require.Contains(t, resText, "access-token-1")
			resMu.Lock()
			require.Equal(t, tt.initialQuery, lastMCPQuery)
			resMu.Unlock()

			// --- Step 4: Admin edits server BaseURL via PUT /admin/config ---
			editedCfg := initialCfg
			editedCfg.MCP.Servers = []config.MCPServerConfig{initialServerCfg}
			editedCfg.MCP.Servers[0].BaseURL = editedURL
			body, err := json.Marshal(editedCfg)
			require.NoError(t, err)
			saveReq := httptest.NewRequest(http.MethodPut, "/admin/config", bytes.NewReader(body))
			saveReq.Header.Set("Content-Type", "application/json")
			saveW := httptest.NewRecorder()
			router.ServeHTTP(saveW, saveReq)
			require.Equal(t, http.StatusOK, saveW.Code, saveW.Body.String())

			// Verify agent allowlist, legacy bot, HA notification, user preferences, and overrides followed the edit.
			updatedAgent, err := agentStore.GetAgent(agent.ID)
			require.NoError(t, err)
			require.Equal(t, []llm.EnabledMCPTool{{ServerOrigin: editedURL, ToolName: "whoami"}}, updatedAgent.EnabledMCPTools)
			require.Equal(t, []llm.EnabledMCPTool{{ServerOrigin: editedURL, ToolName: "whoami"}}, cfgStore.cfg.Bots[0].EnabledMCPTools)
			require.Equal(t, []string{agent.ID}, agentNotifier.published, "HA peers must be notified per remapped agent ID")

			bobPrefs, err := mcp.LoadUserPreferences(mmClient, bobID)
			require.NoError(t, err)
			require.Equal(t, []string{editedURL}, bobPrefs.DisabledServers)

			overrides := clientManager.GetToolRetrievalOverrides()
			require.Equal(t, "Identify caller", overrides[mcp.ToolRetrievalOverrideKey(editedURL, "whoami")].Summary)

			// --- Step 5: Next tool call reuses stored grant without re-auth, hits edited URL, and refreshes tools cache ---
			tools, mcpErrs = clientManager.GetToolsWithSelection(ctx, mcp.UserCatalogRequest(aliceID), mcp.ToolSelection{
				AllowedOrigins: []string{updatedAgent.EnabledMCPTools[0].ServerOrigin},
			})
			require.Nil(t, mcpErrs, "URL query edit must not require re-authentication")
			toolNames = toolNames[:0]
			for _, tool := range tools {
				toolNames = append(toolNames, llm.BareMCPToolName(tool.Name))
			}
			require.ElementsMatch(t, []string{"whoami", tt.wantEditedExtra}, toolNames,
				"tools cache must miss stale tools from the old URL and discover tools from the edited URL")

			allowedTools = llm.FilterMCPToolsByEnabledAllowlist(tools, updatedAgent.EnabledMCPTools)
			require.Len(t, allowedTools, 1, "agent's enabled tool must survive the URL edit")
			resText, err = allowedTools[0].Resolver(ctx, &llm.Context{}, func(v any) error { return nil })
			require.NoError(t, err)
			require.Contains(t, resText, "access-token-1")
			resMu.Lock()
			require.Equal(t, tt.editedQuery, lastMCPQuery, "MCP traffic must hit the edited URL with its updated query")
			resMu.Unlock()

			// --- Step 6: Expire the access token; forced refresh must send resource=<query-less URL> ---
			tokenKey := "mcp_oauth_token_v1_" + aliceID + "_" + serverName
			kvMu.Lock()
			rawEnv, exists := kvMap[tokenKey]
			require.True(t, exists, "stored OAuth grant must survive the URL edit")
			var rawMap map[string]any
			require.NoError(t, json.Unmarshal(rawEnv, &rawMap))
			tokMap := rawMap["token"].(map[string]any)
			tokMap["expiry"] = time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
			updatedRaw, err := json.Marshal(rawMap)
			require.NoError(t, err)
			kvMap[tokenKey] = updatedRaw
			kvMu.Unlock()

			clientManager.InvalidateUserClients(aliceID)
			tools, mcpErrs = clientManager.GetToolsWithSelection(ctx, mcp.UserCatalogRequest(aliceID), mcp.ToolSelection{
				AllowedOrigins: []string{updatedAgent.EnabledMCPTools[0].ServerOrigin},
			})
			require.Nil(t, mcpErrs, "forced refresh must succeed without re-authentication")
			allowedTools = llm.FilterMCPToolsByEnabledAllowlist(tools, updatedAgent.EnabledMCPTools)
			require.Len(t, allowedTools, 1)
			resText, err = allowedTools[0].Resolver(ctx, &llm.Context{}, func(v any) error { return nil })
			require.NoError(t, err)
			require.Contains(t, resText, "access-token-2", "tool call must use the refreshed token")

			asMu.Lock()
			assert.Equal(t, 1, refreshCalls, "exactly one refresh_token grant expected")
			assert.Equal(t, v2ResourceURL, lastRefreshResource, "forced refresh must send the query-less PRM resource")
			asMu.Unlock()
		})
	}
}
