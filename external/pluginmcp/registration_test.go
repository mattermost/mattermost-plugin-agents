// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package pluginmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPluginAPI is a queue-backed PluginAPI. fn overrides the queue.
type mockPluginAPI struct {
	mu        sync.Mutex
	responses []*http.Response
	received  []*http.Request
	fn        func(*http.Request) *http.Response
}

func (m *mockPluginAPI) PluginHTTP(req *http.Request) *http.Response {
	// Snapshot the body so tests can read it after the request runs.
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
	}

	m.mu.Lock()
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

func TestRegisterOnce_URLAndPayload(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(200, "")}}
	s := NewServer(api, Config{
		PluginID:       "com.example.demo",
		Name:           "Demo",
		Path:           "/mcp",
		ExposeExternal: true,
		Version:        "0.5.0",
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
	var got Config
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, s.config, got)
}

func TestRegisterOnce_PayloadIncludesExposeExternalFalse(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(200, "")}}
	s := NewServer(api, Config{
		PluginID: "com.example.demo", Name: "Demo", Path: "/mcp",
		ExposeExternal: false, Version: "0.5.0",
	})

	retriable, err := s.registerOnce(context.Background())
	require.NoError(t, err)
	assert.False(t, retriable)

	reqs := api.requests()
	require.Len(t, reqs, 1)
	body, _ := io.ReadAll(reqs[0].Body)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(body, &raw))
	assert.Equal(t, false, raw["expose_external"])
}

func TestRegisterOnce_Retries5xx(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(500, "boom")}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.True(t, retriable)
	assert.Contains(t, err.Error(), "status 500")
}

func TestRegisterOnce_Retries404(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(404, "not ready")}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.True(t, retriable)
}

func TestRegisterOnce_Retries429(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(429, "slow down")}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.True(t, retriable)
}

func TestRegisterOnce_GiveUpOn4xx(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(400, "bad")}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.False(t, retriable)
}

func TestRegisterOnce_GiveUpOn403(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(403, "forbidden")}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.False(t, retriable)
}

// TestRegisterOnce_NilResponse: PluginHTTP returns nil when the target
// plugin is not loaded; treated as retriable (it may load soon).
func TestRegisterOnce_NilResponse(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response { return nil }}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.True(t, retriable)
	assert.Contains(t, err.Error(), "PluginHTTP returned nil response")
}

func TestRegisterWithBackoff_Succeeds(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{
		newJSONResponse(500, ""),
		newJSONResponse(500, ""),
		newJSONResponse(200, ""),
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	s.retry = fastRetry()

	s.registerWithBackoff(context.Background())

	assert.Len(t, api.requests(), 3, "expected 3 POST attempts (2 x 500 + 1 x 200)")
}

func TestRegisterWithBackoff_GivesUpAfterMaxAttempts(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{
		newJSONResponse(500, ""),
		newJSONResponse(500, ""),
		newJSONResponse(500, ""),
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	s.retry = retryPolicy{baseDelay: 1 * time.Millisecond, maxDelay: 1 * time.Millisecond, maxAttempts: 3}

	start := time.Now()
	s.registerWithBackoff(context.Background())
	elapsed := time.Since(start)

	assert.Len(t, api.requests(), 3, "should stop at maxAttempts (3)")
	assert.Less(t, elapsed, 500*time.Millisecond, "total time should be bounded by the shrunken policy")
}

func TestRegisterWithBackoff_GivesUpOnPermanent4xx(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{
		newJSONResponse(400, "bad"),
		newJSONResponse(200, ""),
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	s.retry = fastRetry()

	s.registerWithBackoff(context.Background())

	assert.Len(t, api.requests(), 1, "should give up after the first permanent 4xx")
}

func TestRegisterWithBackoff_CancelStops(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response {
		return newJSONResponse(500, "")
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	// Delay big enough to guarantee the time.After() select is reached.
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

func TestRegister_IsAsync(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response { return newJSONResponse(200, "") }}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	s.retry = retryPolicy{baseDelay: 1 * time.Millisecond, maxDelay: 1 * time.Millisecond, maxAttempts: 1}

	start := time.Now()
	err := s.Register()
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 50*time.Millisecond, "Register() must not block on the network")

	require.Eventually(t, func() bool {
		return len(api.requests()) == 1
	}, time.Second, 5*time.Millisecond, "background goroutine should POST exactly once")
}

func TestUnregister_Sync_CancelsRetries(t *testing.T) {
	// Register: always 500. Unregister: 200.
	api := &mockPluginAPI{fn: func(req *http.Request) *http.Response {
		if strings.Contains(req.URL.Path, "/unregister") {
			return newJSONResponse(200, "")
		}
		return newJSONResponse(500, "")
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	// Slow enough that the retry loop is mid-wait when Unregister fires.
	s.retry = retryPolicy{baseDelay: 1 * time.Second, maxDelay: 1 * time.Second, maxAttempts: 15}

	require.NoError(t, s.Register())
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, s.Unregister())

	assert.ErrorIs(t, s.regCtx.Err(), context.Canceled, "retry context should be canceled")

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

func TestUnregister_URLAndPayload(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(200, "")}}
	s := NewServer(api, Config{
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
	var got Config
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, s.config, got)
}

func TestUnregister_PropagatesNon200(t *testing.T) {
	api := &mockPluginAPI{responses: []*http.Response{newJSONResponse(500, "boom")}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	err := s.Unregister()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestUnregister_NilResponse(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response { return nil }}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	err := s.Unregister()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PluginHTTP returned nil response")
}

// TestPostRegistration_NilPluginAPI: NewServer accepts a nil PluginAPI, so
// postRegistration must return a normal error instead of panicking on the
// pluginAPI.PluginHTTP dereference.
func TestPostRegistration_NilPluginAPI(t *testing.T) {
	s := NewServer(nil, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.False(t, retriable)
	assert.Contains(t, err.Error(), "PluginAPI is required")
}

func TestUnregister_NilPluginAPI(t *testing.T) {
	s := NewServer(nil, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	err := s.Unregister()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PluginAPI is required")
}

// errReader is an io.ReadCloser that always errors on Read; used to simulate
// a response body that fails mid-read.
type errReader struct{ err error }

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }
func (e *errReader) Close() error               { return nil }

func errResponse(status int, err error) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       &errReader{err: err},
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func TestPostRegistration_SurfacesDrainError(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response {
		return errResponse(http.StatusOK, io.ErrUnexpectedEOF)
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.False(t, retriable)
	assert.Contains(t, err.Error(), "drain response body")
}

func TestPostRegistration_SurfacesReadErrorOnRetriable(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response {
		return errResponse(http.StatusInternalServerError, io.ErrUnexpectedEOF)
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.True(t, retriable)
	assert.Contains(t, err.Error(), "read error response body")
}

func TestPostRegistration_SurfacesReadErrorOnPermanent(t *testing.T) {
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response {
		return errResponse(http.StatusBadRequest, io.ErrUnexpectedEOF)
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	retriable, err := s.registerOnce(context.Background())
	require.Error(t, err)
	assert.False(t, retriable)
	assert.Contains(t, err.Error(), "read error response body")
}

// withUnregisterTimeout swaps the package-level unregisterTimeout for the
// duration of a test so the bounded-timeout behavior can be exercised
// without sleeping for the production default.
func withUnregisterTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := unregisterTimeout
	unregisterTimeout = d
	t.Cleanup(func() { unregisterTimeout = prev })
}

// TestUnregister_BoundedTimeout confirms that when PluginHTTP blocks
// indefinitely (the Agents plugin is hung), Unregister returns within the
// bounded timeout instead of stalling OnDeactivate forever.
func TestUnregister_BoundedTimeout(t *testing.T) {
	withUnregisterTimeout(t, 25*time.Millisecond)

	api := &mockPluginAPI{fn: func(req *http.Request) *http.Response {
		<-req.Context().Done()
		return nil
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	start := time.Now()
	err := s.Unregister()
	elapsed := time.Since(start)

	require.Error(t, err, "Unregister should surface the timeout-induced failure")
	assert.GreaterOrEqual(t, elapsed, 20*time.Millisecond,
		"should wait approximately the bounded timeout")
	assert.Less(t, elapsed, 2*time.Second,
		"Unregister must not block past its bounded timeout")
}

// TestUnregister_WaitsForInFlightRegister confirms Unregister blocks the
// /unregister POST until the in-flight register goroutine drains, so a late
// /register cannot land after /unregister and re-add the entry. The fake
// PluginHTTP intentionally ignores ctx cancellation to simulate a hung
// plugin RPC layer; only WaitGroup gating can serialize the two POSTs.
func TestUnregister_WaitsForInFlightRegister(t *testing.T) {
	withUnregisterTimeout(t, 200*time.Millisecond)

	release := make(chan struct{})
	registerStarted := make(chan struct{})
	var startOnce sync.Once

	api := &mockPluginAPI{fn: func(req *http.Request) *http.Response {
		if strings.Contains(req.URL.Path, "/unregister") {
			return newJSONResponse(200, "")
		}
		startOnce.Do(func() { close(registerStarted) })
		<-release
		return newJSONResponse(200, "")
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	s.retry = retryPolicy{baseDelay: 1 * time.Millisecond, maxDelay: 1 * time.Millisecond, maxAttempts: 1}

	require.NoError(t, s.Register())
	<-registerStarted

	unregDone := make(chan error, 1)
	go func() { unregDone <- s.Unregister() }()

	time.Sleep(50 * time.Millisecond)

	for _, r := range api.requests() {
		require.NotContains(t, r.URL.Path, "/unregister",
			"Unregister must not POST while a register attempt is in flight")
	}

	close(release)

	select {
	case err := <-unregDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Unregister did not return after register goroutine drained")
	}

	var sawRegister, sawUnregister bool
	for _, r := range api.requests() {
		switch {
		case strings.Contains(r.URL.Path, "/unregister"):
			sawUnregister = true
		case strings.Contains(r.URL.Path, "/register"):
			sawRegister = true
		}
	}
	assert.True(t, sawRegister, "register POST must have been sent")
	assert.True(t, sawUnregister, "unregister POST must have been sent after register drained")
}

// TestUnregister_BoundedWaitForInFlightRegister confirms the wait phase is
// bounded: when the in-flight register goroutine never drains (hung plugin
// that ignores ctx), Unregister still proceeds to fire /unregister after
// the bounded wait timeout instead of stalling OnDeactivate forever.
func TestUnregister_BoundedWaitForInFlightRegister(t *testing.T) {
	withUnregisterTimeout(t, 25*time.Millisecond)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	registerStarted := make(chan struct{})
	var startOnce sync.Once

	api := &mockPluginAPI{fn: func(req *http.Request) *http.Response {
		if strings.Contains(req.URL.Path, "/unregister") {
			return newJSONResponse(200, "")
		}
		startOnce.Do(func() { close(registerStarted) })
		<-release
		return newJSONResponse(200, "")
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})
	s.retry = retryPolicy{baseDelay: 1 * time.Millisecond, maxDelay: 1 * time.Millisecond, maxAttempts: 1}

	require.NoError(t, s.Register())
	<-registerStarted

	start := time.Now()
	err := s.Unregister()
	elapsed := time.Since(start)

	require.NoError(t, err, "Unregister POST should succeed once the bounded wait elapses")
	assert.GreaterOrEqual(t, elapsed, 20*time.Millisecond,
		"Unregister should wait approximately the bounded wait timeout")
	assert.Less(t, elapsed, 2*time.Second,
		"Unregister must not block past its bounded budgets")

	var sawUnregister bool
	for _, r := range api.requests() {
		if strings.Contains(r.URL.Path, "/unregister") {
			sawUnregister = true
			break
		}
	}
	assert.True(t, sawUnregister,
		"unregister POST must be sent even when the register goroutine is hung")
}

// closerErrReadCloser wraps an io.Reader and returns a fixed error from
// Close(); used to simulate a response body whose Close() fails.
type closerErrReadCloser struct {
	io.Reader
	closeErr error
	closed   bool
}

func (c *closerErrReadCloser) Close() error {
	c.closed = true
	return c.closeErr
}

// TestPostRegistration_BodyCloseErrorIsLoggedNotReturned ensures a Close()
// error on the response body does not break the OK path: postRegistration
// must still succeed and explicitly log the close failure.
func TestPostRegistration_BodyCloseErrorIsLoggedNotReturned(t *testing.T) {
	body := &closerErrReadCloser{
		Reader:   strings.NewReader(""),
		closeErr: errors.New("synthetic close error"),
	}
	api := &mockPluginAPI{fn: func(_ *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       body,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	retriable, err := s.registerOnce(context.Background())
	require.NoError(t, err, "Close() error must not break the OK path")
	assert.False(t, retriable)
	assert.True(t, body.closed, "Body should have been closed")
	assert.Contains(t, buf.String(), "closing registration response body",
		"explicit Close() error must be logged")
	assert.Contains(t, buf.String(), "synthetic close error",
		"the underlying close error should be included in the log")
}

// TestUnregister_DeadlinePropagated verifies the request context handed to
// PluginHTTP carries a deadline, so downstream code (and the Mattermost
// plugin RPC layer) can short-circuit on cancel.
func TestUnregister_DeadlinePropagated(t *testing.T) {
	var (
		gotDeadline bool
		deadline    time.Time
	)
	api := &mockPluginAPI{fn: func(req *http.Request) *http.Response {
		deadline, gotDeadline = req.Context().Deadline()
		return newJSONResponse(200, "")
	}}
	s := NewServer(api, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	start := time.Now()
	require.NoError(t, s.Unregister())

	require.True(t, gotDeadline, "Unregister must propagate a deadline-bound context to PluginHTTP")
	assert.WithinDuration(t, start.Add(unregisterTimeout), deadline, 500*time.Millisecond,
		"deadline should be ~unregisterTimeout from invocation")
}
