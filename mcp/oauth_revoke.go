// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// revokeGrantBeforeDelete makes a best-effort RFC 7009 revocation of the
// stored grant at the authorization server. Any failure is logged and
// swallowed: the caller always proceeds to delete the local grant, so a user's
// "disconnect" never silently fails just because the provider is unreachable
// or advertises no revocation endpoint. Grants stored before the revocation
// endpoint was captured (or where the AS advertises none) are simply skipped.
func (m *OAuthManager) revokeGrantBeforeDelete(ctx context.Context, userID, serverID string) {
	envelope, _, err := m.loadTokenEnvelope(userID, serverID)
	if err != nil {
		m.pluginAPI.LogWarn("MCP OAuth: could not load grant for revocation on disconnect; deleting locally only",
			"serverID", serverID, "error", err)
		return
	}
	if envelope == nil || envelope.RevocationEndpoint == "" || envelope.Token == nil {
		return
	}

	creds, err := m.revocationCredentials(envelope, serverID)
	if err != nil {
		m.pluginAPI.LogWarn("MCP OAuth: skipping token revocation on disconnect; client credentials unavailable",
			"serverID", serverID, "error", err)
		return
	}

	if revokeErr := m.revokeGrant(ctx, envelope, creds); revokeErr != nil {
		m.pluginAPI.LogWarn("MCP OAuth: token revocation on disconnect failed; deleting local grant anyway",
			"serverID", serverID, "error", revokeErr)
	}
}

// revocationCredentials resolves the client credentials to authenticate a
// revocation request with, mirroring the refresh path: static credentials from
// live config when they match the grant's client, otherwise the dynamically
// registered credentials keyed by the grant's authorization server. When no
// stored registration remains it falls back to the client id pinned in the
// grant so a public-client revocation can still proceed.
func (m *OAuthManager) revocationCredentials(envelope *storedTokenEnvelope, serverID string) (*ClientCredentials, error) {
	if m.serverConfigLookup != nil {
		if cfg, ok := m.serverConfigLookup(serverID); ok {
			if sc := staticOAuthCreds(cfg); sc != nil && sc.ClientID != "" && sc.ClientID == envelope.ClientID {
				return &ClientCredentials{
					ClientID:                sc.ClientID,
					ClientSecret:            sc.ClientSecret,
					TokenEndpointAuthMethod: envelope.AuthMethod,
				}, nil
			}
		}
	}

	creds, err := m.loadClientCredentials(envelope.AuthServerURL)
	if err != nil {
		return nil, err
	}
	if creds == nil {
		if envelope.ClientID == "" {
			return nil, fmt.Errorf("no client credentials available for revocation")
		}
		return &ClientCredentials{
			ClientID:                envelope.ClientID,
			TokenEndpointAuthMethod: envelope.AuthMethod,
		}, nil
	}
	return creds, nil
}

// revokeGrant sends an RFC 7009 revocation request to the grant's pinned
// revocation endpoint. It revokes the refresh token when present (which SHOULD
// cascade to its access tokens per §2.1) and otherwise the access token. The
// request uses the grant's pinned client-authentication method and refuses
// redirects so the token is never replayed to another host.
func (m *OAuthManager) revokeGrant(ctx context.Context, envelope *storedTokenEnvelope, creds *ClientCredentials) (err error) {
	if envelope == nil || envelope.Token == nil || envelope.RevocationEndpoint == "" {
		return nil
	}
	// The endpoint comes from server-controlled metadata; validate its scheme
	// before sending a token to it (HTTPS, or HTTP only for loopback).
	if schemeErr := checkHTTPSOrLoopbackURL(envelope.RevocationEndpoint); schemeErr != nil {
		return fmt.Errorf("refusing to use revocation endpoint %q: %w", envelope.RevocationEndpoint, schemeErr)
	}

	tokenValue := envelope.Token.RefreshToken
	hint := "refresh_token"
	if tokenValue == "" {
		tokenValue = envelope.Token.AccessToken
		hint = "access_token"
	}
	if tokenValue == "" {
		return nil
	}

	ctx, span := telemetry.Tracer().Start(ctx, "mcp oauth token revocation",
		trace.WithAttributes(telemetry.MCPServer.String(envelope.Resource)),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	form := url.Values{
		"token":           {tokenValue},
		"token_type_hint": {hint},
	}
	// Pick the client-authentication method the same way refresh does: the
	// pinned method, or (for unknown-method static credentials) basic when a
	// secret is present, none otherwise.
	secret := ""
	if creds != nil {
		secret = creds.ClientSecret
	}
	method := refreshAuthMethods(envelope.AuthMethod, secret)[0]
	switch method {
	case authMethodNone, authMethodPost:
		if creds != nil {
			form.Set("client_id", creds.ClientID)
			if method == authMethodPost {
				form.Set("client_secret", creds.ClientSecret)
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, envelope.RevocationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create revocation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if method == authMethodBasic && creds != nil {
		req.SetBasicAuth(url.QueryEscape(creds.ClientID), url.QueryEscape(creds.ClientSecret))
	}

	resp, err := noRedirectClient(m.httpClient).Do(req)
	if err != nil {
		return fmt.Errorf("revocation request failed: %w", err)
	}
	defer resp.Body.Close()
	// Drain (bounded) so the connection can be reused; the body is unused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxOAuthResponseBytes))

	// RFC 7009 §2.2: the server returns 200 both on success and for a token it
	// does not recognize. Treat any 2xx as success and surface other statuses.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("revocation endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}
