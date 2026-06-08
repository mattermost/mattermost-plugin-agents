// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "net/http"

// PlaceholderAPIKey is a sentinel value for SDKs that require a non-empty API key
// when real authentication is handled by the transport (e.g., custom auth headers).
const PlaceholderAPIKey = "custom-auth"

// CustomAuthTransport is an http.RoundTripper that removes and sets headers on
// outgoing requests. It clones requests to avoid mutating the original.
type CustomAuthTransport struct {
	// Base is the underlying RoundTripper. If nil, http.DefaultTransport is used.
	Base http.RoundTripper

	// RemoveHeaders is a list of header names to remove from the request.
	RemoveHeaders []string

	// SetHeaders is a map of header names to values to set on the request.
	SetHeaders map[string]string
}

func (t *CustomAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())

	for _, h := range t.RemoveHeaders {
		clone.Header.Del(h)
	}

	for k, v := range t.SetHeaders {
		clone.Header.Set(k, v)
	}

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// CloneHTTPClientWithTransport creates a shallow copy of the given http.Client
// with the specified transport, preserving Timeout, CheckRedirect, and Jar.
// If client is nil, a new http.Client with only the transport is returned.
func CloneHTTPClientWithTransport(client *http.Client, transport http.RoundTripper) *http.Client {
	if client == nil {
		return &http.Client{
			Transport: transport,
		}
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       client.Timeout,
		CheckRedirect: client.CheckRedirect,
		Jar:           client.Jar,
	}
}
