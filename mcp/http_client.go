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
	// Create OAuth-aware transport using go-sdk's auth.HTTPTransport
	transport, err := c.oauthManager.createHTTPTransport(
		c.userID,
		c.config.Name,
		c.config.BaseURL,
	)
	if err != nil {
		// Fallback to default transport if creation fails
		// This shouldn't happen in normal operation, but provides a safety net
		c.oauthManager.pluginAPI.LogError("Failed to create HTTP transport", "error", err.Error())
		transport = http.DefaultTransport
	}

	// Create HTTP client with OAuth-aware transport
	httpClient := &http.Client{
		Transport: transport,
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
