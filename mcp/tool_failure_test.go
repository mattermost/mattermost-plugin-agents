// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// debugCaptureAPI records every debug log line so a test can assert on what the
// client logged.
type debugCaptureAPI struct {
	fixedPluginAPI
	mu    sync.Mutex
	lines []map[string]any
}

func (a *debugCaptureAPI) LogDebug(message string, keyValuePairs ...any) {
	line := map[string]any{"message": message}
	for i := 0; i+1 < len(keyValuePairs); i += 2 {
		line[fmt.Sprint(keyValuePairs[i])] = keyValuePairs[i+1]
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lines = append(a.lines, line)
}

func (a *debugCaptureAPI) failureLines() []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []map[string]any
	for _, line := range a.lines {
		if line["message"] == "MCP tool call failed" {
			out = append(out, line)
		}
	}
	return out
}

func (a *debugCaptureAPI) everything(t *testing.T) string {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, err := json.Marshal(a.lines)
	require.NoError(t, err)
	return string(raw)
}

func TestToolCallFailureIsLoggedAndTraced(t *testing.T) {
	const (
		argSentinel      = "SENTINEL-ARGUMENT-4f1c"
		upstreamSentinel = "SENTINEL-UPSTREAM-9b7e"
	)

	tests := []struct {
		name string
		// httpStatus, when set, rejects the tools/call request at the HTTP layer
		// with upstreamSentinel as the response body.
		httpStatus int
		// toolResult and toolError are what the tool handler produces otherwise.
		toolResult *mcp.CallToolResult
		toolError  error
		oauth      bool
		canceled   bool

		wantClass string
		wantCode  int
	}{
		{
			name:       "upstream server error",
			httpStatus: http.StatusInternalServerError,
			wantClass:  toolFailureHTTPStatus,
			wantCode:   http.StatusInternalServerError,
		},
		{
			name:       "upstream forbidden",
			httpStatus: http.StatusForbidden,
			wantClass:  toolFailureHTTPStatus,
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "authorization lapsed",
			httpStatus: http.StatusUnauthorized,
			oauth:      true,
			wantClass:  toolFailureUnauthorized,
		},
		{
			name:      "json-rpc error from the server",
			toolError: errors.New(upstreamSentinel),
			wantClass: toolFailureJSONRPC,
			wantCode:  0,
		},
		{
			name: "tool reports isError",
			toolResult: &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "boom " + upstreamSentinel}},
			},
			wantClass: toolFailureReturnedError,
		},
		{
			name:       "caller context canceled",
			canceled:   true,
			toolResult: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}},
			wantClass:  toolFailureContextCanceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := otel.GetTracerProvider()
			exporter := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			otel.SetTracerProvider(tp)
			t.Cleanup(func() {
				_ = tp.Shutdown(context.Background())
				otel.SetTracerProvider(previous)
			})

			server := mcp.NewServer(&mcp.Implementation{Name: "tickets", Version: "1.0"}, nil)
			server.AddTool(&mcp.Tool{
				Name:        "search",
				InputSchema: map[string]any{"type": "object"},
			}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return tt.toolResult, tt.toolError
			})
			streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				if tt.httpStatus != 0 && bytes.Contains(body, []byte(`"method":"tools/call"`)) {
					if tt.httpStatus == http.StatusUnauthorized {
						w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
					}
					w.WriteHeader(tt.httpStatus)
					_, _ = w.Write([]byte(upstreamSentinel))
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				streamable.ServeHTTP(w, r)
			}))
			t.Cleanup(httpServer.Close)

			logAPI := &debugCaptureAPI{}
			logService := pluginapi.NewClient(logAPI, nil).Log

			serverConfig := ServerConfig{
				Name:        "Tickets",
				BaseURL:     httpServer.URL,
				Enabled:     true,
				ToolConfigs: []ToolConfig{{Name: "search", Policy: ToolPolicyAsk, Enabled: true}},
			}
			params := clientParams{log: logService, httpClient: httpServer.Client(), serviceAccount: !tt.oauth}
			oauthManager, _ := newStatefulKVManager(t, nil, http.DefaultClient)
			oauthManager.callbackURL = "https://mattermost.example.com/plugins/mattermost-ai/oauth/callback"
			if tt.oauth {
				params.oauthManager = oauthManager
			}
			client, err := newClient(context.Background(), "alice", serverConfig, params)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			uc := NewUserClients("alice", logService, oauthManager, httpServer.Client(), nil)
			uc.clients[serverConfig.Name] = client

			ctx := context.Background()
			if tt.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			resolver := uc.createToolResolver(client, "search")
			_, err = resolver(ctx, llm.NewContext(), func(args any) error {
				target, ok := args.(*map[string]any)
				require.True(t, ok)
				*target = map[string]any{"query": argSentinel}
				return nil
			})
			require.Error(t, err)

			// The log line: one record, with the classification and identifiers.
			lines := logAPI.failureLines()
			require.Len(t, lines, 1)
			line := lines[0]
			assert.Equal(t, "alice", line["userID"])
			assert.Equal(t, "Tickets", line["serverID"])
			assert.Equal(t, "search", line["tool"])
			assert.Equal(t, tt.wantClass, line["errorClass"])
			if tt.wantCode != 0 {
				assert.EqualValues(t, tt.wantCode, line["errorCode"])
			} else {
				assert.NotContains(t, line, "errorCode")
			}

			// The span: error status and event, with the classification.
			spans := exporter.GetSpans()
			var callSpan *tracetest.SpanStub
			for i := range spans {
				if spans[i].Name == "mcp call tool" {
					callSpan = &spans[i]
				}
			}
			require.NotNil(t, callSpan, "the tool call must be traced")
			assert.Equal(t, codes.Error, callSpan.Status.Code)
			assert.Equal(t, tt.wantClass, callSpan.Status.Description)
			require.Len(t, callSpan.Events, 1)
			assert.Equal(t, "exception", callSpan.Events[0].Name)
			attrs := map[string]any{}
			for _, a := range callSpan.Attributes {
				attrs[string(a.Key)] = a.Value.AsInterface()
			}
			assert.Equal(t, tt.wantClass, attrs[string(telemetry.MCPErrorClass)])
			if tt.wantCode != 0 {
				assert.EqualValues(t, tt.wantCode, attrs[string(telemetry.MCPErrorCode)])
			}

			// Nothing from the request or the upstream response may leak into
			// either sink.
			spanDump, err := json.Marshal(callSpan)
			require.NoError(t, err)
			for _, sink := range []string{logAPI.everything(t), string(spanDump)} {
				assert.NotContains(t, sink, argSentinel)
				assert.NotContains(t, sink, upstreamSentinel)
				assert.False(t, strings.Contains(sink, "Bearer"), "headers must not be logged")
			}
		})
	}
}

func TestClassifyToolCallError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want toolFailure
	}{
		{name: "deadline", err: fmt.Errorf("call: %w", context.DeadlineExceeded), want: toolFailure{class: toolFailureDeadlineExceeded}},
		{name: "closed connection", err: fmt.Errorf("call: %w", mcp.ErrConnectionClosed), want: toolFailure{class: toolFailureConnectionClosed}},
		{name: "http status phrase", err: errors.New("failed to call tool x on server y: sending \"tools/call\": Bad Gateway"), want: toolFailure{class: toolFailureHTTPStatus, code: http.StatusBadGateway}},
		{name: "unknown names only the type", err: fmt.Errorf("wrapped: %w", errors.New("secret body")), want: toolFailure{class: toolFailureUnknown, errType: "*errors.errorString"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, *classifyToolCallError(tt.err))
		})
	}
}

func TestLoggableToolNameBoundsServerControlledNames(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "search", want: "search"},
		{name: "long", in: strings.Repeat("a", 500), want: strings.Repeat("a", maxLoggedToolNameLen)},
		{name: "control characters", in: "a\nb", want: llm.SanitizeNonPrintableChars("a\nb")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, loggableToolName(tt.in))
		})
	}
}
