// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package httpexternal

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHostnameAllowed(t *testing.T) {
	tests := []struct {
		name            string
		url             string
		allowedPatterns []string
		want            bool
	}{
		{
			name:            "exact match",
			url:             "https://example.com/path",
			allowedPatterns: []string{"example.com"},
			want:            true,
		},
		{
			name:            "wildcard subdomain match",
			url:             "https://sub.example.com/api",
			allowedPatterns: []string{"*.example.com"},
			want:            true,
		},
		{
			name:            "base domain not matched by wildcard",
			url:             "http://example.com",
			allowedPatterns: []string{"*.example.com"},
			want:            false, // *.example.com should not match example.com itself
		},
		{
			name:            "wildcard subdomain no match",
			url:             "https://sub.different.com/test",
			allowedPatterns: []string{"*.example.com"},
			want:            false,
		},
		{
			name:            "global wildcard match",
			url:             "https://anything.com/path",
			allowedPatterns: []string{"*"},
			want:            true,
		},
		{
			name:            "multiple patterns with match",
			url:             "https://api.github.com/v3",
			allowedPatterns: []string{"*.example.com", "api.github.com", "*.mattermost.com"},
			want:            true,
		},
		{
			name:            "multiple patterns no match",
			url:             "https://evil.com/hack",
			allowedPatterns: []string{"*.example.com", "api.github.com", "*.mattermost.com"},
			want:            false,
		},
		{
			name:            "empty patterns list",
			url:             "http://example.com",
			allowedPatterns: []string{},
			want:            false,
		},
		{
			name:            "nil patterns list",
			url:             "https://example.com",
			allowedPatterns: nil,
			want:            false,
		},
		{
			name:            "deep subdomain match",
			url:             "https://deep.sub.example.com/path",
			allowedPatterns: []string{"*.example.com"},
			want:            true,
		},
		{
			name:            "partial suffix no match",
			url:             "https://notexample.com/path",
			allowedPatterns: []string{"*.example.com"},
			want:            false,
		},
		{
			name:            "ipv6 with zone id blocked",
			url:             "https://[2001:4860:4860::8844%25eth0]",
			allowedPatterns: []string{"2001:4860:4860::8844"},
			want:            false,
		},
		{
			name:            "ipv6 with zone id allowed exact match",
			url:             "https://[2001:4860:4860::8844%25eth0]",
			allowedPatterns: []string{"2001:4860:4860::8844%eth0"},
			want:            true,
		},
		{
			name:            "ipv6 with url encoded zone id",
			url:             "https://[2001:db8::1%25wlan0]",
			allowedPatterns: []string{"2001:db8::1%wlan0"},
			want:            true,
		},
		{
			name:            "ipv6 with numeric zone id",
			url:             "https://[fe80::1234:5678:9abc%252]",
			allowedPatterns: []string{"fe80::1234:5678:9abc%2"},
			want:            true,
		},
		{
			name:            "ipv6 with zone in domain name blocked",
			url:             "https://[2001:4860:4860::8844%25atlassian.net]",
			allowedPatterns: []string{"atlassian.net"},
			want:            false,
		},
		{
			name:            "ipv6 with zone in domain name exact match",
			url:             "https://[2001:4860:4860::8844%25atlassian.net]",
			allowedPatterns: []string{"2001:4860:4860::8844%atlassian.net"},
			want:            true,
		},
		{
			name:            "ipv6 wildcard matched with with zone",
			url:             "https://[2001:db8::1%25eth0.example.com]",
			allowedPatterns: []string{"*.example.com"},
			want:            false,
		},
		{
			name:            "ipv6 literal with wildcard subdomain",
			url:             "https://[fe80::1234:5678:9abc]",
			allowedPatterns: []string{"*.fe80::1234:5678:9abc"},
			want:            false,
		},
		{
			name:            "ipv4 address exact match",
			url:             "http://192.168.1.1/path",
			allowedPatterns: []string{"192.168.1.1"},
			want:            true,
		},
		{
			name:            "ipv4 address no match",
			url:             "http://192.168.1.1/path",
			allowedPatterns: []string{"192.168.1.2"},
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.url)
			assert.NoError(t, err)
			hostname := u.Hostname()
			t.Logf("URL: %q -> Hostname: %q", tt.url, hostname)
			got := hostnameAllowed(hostname, tt.allowedPatterns)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCreateRestrictedClient(t *testing.T) {
	// Start a test HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	// Parse the test server URL to get its hostname
	tsURL, err := url.Parse(ts.URL)
	assert.NoError(t, err)
	testHostname := tsURL.Hostname()

	tests := []struct {
		name           string
		allowedHosts   []string
		targetURL      string
		expectError    bool
		errorSubstring string
	}{
		{
			name:         "allowed host",
			allowedHosts: []string{testHostname},
			targetURL:    ts.URL,
			expectError:  false,
		},
		{
			name:           "blocked host",
			allowedHosts:   []string{"allowed.com"},
			targetURL:      ts.URL,
			expectError:    true,
			errorSubstring: "not on allowed list",
		},
		{
			name:         "wildcard allowed",
			allowedHosts: []string{"*"},
			targetURL:    ts.URL,
			expectError:  false,
		},
		{
			name:         "multiple patterns with match",
			allowedHosts: []string{"other.com", testHostname, "another.com"},
			targetURL:    ts.URL,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := CreateRestrictedClient(nil, tt.allowedHosts)

			req, err := http.NewRequest("GET", tt.targetURL, nil)
			assert.NoError(t, err)

			resp, err := client.Do(req)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorSubstring != "" {
					assert.Contains(t, err.Error(), tt.errorSubstring)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
				if resp != nil {
					resp.Body.Close()
					assert.Equal(t, http.StatusOK, resp.StatusCode)
				}
			}
		})
	}
}

func TestParseAllowedHostnames(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple list",
			input:    "example.com,test.com",
			expected: []string{"example.com", "test.com"},
		},
		{
			name:     "with spaces",
			input:    " example.com , test.com ",
			expected: []string{"example.com", "test.com"},
		},
		{
			name:     "with empty entries",
			input:    "example.com,,test.com,",
			expected: []string{"example.com", "test.com"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "only spaces",
			input:    "   ",
			expected: nil,
		},
		{
			name:     "with wildcards",
			input:    "*.example.com,api.github.com",
			expected: []string{"*.example.com", "api.github.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseAllowedHostnames(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestCustomHeadersTransport(t *testing.T) {
	// Create a test server that captures request headers
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"response":"test"}`))
	}))
	defer server.Close()

	// Create custom headers
	customHeaders := map[string]string{
		"X-Custom-Header-1": "value1",
		"X-Custom-Header-2": "value2",
		"Authorization":     "Bearer custom-token", // This should override any existing auth
	}

	// Create a base HTTP client
	baseClient := &http.Client{}

	// Wrap it with custom headers
	wrappedClient := WrapHTTPClientWithCustomHeaders(baseClient, customHeaders)

	// Make a request
	req, err := http.NewRequest("POST", server.URL, nil)
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := wrappedClient.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	// Verify the response
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify custom headers were added
	assert.Equal(t, "value1", capturedHeaders.Get("X-Custom-Header-1"))
	assert.Equal(t, "value2", capturedHeaders.Get("X-Custom-Header-2"))
	assert.Equal(t, "Bearer custom-token", capturedHeaders.Get("Authorization"))
	assert.Equal(t, "application/json", capturedHeaders.Get("Content-Type"))
}

func TestWrapHTTPClientWithCustomHeadersNoHeaders(t *testing.T) {
	// Create a base HTTP client
	baseClient := &http.Client{}

	// Wrap it with no custom headers
	wrappedClient := WrapHTTPClientWithCustomHeaders(baseClient, nil)

	// Should return the same client when no headers are provided
	assert.Equal(t, baseClient, wrappedClient)

	// Test with empty map too
	wrappedClient2 := WrapHTTPClientWithCustomHeaders(baseClient, map[string]string{})
	assert.Equal(t, baseClient, wrappedClient2)
}
