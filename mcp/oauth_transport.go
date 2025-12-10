// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
)

// authenticationTransport handles 401 responses for MCP
type authenticationTransport struct {
	userID     string
	serverName string
	serverURL  string
	manager    *OAuthManager
}

type mcpUnauthorized struct {
	metadataURL string
	err         error
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

	token, err := t.manager.loadToken(t.userID, t.serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to load token: %w", err)
	}

	transport := http.DefaultTransport

	// Include the token if found
	if token != nil {
		oauthConfig, configErr := t.manager.createOAuthConfig(req.Context(), t.serverURL, "")
		if configErr != nil {
			return nil, fmt.Errorf("failed to create OAuth config: %w", configErr)
		}

		transport = &oauth2.Transport{
			Source: oauthConfig.TokenSource(req.Context(), token),
			Base:   transport,
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
				t.manager.pluginAPI.LogWarn("Failed to delete stale token", "error", delErr)
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
		// Parse WWW-Authenticate header for resource metadata URL
		wwwAuthHeader := resp.Header.Get("WWW-Authenticate")
		if wwwAuthHeader != "" {
			metadataURL, parseErr := parseWWWAuthenticateHeader(wwwAuthHeader)
			if parseErr != nil {
				return nil, &mcpUnauthorized{
					metadataURL: "",
					err:         fmt.Errorf("failed to parse WWW-Authenticate header: %w", parseErr),
				}
			}

			return nil, &mcpUnauthorized{
				metadataURL: metadataURL,
			}
		}
	}

	return resp, err
}
