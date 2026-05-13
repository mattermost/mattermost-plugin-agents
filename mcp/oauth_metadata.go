// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ProtectedResourceMetadata represents the OAuth 2.0 Protected Resource Metadata (RFC 9728)
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
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
func discoverProtectedResourceMetadata(ctx context.Context, httpClient *http.Client, baseURL, metadataURL string) (*ProtectedResourceMetadata, error) {
	if metadataURL == "" {
		// The metadata URL is not provided, use the default well-known endpoint
		// Construct according to RFC 9728 Section 3.1
		var err error
		metadataURL, err = constructWellKnownURL(baseURL, "oauth-protected-resource")
		if err != nil {
			return nil, fmt.Errorf("failed to construct metadata URL: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for protected resource metadata from %s: %w", metadataURL, err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch protected resource metadata from %s: %w", metadataURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch protected resource metadata from %s: HTTP %d: %s", metadataURL, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read protected resource metadata response from %s: %w", metadataURL, err)
	}

	var metadata ProtectedResourceMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse protected resource metadata JSON from %s: %w (body: %s)", metadataURL, err, string(body))
	}

	if len(metadata.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers found in protected resource metadata from %s", metadataURL)
	}

	return &metadata, nil
}

// discoverAuthorizationServerMetadata fetches the OAuth 2.0 Authorization Server Metadata (RFC 8414).
// It first tries the RFC 8414 well-known URL (well-known URI between host and path) and falls back
// to the path-appended form when the standard location returns 404. Some MCP servers (e.g.
// Rocketlane via Scalekit) only publish metadata at the path-appended location.
func discoverAuthorizationServerMetadata(ctx context.Context, httpClient *http.Client, authServerIssuer string) (*AuthorizationServerMetadata, error) {
	primaryURL, err := constructWellKnownURL(authServerIssuer, "oauth-authorization-server")
	if err != nil {
		return nil, fmt.Errorf("failed to construct metadata URL: %w", err)
	}

	candidateURLs := []string{primaryURL}
	if fallbackURL, fallbackErr := constructAppendedWellKnownURL(authServerIssuer, "oauth-authorization-server"); fallbackErr == nil && fallbackURL != primaryURL {
		candidateURLs = append(candidateURLs, fallbackURL)
	}

	var lastErr error
	for _, metadataURL := range candidateURLs {
		metadata, status, fetchErr := fetchAuthorizationServerMetadata(ctx, httpClient, metadataURL)
		if fetchErr == nil {
			return metadata, nil
		}
		lastErr = fetchErr
		// Only fall back when the metadata endpoint is genuinely missing.
		if status != http.StatusNotFound {
			return nil, fetchErr
		}
	}

	return nil, lastErr
}

// fetchAuthorizationServerMetadata performs a single GET against metadataURL and parses the
// response into an AuthorizationServerMetadata. It returns the HTTP status code so callers can
// distinguish 404 (try a fallback URL) from other errors (give up).
func fetchAuthorizationServerMetadata(ctx context.Context, httpClient *http.Client, metadataURL string) (*AuthorizationServerMetadata, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request for authorization server metadata from %s: %w", metadataURL, err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch authorization server metadata from %s: %w", metadataURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, resp.StatusCode, fmt.Errorf("failed to fetch authorization server metadata from %s: HTTP %d: %s", metadataURL, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read authorization server metadata response from %s: %w", metadataURL, err)
	}

	var metadata AuthorizationServerMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to parse authorization server metadata JSON from %s: %w (body: %s)", metadataURL, err, string(body))
	}

	// Validate required fields according to RFC 8414
	if metadata.Issuer == "" {
		return nil, resp.StatusCode, fmt.Errorf("missing required 'issuer' field in authorization server metadata from %s", metadataURL)
	}
	if metadata.AuthorizationEndpoint == "" {
		return nil, resp.StatusCode, fmt.Errorf("missing required 'authorization_endpoint' field in authorization server metadata from %s", metadataURL)
	}
	if metadata.TokenEndpoint == "" {
		return nil, resp.StatusCode, fmt.Errorf("missing required 'token_endpoint' field in authorization server metadata from %s", metadataURL)
	}

	// 2025-03-26 of mcp spec allows mismatches here.
	/*if metadata.Issuer != authServerIssuer {
		return nil, resp.StatusCode, fmt.Errorf("issuer mismatch: expected %s, got %s", authServerIssuer, metadata.Issuer)
	}*/

	return &metadata, resp.StatusCode, nil
}

// constructWellKnownURL constructs a well-known URL according to RFC 8414 Section 3.1
// It inserts the well-known URI suffix between the host and path components of the issuer URL.
// For example:
//   - Input: "https://example.com", suffix: "oauth-authorization-server"
//   - Output: "https://example.com/.well-known/oauth-authorization-server"
//   - Input: "https://example.com/issuer1", suffix: "oauth-authorization-server"
//   - Output: "https://example.com/.well-known/oauth-authorization-server/issuer1"
func constructWellKnownURL(issuer, suffix string) (string, error) {
	parsedURL, err := url.Parse(issuer)
	if err != nil {
		return "", fmt.Errorf("failed to parse issuer URL: %w", err)
	}

	// Remove any trailing slash from the path
	path := parsedURL.Path
	if path != "" && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}

	// Construct the well-known URL by inserting between host and path
	// Format: scheme://host/.well-known/suffix/path
	wellKnownURL := fmt.Sprintf("%s://%s/.well-known/%s%s", parsedURL.Scheme, parsedURL.Host, suffix, path)

	return wellKnownURL, nil
}

// constructAppendedWellKnownURL appends /.well-known/<suffix> after the issuer's
// path component. This is a non-standard discovery pattern (deviating from RFC
// 8414 Section 3.1) used by some MCP servers — for example Rocketlane via
// Scalekit — which publish OAuth metadata under the resource path rather than at
// the host root. Examples:
//   - Input: "https://example.com", suffix: "oauth-authorization-server"
//   - Output: "https://example.com/.well-known/oauth-authorization-server"
//   - Input: "https://example.com/resources/abc", suffix: "oauth-authorization-server"
//   - Output: "https://example.com/resources/abc/.well-known/oauth-authorization-server"
func constructAppendedWellKnownURL(issuer, suffix string) (string, error) {
	parsedURL, err := url.Parse(issuer)
	if err != nil {
		return "", fmt.Errorf("failed to parse issuer URL: %w", err)
	}

	path := parsedURL.Path
	if path != "" && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}

	wellKnownURL := fmt.Sprintf("%s://%s%s/.well-known/%s", parsedURL.Scheme, parsedURL.Host, path, suffix)

	return wellKnownURL, nil
}
