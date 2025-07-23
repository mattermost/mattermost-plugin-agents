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

	return t.base.RoundTrip(req)
}

func (c *Client) httpClient(headers map[string]string) *http.Client {
	// Wrap with discovery-aware transport for 401 handling
	authenticationTransport := &authenticationTransport{
		userID:     c.userID,
		serverName: c.config.Name,
		manager:    c.oauthManager,
		serverURL:  c.config.BaseURL,
	}

	// Create HTTP client with discovery-aware transport
	httpClient := &http.Client{
		Transport: authenticationTransport,
	}

	// Add custom headers to the HTTP client if provided
	if len(headers) > 0 {
		httpClient.Transport = &headerTransport{
			base:    httpClient.Transport,
			headers: headers,
		}
	}

	return httpClient
}
