// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import "net/http"

// headerTransport is a custom RoundTripper that adds headers to requests
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	req = req.Clone(req.Context())

	// Add custom headers
	for key, value := range t.headers {
		req.Header.Set(key, value)
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
func (c *Client) httpClientForMCP(headers map[string]string) *http.Client {
	httpClient := *c.httpClient

	if len(headers) > 0 {
		httpClient.Transport = &headerTransport{
			base:    httpClient.Transport,
			headers: headers,
		}
	}

	return &httpClient
}

// httpClientForLegacySSE returns a copy of the client's http.Client for the
// legacy HTTP+SSE transport, which has no OAuthHandler field. OAuth is applied
// via oauthRoundTripper, a thin adapter that delegates to the same
// userOAuthHandler the streamable transport uses. Custom headers are applied
// outermost so the OAuth adapter sees the fully-populated request.
func (c *Client) httpClientForLegacySSE(oauthHandler *userOAuthHandler, headers map[string]string) *http.Client {
	httpClient := *c.httpClient

	// Plugin-server and embedded clients have a nil oauthManager and thus a
	// nil handler; they must skip the OAuth adapter.
	if oauthHandler != nil {
		httpClient.Transport = &oauthRoundTripper{
			handler: oauthHandler,
			base:    httpClient.Transport,
		}
	}

	if len(headers) > 0 {
		httpClient.Transport = &headerTransport{
			base:    httpClient.Transport,
			headers: headers,
		}
	}

	return &httpClient
}
