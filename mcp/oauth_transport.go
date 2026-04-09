// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
)

// oauthAuthManager is the OAuthManager surface used by authenticationTransport.
// It exists so RoundTrip behavior can be unit-tested without a full plugin stack.
type oauthAuthManager interface {
	loadToken(userID, serverID string) (*oauth2.Token, error)
	deleteToken(userID, serverID string) error
	createOAuthConfig(ctx context.Context, serverURL, metadataURL string, staticCreds *StaticOAuthCredentials) (*oauth2.Config, error)
}

// authenticationTransport handles 401 responses for MCP
type authenticationTransport struct {
	userID              string
	serverName          string
	serverURL           string
	manager             oauthAuthManager
	staticCreds         *StaticOAuthCredentials
	base                http.RoundTripper
	fallbackAuthHeaders map[string]string
	isAutomatedInvoker  bool
}

type mcpUnauthorized struct {
	metadataURL string
	err         error
}

func drainAndCloseResponseBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func (e *mcpUnauthorized) Error() string {
	if e.err != nil {
		return fmt.Sprintf("OAuth authentication needed for resource at %s: Got error: %v", e.metadataURL, e.err)
	}
	return fmt.Sprintf("OAuth authentication needed for resource at %s", e.metadataURL)
}
func (e *mcpUnauthorized) MetadataURL() string {
	return e.metadataURL
}
func (e *mcpUnauthorized) Unwrap() error {
	return e.err
}

// RoundTrip implements http.RoundTripper interface with 401 handling for OAuth
func (t *authenticationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	reqBodyClosed := false
	if req.Body != nil {
		defer func() {
			if !reqBodyClosed {
				req.Body.Close()
			}
		}()
	}

	transport := t.base
	useFallbackAuth := false

	if t.isAutomatedInvoker {
		// Automated invokers must not use per-user OAuth tokens from the Mattermost user.
		// Prefer FallbackAuthHeaders when set; otherwise pass through so outer transports
		// (e.g. headerTransport with static server Headers / API keys) still apply.
		if len(t.fallbackAuthHeaders) > 0 {
			useFallbackAuth = true
			req = req.Clone(req.Context())
			setCount := 0
			for k, v := range t.fallbackAuthHeaders {
				k = strings.TrimSpace(k)
				if k == "" {
					continue
				}
				req.Header.Set(k, v)
				setCount++
			}
			if setCount == 0 {
				return nil, fmt.Errorf("MCP server %q: fallback authentication headers have no valid header names (empty keys are ignored)", t.serverName)
			}
		}
		// When len(fallbackAuthHeaders)==0, transport stays t.base (static Headers are
		// applied by wrapping headerTransport in httpClientForMCP).
	} else {
		token, err := t.manager.loadToken(t.userID, t.serverName)
		if err != nil {
			return nil, fmt.Errorf("failed to load token: %w", err)
		}

		if token != nil {
			oauthConfig, configErr := t.manager.createOAuthConfig(req.Context(), t.serverURL, "", t.staticCreds)
			if configErr != nil {
				return nil, fmt.Errorf("failed to create OAuth config: %w", configErr)
			}

			transport = &oauth2.Transport{
				Source: oauthConfig.TokenSource(req.Context(), token),
				Base:   transport,
			}
		}
	}

	reqBodyClosed = true
	resp, err := transport.RoundTrip(req)
	if err != nil {
		// Check if this is an OAuth token refresh failure (invalid_grant)
		// This happens when client credentials changed (e.g., v1 -> v2 migration)
		// and the old token was issued for different credentials
		if strings.Contains(err.Error(), "invalid_grant") {
			// Clear the stale token - it's no longer valid with current credentials
			if delErr := t.manager.deleteToken(t.userID, t.serverName); delErr != nil {
				if om, ok := t.manager.(*OAuthManager); ok {
					om.pluginAPI.LogWarn("Failed to delete stale token", "error", delErr)
				}
			}
			// Return error that will trigger re-authentication
			return nil, &mcpUnauthorized{
				metadataURL: "",
				err:         fmt.Errorf("token refresh failed (credentials may have changed), re-authentication required: %w", err),
			}
		}
		return nil, fmt.Errorf("authenticationTransport round trip failed: %w", err)
	}

	// If we get a 401, force an actual error so we can handle it. Include the header info in the error
	if resp.StatusCode == http.StatusUnauthorized {
		if useFallbackAuth {
			drainAndCloseResponseBody(resp)
			return nil, fmt.Errorf("MCP server %q: fallback authentication rejected (401 Unauthorized)", t.serverName)
		}
		// Parse WWW-Authenticate header for resource metadata URL
		wwwAuthHeader := resp.Header.Get("WWW-Authenticate")
		if wwwAuthHeader != "" {
			metadataURL, parseErr := parseWWWAuthenticateHeader(wwwAuthHeader)
			if parseErr != nil {
				drainAndCloseResponseBody(resp)
				return nil, &mcpUnauthorized{
					metadataURL: "",
					err:         fmt.Errorf("failed to parse WWW-Authenticate header: %w", parseErr),
				}
			}

			drainAndCloseResponseBody(resp)
			return nil, &mcpUnauthorized{
				metadataURL: metadataURL,
			}
		}
		drainAndCloseResponseBody(resp)
		return nil, &mcpUnauthorized{
			metadataURL: "",
			err:         fmt.Errorf("received 401 response without WWW-Authenticate header"),
		}
	}

	return resp, err
}
