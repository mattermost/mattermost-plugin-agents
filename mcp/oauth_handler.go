// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// userOAuthHandler implements the go-sdk's auth.OAuthHandler for a single
// (user, MCP server) pair. It plugs into mcp.StreamableClientTransport (and,
// via oauthRoundTripper, the legacy SSE transport) so the SDK injects tokens
// on outgoing requests and hands 401/403 responses back to us.
//
// We implement auth.OAuthHandler ourselves instead of using the SDK's
// auth.AuthorizationCodeHandler because the SDK handler's
// AuthorizationCodeFetcher blocks the request awaiting the browser redirect —
// a single-process CLI/desktop pattern. Our flow is asynchronous and
// multi-node (HA): the 401 surfaces an OAuth link into chat as an
// *OAuthNeededError, the user clicks it whenever they like, and the callback
// may arrive later on a different cluster node. PKCE verifiers, state, and
// tokens therefore live in the Mattermost KV store (see OAuthManager), not in
// process memory. Consequently Authorize never returns nil: we never want the
// transport's silent single retry, we want the error to propagate to the
// caller so it can be converted into an *OAuthNeededError.
type userOAuthHandler struct {
	userID      string
	serverName  string
	serverURL   string
	staticCreds *StaticOAuthCredentials
	manager     *OAuthManager
}

func newUserOAuthHandler(userID string, serverConfig ServerConfig, manager *OAuthManager) *userOAuthHandler {
	return &userOAuthHandler{
		userID:      userID,
		serverName:  serverConfig.Name,
		serverURL:   serverConfig.BaseURL,
		staticCreds: staticOAuthCreds(serverConfig),
		manager:     manager,
	}
}

// TokenSource returns a token source backed by the KV-stored grant for this
// user and server. It returns (nil, nil) when the user has no stored token,
// in which case the transport sends the request unauthenticated and the
// server's 401 lands in Authorize.
//
// No discovery happens here: the grant envelope pins the token endpoint and
// client the grant was issued with, and refreshes go exactly there. A
// compromised MCP server that starts advertising different authorization
// server metadata therefore never sees the refresh token or client secret.
func (h *userOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	envelope, err := h.manager.loadTokenEnvelope(h.userID, h.serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to load token: %w", err)
	}
	if envelope == nil {
		return nil, nil
	}

	return &persistingTokenSource{
		ctx:         ctx,
		envelope:    envelope,
		staticCreds: h.staticCreds,
		manager:     h.manager,
		userID:      h.userID,
		serverName:  h.serverName,
	}, nil
}

// oauthPrepTimeout bounds the network work a token refresh may perform so a
// hung identity provider cannot stall MCP sessions indefinitely (the
// streamable transport calls TokenSource under a connection-wide send lock
// with an unbounded connection context). It is a variable only so tests can
// shorten it.
var oauthPrepTimeout = 30 * time.Second

// maxWWWAuthenticateBytes caps the aggregate size of WWW-Authenticate header
// values accepted from a server. Legitimate challenges (including a
// resource_metadata URL) are well under 1 KiB; the cap matches the guard the
// previous hand-rolled parser applied. It also bounds the cost of
// oauthex.ParseWWWAuthenticate, whose challenge splitting rescans the
// remaining header suffix at every unquoted comma (quadratic), which an
// attacker-controlled megabyte-sized header could turn into seconds of CPU.
const maxWWWAuthenticateBytes = 4096

// Authorize is called by the transport on a 401/403 response. For 401 it
// never returns nil (see the type comment): it converts the response into a
// *mcpUnauthorized carrying the RFC 9728 resource_metadata URL and, when the
// challenge names one, the authoritative scope. For 403 it returns a
// *mcpUnauthorized only when a well-formed challenge indicates an
// OAuth-remediable condition (resource_metadata or
// error="insufficient_scope"); everything else — missing, oversized, or
// malformed challenges included — is surfaced as an ordinary error so callers
// do not loop through futile re-authentication.
func (h *userOAuthHandler) Authorize(_ context.Context, _ *http.Request, resp *http.Response) error {
	drainAndCloseResponseBody(resp)
	forbidden := resp.StatusCode == http.StatusForbidden

	headers := resp.Header.Values("WWW-Authenticate")
	if len(headers) == 0 {
		if forbidden {
			return fmt.Errorf("server returned 403 forbidden without an OAuth challenge")
		}
		return &mcpUnauthorized{
			err: fmt.Errorf("received %d response without WWW-Authenticate header", resp.StatusCode),
		}
	}

	totalHeaderBytes := 0
	for _, header := range headers {
		totalHeaderBytes += len(header)
	}
	if totalHeaderBytes > maxWWWAuthenticateBytes {
		if forbidden {
			return fmt.Errorf("server returned 403 forbidden with an oversized WWW-Authenticate header (%d bytes, max %d)", totalHeaderBytes, maxWWWAuthenticateBytes)
		}
		return &mcpUnauthorized{
			err: fmt.Errorf("WWW-Authenticate header too long (%d bytes, max %d)", totalHeaderBytes, maxWWWAuthenticateBytes),
		}
	}

	challenges, parseErr := oauthex.ParseWWWAuthenticate(headers)
	if parseErr != nil {
		if forbidden {
			return fmt.Errorf("server returned 403 forbidden with a malformed WWW-Authenticate header: %w", parseErr)
		}
		return &mcpUnauthorized{
			err: fmt.Errorf("failed to parse WWW-Authenticate header: %w", parseErr),
		}
	}

	// The challenge's scope is authoritative per the MCP specification: it
	// travels with the OAuth-needed error so re-authorization requests
	// exactly the challenged scope.
	scope := challengeScope(challenges)

	// Take the first challenge advertising resource_metadata regardless of
	// scheme (Bearer, DPoP, ...), matching the previous hand-rolled parser.
	for _, challenge := range challenges {
		metadataURL := challenge.Params["resource_metadata"]
		if metadataURL == "" {
			continue
		}
		if err := validateMetadataURL(metadataURL); err != nil {
			return &mcpUnauthorized{
				err: fmt.Errorf("failed to parse WWW-Authenticate header: %w", err),
			}
		}
		return &mcpUnauthorized{metadataURL: metadataURL, scope: scope}
	}

	if forbidden && !hasInsufficientScopeChallenge(challenges) {
		// A 403 whose challenge names neither a resource_metadata URL nor
		// error="insufficient_scope" is a plain authorization denial;
		// re-authenticating would loop without helping.
		return fmt.Errorf("server returned 403 forbidden (not an OAuth challenge)")
	}

	return &mcpUnauthorized{
		scope: scope,
		err:   fmt.Errorf("received %d response without usable WWW-Authenticate header (no resource_metadata)", resp.StatusCode),
	}
}

// hasInsufficientScopeChallenge reports whether any challenge carries the RFC
// 6750 error="insufficient_scope" parameter, the OAuth-remediable form of a
// 403.
func hasInsufficientScopeChallenge(challenges []oauthex.Challenge) bool {
	for _, challenge := range challenges {
		if challenge.Params["error"] == "insufficient_scope" {
			return true
		}
	}
	return false
}

// challengeScope returns the scope parameter from the first challenge that
// carries one (RFC 6750 §3: the scope a client needs to access the resource).
func challengeScope(challenges []oauthex.Challenge) string {
	for _, challenge := range challenges {
		if scope := challenge.Params["scope"]; scope != "" {
			return scope
		}
	}
	return ""
}

// persistingTokenSource returns the stored token while valid and refreshes it
// against the grant envelope's pinned token endpoint and client when expired.
// State transitions on the shared KV entry are compare-and-set (attempted →
// refreshed, attempted → deleted) so concurrent refreshes on other cluster
// nodes can neither be erased nor resurrected; on a lost race the winner's
// grant is adopted. It is safe for concurrent use.
type persistingTokenSource struct {
	mu sync.Mutex
	// ctx is the connection context the SDK hands to TokenSource; each
	// refresh derives a bounded per-call context from it.
	ctx         context.Context
	envelope    *storedTokenEnvelope
	staticCreds *StaticOAuthCredentials
	manager     *OAuthManager
	userID      string
	serverName  string
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.envelope.Token.Valid() {
		return s.envelope.Token, nil
	}

	if s.envelope.Version == 0 || s.envelope.TokenEndpoint == "" || s.envelope.ClientID == "" {
		// Pre-envelope grant: there is no pinned refresh destination, and
		// rediscovering one would let a compromised server redirect the
		// refresh token. Force one re-authorization instead. (Unconditional
		// delete: legacy entries are not CAS-able against the envelope type.)
		if delErr := s.manager.deleteToken(s.userID, s.serverName); delErr != nil {
			s.manager.pluginAPI.LogWarn("Failed to delete legacy token", "error", delErr)
		}
		return nil, &mcpUnauthorized{
			err: fmt.Errorf("stored token predates authorization server binding and cannot be refreshed safely, re-authentication required"),
		}
	}

	if s.envelope.Token.RefreshToken == "" {
		if won, delErr := s.manager.casDeleteTokenEnvelope(s.userID, s.serverName, s.envelope); delErr != nil || !won {
			if adopted := s.adoptLatestGrant(); adopted != nil {
				return adopted, nil
			}
		}
		return nil, &mcpUnauthorized{
			err: fmt.Errorf("access token expired and no refresh token was issued, re-authentication required"),
		}
	}

	creds, credsErr := s.refreshCredentials()
	if credsErr != nil {
		return nil, credsErr
	}

	refreshCtx, cancel := context.WithTimeout(s.ctx, oauthPrepTimeout)
	defer cancel()
	token, err := s.manager.refreshGrant(refreshCtx, s.envelope, creds)
	if err != nil {
		// invalid_grant happens when client credentials changed (e.g. v1 ->
		// v2 migration) — or, in HA, when another node already rotated the
		// refresh token we are holding. The compare-and-delete only clears
		// the grant when it is still the one we attempted to refresh; on a
		// lost race the winner's rotated grant is adopted.
		if isInvalidGrantError(err) {
			if won, delErr := s.manager.casDeleteTokenEnvelope(s.userID, s.serverName, s.envelope); delErr != nil || !won {
				if adopted := s.adoptLatestGrant(); adopted != nil {
					return adopted, nil
				}
				return nil, fmt.Errorf("token refresh failed but the stored token changed concurrently; retry: %w", err)
			}
			return nil, &mcpUnauthorized{
				err: fmt.Errorf("token refresh failed (credentials may have changed), re-authentication required: %w", err),
			}
		}
		return nil, err
	}

	refreshed := *s.envelope
	refreshed.Token = token
	if won, casErr := s.manager.casTokenEnvelope(s.userID, s.serverName, s.envelope, &refreshed); casErr != nil {
		// Don't fail the request over a persistence problem; the refreshed
		// token is still valid for this request and we retry on the next
		// refresh.
		s.manager.pluginAPI.LogWarn("Failed to persist refreshed OAuth token",
			"userID", s.userID,
			"serverID", s.serverName,
			"error", casErr)
	} else if !won {
		// Another node persisted a different grant between our load and this
		// write; keep the winner for future calls. Our freshly issued token
		// is still valid for this request.
		s.adoptLatestGrant()
	} else {
		s.envelope = &refreshed
	}

	return token, nil
}

// adoptLatestGrant reloads the stored grant after a lost compare-and-set race
// and adopts it when it carries a valid token, returning that token (nil
// otherwise). Callers hold s.mu.
func (s *persistingTokenSource) adoptLatestGrant() *oauth2.Token {
	latest, err := s.manager.loadTokenEnvelope(s.userID, s.serverName)
	if err != nil || latest == nil || latest.Token == nil {
		return nil
	}
	s.envelope = latest
	if latest.Token.Valid() {
		return latest.Token
	}
	return nil
}

// refreshCredentials resolves the client credentials for a refresh, strictly
// bound to the client the grant was issued to: static credentials must match
// the pinned client ID, and dynamically registered credentials are loaded
// from the KV store without any registration fallback.
func (s *persistingTokenSource) refreshCredentials() (*ClientCredentials, error) {
	if s.staticCreds != nil && s.staticCreds.ClientID != "" {
		if s.staticCreds.ClientID != s.envelope.ClientID {
			return nil, &mcpUnauthorized{
				err: fmt.Errorf("configured client ID no longer matches the client this grant was issued to, re-authentication required"),
			}
		}
		return &ClientCredentials{
			ClientID:     s.staticCreds.ClientID,
			ClientSecret: s.staticCreds.ClientSecret,
		}, nil
	}

	creds, err := s.manager.loadClientCredentials(s.envelope.AuthServerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to load client credentials for refresh: %w", err)
	}
	if creds == nil || creds.ClientID != s.envelope.ClientID {
		return nil, &mcpUnauthorized{
			err: fmt.Errorf("registered client for this grant is no longer available, re-authentication required"),
		}
	}
	return creds, nil
}

// ValidateResourceMetadataURL validates a resource_metadata URL from an OAuth
// challenge or from the MCP OAuth start redirect query string.
func ValidateResourceMetadataURL(metadataURL string) error {
	return validateMetadataURL(metadataURL)
}

// validateMetadataURL validates the extracted URL
func validateMetadataURL(metadataURL string) error {
	if metadataURL == "" {
		return fmt.Errorf("empty resource_metadata URL")
	}

	// Length validation
	const maxURLLength = 2048 // Common URL length limit
	if len(metadataURL) > maxURLLength {
		return fmt.Errorf("resource_metadata URL too long (max %d bytes)", maxURLLength)
	}

	// Basic URL format validation
	parsedURL, err := url.Parse(metadataURL)
	if err != nil {
		return fmt.Errorf("invalid resource_metadata URL format: %v", err)
	}

	if parsedURL.Scheme == "" {
		return fmt.Errorf("resource_metadata URL missing scheme")
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("resource_metadata URL missing host")
	}

	return nil
}
