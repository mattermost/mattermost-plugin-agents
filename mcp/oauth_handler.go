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

	oauthConfig, err := h.manager.createOAuthConfig(ctx, h.serverURL, "", h.staticCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth config: %w", err)
	}

	if h.manager.httpClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, h.manager.httpClient)
	}

	return &persistingTokenSource{
		base:            oauthConfig.TokenSource(ctx, token),
		manager:         h.manager,
		userID:          h.userID,
		serverName:      h.serverName,
		lastAccessToken: token.AccessToken,
	}, nil
}

// Authorize is called by the transport on a 401/403 response. It never
// returns nil (see the type comment): it always converts the response into a
// *mcpUnauthorized carrying the RFC 9728 resource_metadata URL from the
// WWW-Authenticate challenge when one is present.
func (h *userOAuthHandler) Authorize(_ context.Context, _ *http.Request, resp *http.Response) error {
	drainAndCloseResponseBody(resp)

	headers := resp.Header.Values("WWW-Authenticate")
	if len(headers) == 0 {
		return &mcpUnauthorized{
			err: fmt.Errorf("received %d response without WWW-Authenticate header", resp.StatusCode),
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

	return &mcpUnauthorized{
		err: fmt.Errorf("received %d response without usable WWW-Authenticate header (no resource_metadata)", resp.StatusCode),
	}
}

// persistingTokenSource wraps an oauth2.TokenSource and persists refreshed
// tokens back to the KV store so refresh tokens rotated by the authorization
// server survive process restarts and are visible to other cluster nodes.
// It is safe for concurrent use.
type persistingTokenSource struct {
	mu              sync.Mutex
	base            oauth2.TokenSource
	manager         *OAuthManager
	userID          string
	serverName      string
	lastAccessToken string
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	token, err := s.base.Token()
	if err != nil {
		// Token refresh failure with invalid_grant happens when client
		// credentials changed (e.g. v1 -> v2 migration) and the old token was
		// issued for different credentials. Clear the stale token and surface
		// an error that triggers re-authentication.
		if strings.Contains(err.Error(), "invalid_grant") {
			if delErr := s.manager.deleteToken(s.userID, s.serverName); delErr != nil {
				s.manager.pluginAPI.LogWarn("Failed to delete stale token", "error", delErr)
			}
			return nil, &mcpUnauthorized{
				err: fmt.Errorf("token refresh failed (credentials may have changed), re-authentication required: %w", err),
			}
		}
		return nil, err
	}

	if token.AccessToken != s.lastAccessToken {
		if storeErr := s.manager.storeToken(s.userID, s.serverName, token); storeErr != nil {
			// Don't fail the request over a persistence problem; the refreshed
			// token is still valid for this request and we retry storing on
			// the next refresh.
			s.manager.pluginAPI.LogWarn("Failed to persist refreshed OAuth token",
				"userID", s.userID,
				"serverID", s.serverName,
				"error", storeErr)
		} else {
			s.lastAccessToken = token.AccessToken
		}
	}

	return token, nil
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
