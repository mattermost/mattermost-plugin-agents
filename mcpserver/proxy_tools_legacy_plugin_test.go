// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

const (
	legacyPluginID        = "com.mattermost.cloud"
	modernProtocolVersion = "2026-07-28"
	legacyProtocolVersion = "2025-11-25"
	legacyEchoToolName    = "list_installations"
)

// The protocol versions go-sdk v1.4.1 accepts, the SDK older source plugins
// such as the Cloud plugin still embed.
var legacyProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

type legacyPluginRequest struct {
	method          string
	protocolVersion string
}

// jsonRPCRequest reads the JSON-RPC envelope of r and restores r.Body.
func jsonRPCRequest(t *testing.T, r *http.Request) (id json.RawMessage, method string) {
	if r.Body == nil {
		return nil, ""
	}
	body, err := io.ReadAll(r.Body)
	assert.NoError(t, err)
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) == 0 {
		return nil, ""
	}
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	assert.NoError(t, json.Unmarshal(body, &msg))
	return msg.ID, msg.Method
}

// legacyPluginMCP wraps a current go-sdk handler so it answers like a go-sdk
// v1.4.1 source plugin: a plain-text 400 for any protocol version newer than
// 2025-11-25, and no server/discover.
type legacyPluginMCP struct {
	t    *testing.T
	next http.Handler

	mu       sync.Mutex
	requests []legacyPluginRequest
}

func newLegacyPluginMCP(t *testing.T) *legacyPluginMCP {
	t.Helper()
	srv := gosdkmcp.NewServer(&gosdkmcp.Implementation{Name: "legacy-plugin", Version: "0.1.41"}, nil)
	type echoIn struct {
		Message string `json:"message"`
	}
	type echoOut struct {
		Echo string `json:"echo"`
	}
	gosdkmcp.AddTool(srv, &gosdkmcp.Tool{Name: legacyEchoToolName, Description: "test"}, func(_ context.Context, _ *gosdkmcp.CallToolRequest, in echoIn) (*gosdkmcp.CallToolResult, echoOut, error) {
		return nil, echoOut{Echo: in.Message}, nil
	})
	return &legacyPluginMCP{
		t: t,
		next: gosdkmcp.NewStreamableHTTPHandler(
			func(*http.Request) *gosdkmcp.Server { return srv },
			&gosdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true},
		),
	}
}

func (l *legacyPluginMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, method := jsonRPCRequest(l.t, r)
	version := r.Header.Get("Mcp-Protocol-Version")

	l.mu.Lock()
	l.requests = append(l.requests, legacyPluginRequest{method: method, protocolVersion: version})
	l.mu.Unlock()

	if version != "" && !slices.Contains(legacyProtocolVersions, version) {
		http.Error(w, fmt.Sprintf("Bad Request: Unsupported protocol version (supported versions: %s)", strings.Join(legacyProtocolVersions, ",")), http.StatusBadRequest)
		return
	}
	if method == "server/discover" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}`, id)
		return
	}
	l.next.ServeHTTP(w, r)
}

func (l *legacyPluginMCP) received() []legacyPluginRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.requests)
}

// A caller of the external server must be able to use tools proxied from a
// source plugin that only speaks older protocol versions.
func TestPluginMCPHandlers_ProxyToolReachesLegacyPlugin(t *testing.T) {
	tests := []struct {
		name          string
		callerVersion string
	}{
		{name: "modern caller", callerVersion: modernProtocolVersion},
		{name: "2025-11-25 caller", callerVersion: legacyProtocolVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			legacy := newLegacyPluginMCP(t)
			target := httptest.NewServer(legacy)
			t.Cleanup(target.Close)

			reg := &stubRegistry{servers: []mcppkg.PluginServerConfig{
				{PluginID: legacyPluginID, Name: "Cloud", Path: "/mcp", Enabled: true, ExposeExternal: true},
			}}
			logger, err := loggerlib.CreateDefaultLogger()
			require.NoError(t, err)
			h, err := NewPluginMCPHandlers("https://mm.test", "http://mm.internal", logger, reg,
				newPerPluginForwarder(t, map[string]*httptest.Server{legacyPluginID: target}),
				nil, nil, nil, allowAllStateChangingTools, nil, nil)
			require.NoError(t, err)
			t.Cleanup(h.Close)

			external := httptest.NewServer(injectUserID(h.MCPHandler, "user1"))
			t.Cleanup(external.Close)

			client := gosdkmcp.NewClient(&gosdkmcp.Implementation{Name: "claude-ai", Version: "1.0"}, nil)
			sess, err := client.Connect(context.Background(), &gosdkmcp.StreamableClientTransport{
				Endpoint:   external.URL,
				HTTPClient: &http.Client{},
			}, &gosdkmcp.ClientSessionOptions{ProtocolVersion: tt.callerVersion})
			require.NoError(t, err)
			t.Cleanup(func() { _ = sess.Close() })
			require.Equal(t, tt.callerVersion, sess.InitializeResult().ProtocolVersion)

			result, err := sess.CallTool(context.Background(), &gosdkmcp.CallToolParams{
				Name:      legacyEchoToolName,
				Arguments: map[string]any{"message": "hello cloud"},
			})
			require.NoError(t, err)
			require.False(t, result.IsError, toolResultText(result))
			require.Contains(t, toolResultText(result), "hello cloud")

			var sawToolCall bool
			for _, req := range legacy.received() {
				if req.method == "tools/call" {
					sawToolCall = true
				}
				if req.method == "initialize" || req.method == "tools/call" {
					assert.NotEqual(t, modernProtocolVersion, req.protocolVersion,
						"the caller's protocol version must not reach the source plugin on %s", req.method)
				}
			}
			require.True(t, sawToolCall, "the tool call should have been proxied to the source plugin")
		})
	}
}

// proxyToolCallObserver forwards PluginHTTP to the legacy plugin and hands
// each tools/call request to onToolCall first.
func proxyToolCallObserver(t *testing.T, onToolCall func(*http.Request)) *stubPluginAPI {
	t.Helper()
	legacy := newLegacyPluginMCP(t)
	return &stubPluginAPI{pluginHTTP: func(req *http.Request) *http.Response {
		if _, method := jsonRPCRequest(t, req); method == "tools/call" {
			onToolCall(req)
		}
		_, rest := splitPluginHTTPPath(req.URL.Path)
		fwd := req.Clone(req.Context())
		fwd.URL.Path = rest
		rec := httptest.NewRecorder()
		legacy.ServeHTTP(rec, fwd)
		return rec.Result()
	}}
}

// The caller's session-describing _meta stays with the caller; everything
// else in _meta reaches the source plugin.
func TestBuildProxyTools_HandlerForwardsNonSessionMeta(t *testing.T) {
	sessionMeta := map[string]any{
		gosdkmcp.MetaKeyProtocolVersion:    modernProtocolVersion,
		gosdkmcp.MetaKeyClientInfo:         map[string]any{"name": "claude-ai", "version": "1.0"},
		gosdkmcp.MetaKeyClientCapabilities: map[string]any{},
	}
	tests := []struct {
		name     string
		meta     map[string]any
		wantMeta map[string]any
	}{
		{
			name: "session keys with progress token and custom key",
			meta: map[string]any{
				gosdkmcp.MetaKeyProtocolVersion:    modernProtocolVersion,
				gosdkmcp.MetaKeyClientInfo:         sessionMeta[gosdkmcp.MetaKeyClientInfo],
				gosdkmcp.MetaKeyClientCapabilities: sessionMeta[gosdkmcp.MetaKeyClientCapabilities],
				"progressToken":                    "tok-1",
				"com.example/trace":                "abc",
			},
			wantMeta: map[string]any{"progressToken": "tok-1", "com.example/trace": "abc"},
		},
		{name: "session keys only", meta: sessionMeta},
		{name: "no meta"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMeta map[string]any
			sourceAPI := proxyToolCallObserver(t, func(req *http.Request) {
				body, err := io.ReadAll(req.Body)
				assert.NoError(t, err)
				req.Body = io.NopCloser(bytes.NewReader(body))
				var msg struct {
					Params struct {
						Meta map[string]any `json:"_meta"`
					} `json:"params"`
				}
				assert.NoError(t, json.Unmarshal(body, &msg))
				gotMeta = msg.Params.Meta
			})
			cfg := mcppkg.PluginServerConfig{PluginID: legacyPluginID, Name: "Cloud", Path: "/mcp", Enabled: true, ExposeExternal: true}
			_, handlers, err := BuildProxyTools(context.Background(), cfg, sourceAPI)
			require.NoError(t, err)
			require.Len(t, handlers, 1)

			callerMeta := maps.Clone(tt.meta)
			ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user1")
			result, err := handlers[0](ctx, &gosdkmcp.CallToolRequest{Params: &gosdkmcp.CallToolParamsRaw{
				Meta:      callerMeta,
				Name:      legacyEchoToolName,
				Arguments: json.RawMessage(`{"message":"hi"}`),
			}})
			require.NoError(t, err)
			require.False(t, result.IsError)

			if tt.wantMeta == nil {
				require.Empty(t, gotMeta)
			} else {
				require.Equal(t, tt.wantMeta, gotMeta)
			}
			require.Equal(t, tt.meta, callerMeta, "the caller's _meta must not be modified")
		})
	}
}

type unrelatedContextKey struct{}

// The proxied call drops the caller's context values but keeps its deadline
// and trace span, so the source plugin call stays bounded and correlated.
func TestBuildProxyTools_HandlerCarriesDeadlineAndSpanOnly(t *testing.T) {
	var (
		gotDeadline    time.Time
		gotHasDeadline bool
		gotSpan        trace.SpanContext
		gotUnrelated   any
	)
	sourceAPI := proxyToolCallObserver(t, func(req *http.Request) {
		gotDeadline, gotHasDeadline = req.Context().Deadline()
		gotSpan = trace.SpanContextFromContext(req.Context())
		gotUnrelated = req.Context().Value(unrelatedContextKey{})
	})
	cfg := mcppkg.PluginServerConfig{PluginID: legacyPluginID, Name: "Cloud", Path: "/mcp", Enabled: true, ExposeExternal: true}
	_, handlers, err := BuildProxyTools(context.Background(), cfg, sourceAPI)
	require.NoError(t, err)
	require.Len(t, handlers, 1)

	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3},
		SpanID:     trace.SpanID{4, 5, 6},
		TraceFlags: trace.FlagsSampled,
	})
	deadline := time.Now().Add(time.Minute)
	ctx := trace.ContextWithSpanContext(context.Background(), span)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, "user1")
	ctx = context.WithValue(ctx, unrelatedContextKey{}, "inbound value")
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	result, err := handlers[0](ctx, &gosdkmcp.CallToolRequest{Params: &gosdkmcp.CallToolParamsRaw{
		Name:      legacyEchoToolName,
		Arguments: json.RawMessage(`{"message":"hi"}`),
	}})
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.True(t, gotHasDeadline, "the source plugin call should keep the caller's deadline")
	require.True(t, deadline.Equal(gotDeadline), "want deadline %v, got %v", deadline, gotDeadline)
	require.Equal(t, span.TraceID(), gotSpan.TraceID())
	require.Equal(t, span.SpanID(), gotSpan.SpanID())
	require.Nil(t, gotUnrelated, "caller context values must not reach the source plugin")
}

func TestBuildProxyTools_CancelingCallerCancelsProxiedCall(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	sourceAPI := proxyToolCallObserver(t, func(req *http.Request) {
		close(started)
		<-req.Context().Done()
		close(canceled)
	})
	cfg := mcppkg.PluginServerConfig{PluginID: legacyPluginID, Name: "Cloud", Path: "/mcp", Enabled: true, ExposeExternal: true}
	_, handlers, err := BuildProxyTools(context.Background(), cfg, sourceAPI)
	require.NoError(t, err)
	require.Len(t, handlers, 1)

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), auth.UserIDContextKey, "user1"))
	defer cancel()
	callErr := make(chan error, 1)
	go func() {
		_, err := handlers[0](ctx, &gosdkmcp.CallToolRequest{Params: &gosdkmcp.CallToolParamsRaw{
			Name:      legacyEchoToolName,
			Arguments: json.RawMessage(`{"message":"hi"}`),
		}})
		callErr <- err
	}()

	select {
	case <-started:
	case err := <-callErr:
		require.FailNow(t, "proxied call ended before reaching the source plugin", "error: %v", err)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "timed out waiting for the proxied call to reach the source plugin")
	}

	cancel()

	select {
	case <-canceled:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "canceling the caller's context should cancel the proxied call")
	}
	require.ErrorIs(t, <-callErr, context.Canceled)
}
