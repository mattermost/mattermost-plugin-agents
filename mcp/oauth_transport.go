// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"io"
	"net/http"
)

// mcpUnauthorized is the internal error produced when an MCP server rejects a
// request with 401 (or a token refresh fails permanently). Client code
// converts it into the public *OAuthNeededError via errors.As.
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

// oauthRoundTripper adapts a userOAuthHandler for the legacy HTTP+SSE
// transport, which (unlike mcp.StreamableClientTransport) has no OAuthHandler
// field. It contains no OAuth logic of its own: it asks the handler for a
// token source before the request and delegates 401 responses to
// handler.Authorize, returning its error.
type oauthRoundTripper struct {
	handler *userOAuthHandler
	base    http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *oauthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	reqBodyClosed := false
	if req.Body != nil {
		defer func() {
			if !reqBodyClosed {
				req.Body.Close()
			}
		}()
	}

	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	tokenSource, err := t.handler.TokenSource(req.Context())
	if err != nil {
		return nil, err
	}
	if tokenSource != nil {
		token, tokenErr := tokenSource.Token()
		if tokenErr != nil {
			return nil, tokenErr
		}
		req = req.Clone(req.Context())
		token.SetAuthHeader(req)
	}

	reqBodyClosed = true
	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("oauthRoundTripper round trip failed: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// Match the auth.OAuthHandler contract (and the streamable transport):
		// both 401 and 403 are delegated to Authorize, which drains and closes
		// the response body and always returns a non-nil *mcpUnauthorized.
		return nil, t.handler.Authorize(req.Context(), req, resp)
	}

	return resp, nil
}
