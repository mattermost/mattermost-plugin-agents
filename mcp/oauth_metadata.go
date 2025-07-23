// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ProtectedResourceMetadata represents the OAuth 2.0 Protected Resource Metadata (RFC 9728)
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

// AuthorizationServerMetadata represents the OAuth 2.0 Authorization Server Metadata (RFC 8414)
type AuthorizationServerMetadata struct {
	Issuer                 string   `json:"issuer"`
	AuthorizationEndpoint  string   `json:"authorization_endpoint"`
	TokenEndpoint          string   `json:"token_endpoint"`
	ResponseTypesSupported []string `json:"response_types_supported"`
	GrantTypesSupported    []string `json:"grant_types_supported,omitempty"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	RegistrationEndpoint   string   `json:"registration_endpoint,omitempty"`
}

// discoverProtectedResourceMetadata fetches the OAuth 2.0 Protected Resource Metadata (RFC 9728)
func discoverProtectedResourceMetadata(ctx context.Context, baseURL, metadataURL string) (*ProtectedResourceMetadata, error) {
	if metadataURL == "" {
		// The metadata URL is not provided, use the default well-known endpoint
		metadataURL = baseURL + "/.well-known/oauth-protected-resource"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for protected resource metadata: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch protected resource metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch protected resource metadata: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read protected resource metadata response: %w", err)
	}

	var metadata ProtectedResourceMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse protected resource metadata JSON: %w", err)
	}

	if len(metadata.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers found in protected resource metadata")
	}

	return &metadata, nil
}

// discoverAuthorizationServerMetadata fetches the OAuth 2.0 Authorization Server Metadata (RFC 8414)
func discoverAuthorizationServerMetadata(ctx context.Context, authServerIssuer string) (*AuthorizationServerMetadata, error) {
	// Construct the well-known metadata URL according to RFC 8414
	metadataURL := strings.TrimSuffix(authServerIssuer, "/") + "/.well-known/oauth-authorization-server"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for authorization server metadata: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch authorization server metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch authorization server metadata: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read authorization server metadata response: %w", err)
	}

	var metadata AuthorizationServerMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse authorization server metadata JSON: %w", err)
	}

	// Validate required fields according to RFC 8414
	if metadata.Issuer == "" {
		return nil, fmt.Errorf("missing required 'issuer' field in authorization server metadata")
	}
	if metadata.AuthorizationEndpoint == "" {
		return nil, fmt.Errorf("missing required 'authorization_endpoint' field in authorization server metadata")
	}
	if metadata.TokenEndpoint == "" {
		return nil, fmt.Errorf("missing required 'token_endpoint' field in authorization server metadata")
	}

	// Validate that the issuer matches the expected value
	// 2025-03-26 of mcp spec allows mismatches here.
	/*if metadata.Issuer != authServerIssuer {
		return nil, fmt.Errorf("issuer mismatch: expected %s, got %s", authServerIssuer, metadata.Issuer)
	}*/

	return &metadata, nil
}
