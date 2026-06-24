// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"strings"
	"testing"
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

func TestRequireSafeOAuthURL(t *testing.T) {
	tests := []struct {
		name      string
		rawURL    string
		expectErr bool
	}{
		// Allowed: legitimate public OAuth servers over HTTPS.
		{name: "https public host", rawURL: "https://auth.example.com/authorize", expectErr: false},
		{name: "https public host with port", rawURL: "https://auth.example.com:8443/token", expectErr: false},
		// Allowed: loopback / localhost (supported local deployment; SSRF to
		// loopback in production is blocked by the untrusted HTTP client).
		{name: "loopback ipv4 http", rawURL: "http://127.0.0.1:53257/authorize", expectErr: false},
		{name: "loopback ipv6 http", rawURL: "http://[::1]:9000/token", expectErr: false},
		{name: "localhost http", rawURL: "http://localhost:8080/register", expectErr: false},
		// Rejected: plaintext downgrade to a non-local host.
		{name: "http public host", rawURL: "http://auth.example.com/authorize", expectErr: true},
		{name: "non-http scheme", rawURL: "file:///etc/passwd", expectErr: true},
		// Rejected: SSRF into internal ranges via a crafted metadata body.
		{name: "private 10/8", rawURL: "https://10.0.0.5/token", expectErr: true},
		{name: "private 192.168/16", rawURL: "https://192.168.1.1/authorize", expectErr: true},
		{name: "private 172.16/12", rawURL: "https://172.16.0.1/token", expectErr: true},
		{name: "link-local 169.254", rawURL: "https://169.254.169.254/latest/meta-data", expectErr: true},
		{name: "unspecified", rawURL: "https://0.0.0.0/token", expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireSafeOAuthURL(tt.rawURL)
			if tt.expectErr && err == nil {
				t.Errorf("expected error for %q, got nil", tt.rawURL)
			}
			if !tt.expectErr && err != nil {
				t.Errorf("expected no error for %q, got %v", tt.rawURL, err)
			}
		})
	}
}
