// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"strings"
	"testing"
)

func TestParseWWWAuthenticateHeader(t *testing.T) {
	tests := []struct {
		name        string
		header      string
		expectedURL string
		expectError bool
	}{
		{
			name:        "Valid Bearer with resource_metadata",
			header:      `Bearer resource_metadata="https://resource.example.com/.well-known/oauth-protected-resource"`,
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
			expectError: false,
		},
		{
			name:        "Valid Bearer with resource_metadata and spaces",
			header:      `Bearer resource_metadata= "https://resource.example.com/.well-known/oauth-protected-resource"`,
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
			expectError: false,
		},
		{
			name:        "Valid Bearer with resource_metadata and other parameters",
			header:      `Bearer realm="protected", resource_metadata="https://resource.example.com/.well-known/oauth-protected-resource", max_age=3600`,
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
			expectError: false,
		},
		{
			name:        "Valid with different scheme (DPoP)",
			header:      `DPoP resource_metadata="https://resource.example.com/.well-known/oauth-protected-resource"`,
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
			expectError: false,
		},
		{
			name:        "Multiple challenges with resource_metadata in second",
			header:      `Basic realm="simple", Bearer resource_metadata="https://resource.example.com/.well-known/oauth-protected-resource"`,
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
			expectError: false,
		},
		{
			name:        "Valid with single quotes (if supported)",
			header:      `Bearer resource_metadata='https://resource.example.com/.well-known/oauth-protected-resource'`,
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
			expectError: false,
		},
		{
			name:        "Missing resource_metadata parameter",
			header:      `Bearer realm="protected"`,
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Empty header",
			header:      "",
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Header without quotes around resource_metadata value",
			header:      `Bearer resource_metadata=https://resource.example.com/.well-known/oauth-protected-resource`,
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Malformed header",
			header:      `Bearer resource_metadata="https://resource.example.com/.well-known/oauth-protected-resource`,
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Empty resource_metadata value",
			header:      `Bearer resource_metadata=""`,
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Resource metadata with path and query parameters",
			header:      `Bearer resource_metadata="https://resource.example.com/.well-known/oauth-protected-resource?version=1.0"`,
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource?version=1.0",
			expectError: false,
		},
		{
			name:        "Case insensitive scheme",
			header:      `bearer resource_metadata="https://resource.example.com/.well-known/oauth-protected-resource"`,
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
			expectError: false,
		},
		{
			name:        "Case insensitive parameter name",
			header:      `Bearer Resource_Metadata="https://resource.example.com/.well-known/oauth-protected-resource"`,
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
			expectError: false,
		},
		{
			name:        "Multiple spaces and tabs",
			header:      `Bearer 	resource_metadata  =  "https://resource.example.com/.well-known/oauth-protected-resource"`,
			expectedURL: "https://resource.example.com/.well-known/oauth-protected-resource",
			expectError: false,
		},
		{
			name:        "ReDoS attack - excessive whitespace",
			header:      `Bearer resource_metadata` + strings.Repeat(" ", 100) + `= "https://resource.example.com/.well-known/oauth-protected-resource"`,
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "ReDoS attack - header too long",
			header:      `Bearer resource_metadata="` + strings.Repeat("a", 5000) + `"`,
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "URL too long",
			header:      `Bearer resource_metadata="https://resource.example.com/.well-known/oauth-protected-resource` + strings.Repeat("a", 2100) + `"`,
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Invalid URL - no scheme",
			header:      `Bearer resource_metadata="resource.example.com/.well-known/oauth-protected-resource"`,
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Invalid URL - no host",
			header:      `Bearer resource_metadata="https:///.well-known/oauth-protected-resource"`,
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Invalid URL - malformed",
			header:      `Bearer resource_metadata="https://[::1:80"`,
			expectedURL: "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualURL, err := parseWWWAuthenticateHeader(tt.header)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if actualURL != tt.expectedURL {
				t.Errorf("Expected URL %q, got %q", tt.expectedURL, actualURL)
			}
		})
	}
}
