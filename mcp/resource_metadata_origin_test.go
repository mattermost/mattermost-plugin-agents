// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateResourceMetadataMatchesServerBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		serverBaseURL string
		metadataURL   string
		wantErr       bool
	}{
		{
			name:          "same origin https default port implicit and explicit",
			serverBaseURL: "https://example.com/mcp",
			metadataURL:   "https://example.com:443/.well-known/oauth-protected-resource",
			wantErr:       false,
		},
		{
			name:          "same host case insensitive",
			serverBaseURL: "https://EXAMPLE.COM/api",
			metadataURL:   "https://example.com/.well-known/oauth-protected-resource",
			wantErr:       false,
		},
		{
			name:          "metadata path only differs",
			serverBaseURL: "https://resource.example/v1/mcp",
			metadataURL:   "https://resource.example/foo/.well-known/oauth-protected-resource/bar",
			wantErr:       false,
		},
		{
			name:          "http default port 80 normalized",
			serverBaseURL: "http://127.0.0.1:8080/mcp",
			metadataURL:   "http://127.0.0.1:8080/.well-known/oauth-protected-resource",
			wantErr:       false,
		},
		{
			name:          "http explicit 80 matches implicit",
			serverBaseURL: "http://example.com",
			metadataURL:   "http://example.com:80/path",
			wantErr:       false,
		},
		{
			name:          "different host",
			serverBaseURL: "https://trusted.example/mcp",
			metadataURL:   "https://evil.example/.well-known/oauth-protected-resource",
			wantErr:       true,
		},
		{
			name:          "subdomain does not match parent",
			serverBaseURL: "https://api.example.com",
			metadataURL:   "https://example.com/.well-known/oauth-protected-resource",
			wantErr:       true,
		},
		{
			name:          "http vs https",
			serverBaseURL: "https://example.com",
			metadataURL:   "http://example.com/.well-known/oauth-protected-resource",
			wantErr:       true,
		},
		{
			name:          "different non default port",
			serverBaseURL: "https://example.com:8443/mcp",
			metadataURL:   "https://example.com:9443/.well-known/oauth-protected-resource",
			wantErr:       true,
		},
		{
			name:          "userinfo in metadata rejected",
			serverBaseURL: "https://example.com",
			metadataURL:   "https://user@example.com/.well-known/oauth-protected-resource",
			wantErr:       true,
		},
		{
			name:          "invalid server base missing scheme",
			serverBaseURL: "example.com/mcp",
			metadataURL:   "https://example.com/.well-known/oauth-protected-resource",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateResourceMetadataMatchesServerBaseURL(tt.serverBaseURL, tt.metadataURL)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOriginComparableKeyIPv6(t *testing.T) {
	t.Parallel()
	u, err := url.Parse("http://[::1]:8080/mcp")
	require.NoError(t, err)
	require.Equal(t, "http://[::1]:8080", originComparableKey(u))

	u2, err := url.Parse("http://[::1]:80/foo")
	require.NoError(t, err)
	require.Equal(t, "http://[::1]", originComparableKey(u2))
}
