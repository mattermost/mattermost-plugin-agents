// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const gdcTokenSentinel = "upstream-secret-detail"

// gdcGatewayRequest is what the fake gateway saw for one inference request.
type gdcGatewayRequest struct {
	path          string
	authorization string
	userProject   string
	apiKey        string
}

// fakeGDC is a TLS server that plays both the GDC token endpoint
// (/authenticate) and the inference gateway.
type fakeGDC struct {
	server *httptest.Server
	key    *ecdsa.PrivateKey

	tokenStatus  int
	tokenExpires int64
	gatewayFails bool

	mu            sync.Mutex
	tokenRequests []map[string]string
	gateway       []gdcGatewayRequest
}

func newFakeGDC(t *testing.T) *fakeGDC {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	f := &fakeGDC{key: key, tokenStatus: http.StatusOK, tokenExpires: 3600}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGDC) serveHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if r.URL.Path == "/authenticate" {
		var req map[string]string
		_ = json.Unmarshal(body, &req)
		f.mu.Lock()
		f.tokenRequests = append(f.tokenRequests, req)
		n := len(f.tokenRequests)
		f.mu.Unlock()
		if f.tokenStatus != http.StatusOK {
			http.Error(w, gdcTokenSentinel, f.tokenStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"tok-%d","token_type":"Bearer","expires_in":%d}`, n, f.tokenExpires)
		return
	}

	f.mu.Lock()
	f.gateway = append(f.gateway, gdcGatewayRequest{
		path:          r.URL.Path,
		authorization: r.Header.Get("Authorization"),
		userProject:   r.Header.Get("x-goog-user-project"),
		apiKey:        r.Header.Get("x-goog-api-key"),
	})
	f.mu.Unlock()

	switch {
	case f.gatewayFails:
		http.Error(w, `{"error":{"code":400,"message":"rejected","status":"INVALID_ARGUMENT"}}`, http.StatusBadRequest)
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		chatCompletionSSE(w, "ok")
	case strings.Contains(r.URL.Path, ":streamGenerateContent"):
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`+"\n\n")
	default:
		http.Error(w, "unexpected path", http.StatusNotFound)
	}
}

// caPEM returns the PEM of the server's self-signed certificate.
func (f *fakeGDC) caPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}))
}

// serviceAccountKey returns a gdch_service_account key for f's private key.
func (f *fakeGDC) serviceAccountKey(t *testing.T) string {
	t.Helper()
	return gdcKeyJSON(t, f.key, f.server.URL+"/authenticate")
}

func gdcKeyJSON(t *testing.T, key any, tokenURI string) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	data, err := json.Marshal(map[string]string{
		"type":           "gdch_service_account",
		"format_version": "1",
		"project":        "mc-mm-510014",
		"name":           "mm-sa-gemini",
		"private_key_id": "key-1",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":      tokenURI,
		"ca_cert_path":   "/does/not/exist.crt",
	})
	require.NoError(t, err)
	return string(data)
}

func (f *fakeGDC) service(t *testing.T, mode string) llm.ServiceConfig {
	t.Helper()
	return llm.ServiceConfig{
		ID:                   "gdc",
		Type:                 llm.ServiceTypeGDCGemini,
		DefaultModel:         "gemini-3.5-flash",
		APIURL:               f.server.URL,
		Region:               "us-east92-a",
		GDCProjectID:         "mc-mm-510014",
		GDCServiceAccountKey: f.serviceAccountKey(t),
		GDCCACertificate:     f.caPEM(),
		GDCAPIMode:           mode,
	}
}

func (f *fakeGDC) snapshot() ([]map[string]string, []gdcGatewayRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.tokenRequests...), append([]gdcGatewayRequest(nil), f.gateway...)
}

func gdcRequest() llm.CompletionRequest {
	return llm.CompletionRequest{Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}}}
}

// TestGDCServiceRequests drives requests through the production wiring and
// checks what reaches the gateway and the token endpoint for each API mode.
func TestGDCServiceRequests(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		wantPath string
	}{
		{
			name:     "GenerateContent is the default",
			mode:     "",
			wantPath: "/v1/projects/mc-mm-510014/locations/us-east92-a/publishers/google/models/gemini-3.5-flash:streamGenerateContent",
		},
		{
			name:     "Chat Completions",
			mode:     llm.GDCAPIModeChatCompletions,
			wantPath: "/v1/projects/mc-mm-510014/locations/us-east92-a/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGDC(t)
			llmInstance, err := NewFromServiceConfig(f.service(t, tt.mode), llm.BotConfig{ID: "bot-1", Name: "ai", DisplayName: "AI", ServiceID: "gdc"}, nil)
			require.NoError(t, err)
			t.Cleanup(llmInstance.Shutdown)

			for range 2 {
				text, completionErr := llmInstance.ChatCompletionNoStream(context.Background(), gdcRequest(), llm.WithToolsDisabled())
				require.NoError(t, completionErr)
				assert.Equal(t, "ok", text)
			}

			tokenRequests, gateway := f.snapshot()
			require.Len(t, tokenRequests, 1, "a cached token must be reused")
			require.Len(t, gateway, 2)
			for _, req := range gateway {
				assert.Equal(t, tt.wantPath, req.path)
				assert.Equal(t, "Bearer tok-1", req.authorization)
				assert.Equal(t, "projects/mc-mm-510014", req.userProject)
				assert.Empty(t, req.apiKey)
			}

			tokenReq := tokenRequests[0]
			assert.Equal(t, gdcTokenGrantType, tokenReq["grant_type"])
			assert.Equal(t, f.server.URL, tokenReq["audience"])
			assert.Equal(t, gdcRequestedTokenType, tokenReq["requested_token_type"])
			assert.Equal(t, gdcSubjectTokenType, tokenReq["subject_token_type"])

			claims := jwt.MapClaims{}
			parsed, err := jwt.ParseWithClaims(tokenReq["subject_token"], claims, func(*jwt.Token) (any, error) {
				return &f.key.PublicKey, nil
			}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience(f.server.URL+"/authenticate"))
			require.NoError(t, err, "the assertion must be signed by the service account key")
			assert.Equal(t, "key-1", parsed.Header["kid"])
			assert.Equal(t, "system:serviceaccount:mc-mm-510014:mm-sa-gemini", claims["iss"])
			assert.Equal(t, "system:serviceaccount:mc-mm-510014:mm-sa-gemini", claims["sub"])
		})
	}
}

// TestGDCTokenFailureFailsClosed checks that a failed token exchange never
// sends the request unauthenticated, and that the token endpoint's response
// body does not reach the caller.
func TestGDCTokenFailureFailsClosed(t *testing.T) {
	f := newFakeGDC(t)
	f.tokenStatus = http.StatusForbidden
	llmInstance, err := NewFromServiceConfig(f.service(t, ""), llm.BotConfig{ID: "bot-1", Name: "ai", DisplayName: "AI", ServiceID: "gdc"}, nil)
	require.NoError(t, err)
	t.Cleanup(llmInstance.Shutdown)

	_, err = llmInstance.ChatCompletionNoStream(context.Background(), gdcRequest(), llm.WithToolsDisabled())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), gdcTokenSentinel)

	_, gateway := f.snapshot()
	assert.Empty(t, gateway, "the gateway must not be called without a token")
}

// TestGDCTokenDoesNotReachFallbacks checks that the GDC bearer token is sent
// only to the GDC gateway when the request fails over to other providers.
func TestGDCTokenDoesNotReachFallbacks(t *testing.T) {
	f := newFakeGDC(t)
	f.gatewayFails = true

	var anthropicAuth, localAuth atomic.Value
	headerServer := func(auth *atomic.Value, succeed bool) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth.Store(r.Header.Get("Authorization"))
			if !succeed {
				http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"rejected"}}`, http.StatusBadRequest)
				return
			}
			chatCompletionSSE(w, "ok")
		}))
		t.Cleanup(server.Close)
		return server
	}

	anthropic := llm.ServiceConfig{ID: "anthropic", Type: llm.ServiceTypeAnthropic, DefaultModel: "claude-sonnet-4-5", APIKey: "anthropic-key", APIURL: headerServer(&anthropicAuth, false).URL}
	local := llm.ServiceConfig{ID: "local", Type: llm.ServiceTypeOpenAICompatible, DefaultModel: "llama3", APIURL: headerServer(&localAuth, true).URL}

	llmInstance, err := NewFromServiceConfig(f.service(t, ""), llm.BotConfig{ID: "bot-1", Name: "ai", DisplayName: "AI", ServiceID: "gdc"}, []llm.ServiceConfig{anthropic, local})
	require.NoError(t, err)
	t.Cleanup(llmInstance.Shutdown)

	text, err := llmInstance.ChatCompletionNoStream(context.Background(), gdcRequest(), llm.WithToolsDisabled())
	require.NoError(t, err)
	assert.Equal(t, "ok", text)

	_, gateway := f.snapshot()
	require.NotEmpty(t, gateway)
	assert.Equal(t, "Bearer tok-1", gateway[0].authorization)

	gotAnthropic, ok := anthropicAuth.Load().(string)
	require.True(t, ok, "the Anthropic fallback must have been called")
	assert.Empty(t, gotAnthropic)
	gotLocal, ok := localAuth.Load().(string)
	require.True(t, ok, "the keyless local fallback must have been called")
	assert.Empty(t, gotLocal)
}

func TestGDCTokenSourceRefresh(t *testing.T) {
	tests := []struct {
		name          string
		expiresIn     int64
		advance       time.Duration
		wantExchanges int
	}{
		{name: "reuses a token well before expiry", expiresIn: 3600, advance: 30 * time.Minute, wantExchanges: 1},
		{name: "refreshes inside the refresh margin", expiresIn: 3600, advance: 56 * time.Minute, wantExchanges: 2},
		{name: "refreshes after expiry", expiresIn: 600, advance: 11 * time.Minute, wantExchanges: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGDC(t)
			f.tokenExpires = tt.expiresIn
			tokens, err := newGDCTokenSource(GDCSettings{
				GatewayURL:        f.server.URL,
				ServiceAccountKey: f.serviceAccountKey(t),
				CACertificatePEM:  f.caPEM(),
			})
			require.NoError(t, err)
			now := time.Now()
			tokens.now = func() time.Time { return now }

			first, err := tokens.Token(context.Background())
			require.NoError(t, err)
			assert.Equal(t, "tok-1", first)

			now = now.Add(tt.advance)
			second, err := tokens.Token(context.Background())
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("tok-%d", tt.wantExchanges), second)

			tokenRequests, _ := f.snapshot()
			assert.Len(t, tokenRequests, tt.wantExchanges)
		})
	}
}

// TestGDCServiceRejectsBadSettings checks that a GDC service with unusable
// credentials fails client setup instead of the first request.
func TestGDCServiceRejectsBadSettings(t *testing.T) {
	f := newFakeGDC(t)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(svc *llm.ServiceConfig)
	}{
		{name: "key is not a GDC key", mutate: func(svc *llm.ServiceConfig) {
			svc.GDCServiceAccountKey = strings.Replace(svc.GDCServiceAccountKey, "gdch_service_account", "service_account", 1)
		}},
		{name: "key is not ECDSA", mutate: func(svc *llm.ServiceConfig) {
			svc.GDCServiceAccountKey = gdcKeyJSON(t, rsaKey, f.server.URL+"/authenticate")
		}},
		{name: "token_uri is not https", mutate: func(svc *llm.ServiceConfig) {
			svc.GDCServiceAccountKey = gdcKeyJSON(t, f.key, "http://example.com/authenticate")
		}},
		{name: "CA certificate is not PEM", mutate: func(svc *llm.ServiceConfig) {
			svc.GDCCACertificate = "not a certificate"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := f.service(t, "")
			tt.mutate(&svc)
			_, err := NewServiceClient(svc, nil)
			require.Error(t, err)
		})
	}
}
