// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package mcp implements Dynamic Client Registration Protocol (RFC 7591)
package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// RegistrationRequest is an alias to the go-sdk type
type RegistrationRequest = oauthex.ClientRegistrationMetadata

// RegistrationResponse is an alias to the go-sdk type
type RegistrationResponse = oauthex.ClientRegistrationResponse

// RegistrationError is an alias to the go-sdk type
type RegistrationError = oauthex.ClientRegistrationError

// RegisterClient performs dynamic client registration per RFC 7591
// using the go-sdk implementation
func RegisterClient(ctx context.Context, httpClient *http.Client, registrationEndpoint string, request *RegistrationRequest, initialAccessToken string) (*RegistrationResponse, error) {
	// Note: The go-sdk RegisterClient doesn't support initial access tokens in the current signature
	// If we need that, we'd need to fork or extend the go-sdk implementation
	response, err := oauthex.RegisterClient(ctx, registrationEndpoint, request, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to register client: %w", err)
	}

	return response, nil
}

// DefaultRegistrationRequest creates a default registration request for MCP clients
func DefaultRegistrationRequest(redirectURI, clientName string) *RegistrationRequest {
	return &RegistrationRequest{
		RedirectURIs:            []string{redirectURI},
		TokenEndpointAuthMethod: "client_secret_basic",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              clientName,
		Scope:                   "",
	}
}

// DiscoverAndRegisterClient performs the complete client registration flow:
// 1. Discovers the registration endpoint from server metadata
// 2. Creates a default registration request
// 3. Registers the client with the server
func DiscoverAndRegisterClient(ctx context.Context, httpClient *http.Client, serverURL, callbackURL, clientID, initialAccessToken string) (*RegistrationResponse, error) {
	// Discover registration endpoint
	registrationEndpoint, err := GetRegistrationEndpoint(ctx, httpClient, serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to discover registration endpoint for server %s: %w", serverURL, err)
	}

	// Create registration request
	request := DefaultRegistrationRequest(callbackURL, clientID)

	// Perform registration
	response, err := RegisterClient(ctx, httpClient, registrationEndpoint, request, initialAccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to register OAuth client with server %s (registration endpoint: %s): %w", serverURL, registrationEndpoint, err)
	}

	return response, nil
}

// GetRegistrationEndpoint discovers the registration endpoint from server metadata
func GetRegistrationEndpoint(ctx context.Context, httpClient *http.Client, serverURL string) (string, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// Use go-sdk to get auth server metadata which includes the registration endpoint
	metadata, err := oauthex.GetAuthServerMeta(ctx, serverURL, httpClient)
	if err != nil {
		return "", fmt.Errorf("failed to fetch server metadata from %s: %w", serverURL, err)
	}

	if metadata.RegistrationEndpoint == "" {
		return "", fmt.Errorf("server %s does not support dynamic client registration (no registration_endpoint in metadata)", serverURL)
	}

	return metadata.RegistrationEndpoint, nil
}
