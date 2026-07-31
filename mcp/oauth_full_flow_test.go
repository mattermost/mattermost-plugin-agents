// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// ---------------------------------------------------------------------------
// Fake OAuth-protected MCP server, built on the go-sdk v1.7.0 itself:
//   - streamable MCP handler guarded by auth.RequireBearerToken (401 +
//     WWW-Authenticate: Bearer resource_metadata="...")
//   - RFC 9728 protected resource metadata + RFC 8414 AS metadata (S256)
//   - /authorize (auto-approve), /token (PKCE + rotating refresh tokens)
//   - RFC 7591 dynamic client registration
// ---------------------------------------------------------------------------

type fullFlowAuthCode struct {
	challenge   string
	redirectURI string
}

type fullFlowOAuthServer struct {
	mu      sync.Mutex
	baseURL string

	clientID      string
	clientSecret  string
	registerCalls int

	codes        map[string]fullFlowAuthCode
	accessTokens map[string]time.Time
	refreshToken string
	tokenSeq     int

	pkceVerified        bool
	refreshCalls        int
	lastAuthorizedToken string
}

// counters returns the server-side observation fields under s.mu so test
// assertions do not race with HTTP handler goroutines.
func (s *fullFlowOAuthServer) counters() (registerCalls, refreshCalls int, pkceVerified bool, lastAuthorizedToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registerCalls, s.refreshCalls, s.pkceVerified, s.lastAuthorizedToken
}

func newFullFlowOAuthServer(t *testing.T) *fullFlowOAuthServer {
	t.Helper()

	s := &fullFlowOAuthServer{
		codes:        map[string]fullFlowAuthCode{},
		accessTokens: map[string]time.Time{},
	}

	mcpServer := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "oauth-full-flow", Version: "1.0"}, nil)
	type whoamiIn struct{}
	type whoamiOut struct {
		Token string `json:"token"`
	}
	sdkmcp.AddTool(mcpServer,
		&sdkmcp.Tool{Name: "whoami", Description: "Returns the bearer token the call was authenticated with."},
		func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ whoamiIn) (*sdkmcp.CallToolResult, whoamiOut, error) {
			token := ""
			if ti := auth.TokenInfoFromContext(ctx); ti != nil {
				token, _ = ti.Extra["token"].(string)
			}
			return nil, whoamiOut{Token: token}, nil
		})
	streamable := sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return mcpServer },
		&sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)

	mux := http.NewServeMux()
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	s.baseURL = httpServer.URL

	verifier := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		expiry, ok := s.accessTokens[token]
		if !ok {
			return nil, auth.ErrInvalidToken
		}
		s.lastAuthorizedToken = token
		return &auth.TokenInfo{
			Expiration: expiry,
			Extra:      map[string]any{"token": token},
		}, nil
	}

	mux.Handle("/mcp", auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: s.prmURL(),
	})(streamable))
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", s.handleProtectedResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleAuthServerMetadata)
	mux.HandleFunc("/register", s.handleRegister)
	mux.HandleFunc("/authorize", s.handleAuthorize)
	mux.HandleFunc("/token", s.handleToken)

	return s
}

func (s *fullFlowOAuthServer) mcpURL() string { return s.baseURL + "/mcp" }
func (s *fullFlowOAuthServer) prmURL() string {
	return s.baseURL + "/.well-known/oauth-protected-resource/mcp"
}

func (s *fullFlowOAuthServer) handleProtectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":              s.mcpURL(),
		"authorization_servers": []string{s.baseURL},
	})
}

func (s *fullFlowOAuthServer) handleAuthServerMetadata(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                s.baseURL,
		"authorization_endpoint":                s.baseURL + "/authorize",
		"token_endpoint":                        s.baseURL + "/token",
		"registration_endpoint":                 s.baseURL + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		// RFC 9207: the client must require a matching iss parameter on the
		// authorization response.
		"authorization_response_iss_parameter_supported": true,
	})
}

// handleRegister implements RFC 7591 dynamic client registration.
func (s *fullFlowOAuthServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var meta map[string]any
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
		http.Error(w, "bad registration request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.registerCalls++
	s.clientID = "dcr-client-1"
	s.clientSecret = "dcr-secret-1"
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client_id":                  "dcr-client-1",
		"client_secret":              "dcr-secret-1",
		"redirect_uris":              meta["redirect_uris"],
		"token_endpoint_auth_method": "client_secret_basic",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"client_name":                meta["client_name"],
	})
}

// handleAuthorize auto-approves: it validates the request and immediately
// redirects back to redirect_uri with code+state (simulating user consent).
func (s *fullFlowOAuthServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	s.mu.Lock()
	defer s.mu.Unlock()

	if q.Get("client_id") != s.clientID || s.clientID == "" {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if q.Get("response_type") != "code" {
		http.Error(w, "unsupported response_type", http.StatusBadRequest)
		return
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		http.Error(w, "PKCE S256 challenge required", http.StatusBadRequest)
		return
	}
	// RFC 8707: the MCP spec requires the canonical resource on the
	// authorization request.
	if q.Get("resource") != s.mcpURL() {
		http.Error(w, "missing or wrong resource parameter", http.StatusBadRequest)
		return
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}

	code := fmt.Sprintf("auth-code-%d", len(s.codes)+1)
	s.codes[code] = fullFlowAuthCode{
		challenge:   q.Get("code_challenge"),
		redirectURI: redirectURI,
	}

	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	values := u.Query()
	values.Set("code", code)
	values.Set("state", q.Get("state"))
	// RFC 9207: identify the issuer in the authorization response.
	values.Set("iss", s.baseURL)
	u.RawQuery = values.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func writeOAuthError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (s *fullFlowOAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	clientID, clientSecret, ok := r.BasicAuth()
	if !ok {
		clientID = r.PostFormValue("client_id")
		clientSecret = r.PostFormValue("client_secret")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if clientID != s.clientID || clientSecret != s.clientSecret || s.clientID == "" {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}

	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		// RFC 8707: the MCP spec requires the canonical resource on the
		// token request.
		if r.PostFormValue("resource") != s.mcpURL() {
			writeOAuthError(w, http.StatusBadRequest, "invalid_target")
			return
		}
		code := r.PostFormValue("code")
		ac, found := s.codes[code]
		if !found {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		delete(s.codes, code)
		sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != ac.challenge {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		s.pkceVerified = true
		s.issueTokensLocked(w)
	case "refresh_token":
		// RFC 8707: the MCP spec requires the canonical resource on refresh
		// requests too.
		if r.PostFormValue("resource") != s.mcpURL() {
			writeOAuthError(w, http.StatusBadRequest, "invalid_target")
			return
		}
		if s.refreshToken == "" || r.PostFormValue("refresh_token") != s.refreshToken {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		s.refreshCalls++
		// Rotate: revoke all previously issued access tokens and the used
		// refresh token, then issue a fresh pair.
		s.accessTokens = map[string]time.Time{}
		s.issueTokensLocked(w)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type")
	}
}

func (s *fullFlowOAuthServer) issueTokensLocked(w http.ResponseWriter) {
	s.tokenSeq++
	access := fmt.Sprintf("access-token-%d", s.tokenSeq)
	refresh := fmt.Sprintf("refresh-token-%d", s.tokenSeq)
	s.accessTokens[access] = time.Now().Add(time.Hour)
	s.refreshToken = refresh
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

// ---------------------------------------------------------------------------
// Stateful in-memory KV backing the mmapi mock, mirroring the real client's
// JSON marshal/unmarshal semantics (raw []byte values are stored as-is).
// ---------------------------------------------------------------------------

type fullFlowKV struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFullFlowKVClient(t *testing.T) (*mocks.MockClient, *fullFlowKV) {
	t.Helper()
	kv := &fullFlowKV{data: map[string][]byte{}}
	client := mocks.NewMockClient(t)

	get := func(key string, value interface{}) error {
		kv.mu.Lock()
		defer kv.mu.Unlock()
		raw, ok := kv.data[key]
		if !ok {
			return mmapi.ErrKVNotFound
		}
		return json.Unmarshal(raw, value)
	}
	set := func(key string, value interface{}) error {
		raw, ok := value.([]byte)
		if !ok {
			var err error
			raw, err = json.Marshal(value)
			if err != nil {
				return err
			}
		}
		kv.mu.Lock()
		defer kv.mu.Unlock()
		kv.data[key] = raw
		return nil
	}

	client.On("KVGet", mock.Anything, mock.Anything).Maybe().
		Return(func(key string, value interface{}) error { return get(key, value) })
	client.On("KVSet", mock.Anything, mock.Anything).Maybe().
		Return(func(key string, value interface{}) error { return set(key, value) })
	client.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Maybe().
		Return(func(key string, value interface{}, _ time.Duration) error { return set(key, value) })
	client.On("KVDelete", mock.Anything).Maybe().
		Return(func(key string) error {
			kv.mu.Lock()
			defer kv.mu.Unlock()
			delete(kv.data, key)
			return nil
		})
	// Compare-and-set mirrors pluginapi's SetAtomic semantics: old nil means
	// "only if absent", new nil means delete.
	client.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Maybe().
		Return(func(key string, oldValue, newValue interface{}) (bool, error) {
			kv.mu.Lock()
			defer kv.mu.Unlock()
			current, exists := kv.data[key]
			if oldValue == nil {
				if exists {
					return false, nil
				}
			} else {
				oldRaw, err := json.Marshal(oldValue)
				if err != nil {
					return false, err
				}
				if !exists || !bytes.Equal(current, oldRaw) {
					return false, nil
				}
			}
			if newValue == nil {
				delete(kv.data, key)
				return true, nil
			}
			newRaw, err := json.Marshal(newValue)
			if err != nil {
				return false, err
			}
			kv.data[key] = newRaw
			return true, nil
		})
	for _, logMethod := range []string{"LogDebug", "LogInfo", "LogWarn", "LogError"} {
		client.On(logMethod, mock.Anything).Maybe().Return()
		client.On(logMethod, mock.Anything, mock.Anything).Maybe().Return()
	}

	return client, kv
}

func (kv *fullFlowKV) storedEnvelope(t *testing.T, userID, serverID string) *storedTokenEnvelope {
	t.Helper()
	kv.mu.Lock()
	defer kv.mu.Unlock()
	raw, ok := kv.data[buildTokenKey(userID, serverID)]
	if !ok {
		return nil
	}
	var envelope storedTokenEnvelope
	require.NoError(t, json.Unmarshal(raw, &envelope))
	return &envelope
}

func (kv *fullFlowKV) storedToken(t *testing.T, userID, serverID string) *oauth2.Token {
	t.Helper()
	envelope := kv.storedEnvelope(t, userID, serverID)
	if envelope == nil {
		return nil
	}
	return envelope.Token
}

// overwriteToken swaps the token inside the stored grant envelope, keeping
// its authorization server binding (used to simulate expiry).
func (kv *fullFlowKV) overwriteToken(t *testing.T, userID, serverID string, token *oauth2.Token) {
	t.Helper()
	envelope := kv.storedEnvelope(t, userID, serverID)
	require.NotNil(t, envelope, "no stored grant to overwrite")
	envelope.Token = token
	raw, err := json.Marshal(envelope)
	require.NoError(t, err)
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.data[buildTokenKey(userID, serverID)] = raw
}

// ---------------------------------------------------------------------------
// The test: the plugin's REAL OAuth flow, end to end, against the fake server.
// ---------------------------------------------------------------------------

// TestOAuthFullFlowAgainstGoSDKServer drives the complete migrated OAuth stack
// through its production entry points:
//
//  1. NewClient with no stored token -> *OAuthNeededError carrying the RFC 9728
//     resource_metadata URL from the server's WWW-Authenticate challenge.
//  2. InitiateOAuthFlowForServerWithMetadata -> discovery (strict oauthex path),
//     RFC 7591 dynamic client registration, PKCE S256 authorization URL.
//  3. Browser simulation: GET the authorization URL, parse code+state from the
//     auto-approve redirect.
//  4. ProcessCallback -> PKCE-verified token exchange, token persisted to KV.
//  5. NewClient again -> connects, lists tools, calls a tool authenticated.
//  6. Expire the stored access token -> next NewClient+CallTool triggers a
//     refresh_token grant; the server ROTATES both tokens and the
//     persistingTokenSource must persist the rotated pair to the KV store.
func TestOAuthFullFlowAgainstGoSDKServer(t *testing.T) {
	const userID = "full-flow-user"
	const serverName = "full-flow-server"
	const callbackURL = "http://localhost:3333/plugins/mattermost-ai/oauth/callback"

	ctx := context.Background()
	oauthServer := newFullFlowOAuthServer(t)

	serverConfig := ServerConfig{
		Name:    serverName,
		Enabled: true,
		BaseURL: oauthServer.mcpURL(),
		// No ClientID/ClientSecret: force dynamic client registration.
	}

	mockClient, kv := newFullFlowKVClient(t)
	httpClient := &http.Client{Transport: http.DefaultTransport}
	manager := NewOAuthManager(mockClient, callbackURL, httpClient, func(id string) (ServerConfig, bool) {
		if id == serverName {
			return serverConfig, true
		}
		return ServerConfig{}, false
	})

	// --- Step 1: unauthenticated connect surfaces *OAuthNeededError ---
	_, err := NewClient(ctx, userID, serverConfig, newTestLogService(), manager, httpClient, nil, false)
	require.Error(t, err)
	var oauthNeeded *OAuthNeededError
	require.ErrorAs(t, err, &oauthNeeded, "unauthenticated connect must surface *OAuthNeededError")
	require.Equal(t, oauthServer.prmURL(), oauthNeeded.MetadataURL(),
		"metadata URL must come from the WWW-Authenticate challenge")
	wantAuthURL := "http://localhost:3333/plugins/mattermost-ai/mcp/oauth/" + serverName + "/start?resource_metadata=" +
		url.QueryEscape(oauthServer.prmURL())
	require.Equal(t, wantAuthURL, oauthNeeded.AuthURL())
	t.Logf("step 1 OK: OAuthNeededError metadataURL=%s authURL=%s", oauthNeeded.MetadataURL(), oauthNeeded.AuthURL())

	// --- Step 2: initiate the flow (discovery + DCR + PKCE auth URL) ---
	authorizationURL, err := manager.InitiateOAuthFlowForServerWithMetadata(ctx, userID, serverConfig, oauthNeeded.MetadataURL(), "")
	require.NoError(t, err)
	registerCalls, _, _, _ := oauthServer.counters()
	require.Equal(t, 1, registerCalls, "dynamic client registration must have happened exactly once")
	parsedAuthURL, err := url.Parse(authorizationURL)
	require.NoError(t, err)
	require.Equal(t, "/authorize", parsedAuthURL.Path)
	require.Equal(t, "dcr-client-1", parsedAuthURL.Query().Get("client_id"))
	require.Equal(t, "S256", parsedAuthURL.Query().Get("code_challenge_method"))
	require.NotEmpty(t, parsedAuthURL.Query().Get("code_challenge"))
	require.NotEmpty(t, parsedAuthURL.Query().Get("state"))
	t.Logf("step 2 OK: authorization URL %s", authorizationURL)

	// --- Step 3: simulate the user's browser (auto-approve redirect) ---
	noRedirect := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noRedirect.Get(authorizationURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	location, err := resp.Location()
	require.NoError(t, err)
	require.Equal(t, "localhost:3333", location.Host, "redirect must land on the plugin callback")
	code := location.Query().Get("code")
	state := location.Query().Get("state")
	iss := location.Query().Get("iss")
	require.NotEmpty(t, code)
	require.Equal(t, parsedAuthURL.Query().Get("state"), state)
	require.Equal(t, oauthServer.baseURL, iss, "authorization response must carry the RFC 9207 iss parameter")
	t.Logf("step 3 OK: provider redirected to %s", location.String())

	// --- Step 4: ProcessCallback exchanges the code and stores the token ---
	// The wrong issuer must be rejected (RFC 9207), then the real one accepted.
	_, err = manager.ProcessCallback(ctx, userID, state, code, "https://evil.example.com")
	require.ErrorContains(t, err, "issuer mismatch")

	// The session was consumed by the failed attempt; restart the flow to get
	// a fresh code+state for the successful exchange.
	authorizationURL2, err := manager.InitiateOAuthFlowForServerWithMetadata(ctx, userID, serverConfig, oauthNeeded.MetadataURL(), "")
	require.NoError(t, err)
	resp2, err := noRedirect.Get(authorizationURL2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	location2, err := resp2.Location()
	require.NoError(t, err)
	code = location2.Query().Get("code")
	state = location2.Query().Get("state")
	iss = location2.Query().Get("iss")

	session, err := manager.ProcessCallback(ctx, userID, state, code, iss)
	require.NoError(t, err)
	require.Equal(t, serverName, session.ServerID)
	_, _, pkceVerified, _ := oauthServer.counters()
	require.True(t, pkceVerified, "token endpoint must have verified the PKCE code_verifier")
	stored := kv.storedToken(t, userID, serverName)
	require.NotNil(t, stored, "token must be persisted to the KV store")
	require.Equal(t, "access-token-1", stored.AccessToken)
	require.Equal(t, "refresh-token-1", stored.RefreshToken)
	t.Logf("step 4 OK: token exchanged and stored (access=%s refresh=%s)", stored.AccessToken, stored.RefreshToken)

	// --- Step 5: authenticated connect, list tools, call a tool ---
	client, err := NewClient(ctx, userID, serverConfig, newTestLogService(), manager, httpClient, nil, false)
	require.NoError(t, err, "connect must succeed with the stored token")
	tools := client.Tools()
	require.Contains(t, tools, "whoami")
	result, err := client.CallTool(ctx, "whoami", map[string]any{})
	require.NoError(t, err)
	require.Contains(t, result, "access-token-1", "tool must observe the bearer token from the exchange")
	_, refreshCalls, _, lastAuthorizedToken := oauthServer.counters()
	require.Equal(t, "access-token-1", lastAuthorizedToken)
	require.Zero(t, refreshCalls, "no refresh should have happened yet")
	require.NoError(t, client.Close())
	t.Logf("step 5 OK: authenticated tool call result: %s", result)

	// --- Step 6: expire the access token; refresh must rotate + persist ---
	expired := *stored
	expired.Expiry = time.Now().Add(-time.Hour)
	kv.overwriteToken(t, userID, serverName, &expired)

	client, err = NewClient(ctx, userID, serverConfig, newTestLogService(), manager, httpClient, nil, false)
	require.NoError(t, err, "connect must succeed by refreshing the expired token")
	result, err = client.CallTool(ctx, "whoami", map[string]any{})
	require.NoError(t, err)
	require.Contains(t, result, "access-token-2", "tool call must use the refreshed access token")
	_, refreshCalls, _, _ = oauthServer.counters()
	require.Equal(t, 1, refreshCalls, "exactly one refresh_token grant expected")

	rotated := kv.storedToken(t, userID, serverName)
	require.NotNil(t, rotated)
	require.Equal(t, "access-token-2", rotated.AccessToken, "rotated access token must be persisted to KV")
	require.Equal(t, "refresh-token-2", rotated.RefreshToken, "ROTATED refresh token must be persisted to KV (persistingTokenSource)")
	require.NoError(t, client.Close())
	t.Logf("step 6 OK: refresh rotated and persisted (access=%s refresh=%s)", rotated.AccessToken, rotated.RefreshToken)

	registerCalls, _, _, _ = oauthServer.counters()
	require.Equal(t, 1, registerCalls, "DCR credentials must be reused from KV, not re-registered")
}
