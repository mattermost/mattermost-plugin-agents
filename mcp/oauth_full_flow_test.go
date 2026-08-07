// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
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

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
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

	revokeCalls   int
	revokedTokens []string
}

// counters returns the server-side observation fields under s.mu so test
// assertions do not race with HTTP handler goroutines.
func (s *fullFlowOAuthServer) counters() (registerCalls, refreshCalls int, pkceVerified bool, lastAuthorizedToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registerCalls, s.refreshCalls, s.pkceVerified, s.lastAuthorizedToken
}

// revocations returns the number of revocation calls and the tokens revoked.
func (s *fullFlowOAuthServer) revocations() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revokeCalls, append([]string(nil), s.revokedTokens...)
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
	mux.HandleFunc("/revoke", s.handleRevoke)

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
		"revocation_endpoint":                   s.baseURL + "/revoke",
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

// handleRevoke implements RFC 7009 token revocation. It authenticates the
// (DCR, client_secret_basic) client, invalidates the presented token and any
// tokens it is associated with, and returns 200.
func (s *fullFlowOAuthServer) handleRevoke(w http.ResponseWriter, r *http.Request) {
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

	token := r.PostFormValue("token")
	s.revokeCalls++
	s.revokedTokens = append(s.revokedTokens, token)
	// Revoking the refresh token invalidates it and all its access tokens
	// (RFC 7009 §2.1).
	if token == s.refreshToken {
		s.refreshToken = ""
		s.accessTokens = map[string]time.Time{}
	} else {
		delete(s.accessTokens, token)
	}
	w.WriteHeader(http.StatusOK)
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

	kv := newStatefulKVClient(t)
	mockClient := kv.mockClient(t)
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

	// --- Step 7: disconnect revokes the grant at the AS (RFC 7009) ---
	// The discovered revocation endpoint must have been pinned into the stored
	// grant so disconnect can reach it.
	storedEnvelope := kv.storedEnvelope(t, userID, serverName)
	require.NotNil(t, storedEnvelope)
	require.Equal(t, oauthServer.baseURL+"/revoke", storedEnvelope.RevocationEndpoint,
		"discovered revocation endpoint must be pinned into the stored grant")

	require.NoError(t, manager.DeleteUserToken(ctx, userID, serverName))

	revokeCalls, revokedTokens := oauthServer.revocations()
	require.Equal(t, 1, revokeCalls, "disconnect must revoke exactly once at the authorization server")
	require.Equal(t, []string{"refresh-token-2"}, revokedTokens,
		"disconnect must revoke the current (rotated) refresh token")
	require.False(t, kv.exists(userID, serverName), "grant must be deleted locally after revocation")
	t.Logf("step 7 OK: disconnect revoked %v at the authorization server", revokedTokens)
}
