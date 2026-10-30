// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/maximhq/bifrost/core/schemas"
	"golang.org/x/sync/singleflight"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// Gemini on Google Distributed Cloud (GDC) air-gapped is served from an
// in-cluster inference gateway, through either the native GenerateContent API
// or an OpenAI-compatible Chat Completions API. It authenticates with
// short-lived bearer tokens minted from a gdch_service_account key, not with
// an API key, so the service is registered with Bifrost as a keyless custom
// provider and gdcAuthPlugin attaches a fresh token to every attempt that
// targets it.

const (
	gdcServiceAccountKeyType = "gdch_service_account"
	gdcSupportedKeyFormat    = "1"

	// RFC 8693 token exchange, as in the GDCH flow of Google's auth libraries
	// (cloud.google.com/go/auth, google-auth-library-python).
	gdcTokenGrantType        = "urn:ietf:params:oauth:token-type:token-exchange" //nolint:gosec // OAuth token-type URN, not a credential
	gdcRequestedTokenType    = "urn:ietf:params:oauth:token-type:access_token"   //nolint:gosec // OAuth token-type URN, not a credential
	gdcSubjectTokenType      = "urn:k8s:params:oauth:token-type:serviceaccount"  //nolint:gosec // OAuth token-type URN, not a credential
	gdcAssertionLifetime     = time.Hour
	gdcTokenRequestTimeout   = 30 * time.Second
	gdcTokenRefreshMargin    = 5 * time.Minute
	gdcDefaultTokenLifetime  = 10 * time.Minute
	gdcUserProjectHeader     = "x-goog-user-project"
	gdcAuthorizationHeader   = "Authorization"
	gdcMaxTokenResponseBytes = 1 << 20
)

// GDCSettings holds the connection settings for a Gemini service on GDC
// air-gapped.
type GDCSettings struct {
	// GatewayURL is the inference gateway origin, e.g.
	// https://inference-gateway-aics-system.aics.<org>.<zone>.google.gdch.test.
	// It is also the audience tokens are minted for.
	GatewayURL string
	ProjectID  string
	Zone       string
	// ServiceAccountKey is the gdch_service_account JSON key.
	ServiceAccountKey string
	// CACertificatePEM, when set, is trusted in addition to the system roots
	// for both the token endpoint and the gateway.
	CACertificatePEM string
	// ChatCompletions selects the gateway's OpenAI-compatible Chat
	// Completions API instead of native GenerateContent.
	ChatCompletions bool
}

// gdcSettingsFromService returns the GDC settings for svc, or nil when svc is
// not a GDC service.
func gdcSettingsFromService(svc llm.ServiceConfig) *GDCSettings {
	if svc.Type != llm.ServiceTypeGDCGemini {
		return nil
	}
	return &GDCSettings{
		GatewayURL:        svc.APIURL,
		ProjectID:         svc.GDCProjectID,
		Zone:              svc.Region,
		ServiceAccountKey: svc.GDCServiceAccountKey,
		CACertificatePEM:  svc.GDCCACertificate,
		ChatCompletions:   svc.GDCAPIMode == llm.GDCAPIModeChatCompletions,
	}
}

// gdcProvider is the Bifrost base provider serving s.
func gdcProvider(s GDCSettings) schemas.ModelProvider {
	if s.ChatCompletions {
		return schemas.OpenAI
	}
	return schemas.Gemini
}

// gdcBaseURL returns the Bifrost base URL for s. The Gemini provider appends
// /models/{model}:generateContent (or :streamGenerateContent); the native
// route is assumed to follow Vertex AI's publishers/google layout, which
// Google has not documented for air-gapped GDC. Chat Completions requests use
// gdcChatCompletionsPath instead, so their base is the gateway origin.
func gdcBaseURL(s GDCSettings) string {
	if s.ChatCompletions {
		return normalizeGDCGatewayURL(s.GatewayURL)
	}
	return normalizeGDCGatewayURL(s.GatewayURL) + gdcProjectPath(s) + "/publishers/google"
}

// gdcChatCompletionsPath is the gateway's OpenAI-compatible chat path.
func gdcChatCompletionsPath(s GDCSettings) string {
	return gdcProjectPath(s) + "/chat/completions"
}

func gdcProjectPath(s GDCSettings) string {
	return fmt.Sprintf("/v1/projects/%s/locations/%s", url.PathEscape(s.ProjectID), url.PathEscape(s.Zone))
}

// normalizeGDCGatewayURL trims an admin-entered gateway URL, adding https://
// when no scheme was given.
func normalizeGDCGatewayURL(gateway string) string {
	gateway = strings.TrimSpace(gateway)
	if gateway != "" && !strings.Contains(gateway, "://") {
		gateway = "https://" + gateway
	}
	return strings.TrimRight(gateway, "/")
}

// gdcUserProject is the x-goog-user-project header value GDC requires.
func gdcUserProject(s GDCSettings) string {
	return "projects/" + s.ProjectID
}

// gdcServiceAccountKey is the subset of a gdch_service_account key file the
// token exchange needs.
type gdcServiceAccountKey struct {
	Type          string `json:"type"`
	FormatVersion string `json:"format_version"`
	Project       string `json:"project"`
	Name          string `json:"name"`
	PrivateKeyID  string `json:"private_key_id"`
	PrivateKey    string `json:"private_key"`
	TokenURI      string `json:"token_uri"`
}

func parseGDCServiceAccountKey(raw string) (*gdcServiceAccountKey, *ecdsa.PrivateKey, error) {
	var key gdcServiceAccountKey
	if err := json.Unmarshal([]byte(raw), &key); err != nil {
		return nil, nil, errors.New("GDC service account key is not valid JSON")
	}
	if key.Type != gdcServiceAccountKeyType {
		return nil, nil, fmt.Errorf("GDC service account key has type %q, expected %q", key.Type, gdcServiceAccountKeyType)
	}
	if key.FormatVersion != gdcSupportedKeyFormat {
		return nil, nil, fmt.Errorf("GDC service account key has unsupported format_version %q", key.FormatVersion)
	}
	if key.Project == "" || key.Name == "" || key.PrivateKeyID == "" || key.TokenURI == "" {
		return nil, nil, errors.New("GDC service account key is missing project, name, private_key_id, or token_uri")
	}
	tokenURI, err := url.Parse(key.TokenURI)
	if err != nil || tokenURI.Scheme != "https" || tokenURI.Host == "" {
		return nil, nil, errors.New("GDC service account key token_uri must be an https URL")
	}

	block, _ := pem.Decode([]byte(key.PrivateKey))
	if block == nil {
		return nil, nil, errors.New("GDC service account key private_key is not PEM encoded")
	}
	var parsed any
	if parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		if parsed, err = x509.ParseECPrivateKey(block.Bytes); err != nil {
			return nil, nil, errors.New("GDC service account key private_key could not be parsed")
		}
	}
	signer, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("GDC service account key private_key is not an ECDSA key")
	}
	return &key, signer, nil
}

// newGDCHTTPClient returns a client that trusts caPEM in addition to the
// system roots and honors the proxy environment, as Bifrost's own client does.
func newGDCHTTPClient(caPEM string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(caPEM) != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, errors.New("GDC CA certificate is not a valid PEM certificate")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: transport, Timeout: gdcTokenRequestTimeout}, nil
}

// gdcTokenSource mints and caches GDC access tokens for one service account
// and audience. Concurrent callers share one in-flight exchange.
type gdcTokenSource struct {
	key      *gdcServiceAccountKey
	signer   *ecdsa.PrivateKey
	audience string
	client   *http.Client
	now      func() time.Time

	group singleflight.Group

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func newGDCTokenSource(s GDCSettings) (*gdcTokenSource, error) {
	key, signer, err := parseGDCServiceAccountKey(s.ServiceAccountKey)
	if err != nil {
		return nil, err
	}
	client, err := newGDCHTTPClient(s.CACertificatePEM)
	if err != nil {
		return nil, err
	}
	audience := normalizeGDCGatewayURL(s.GatewayURL)
	if audience == "" {
		return nil, errors.New("GDC gateway URL is required")
	}
	return &gdcTokenSource{
		key:      key,
		signer:   signer,
		audience: audience,
		client:   client,
		now:      time.Now,
	}, nil
}

// Token returns a cached token while it is outside the refresh margin, and
// otherwise exchanges a fresh service-account assertion for a new one.
func (s *gdcTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	token, expiry := s.token, s.expiry
	s.mu.Unlock()
	if token != "" && s.now().Add(gdcTokenRefreshMargin).Before(expiry) {
		return token, nil
	}

	v, err, _ := s.group.Do("token", func() (any, error) {
		token, expiry, err := s.exchange(ctx)
		if err != nil {
			return "", err
		}
		s.mu.Lock()
		s.token, s.expiry = token, expiry
		s.mu.Unlock()
		return token, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (s *gdcTokenSource) exchange(ctx context.Context) (string, time.Time, error) {
	now := s.now()
	identity := fmt.Sprintf("system:serviceaccount:%s:%s", s.key.Project, s.key.Name)
	assertion := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": identity,
		"sub": identity,
		"aud": s.key.TokenURI,
		"iat": now.Unix(),
		"exp": now.Add(gdcAssertionLifetime).Unix(),
	})
	assertion.Header["kid"] = s.key.PrivateKeyID
	subjectToken, err := assertion.SignedString(s.signer)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to sign GDC service account assertion: %w", err)
	}

	body, err := json.Marshal(map[string]string{
		"grant_type":           gdcTokenGrantType,
		"audience":             s.audience,
		"requested_token_type": gdcRequestedTokenType,
		"subject_token":        subjectToken,
		"subject_token_type":   gdcSubjectTokenType,
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to encode GDC token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.key.TokenURI, bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to build GDC token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("GDC token request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, gdcMaxTokenResponseBytes))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to read GDC token response: %w", err)
	}
	// The response body is not included: it is upstream content and the
	// error can reach the client.
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", time.Time{}, fmt.Errorf("GDC token endpoint returned HTTP %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil || tokenResp.AccessToken == "" {
		return "", time.Time{}, errors.New("GDC token endpoint returned no access token")
	}
	lifetime := gdcDefaultTokenLifetime
	if tokenResp.ExpiresIn > 0 {
		lifetime = time.Duration(tokenResp.ExpiresIn) * time.Second
	}
	return tokenResp.AccessToken, now.Add(lifetime), nil
}

// gdcAuthPlugin attaches a bearer token to each attempt served by a GDC
// service. It runs on every attempt, primary and fallback, and removes the
// header again for attempts served by any other provider: Bifrost shares the
// request context across the fallback chain, and a provider that does not set
// Authorization itself (Gemini, Anthropic) would otherwise send the GDC token
// to its own endpoint.
type gdcAuthPlugin struct {
	account *multiProviderAccount
}

func (gdcAuthPlugin) GetName() string { return "mattermost-ai-gdc-auth" }

func (gdcAuthPlugin) Cleanup() error { return nil }

func (gdcAuthPlugin) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error {
	return nil
}

func (p gdcAuthPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	if req == nil {
		return req, nil, nil
	}
	provider, _, _ := req.GetRequestFields()
	var tokens *gdcTokenSource
	if entry := p.account.entries[provider]; entry != nil {
		tokens = entry.gdcTokens
	}

	headers := map[string][]string{}
	if existing, ok := ctx.Value(schemas.BifrostContextKeyExtraHeaders).(map[string][]string); ok {
		maps.Copy(headers, existing)
	}
	delete(headers, gdcAuthorizationHeader)

	if tokens != nil {
		token, err := tokens.Token(ctx)
		if err != nil {
			// Fail closed: never send the attempt unauthenticated. Later
			// fallbacks may still serve the request.
			return req, &schemas.LLMPluginShortCircuit{Error: &schemas.BifrostError{
				Error: &schemas.ErrorField{Message: fmt.Sprintf("failed to obtain GDC access token: %v", err)},
			}}, nil
		}
		headers[gdcAuthorizationHeader] = []string{"Bearer " + token}
	}
	ctx.SetValue(schemas.BifrostContextKeyExtraHeaders, headers)
	return req, nil, nil
}

func (gdcAuthPlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, bifrostErr, nil
}
