// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// fetchProtectedResourceMetadata fetches OAuth 2.0 Protected Resource
// Metadata (RFC 9728) via oauthex, with a lenient fallback for the resource
// identifier check.
//
// oauthex.GetProtectedResourceMetadata enforces RFC 9728 §3.3 strictly: the
// metadata's "resource" value must equal the resource identifier the client
// used, byte for byte. Real-world MCP servers frequently violate this (the
// advertised resource differs by a trailing slash or a path component), so on
// that specific failure we log a warning and re-fetch the document leniently,
// validating the authorization server URLs ourselves.
func (m *OAuthManager) fetchProtectedResourceMetadata(ctx context.Context, serverURL, metadataURL string) (*oauthex.ProtectedResourceMetadata, error) {
	if metadataURL == "" {
		// The metadata URL is not provided, use the default well-known
		// endpoint constructed according to RFC 9728 Section 3.1.
		var err error
		metadataURL, err = constructWellKnownURL(serverURL, "oauth-protected-resource")
		if err != nil {
			return nil, fmt.Errorf("failed to construct metadata URL: %w", err)
		}
	}

	prm, err := oauthex.GetProtectedResourceMetadata(ctx, metadataURL, serverURL, m.httpClient)
	if err != nil {
		// Match the resource-mismatch error only; every other failure
		// (unreachable, non-200, bad JSON, insecure URLs) stays fatal so the
		// caller can fall back to authorization server metadata discovery.
		if !strings.Contains(err.Error(), "got metadata resource") {
			return nil, fmt.Errorf("failed to fetch protected resource metadata from %s: %w", metadataURL, err)
		}
		m.pluginAPI.LogWarn("Protected resource metadata resource does not match the MCP server URL; retrying leniently (RFC 9728 Section 3.3 violation by the server)",
			"metadataURL", metadataURL,
			"serverURL", serverURL,
			"error", err)
		prm, err = fetchJSONLenient[oauthex.ProtectedResourceMetadata](ctx, m.httpClient, metadataURL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch protected resource metadata from %s: %w", metadataURL, err)
		}
		for _, authServer := range prm.AuthorizationServers {
			if urlErr := checkHTTPSOrLoopbackURL(authServer); urlErr != nil {
				return nil, fmt.Errorf("invalid authorization server URL in protected resource metadata from %s: %w", metadataURL, urlErr)
			}
		}
	}

	if len(prm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers found in protected resource metadata from %s", metadataURL)
	}

	return prm, nil
}

// fetchAuthorizationServerMetadata fetches OAuth 2.0 Authorization Server
// Metadata (RFC 8414) via oauthex, with a lenient fallback for two strictness
// checks legacy MCP servers commonly fail:
//
//   - Issuer equality: oauthex enforces that the metadata's issuer matches the
//     URL it was derived from. The 2025-03-26 MCP spec era tolerated
//     mismatches, and legacy servers still serve mismatched issuers.
//   - PKCE advertisement: oauthex requires a non-empty
//     code_challenge_methods_supported. The MCP spec mandates PKCE but older
//     servers don't advertise it. We always use PKCE regardless of what the
//     server advertises, so this is safe to relax.
//
// Note oauthex also requires the well-known metadata URL itself to be HTTPS or
// loopback; we keep that strictness (a plain-HTTP non-loopback OAuth server
// was never legitimate).
func (m *OAuthManager) fetchAuthorizationServerMetadata(ctx context.Context, issuer string) (*oauthex.AuthServerMeta, error) {
	// Construct the well-known metadata URL according to RFC 8414 Section 3.1:
	// the well-known component is inserted between host and path.
	metadataURL, err := constructWellKnownURL(issuer, "oauth-authorization-server")
	if err != nil {
		return nil, fmt.Errorf("failed to construct metadata URL: %w", err)
	}

	asm, err := oauthex.GetAuthServerMeta(ctx, metadataURL, issuer, m.httpClient)
	if err == nil && asm == nil {
		// oauthex returns (nil, nil) — no error — when the fetch got a 4xx
		// status. Treat it as discovery failure so callers fall back.
		return nil, fmt.Errorf("failed to fetch authorization server metadata from %s: not found", metadataURL)
	}
	if err != nil {
		if !isLenientRecoverableAuthServerMetaErr(err) {
			return nil, fmt.Errorf("failed to fetch authorization server metadata from %s: %w", metadataURL, err)
		}
		m.pluginAPI.LogWarn("Authorization server metadata failed strict validation; retrying leniently (legacy MCP servers may serve mismatched issuers or omit PKCE advertisement)",
			"metadataURL", metadataURL,
			"issuer", issuer,
			"error", err)
		asm, err = fetchJSONLenient[oauthex.AuthServerMeta](ctx, m.httpClient, metadataURL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch authorization server metadata from %s: %w", metadataURL, err)
		}
		if err := validateLenientAuthServerMeta(asm, metadataURL); err != nil {
			return nil, err
		}
	}

	return asm, nil
}

// isLenientRecoverableAuthServerMetaErr reports whether a strict
// oauthex.GetAuthServerMeta failure is one of the two deliberate strictness
// relaxations (issuer mismatch, missing PKCE advertisement). oauthex flattens
// error types, so the messages are the only stable signal.
func isLenientRecoverableAuthServerMetaErr(err error) bool {
	return strings.Contains(err.Error(), "does not match issuer URL") ||
		strings.Contains(err.Error(), "does not implement PKCE")
}

// validateLenientAuthServerMeta applies the validations the lenient fetch path
// still needs: RFC 8414 required fields, and HTTPS-or-loopback on the
// endpoints we will actually contact (mirroring oauthex's own endpoint
// validation).
func validateLenientAuthServerMeta(asm *oauthex.AuthServerMeta, metadataURL string) error {
	if asm.Issuer == "" {
		return fmt.Errorf("missing required 'issuer' field in authorization server metadata from %s", metadataURL)
	}
	if asm.AuthorizationEndpoint == "" {
		return fmt.Errorf("missing required 'authorization_endpoint' field in authorization server metadata from %s", metadataURL)
	}
	if asm.TokenEndpoint == "" {
		return fmt.Errorf("missing required 'token_endpoint' field in authorization server metadata from %s", metadataURL)
	}

	for _, endpoint := range []string{asm.AuthorizationEndpoint, asm.TokenEndpoint, asm.RegistrationEndpoint} {
		if err := checkHTTPSOrLoopbackURL(endpoint); err != nil {
			return fmt.Errorf("invalid endpoint URL in authorization server metadata from %s: %w", metadataURL, err)
		}
	}

	return nil
}

// discoverRegistrationEndpoint discovers the RFC 7591 registration endpoint
// from the authorization server metadata at the server's base URL (path
// stripped per the MCP spec). It is used when createOAuthConfig's discovery
// did not produce a registration endpoint. The fetch is deliberately lenient
// (no issuer/PKCE checks), matching the previous hand-rolled behavior.
func (m *OAuthManager) discoverRegistrationEndpoint(ctx context.Context, serverURL string) (string, error) {
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse server URL %s: %w", serverURL, err)
	}
	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	metadataURL, err := constructWellKnownURL(baseURL, "oauth-authorization-server")
	if err != nil {
		return "", fmt.Errorf("failed to construct metadata URL from server URL %s: %w", serverURL, err)
	}

	metadata, err := fetchJSONLenient[oauthex.AuthServerMeta](ctx, m.httpClient, metadataURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch server metadata from %s: %w", metadataURL, err)
	}

	if metadata.RegistrationEndpoint == "" {
		return "", fmt.Errorf("server %s does not support dynamic client registration (no registration_endpoint in metadata from %s)", serverURL, metadataURL)
	}
	if err := checkHTTPSOrLoopbackURL(metadata.RegistrationEndpoint); err != nil {
		return "", fmt.Errorf("invalid registration endpoint in metadata from %s: %w", metadataURL, err)
	}

	return metadata.RegistrationEndpoint, nil
}

// maxMetadataBytes limits how much of a metadata document we read, matching
// the limit oauthex applies on its strict path.
const maxMetadataBytes = 1 << 20

// fetchJSONLenient fetches and decodes an OAuth metadata document without the
// strict RFC validations oauthex applies (no Content-Type requirement, no
// issuer/resource/PKCE checks). It is the shared fallback for both protected
// resource metadata and authorization server metadata when a real-world
// server fails oauthex's strict validation; callers are responsible for any
// validation the lenient path still needs.
func fetchJSONLenient[T any](ctx context.Context, httpClient *http.Client, metadataURL string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", metadataURL, err)
	}
	req.Header.Set("Accept", "application/json")

	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", metadataURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes))
		return nil, fmt.Errorf("failed to fetch %s: HTTP %d: %s", metadataURL, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read response from %s: %w", metadataURL, err)
	}

	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("failed to parse JSON from %s: %w", metadataURL, err)
	}

	return &value, nil
}

// checkHTTPSOrLoopbackURL enforces the same constraint oauthex applies to
// endpoint URLs on its strict path: HTTPS, or plain HTTP only for loopback
// addresses (testing and development).
func checkHTTPSOrLoopbackURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("URL %q does not use HTTPS or is not a loopback address", rawURL)
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
