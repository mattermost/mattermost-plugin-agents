// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConstructWellKnownURL(t *testing.T) {
	tests := []struct {
		name           string
		issuer         string
		suffix         string
		expectedURL    string
		expectError    bool
		errorSubstring string
	}{
		{
			name:        "Simple URL without path",
			issuer:      "https://example.com",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server",
			expectError: false,
		},
		{
			name:        "URL with single path component",
			issuer:      "https://example.com/issuer1",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server/issuer1",
			expectError: false,
		},
		{
			name:        "URL with multiple path components",
			issuer:      "https://example.com/path/to/issuer",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server/path/to/issuer",
			expectError: false,
		},
		{
			name:        "URL with trailing slash",
			issuer:      "https://example.com/issuer1/",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server/issuer1",
			expectError: false,
		},
		{
			name:        "URL with port",
			issuer:      "https://example.com:8443",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com:8443/.well-known/oauth-authorization-server",
			expectError: false,
		},
		{
			name:        "URL with port and path",
			issuer:      "https://example.com:8443/issuer1",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com:8443/.well-known/oauth-authorization-server/issuer1",
			expectError: false,
		},
		{
			name:        "Protected resource metadata suffix",
			issuer:      "https://resource.example.com",
			suffix:      "oauth-protected-resource",
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
			expectError: false,
		},
		{
			name:        "Protected resource metadata suffix with path",
			issuer:      "https://resource.example.com/api/v1",
			suffix:      "oauth-protected-resource",
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource/api/v1",
			expectError: false,
		},
		{
			name:        "localhost URL",
			issuer:      "http://localhost:3000",
			suffix:      "oauth-authorization-server",
			expectedURL: "http://localhost:3000/.well-known/oauth-authorization-server",
			expectError: false,
		},
		{
			name:        "localhost URL with path",
			issuer:      "http://localhost:3000/oauth",
			suffix:      "oauth-authorization-server",
			expectedURL: "http://localhost:3000/.well-known/oauth-authorization-server/oauth",
			expectError: false,
		},
		{
			name:        "URL with only root path",
			issuer:      "https://example.com/",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server",
			expectError: false,
		},
		{
			name:        "URL with subdomain",
			issuer:      "https://auth.example.com/oauth",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://auth.example.com/.well-known/oauth-authorization-server/oauth",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := constructWellKnownURL(tt.issuer, tt.suffix)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.errorSubstring != "" && !strings.Contains(err.Error(), tt.errorSubstring) {
					t.Errorf("Expected error to contain %q, but got: %v", tt.errorSubstring, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if result != tt.expectedURL {
					t.Errorf("Expected URL %q, but got %q", tt.expectedURL, result)
				}
			}
		})
	}
}

func TestConstructAppendedWellKnownURL(t *testing.T) {
	tests := []struct {
		name        string
		issuer      string
		suffix      string
		expectedURL string
	}{
		{
			name:        "URL without path collapses to host root",
			issuer:      "https://example.com",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/.well-known/oauth-authorization-server",
		},
		{
			name:        "URL with single path component appends after path",
			issuer:      "https://example.com/issuer1",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/issuer1/.well-known/oauth-authorization-server",
		},
		{
			name:        "URL with multiple path components appends after path",
			issuer:      "https://example.com/path/to/issuer",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/path/to/issuer/.well-known/oauth-authorization-server",
		},
		{
			name:        "URL with trailing slash strips it before appending",
			issuer:      "https://example.com/issuer1/",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://example.com/issuer1/.well-known/oauth-authorization-server",
		},
		{
			name:        "Rocketlane-style URL appends well-known after resource path",
			issuer:      "https://rocketlane.scalekit.com/resources/res_121247790507492638",
			suffix:      "oauth-authorization-server",
			expectedURL: "https://rocketlane.scalekit.com/resources/res_121247790507492638/.well-known/oauth-authorization-server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := constructAppendedWellKnownURL(tt.issuer, tt.suffix)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedURL, result)
		})
	}
}

// TestDiscoverAuthorizationServerMetadata_PathAppendedFallback verifies that
// authorization-server metadata discovery falls back to the path-appended
// well-known URL when the RFC 8414 location returns 404. Some MCP servers
// (e.g. Rocketlane via Scalekit) only publish metadata at the path-appended
// location.
func TestDiscoverAuthorizationServerMetadata_PathAppendedFallback(t *testing.T) {
	const resourcePath = "/resources/res_121247790507492638"

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server" + resourcePath:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404 page not found"))
		case resourcePath + "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			metadata := AuthorizationServerMetadata{
				Issuer:                serverURL + resourcePath,
				AuthorizationEndpoint: serverURL + "/authorize",
				TokenEndpoint:         serverURL + "/token",
				RegistrationEndpoint:  serverURL + "/register",
			}
			_ = json.NewEncoder(w).Encode(metadata)
		default:
			t.Errorf("Unexpected request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	metadata, err := discoverAuthorizationServerMetadata(context.Background(), http.DefaultClient, server.URL+resourcePath)
	require.NoError(t, err)
	assert.Equal(t, serverURL+"/authorize", metadata.AuthorizationEndpoint)
	assert.Equal(t, serverURL+"/token", metadata.TokenEndpoint)
	assert.Equal(t, serverURL+"/register", metadata.RegistrationEndpoint)
}

// TestDiscoverAuthorizationServerMetadata_NonNotFoundErrorDoesNotFallBack
// verifies that we only attempt the path-appended fallback when the primary
// URL returns 404. Other status codes (e.g. 500) indicate a real upstream
// problem and the original error should be returned.
func TestDiscoverAuthorizationServerMetadata_NonNotFoundErrorDoesNotFallBack(t *testing.T) {
	const resourcePath = "/resources/res_121247790507492638"

	var appendedHit bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server" + resourcePath:
			w.WriteHeader(http.StatusInternalServerError)
		case resourcePath + "/.well-known/oauth-authorization-server":
			appendedHit = true
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	_, err := discoverAuthorizationServerMetadata(context.Background(), http.DefaultClient, server.URL+resourcePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
	assert.False(t, appendedHit, "fallback URL must not be tried for non-404 responses")
}
