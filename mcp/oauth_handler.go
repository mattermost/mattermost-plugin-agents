// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// TokenSource returns a token source backed by the KV-stored token for this
// user and server. It returns (nil, nil) when the user has no stored token, in
// which case the transport sends the request unauthenticated and the server's
// 401 lands in Authorize.
func (h *userOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	token, err := h.manager.loadToken(h.userID, h.serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to load token: %w", err)
	}
	if token == nil {
		return nil, nil
	}

	// The streamable transport calls TokenSource while holding a
	// connection-wide send lock, with the connection context rather than the
	// per-request one — so caller deadlines do not bound this work and a hung
	// identity provider would stall every queued send on the session. Bound
	// the discovery here (and each refresh in persistingTokenSource.Token)
	// with our own timeout instead. The cached variant additionally avoids
	// re-running metadata discovery on every outgoing MCP request.
	discoveryCtx, cancel := context.WithTimeout(ctx, oauthPrepTimeout)
	defer cancel()
	oauthConfig, err := h.manager.createOAuthConfigCached(discoveryCtx, h.serverURL, "", h.staticCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth config: %w", err)
	}

	if h.manager.httpClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, h.manager.httpClient)
	}

	return &persistingTokenSource{
		ctx:        ctx,
		config:     oauthConfig,
		token:      token,
		manager:    h.manager,
		userID:     h.userID,
		serverName: h.serverName,
	}, nil
}

// oauthPrepTimeout bounds the network work TokenSource may perform (metadata
// discovery on cache misses, token refresh) so a hung identity provider
// cannot stall MCP sessions indefinitely. It is a variable only so tests can
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
// *mcpUnauthorized carrying the RFC 9728 resource_metadata URL from the
// WWW-Authenticate challenge when one is present. For 403 it returns a
// *mcpUnauthorized only when the challenge indicates an OAuth-remediable
// condition; a plain authorization denial is surfaced as an ordinary error so
// callers do not loop through futile re-authentication.
func (h *userOAuthHandler) Authorize(_ context.Context, _ *http.Request, resp *http.Response) error {
	drainAndCloseResponseBody(resp)

	headers := resp.Header.Values("WWW-Authenticate")
	if len(headers) == 0 {
		if resp.StatusCode == http.StatusForbidden {
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
		return &mcpUnauthorized{
			err: fmt.Errorf("WWW-Authenticate header too long (%d bytes, max %d)", totalHeaderBytes, maxWWWAuthenticateBytes),
		}
	}

	challenges, parseErr := oauthex.ParseWWWAuthenticate(headers)
	if parseErr != nil {
		return &mcpUnauthorized{
			err: fmt.Errorf("failed to parse WWW-Authenticate header: %w", parseErr),
		}
	}

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
		return &mcpUnauthorized{metadataURL: metadataURL}
	}

	if resp.StatusCode == http.StatusForbidden && !hasInsufficientScopeChallenge(challenges) {
		// A 403 whose challenge names neither a resource_metadata URL nor
		// error="insufficient_scope" is a plain authorization denial;
		// re-authenticating would loop without helping.
		return fmt.Errorf("server returned 403 forbidden (not an OAuth challenge)")
	}

	return &mcpUnauthorized{
		err: fmt.Errorf("received %d response without usable WWW-Authenticate header (no resource_metadata)", resp.StatusCode),
	}
}

// hasInsufficientScopeChallenge reports whether any challenge carries the RFC
// 6750 error="insufficient_scope" parameter, the OAuth-remediable form of a
// 403. Full scope step-up (preserving the challenged scope and unioning
// previously granted scopes) is a deliberate non-goal here; re-running the
// ordinary authorization flow is the supported remediation.
func hasInsufficientScopeChallenge(challenges []oauthex.Challenge) bool {
	for _, challenge := range challenges {
		if challenge.Params["error"] == "insufficient_scope" {
			return true
		}
	}
	return false
}

// persistingTokenSource refreshes tokens through the resolved OAuth config
// and persists refreshed tokens back to the KV store so refresh tokens
// rotated by the authorization server survive process restarts and are
// visible to other cluster nodes. It is safe for concurrent use.
type persistingTokenSource struct {
	mu sync.Mutex
	// ctx is the connection context the SDK hands to TokenSource (carrying
	// the oauth2.HTTPClient value); each Token call derives a bounded
	// per-refresh context from it.
	ctx        context.Context
	config     *oauth2.Config
	token      *oauth2.Token
	manager    *OAuthManager
	userID     string
	serverName string
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Build a fresh oauth2 source per call so every refresh runs under its
	// own bounded window: oauth2.Config.TokenSource captures its context for
	// all future refreshes, so a long-lived source constructed with a
	// timeout context would be poisoned once that timeout fires. The valid
	// (non-expired) path returns the token without any network I/O.
	refreshCtx, cancel := context.WithTimeout(s.ctx, oauthPrepTimeout)
	defer cancel()

	token, err := s.config.TokenSource(refreshCtx, s.token).Token()
	if err != nil {
		// Token refresh failure with invalid_grant happens when client
		// credentials changed (e.g. v1 -> v2 migration) and the old token was
		// issued for different credentials — or, in HA, when another node
		// already rotated the refresh token we are holding. Only clear the
		// stored token when it is still the one we attempted to refresh;
		// deleting unconditionally would erase a concurrently rotated, valid
		// token. (A tiny read-then-delete window remains; true prevention
		// would need a KV compare-and-delete.)
		if strings.Contains(err.Error(), "invalid_grant") {
			stored, loadErr := s.manager.loadToken(s.userID, s.serverName)
			if loadErr == nil && stored != nil && !sameStoredToken(stored, s.token) {
				return nil, fmt.Errorf("token refresh failed but the stored token was rotated concurrently; retry with the new token: %w", err)
			}
			if delErr := s.manager.deleteToken(s.userID, s.serverName); delErr != nil {
				s.manager.pluginAPI.LogWarn("Failed to delete stale token", "error", delErr)
			}
			return nil, &mcpUnauthorized{
				err: fmt.Errorf("token refresh failed (credentials may have changed), re-authentication required: %w", err),
			}
		}
		return nil, err
	}

	if token.AccessToken != s.token.AccessToken {
		if storeErr := s.manager.storeToken(s.userID, s.serverName, token); storeErr != nil {
			// Don't fail the request over a persistence problem; the refreshed
			// token is still valid for this request and we retry storing on
			// the next refresh.
			s.manager.pluginAPI.LogWarn("Failed to persist refreshed OAuth token",
				"userID", s.userID,
				"serverID", s.serverName,
				"error", storeErr)
		} else {
			s.token = token
		}
	}

	return token, nil
}

// sameStoredToken reports whether the stored token is the same credential this
// source attempted to refresh, comparing refresh tokens (the rotating part)
// and falling back to access tokens when neither has one.
func sameStoredToken(stored, attempted *oauth2.Token) bool {
	if stored.RefreshToken != "" || attempted.RefreshToken != "" {
		return stored.RefreshToken == attempted.RefreshToken
	}
	return stored.AccessToken == attempted.AccessToken
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
