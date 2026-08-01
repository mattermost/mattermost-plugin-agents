// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
		// Only auto-detection (multiple candidates) retries, and only on
		// invalid_client, which specifically signals wrong client
		// authentication rather than a dead refresh token.
		var tokenErr *oauthTokenError
		if i < len(methods)-1 && errors.As(refreshErr, &tokenErr) && tokenErr.Code == "invalid_client" {
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

	httpClient := m.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
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
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
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
	if tokenBody.ExpiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(tokenBody.ExpiresIn) * time.Second)
	}
	return token, nil
}
