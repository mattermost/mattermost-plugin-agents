// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// headerTransport is a custom RoundTripper that adds headers to requests.
// A non-empty originKey (from originComparableKey) restricts injection to that
// origin so credentials cannot follow cross-origin redirects; empty means always
// inject, which in-process plugin transports with placeholder URLs rely on.
type headerTransport struct {
	base      http.RoundTripper
	headers   map[string]string
	originKey string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.originKey == "" || t.originKey == originComparableKey(req.URL) {
		// Clone so we do not mutate the caller's request.
		req = req.Clone(req.Context())
		for key, value := range t.headers {
			req.Header.Set(key, value)
		}
	}
	return t.base.RoundTrip(req)
}

// parseOriginURL parses raw and requires an absolute URL with scheme and host.
func parseOriginURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("URL %q missing scheme or host", raw)
	}
	return u, nil
}

func (c *Client) httpClientForMCP(headers map[string]string) *http.Client {
	httpClient := *c.httpClient
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}

	// Parse the admin-configured origin once; both credential-injecting
	// transports and the redirect policy share it. A nil origin fails closed:
	// no credentials are ever attached and every redirect is rejected.
	origin, originErr := parseOriginURL(c.config.BaseURL)

	// Plugin-server clients have a nil oauthManager and must skip the auth
	// wrapper, which would otherwise dereference it on every RoundTrip.
	if c.oauthManager != nil {
		base = &authenticationTransport{
			userID:       c.userID,
			serverName:   c.config.Name,
			manager:      c.oauthManager,
			serverURL:    c.config.BaseURL,
			serverOrigin: origin,
			staticCreds:  staticOAuthCreds(c.config),
			base:         base,
		}
	}

	if len(headers) > 0 && originErr == nil {
		base = &headerTransport{
			base:      base,
			headers:   headers,
			originKey: originComparableKey(origin),
		}
	}

	httpClient.Transport = base
	httpClient.CheckRedirect = sameOriginRedirectPolicy(c.config.Name, origin)

	return &httpClient
}

// sameOriginRedirectPolicy rejects redirects that leave the configured origin so
// credentials cannot follow to another host, keeping stdlib's 10-redirect cap.
func sameOriginRedirectPolicy(serverName string, origin *url.URL) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if origin == nil {
			return fmt.Errorf("MCP server %q: refusing redirect because configured base URL is invalid", serverName)
		}
		if !sameOrigin(origin, req.URL) {
			return fmt.Errorf("MCP server %q redirected to different origin %s", serverName, originComparableKey(req.URL))
		}
		return nil
	}
}
