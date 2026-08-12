// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// This file hand-implements the OAuth refresh_token grant instead of using
// golang.org/x/oauth2's built-in refresh (oauth2.Config.TokenSource) or the
// go-sdk's auth package. That is a deliberate tradeoff:
//
// Why the libraries cannot be used here:
//   - RFC 8707 resource parameter: the MCP spec requires the canonical
//     resource on EVERY token request, but x/oauth2's internal tokenRefresher
//     offers no way to add form parameters to a refresh (SetAuthURLParam only
//     applies to AuthCodeURL/Exchange). Strict servers reject resource-less
//     refreshes; permissive multi-resource servers may issue replayable
//     default-audience tokens. The go-sdk's own refresh is just
//     oauth2.Config.TokenSource, so it inherits the same gap (and oauthex has
//     no refresh helper at all).
//   - Pinned client authentication: the grant envelope pins
//     token_endpoint_auth_method so every HA node refreshes with the method
//     that actually works. x/oauth2 auto-detects basic-vs-post and caches the
//     result per endpoint in process memory only, with no way to read or
//     persist which style succeeded.
//   - Typed error classification: invalid_grant (dead token → clear grant,
//     re-authenticate) must be distinguished from client-auth failures
//     (→ retry with the other auth method) via the parsed RFC 6749 §5.2
//     error field, and each refresh must run under its own bounded context
//     (x/oauth2 token sources capture their context for all future
//     refreshes).
//
// The cost we accept: we own security-sensitive wire code and do not inherit
// x/oauth2's accumulated tolerance for provider quirks — e.g. quoted
// "expires_in" strings, which x/oauth2 accepts and our first version
// rejected (now handled by flexibleNumber). Changes here should cross-check
// x/oauth2's internal/token.go for provider-compatibility behavior we may be
// missing.
//
// When this file can be removed: if the go-sdk (or x/oauth2) grows a refresh
// API that (a) sends the RFC 8707 resource parameter, (b) lets the caller
// select and observe the client authentication method, and (c) surfaces
// typed token-endpoint errors, this implementation can be replaced with it,
// keeping only the grant-envelope wiring in oauth_handler.go.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"
)

// oauthTokenError is a typed RFC 6749 §5.2 token endpoint error. Detection of
// invalid_grant goes through the parsed "error" field rather than substring
// matching over arbitrary error text.
type oauthTokenError struct {
	StatusCode  int
	Code        string
	Description string
}

func (e *oauthTokenError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("token endpoint returned %q (HTTP %d): %s", e.Code, e.StatusCode, e.Description)
	}
	return fmt.Sprintf("token endpoint returned %q (HTTP %d)", e.Code, e.StatusCode)
}

// isInvalidGrantError reports whether err is a typed invalid_grant token
// endpoint error (RFC 6749 §5.2).
func isInvalidGrantError(err error) bool {
	var tokenErr *oauthTokenError
	return errors.As(err, &tokenErr) && tokenErr.Code == "invalid_grant"
}

// Client authentication methods (RFC 7591 token_endpoint_auth_method /
// RFC 6749 client authentication).
const (
	authMethodNone  = "none"
	authMethodBasic = "client_secret_basic"
	authMethodPost  = "client_secret_post"
)

// selectTokenEndpointAuthMethod chooses the client authentication method to
// request during dynamic client registration from what the authorization
// server advertises. Preference order among the methods we implement:
// client_secret_basic (RFC-preferred, confidential) > client_secret_post
// (confidential) > none (public client, protected by PKCE).
//
// When the server advertises nothing we default to client_secret_basic (the
// RFC 7591 default, and the plugin's prior behavior). When it advertises only
// methods we do not implement (e.g. private_key_jwt), we also request
// client_secret_basic so registration fails with a clear
// invalid_client_metadata error rather than silently picking an unsupported
// method.
func selectTokenEndpointAuthMethod(supported []string) string {
	if len(supported) == 0 {
		return authMethodBasic
	}
	for _, preferred := range []string{authMethodBasic, authMethodPost, authMethodNone} {
		if slices.Contains(supported, preferred) {
			return preferred
		}
	}
	return authMethodBasic
}

// refreshGrant redeems the grant's refresh token against exactly the token
// endpoint and client the grant envelope is pinned to — no discovery — and
// includes the pinned canonical RFC 8707 resource, as the MCP specification
// requires on every token request. When the response omits a new refresh
// token, the previous one is retained (RFC 6749 §6).
//
// Client authentication uses the envelope's pinned method when known. When it
// is unknown (empty) and a secret is present — the static-credentials case,
// where the exchange relied on oauth2's basic/post auto-detection — the
// refresh auto-detects too: it tries client_secret_basic and, on an
// invalid_client error, retries with client_secret_post. The method that
// worked is returned so the caller can persist it and skip detection next
// time.
func (m *OAuthManager) refreshGrant(ctx context.Context, envelope *storedTokenEnvelope, creds *ClientCredentials) (_ *oauth2.Token, usedMethod string, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "mcp oauth token refresh",
		trace.WithAttributes(telemetry.MCPServer.String(envelope.Resource)),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	methods := refreshAuthMethods(envelope.AuthMethod, creds.ClientSecret)
	for i, method := range methods {
		token, refreshErr := m.doRefresh(ctx, envelope, creds, method)
		if refreshErr == nil {
			return token, method, nil
		}
		// Only auto-detection (multiple candidates) retries, and only on a
		// client-authentication failure — never on invalid_grant, which
		// signals a dead refresh token (retrying would be pointless and could
		// trip token-family replay protection). This mirrors x/oauth2's
		// exchange auto-detection, which retries client_secret_post after a
		// failed Basic attempt: providers signal a rejected Basic attempt
		// with invalid_client or invalid_request.
		var tokenErr *oauthTokenError
		if i < len(methods)-1 && errors.As(refreshErr, &tokenErr) && tokenErr.Code != "invalid_grant" {
			continue
		}
		return nil, "", refreshErr
	}
	return nil, "", fmt.Errorf("no client authentication method available for refresh")
}

// refreshAuthMethods returns the ordered client-authentication methods to try
// for a refresh given the pinned method and whether a secret is present.
func refreshAuthMethods(pinned, secret string) []string {
	switch pinned {
	case authMethodNone:
		return []string{authMethodNone}
	case authMethodBasic:
		return []string{authMethodBasic}
	case authMethodPost:
		return []string{authMethodPost}
	}
	if secret == "" {
		return []string{authMethodNone}
	}
	// Unknown method with a secret (static credentials): auto-detect, matching
	// oauth2's exchange behavior.
	return []string{authMethodBasic, authMethodPost}
}

func (m *OAuthManager) doRefresh(ctx context.Context, envelope *storedTokenEnvelope, creds *ClientCredentials, method string) (*oauth2.Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {envelope.Token.RefreshToken},
	}
	if envelope.Resource != "" {
		form.Set("resource", envelope.Resource)
	}
	switch method {
	case authMethodNone, authMethodPost:
		form.Set("client_id", creds.ClientID)
		if method == authMethodPost {
			form.Set("client_secret", creds.ClientSecret)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, envelope.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if method == authMethodBasic {
		// RFC 6749 §2.3.1: credentials are form-urlencoded before being used
		// in HTTP basic authentication.
		req.SetBasicAuth(url.QueryEscape(creds.ClientID), url.QueryEscape(creds.ClientSecret))
	}

	// A token endpoint must not redirect us elsewhere: refuse to follow any
	// redirect so the refresh token and client secret are never replayed to
	// another host.
	resp, err := noRedirectClient(m.httpClient).Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	const maxTokenResponseBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read token refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &errBody)
		return nil, &oauthTokenError{
			StatusCode:  resp.StatusCode,
			Code:        errBody.Error,
			Description: errBody.ErrorDescription,
		}
	}

	var tokenBody struct {
		AccessToken  string         `json:"access_token"`
		TokenType    string         `json:"token_type"`
		RefreshToken string         `json:"refresh_token"`
		ExpiresIn    flexibleNumber `json:"expires_in"`
		Scope        string         `json:"scope"`
	}
	if err := json.Unmarshal(body, &tokenBody); err != nil {
		return nil, fmt.Errorf("failed to parse token refresh response: %w", err)
	}
	if tokenBody.AccessToken == "" {
		return nil, fmt.Errorf("token refresh response is missing access_token")
	}

	token := &oauth2.Token{
		AccessToken:  tokenBody.AccessToken,
		TokenType:    tokenBody.TokenType,
		RefreshToken: tokenBody.RefreshToken,
	}
	if token.RefreshToken == "" {
		token.RefreshToken = envelope.Token.RefreshToken
	}
	if expiresIn := int64(tokenBody.ExpiresIn); expiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	return token, nil
}

// flexibleNumber decodes a JSON value that may be either a number or a
// numeric string. Real OAuth providers return expires_in both ways, and
// x/oauth2 (which the exchange path uses) accepts both; the refresh decoder
// must match so a valid rotation response is never rejected before the new
// grant is persisted.
type flexibleNumber int64

func (n *flexibleNumber) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	// Strip surrounding quotes if the value was sent as a string.
	if data[0] == '"' {
		if len(data) < 2 || data[len(data)-1] != '"' {
			return fmt.Errorf("invalid quoted number %q", string(data))
		}
		data = data[1 : len(data)-1]
		if len(data) == 0 {
			return nil
		}
	}
	// Parse as a float first (RFC 6749 uses seconds, but some providers send
	// fractional values) and truncate to whole seconds.
	f, err := strconv.ParseFloat(string(data), 64)
	if err != nil {
		return fmt.Errorf("invalid number %q: %w", string(data), err)
	}
	*n = flexibleNumber(int64(f))
	return nil
}
