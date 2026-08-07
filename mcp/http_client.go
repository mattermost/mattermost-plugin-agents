// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"net/http"
	"net/url"
)

// canonicalOrigin returns a scheme://host:port origin for u with the scheme's
// default port filled in, so "https://x" and "https://x:443" compare equal.
// It returns "" for a nil URL or a scheme without a known default port.
func canonicalOrigin(u *url.URL) string {
	if u == nil || u.Scheme == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			return ""
		}
	}
	return u.Scheme + "://" + u.Hostname() + ":" + port
}

// canonicalOriginFromString parses raw and returns its canonical origin, or ""
// if it cannot be parsed.
func canonicalOriginFromString(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return canonicalOrigin(u)
}

// blockCrossOriginRedirect returns an http.Client CheckRedirect that refuses to
// follow a redirect whose target origin differs from the pinned server origin.
// Credentials (OAuth bearer tokens, admin-configured auth headers) are attached
// per-hop by our RoundTrippers, so without this an MCP server could redirect to
// an attacker host and have the client replay those credentials there. Blocking
// the redirect before the request is sent prevents any leak; same-origin
// redirects and Go's default 10-hop limit still apply.
func blockCrossOriginRedirect(pinnedOrigin string) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if pinnedOrigin != "" && canonicalOrigin(req.URL) != pinnedOrigin {
			return fmt.Errorf("refusing cross-origin redirect to %q (pinned MCP server origin %q)", canonicalOrigin(req.URL), pinnedOrigin)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
}

// headerTransport is a custom RoundTripper that adds headers to requests bound
// for the pinned MCP server origin.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
	// expectedOrigin pins the origin the headers may be sent to. Custom
	// headers commonly carry credentials (API keys, Authorization), so they
	// must not be replayed to a different host across a redirect.
	expectedOrigin string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	req = req.Clone(req.Context())

	// Only attach the configured headers when the request is still going to
	// the pinned server origin. A redirect to another host must not carry
	// them (defense in depth alongside the client's CheckRedirect).
	if t.expectedOrigin == "" || canonicalOrigin(req.URL) == t.expectedOrigin {
		for key, value := range t.headers {
			req.Header.Set(key, value)
		}
	}

	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// httpClientForMCP returns a copy of the client's http.Client with the custom
// headers applied. OAuth is intentionally NOT wired here: the streamable
// transport handles it through mcp.StreamableClientTransport.OAuthHandler.
func (c *Client) httpClientForMCP(baseURL string, headers map[string]string) *http.Client {
	httpClient := *c.httpClient
	origin := canonicalOriginFromString(baseURL)
	httpClient.CheckRedirect = blockCrossOriginRedirect(origin)

	if len(headers) > 0 {
		httpClient.Transport = &headerTransport{
			base:           httpClient.Transport,
			headers:        headers,
			expectedOrigin: origin,
		}
	}

	return &httpClient
}

// httpClientForLegacySSE returns a copy of the client's http.Client for the
// legacy HTTP+SSE transport, which has no OAuthHandler field. OAuth is applied
// via oauthRoundTripper, a thin adapter that delegates to the same
// userOAuthHandler the streamable transport uses. Custom headers are applied
// outermost so the OAuth adapter sees the fully-populated request.
func (c *Client) httpClientForLegacySSE(baseURL string, oauthHandler *userOAuthHandler, headers map[string]string) *http.Client {
	httpClient := *c.httpClient
	origin := canonicalOriginFromString(baseURL)
	httpClient.CheckRedirect = blockCrossOriginRedirect(origin)

	// Plugin-server and embedded clients have a nil oauthManager and thus a
	// nil handler; they must skip the OAuth adapter.
	if oauthHandler != nil {
		httpClient.Transport = &oauthRoundTripper{
			handler:        oauthHandler,
			base:           httpClient.Transport,
			expectedOrigin: origin,
		}
	}

	if len(headers) > 0 {
		httpClient.Transport = &headerTransport{
			base:           httpClient.Transport,
			headers:        headers,
			expectedOrigin: origin,
		}
	}

	return &httpClient
}
