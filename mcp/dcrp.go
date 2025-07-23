// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package mcp implements Dynamic Client Registration Protocol (RFC 7591)
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// RegistrationRequest represents a client registration request per RFC 7591
type RegistrationRequest struct {
	// Required fields
	RedirectURIs []string `json:"redirect_uris"`

	// Optional fields commonly used
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	ClientName              string   `json:"client_name,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	Contacts                []string `json:"contacts,omitempty"`

	// Additional optional fields can be added as needed
	ClientURI string `json:"client_uri,omitempty"`
	LogoURI   string `json:"logo_uri,omitempty"`
	ToSURI    string `json:"tos_uri,omitempty"`
	PolicyURI string `json:"policy_uri,omitempty"`
}

// RegistrationResponse represents the server's response per RFC 7591
type RegistrationResponse struct {
	// Required fields
	ClientID string `json:"client_id"`

	// Optional fields
	ClientSecret          string `json:"client_secret,omitempty"`
	ClientIDIssuedAt      *int64 `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt *int64 `json:"client_secret_expires_at,omitempty"`

	// Echo back the registration metadata
	RedirectURIs            []string `json:"redirect_uris,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	ClientName              string   `json:"client_name,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	Contacts                []string `json:"contacts,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	LogoURI                 string   `json:"logo_uri,omitempty"`
	ToSURI                  string   `json:"tos_uri,omitempty"`
	PolicyURI               string   `json:"policy_uri,omitempty"`
}

// RegistrationError represents an error response per RFC 7591
type RegistrationError struct {
	ErrorCode        string         `json:"error"`
	ErrorDescription string         `json:"error_description,omitempty"`
	HTTPStatusCode   int            `json:"-"`
	HTTPResponse     *http.Response `json:"-"`
}

func (e *RegistrationError) Error() string {
	if e.ErrorDescription != "" {
		return fmt.Sprintf("registration error (%s): %s", e.ErrorCode, e.ErrorDescription)
	}
	return fmt.Sprintf("registration error: %s", e.ErrorCode)
}

// RegisterClient performs dynamic client registration per RFC 7591
func RegisterClient(ctx context.Context, httpClient *http.Client, registrationEndpoint string, request *RegistrationRequest, initialAccessToken string) (*RegistrationResponse, error) {
	// Validate registration endpoint URL
	if _, err := url.Parse(registrationEndpoint); err != nil {
		return nil, fmt.Errorf("invalid registration endpoint URL: %w", err)
	}

	// Validate required fields per RFC 7591
	if len(request.RedirectURIs) == 0 {
		return nil, fmt.Errorf("redirect_uris is required")
	}

	// Validate redirect URIs
	for _, uri := range request.RedirectURIs {
		if _, err := url.Parse(uri); err != nil {
			return nil, fmt.Errorf("invalid redirect_uri %s: %w", uri, err)
		}
	}

	// Marshal request to JSON
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal registration request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", registrationEndpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set required headers per RFC 7591
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Add initial access token if provided
	if initialAccessToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+initialAccessToken)
	}

	// Make the request
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make registration request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for success status (RFC 7591 requires 201 Created)
	if resp.StatusCode == http.StatusCreated {
		var registrationResp RegistrationResponse
		if err := json.Unmarshal(responseBody, &registrationResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal registration response: %w", err)
		}

		// Validate required response fields per RFC 7591
		if registrationResp.ClientID == "" {
			return nil, fmt.Errorf("server response missing required client_id")
		}

		return &registrationResp, nil
	}

	// Handle error response
	var regError RegistrationError
	regError.HTTPStatusCode = resp.StatusCode
	regError.HTTPResponse = resp

	// Try to parse error response per RFC 7591
	if resp.Header.Get("Content-Type") == "application/json" {
		if err := json.Unmarshal(responseBody, &regError); err != nil {
			// If we can't parse the error, create a generic one
			regError.ErrorCode = "unknown_error"
			regError.ErrorDescription = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(responseBody))
		}
	} else {
		regError.ErrorCode = "unknown_error"
		regError.ErrorDescription = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(responseBody))
	}

	return nil, &regError
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
		return nil, fmt.Errorf("failed to discover registration endpoint: %w", err)
	}

	// Create registration request
	request := DefaultRegistrationRequest(callbackURL, clientID)

	// Perform registration
	response, err := RegisterClient(ctx, httpClient, registrationEndpoint, request, initialAccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to register OAuth client: %w", err)
	}

	return response, nil
}

// GetRegistrationEndpoint discovers the registration endpoint from server metadata
func GetRegistrationEndpoint(ctx context.Context, httpClient *http.Client, serverURL string) (string, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// Try standard OAuth 2.0 Authorization Server Metadata endpoint first
	metadataURL := serverURL + "/.well-known/oauth-authorization-server"

	req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create metadata request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch server metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server metadata request failed with status %d", resp.StatusCode)
	}

	var metadata struct {
		RegistrationEndpoint string `json:"registration_endpoint"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return "", fmt.Errorf("failed to decode server metadata: %w", err)
	}

	if metadata.RegistrationEndpoint == "" {
		return "", fmt.Errorf("server does not support dynamic client registration")
	}

	return metadata.RegistrationEndpoint, nil
}
