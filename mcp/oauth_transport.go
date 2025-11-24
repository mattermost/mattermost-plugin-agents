// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// mcpUnauthrorized is returned when OAuth authentication is needed
type mcpUnauthrorized struct {
	metadataURL string
	err         error
}

func (e *mcpUnauthrorized) Error() string {
	if e.err != nil {
		return fmt.Sprintf("OAuth authentication needed for resource at %s: Got error: %v", e.metadataURL, e.err)
	}
	return fmt.Sprintf("OAuth authentication needed for resource at %s", e.metadataURL)
}
func (e *mcpUnauthrorized) MetadataURL() string {
	return e.metadataURL
}
func (e *mcpUnauthrorized) Unwrap() error {
	return e.err
}

// createOAuthHandler creates an OAuthHandler for use with go-sdk's auth.HTTPTransport
// The handler will:
// - Load existing token from storage and return TokenSource if available
// - Initiate async OAuth flow and return error if no token exists
func (m *OAuthManager) createOAuthHandler(userID, serverID, serverURL string) auth.OAuthHandler {
	return func(req *http.Request, res *http.Response) (oauth2.TokenSource, error) {
		// Try to load existing token from storage
		token, err := m.loadToken(userID, serverID)
		if err != nil {
			return nil, fmt.Errorf("failed to load token: %w", err)
		}

		if token == nil {
			// No token available - need to initiate async OAuth flow
			// Use go-sdk to parse WWW-Authenticate header and get metadata
			metadata, parseErr := oauthex.GetProtectedResourceMetadataFromHeader(
				req.Context(),
				serverURL,
				res.Header,
				http.DefaultClient,
			)

			metadataURL := ""
			if parseErr == nil && metadata != nil {
				metadataURL = metadata.Resource
			} else {
				// Fallback to parsing header directly
				metadataURL, _ = parseWWWAuthenticateHeader(res.Header.Get("WWW-Authenticate"))
			}

			// Initiate async OAuth flow (stores session, generates auth URL)
			authURL, flowErr := m.InitiateOAuthFlow(
				req.Context(),
				userID,
				serverID,
				serverURL,
				metadataURL,
			)
			if flowErr != nil {
				return nil, &mcpUnauthrorized{
					metadataURL: metadataURL,
					err:         fmt.Errorf("failed to initiate OAuth flow: %w", flowErr),
				}
			}

			// Return error to cancel request - caller will see this and can redirect user
			return nil, &OAuthNeededError{authURL: authURL}
		}

		// We have a token - create TokenSource for go-sdk to use
		oauthConfig, err := m.createOAuthConfig(req.Context(), serverURL, "")
		if err != nil {
			return nil, fmt.Errorf("failed to create OAuth config: %w", err)
		}

		return oauthConfig.TokenSource(req.Context(), token), nil
	}
}

// createHTTPTransport creates an http.RoundTripper using go-sdk's auth.HTTPTransport
// This transport handles:
// - Loading tokens from storage
// - Automatic retry with token on 401
// - Initiating async OAuth flows when no token exists
func (m *OAuthManager) createHTTPTransport(userID, serverID, serverURL string) (http.RoundTripper, error) {
	handler := m.createOAuthHandler(userID, serverID, serverURL)

	transport, err := auth.NewHTTPTransport(handler, &auth.HTTPTransportOptions{
		Base: http.DefaultTransport,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP transport: %w", err)
	}

	return transport, nil
}
