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
// Metadata (RFC 9728) via oauthex, which enforces the spec strictly —
// including the §3.3 requirement that the metadata's "resource" value equal
// the resource identifier the client used.
//
// When no explicit metadata URL is known (i.e. it did not come from a 401
// challenge), the MCP specification's ordered discovery sequence is used:
// first the path-inserted well-known URL (expecting the full server URL as
// the resource), then the root well-known URL (expecting the server's origin
// as the resource), mirroring the go-sdk's own client discovery. The first
// candidate to succeed wins; if all fail, the first candidate's error is
// returned since it is the primary variant.
func (m *OAuthManager) fetchProtectedResourceMetadata(ctx context.Context, serverURL, metadataURL string) (*oauthex.ProtectedResourceMetadata, error) {
	type prmCandidate struct {
		metadataURL string
		resource    string
	}

	var candidates []prmCandidate
	if metadataURL != "" {
		candidates = []prmCandidate{{metadataURL: metadataURL, resource: serverURL}}
	} else {
		pathInserted, err := constructWellKnownURL(serverURL, "oauth-protected-resource")
		if err != nil {
			return nil, fmt.Errorf("failed to construct metadata URL: %w", err)
		}
		candidates = []prmCandidate{{metadataURL: pathInserted, resource: serverURL}}

		if parsed, parseErr := url.Parse(serverURL); parseErr == nil && strings.Trim(parsed.Path, "/") != "" {
			origin := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host}
			candidates = append(candidates, prmCandidate{
				metadataURL: origin.String() + "/.well-known/oauth-protected-resource",
				resource:    origin.String(),
			})
		}
	}

	var firstErr error
	for _, candidate := range candidates {
		prm, err := oauthex.GetProtectedResourceMetadata(ctx, candidate.metadataURL, candidate.resource, m.httpClient)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to fetch protected resource metadata from %s: %w", candidate.metadataURL, err)
			}
			continue
		}
		if len(prm.AuthorizationServers) == 0 {
			if firstErr == nil {
				firstErr = fmt.Errorf("no authorization servers found in protected resource metadata from %s", candidate.metadataURL)
			}
			continue
		}
		return prm, nil
	}

	return nil, firstErr
}

// fetchAuthorizationServerMetadata fetches OAuth 2.0 Authorization Server
// Metadata (RFC 8414) via oauthex, adopting its strict validation (issuer
// equality, HTTPS-or-loopback URLs) with a single deliberate relaxation:
// oauthex requires a non-empty code_challenge_methods_supported, but the most
// common real-world non-compliance is servers that support PKCE without
// advertising it. Since we always use PKCE regardless of what the server
// advertises, that specific failure triggers a lenient re-fetch (with a
// warning) instead of failing discovery. oauthex flattens error types, so the
// error message is the only stable signal for detecting it.
func (m *OAuthManager) fetchAuthorizationServerMetadata(ctx context.Context, issuer string) (*oauthex.AuthServerMeta, error) {
	urls := authServerMetadataURLs(issuer)
	if len(urls) == 0 {
		return nil, fmt.Errorf("failed to construct metadata URLs for issuer %s", issuer)
	}

	for _, metadataURL := range urls {
		asm, err := oauthex.GetAuthServerMeta(ctx, metadataURL, issuer, m.httpClient)
		if err == nil && asm == nil {
			// oauthex returns (nil, nil) — no error — when the fetch got a
			// 4xx status: this variant is not served, try the next one.
			continue
		}
		if err != nil {
			if !strings.Contains(err.Error(), "does not implement PKCE") {
				// Hard failures (unreachable, issuer mismatch, insecure URLs,
				// bad JSON) abort the walk, mirroring the SDK's own client.
				return nil, fmt.Errorf("failed to fetch authorization server metadata from %s: %w", metadataURL, err)
			}
			m.pluginAPI.LogWarn("Authorization server metadata does not advertise PKCE support; retrying leniently (we always use PKCE regardless)",
				"metadataURL", metadataURL,
				"issuer", issuer,
				"error", err)
			asm, err = fetchJSONLenient[oauthex.AuthServerMeta](ctx, m.httpClient, metadataURL)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch authorization server metadata from %s: %w", metadataURL, err)
			}
			if err := validateLenientAuthServerMeta(asm, metadataURL, issuer); err != nil {
				return nil, err
			}
		}
		return asm, nil
	}

	return nil, fmt.Errorf("no authorization server metadata found for issuer %s (tried %d well-known variants)", issuer, len(urls))
}

// authServerMetadataURLs returns the ordered discovery URLs for authorization
// server metadata mandated by the MCP specification, mirroring the go-sdk's
// own client: for issuers without a path component, RFC 8414 then OpenID
// Connect Discovery at the root; for issuers with a path component, the
// path-inserted RFC 8414 and OIDC variants followed by the path-appended OIDC
// variant.
func authServerMetadataURLs(issuer string) []string {
	baseURL, err := url.Parse(issuer)
	if err != nil {
		return nil
	}

	var urls []string
	if strings.Trim(baseURL.Path, "/") == "" {
		// "OAuth 2.0 Authorization Server Metadata".
		baseURL.Path = "/.well-known/oauth-authorization-server"
		urls = append(urls, baseURL.String())
		// "OpenID Connect Discovery 1.0".
		baseURL.Path = "/.well-known/openid-configuration"
		urls = append(urls, baseURL.String())
		return urls
	}

	originalPath := baseURL.Path
	// "OAuth 2.0 Authorization Server Metadata with path insertion".
	baseURL.Path = "/.well-known/oauth-authorization-server/" + strings.TrimLeft(originalPath, "/")
	urls = append(urls, baseURL.String())
	// "OpenID Connect Discovery 1.0 with path insertion".
	baseURL.Path = "/.well-known/openid-configuration/" + strings.TrimLeft(originalPath, "/")
	urls = append(urls, baseURL.String())
	// "OpenID Connect Discovery 1.0 with path appending".
	baseURL.Path = "/" + strings.Trim(originalPath, "/") + "/.well-known/openid-configuration"
	urls = append(urls, baseURL.String())

	return urls
}

// validateLenientAuthServerMeta applies the validations the PKCE-lenient
// re-fetch still needs: RFC 8414 required fields, issuer equality against the
// expected issuer (the strict attempt verified the FIRST response's issuer,
// but the lenient path is a second fetch and the server could answer
// differently), and HTTPS-or-loopback on the endpoints we will actually
// contact (oauthex validates endpoint URLs only after its PKCE check, so a
// PKCE failure means they were never validated).
func validateLenientAuthServerMeta(asm *oauthex.AuthServerMeta, metadataURL, expectedIssuer string) error {
	if asm.Issuer == "" {
		return fmt.Errorf("missing required 'issuer' field in authorization server metadata from %s", metadataURL)
	}
	// Trailing-slash-insensitive, mirroring the SDK's IssuersEqual.
	if strings.TrimSuffix(asm.Issuer, "/") != strings.TrimSuffix(expectedIssuer, "/") {
		return fmt.Errorf("metadata issuer %q from %s does not match expected issuer %q", asm.Issuer, metadataURL, expectedIssuer)
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
// issuer/PKCE checks). It is used by the PKCE-advertisement fallback in
// fetchAuthorizationServerMetadata and by discoverRegistrationEndpoint;
// callers are responsible for any validation the lenient path still needs.
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

// checkHTTPSOrLoopbackURL enforces HTTPS, or plain HTTP only for loopback
// addresses (testing and development). Note this is deliberately stricter
// than oauthex's equivalent, which exempts loopback hosts from the scheme
// check entirely: URLs with non-HTTP schemes (ftp:, javascript:, ...) are
// rejected even on loopback, since these endpoints end up in authorization
// URLs handed to browsers.
func checkHTTPSOrLoopbackURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("URL %q does not use HTTPS or is not an HTTP loopback address", rawURL)
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
