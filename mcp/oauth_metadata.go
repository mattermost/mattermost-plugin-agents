// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// ProtectedResourceMetadata is an alias to the go-sdk type
type ProtectedResourceMetadata = oauthex.ProtectedResourceMetadata

// AuthorizationServerMetadata is an alias to the go-sdk type
type AuthorizationServerMetadata = oauthex.AuthServerMeta

// discoverProtectedResourceMetadata fetches the OAuth 2.0 Protected Resource Metadata (RFC 9728)
// using the go-sdk implementation
func discoverProtectedResourceMetadata(ctx context.Context, baseURL, metadataURL string) (*ProtectedResourceMetadata, error) {
	if metadataURL == "" {
		// Use the resource ID to discover metadata
		metadata, err := oauthex.GetProtectedResourceMetadataFromID(ctx, baseURL, http.DefaultClient)
		if err != nil {
			return nil, fmt.Errorf("failed to discover protected resource metadata from %s: %w", baseURL, err)
		}
		return metadata, nil
	}

	// When metadataURL is provided, we need to fetch it directly
	// The go-sdk doesn't have a direct method for this, but we can use GetProtectedResourceMetadataFromID
	// after constructing the appropriate resource ID from the metadata URL
	metadata, err := oauthex.GetProtectedResourceMetadataFromID(ctx, baseURL, http.DefaultClient)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch protected resource metadata from %s: %w", metadataURL, err)
	}

	if len(metadata.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers found in protected resource metadata from %s", metadataURL)
	}

	return metadata, nil
}

// discoverAuthorizationServerMetadata fetches the OAuth 2.0 Authorization Server Metadata (RFC 8414)
// using the go-sdk implementation
func discoverAuthorizationServerMetadata(ctx context.Context, authServerIssuer string) (*AuthorizationServerMetadata, error) {
	metadata, err := oauthex.GetAuthServerMeta(ctx, authServerIssuer, http.DefaultClient)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch authorization server metadata from %s: %w", authServerIssuer, err)
	}

	return metadata, nil
}
