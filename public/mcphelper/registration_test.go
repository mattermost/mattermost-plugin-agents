// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcphelper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPluginAPI is a minimal PluginAPI implementation backed by an ordered
// queue of canned responses plus a capture slice for inspection. If fn is
// non-nil it overrides the queue.
type mockPluginAPI struct {
	mu        sync.Mutex
	responses []*http.Response
	received  []*http.Request
	fn        func(*http.Request) *http.Response
}

func (m *mockPluginAPI) PluginHTTP(req *http.Request) *http.Response {
	// Fully read + restore the body so tests can assert on it after the fact.
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
	}

	m.mu.Lock()
	// Make a shallow copy + snapshot the body so subsequent reads on the
	// original don't race with our assertions.
	cloned := req.Clone(req.Context())
	cloned.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
	m.received = append(m.received, cloned)

	var resp *http.Response
	if m.fn != nil {
		fn := m.fn
		m.mu.Unlock()
		return fn(req)
	}
	if len(m.responses) > 0 {
		resp = m.responses[0]
		m.responses = m.responses[1:]
	}
	m.mu.Unlock()
	return resp
}

func (m *mockPluginAPI) requests() []*http.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*http.Request, len(m.received))
	copy(out, m.received)
	return out
}

// newJSONResponse builds a canned response. Pass body=="" for an empty body.
func newJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func fastRetry() retryPolicy {
	return retryPolicy{
		baseDelay:   1 * time.Millisecond,
		maxDelay:    2 * time.Millisecond,
		maxAttempts: 15,
	}
}

// TestRegisterOnce_URLAndPayload verifies a single register attempt targets
// /<AiPluginID>/bridge/v1/mcp/register with the expected JSON body.
func TestRegisterOnce_URLAndPayload(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(200, "")}}
	s := NewServer(api, PluginMCPServer{
		PluginID: "com.example.demo",
		Name:     "Demo",
		Path:     "/mcp",
		Version:  "0.5.0",
	})

	retriable, err := s.registerOnce(context.Background())
	require.NoError(t, err)
	assert.False(t, retriable)

	reqs := api.requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, http.MethodPost, reqs[0].Method)
	assert.Equal(t, "/mattermost-ai/bridge/v1/mcp/register", reqs[0].URL.Path)
	assert.Equal(t, "application/json", reqs[0].Header.Get("Content-Type"))

	body, _ := io.ReadAll(reqs[0].Body)
	var got PluginMCPServer
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, s.config, got)
}

// TestRegisterOnce_Retries5xx — 500 → retriable.
func TestRegisterOnce_Retries5xx(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(500, "boom")}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.True(t, retriable)
	assert.Contains(t, err.Error(), "status 500")
}

// TestRegisterOnce_Retries404 — 404 → retriable (agents plugin not ready yet).
func TestRegisterOnce_Retries404(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(404, "not ready")}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.True(t, retriable)
}

// TestRegisterOnce_Retries429 — 429 → retriable (rate limiting is transient).
func TestRegisterOnce_Retries429(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(429, "slow down")}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.True(t, retriable)
}

// TestRegisterOnce_GiveUpOn4xx — 400 → permanent.
func TestRegisterOnce_GiveUpOn4xx(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(400, "bad")}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.False(t, retriable)
}

// TestRegisterOnce_GiveUpOn403 — 403 is auth/authz-level and cannot be
// fixed by retry.
func TestRegisterOnce_GiveUpOn403(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(403, "forbidden")}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.False(t, retriable)
}

// TestRegisterOnce_NilResponse — PluginHTTP returns nil when the target
// plugin is not loaded. Treated as retriable (it may load soon).
func TestRegisterOnce_NilResponse(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response { return nil }}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.True(t, retriable)
	assert.Contains(t, err.Error(), "PluginHTTP returned nil response")
}

// TestRegisterWithBackoff_Succeeds — transient failures are swallowed as
// long as a later attempt succeeds.
func TestRegisterWithBackoff_Succeeds(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{
		newJSONResponse(500, ""),
		newJSONResponse(500, ""),
		newJSONResponse(200, ""),
	}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	s.retry = fastRetry()

	s.registerWithBackoff(context.Background())

	assert.Len(t, api.requests(), 3, "expected 3 POST attempts (2 × 500 + 1 × 200)")
}

// TestRegisterWithBackoff_GivesUpAfterMaxAttempts — loop exits without
// panicking; the error is logged (not asserted here — we just verify the
// attempt count).
func TestRegisterWithBackoff_GivesUpAfterMaxAttempts(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{
		newJSONResponse(500, ""),
		newJSONResponse(500, ""),
		newJSONResponse(500, ""),
	}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	s.retry = retryPolicy{baseDelay: 1 * time.Millisecond, maxDelay: 1 * time.Millisecond, maxAttempts: 3}

	start := time.Now()
	s.registerWithBackoff(context.Background())
	elapsed := time.Since(start)

	assert.Len(t, api.requests(), 3, "should stop at maxAttempts (3)")
	assert.Less(t, elapsed, 500*time.Millisecond, "total time should be bounded by the shrunken policy")
}

// TestRegisterWithBackoff_GivesUpOnPermanent4xx — stops immediately on a
// non-retriable status code.
func TestRegisterWithBackoff_GivesUpOnPermanent4xx(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{
		newJSONResponse(400, "bad"),
		newJSONResponse(200, ""), // unreachable if we correctly give up
	}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	s.retry = fastRetry()

	s.registerWithBackoff(context.Background())

	assert.Len(t, api.requests(), 1, "should give up after the first permanent 4xx")
}

// TestRegisterWithBackoff_CancelStops — context cancellation short-circuits
// the retry loop.
func TestRegisterWithBackoff_CancelStops(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response {
		return newJSONResponse(500, "")
	}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	// Larger delay so the test is guaranteed to hit the time.After() select,
	// where cancel() can short-circuit.
	s.retry = retryPolicy{baseDelay: 50 * time.Millisecond, maxDelay: 50 * time.Millisecond, maxAttempts: 15}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	s.registerWithBackoff(ctx)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 500*time.Millisecond, "cancellation should short-circuit the loop")
	attempts := len(api.requests())
	assert.Less(t, attempts, 15, "should not complete all 15 attempts when canceled; got %d", attempts)
}

// TestRegister_IsAsync — Register() returns immediately; the goroutine runs
// asynchronously.
func TestRegister_IsAsync(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response { return newJSONResponse(200, "") }}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	s.retry = retryPolicy{baseDelay: 1 * time.Millisecond, maxDelay: 1 * time.Millisecond, maxAttempts: 1}

	start := time.Now()
	err := s.Register()
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 50*time.Millisecond, "Register() must not block on the network")

	// Give the goroutine time to run.
	require.Eventually(t, func() bool {
		return len(api.requests()) == 1
	}, time.Second, 5*time.Millisecond, "background goroutine should POST exactly once")
}

// TestUnregister_Sync_CancelsRetries — Unregister cancels the Register retry
// goroutine before firing its own POST.
func TestUnregister_Sync_CancelsRetries(t *testing.T) {
	// Register mock: always 500 (so Register would retry forever under the
	// default policy); Unregister mock: 200 once.
	api := &mockPluginAPI{fn: func(req *http.Request) *http.Response {
		if strings.Contains(req.URL.Path, "/unregister") {
			return newJSONResponse(200, "")
		}
		return newJSONResponse(500, "")
	}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})
	// Slow enough that the retry goroutine is still looping when we call
	// Unregister(). 1-second baseDelay is fine because ctx cancellation will
	// short-circuit the time.After wait.
	s.retry = retryPolicy{baseDelay: 1 * time.Second, maxDelay: 1 * time.Second, maxAttempts: 15}

	require.NoError(t, s.Register())
	// Tiny sleep to ensure the first register attempt has at least started.
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, s.Unregister())

	assert.ErrorIs(t, s.regCtx.Err(), context.Canceled, "retry context should be canceled")

	// Look for the unregister request in the captured traffic.
	found := false
	for _, r := range api.requests() {
		if strings.Contains(r.URL.Path, "/unregister") {
			found = true
			assert.Equal(t, "/mattermost-ai/bridge/v1/mcp/unregister", r.URL.Path)
			break
		}
	}
	assert.True(t, found, "unregister POST should have been fired")
}

// TestUnregister_URLAndPayload — URL and body shape for the unregister path.
func TestUnregister_URLAndPayload(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(200, "")}}
	s := NewServer(api, PluginMCPServer{
		PluginID: "com.example.demo",
		Name:     "Demo",
		Path:     "/mcp",
		Version:  "1.0.0",
	})

	require.NoError(t, s.Unregister())

	reqs := api.requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, http.MethodPost, reqs[0].Method)
	assert.Equal(t, "/mattermost-ai/bridge/v1/mcp/unregister", reqs[0].URL.Path)
	assert.Equal(t, "application/json", reqs[0].Header.Get("Content-Type"))

	body, _ := io.ReadAll(reqs[0].Body)
	var got PluginMCPServer
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, s.config, got)
}

// TestUnregister_PropagatesNon200 — a non-200 reply surfaces as a non-nil
// error from Unregister().
func TestUnregister_PropagatesNon200(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(500, "boom")}}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})

	err := s.Unregister()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

// TestUnregister_NilResponse mirrors the register nil-response behavior:
// PluginHTTP returns nil when the target plugin is not loaded, and Unregister
// surfaces that failure to the caller.
func TestUnregister_NilResponse(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response { return nil }}
	s := NewServer(api, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})

	err := s.Unregister()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PluginHTTP returned nil response")
}
