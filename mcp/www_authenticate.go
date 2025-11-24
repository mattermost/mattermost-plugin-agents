// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// parseWWWAuthenticateHeader parses the WWW-Authenticate header to extract resource_metadata URL
// According to RFC 9728, using the go-sdk implementation
func parseWWWAuthenticateHeader(header string) (string, error) {
	if header == "" {
		return "", fmt.Errorf("empty WWW-Authenticate header")
	}

	// Create a http.Header and add the WWW-Authenticate header
	h := http.Header{}
	h.Add("WWW-Authenticate", header)

	// Parse using go-sdk
	challenges, err := oauthex.ParseWWWAuthenticate([]string{header})
	if err != nil {
		return "", fmt.Errorf("failed to parse WWW-Authenticate header: %w", err)
	}

	// Extract resource_metadata URL from challenges
	metadataURL := oauthex.ResourceMetadataURL(challenges)
	if metadataURL == "" {
		return "", fmt.Errorf("resource_metadata not found in WWW-Authenticate header")
	}

	return metadataURL, nil
}
