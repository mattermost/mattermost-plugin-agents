// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const (
	testInitializeMethod = "initialize"
	testDiscoverMethod   = "server/discover"
)

// simulateLegacy20241105Server makes a go-sdk server behave like a real
// 2024-11-05-only MCP server: server/discover (introduced in 2026-07-28) is
// unknown, and the initialize handshake always answers with protocol version
// 2024-11-05 regardless of what the client requested.
func simulateLegacy20241105Server(server *mcp.Server) {
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == testDiscoverMethod {
				return nil, fmt.Errorf("method not found: %s", method)
			}
			result, err := next(ctx, method, req)
			if err == nil && method == testInitializeMethod {
				if initResult, ok := result.(*mcp.InitializeResult); ok {
					initResult.ProtocolVersion = "2024-11-05"
				}
			}
			return result, err
		}
	})
}

// TestNewClientCrossProtocolVersionCompat verifies that NewClient connects,
// discovers tools, and calls tools against in-process go-sdk servers speaking
// different MCP protocol versions, and that the expected protocol version is
// negotiated in each configuration.
func TestNewClientCrossProtocolVersionCompat(t *testing.T) {
	const toolName = "compat_tool"

	tests := []struct {
		name            string
		configureServer func(*mcp.Server)
		handler         func(*mcp.Server) http.Handler
		expectedVersion string
	}{
		{
			// The stateless streamable transport supports SEP-2575
			// server/discover, so the newest protocol version is negotiated.
			name: "streamable stateless negotiates 2026-07-28",
			handler: func(server *mcp.Server) http.Handler {
				return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
					return server
				}, &mcp.StreamableHTTPOptions{Stateless: true})
			},
			expectedVersion: "2026-07-28",
		},
		{
			// Stateful streamable transports do not serve protocol versions
			// >= 2026-07-28, so the SDK client's server/discover probe fails
			// and it falls back to the legacy initialize handshake. Seeing
			// 2025-11-25 therefore also proves the fallback path ran: the
			// discover path only ever negotiates versions >= 2026-07-28.
			name: "streamable stateful falls back to legacy initialize and negotiates 2025-11-25",
			handler: func(server *mcp.Server) http.Handler {
				return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
					return server
				}, nil)
			},
			expectedVersion: "2025-11-25",
		},
		{
			// The SSE handler rejects the streamable transport's bare POSTs,
			// so NewClient must succeed through its SSE fallback. The go-sdk
			// SSE server still answers server/discover over the SSE stream
			// and does not filter protocol versions by transport, so the SDK
			// negotiates the newest version even on this legacy transport.
			name: "http+sse server connects via SSE fallback",
			handler: func(server *mcp.Server) http.Handler {
				return mcp.NewSSEHandler(func(*http.Request) *mcp.Server {
					return server
				}, nil)
			},
			expectedVersion: "2026-07-28",
		},
		{
			// A real 2024-11-05 server knows nothing about server/discover
			// and answers initialize with its own (old) protocol version. Our
			// client must still connect via the SSE fallback and end up on
			// the legacy version.
			name:            "2024-11-05-only http+sse server negotiates 2024-11-05",
			configureServer: simulateLegacy20241105Server,
			handler: func(server *mcp.Server) http.Handler {
				return mcp.NewSSEHandler(func(*http.Request) *mcp.Server {
					return server
				}, nil)
			},
			expectedVersion: "2024-11-05",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newTestMCPServer(0, toolName)
			if tt.configureServer != nil {
				tt.configureServer(server)
			}
			httpServer := httptest.NewServer(tt.handler(server))
			t.Cleanup(httpServer.Close)

			client, err := NewClient(context.Background(), "user-id", ServerConfig{
				Name:    "compat",
				BaseURL: httpServer.URL,
				Enabled: true,
			}, newTestLogService(), newTestOAuthManager(), httpServer.Client(), newTestToolsCache(), false)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			require.Contains(t, client.Tools(), toolName)

			result, err := client.CallTool(context.Background(), toolName, map[string]any{})
			require.NoError(t, err)
			require.Contains(t, result, toolName+" ok")

			initResult := client.session.InitializeResult()
			require.NotNil(t, initResult)
			require.Equal(t, tt.expectedVersion, initResult.ProtocolVersion)
			t.Logf("negotiated protocol version: %s", initResult.ProtocolVersion)
		})
	}
}
