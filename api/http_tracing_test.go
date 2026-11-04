// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestHTTPTracing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		method    string
		path      string
		pluginID  string
		wantSpan  string // empty: no span expected
		wantAttrs map[string]string
	}{
		{
			name:     "bridge completion names the caller and agent",
			method:   http.MethodPost,
			path:     "/bridge/v1/completion/agent/agent1",
			pluginID: "com.example.caller",
			wantSpan: "POST /bridge/v1/completion/agent/:agent",
			wantAttrs: map[string]string{
				string(telemetry.BridgePluginID): "com.example.caller",
				string(telemetry.AgentID):        "agent1",
			},
		},
		{
			name:     "bridge service completion names the service",
			method:   http.MethodPost,
			path:     "/bridge/v1/completion/service/svc1",
			pluginID: "com.example.caller",
			wantSpan: "POST /bridge/v1/completion/service/:service",
			wantAttrs: map[string]string{
				string(telemetry.BridgePluginID): "com.example.caller",
				string(telemetry.ServiceID):      "svc1",
			},
		},
		{name: "mcp tool call POST is traced", method: http.MethodPost, path: "/mcp-server/mcp", wantSpan: "POST /mcp-server/mcp"},
		{name: "mcp stream GET is not traced", method: http.MethodGet, path: "/mcp-server/mcp"},
		{name: "unmatched route is not traced", method: http.MethodPost, path: "/no/such/route"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			previous := otel.GetTracerProvider()
			exporter := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			otel.SetTracerProvider(tp)
			t.Cleanup(func() {
				_ = tp.Shutdown(context.Background())
				otel.SetTracerProvider(previous)
			})

			a := &API{}
			ok := func(c *gin.Context) { c.Status(http.StatusOK) }
			router := gin.New()
			router.Use(httpTracingMiddleware())
			bridge := router.Group("/bridge/v1")
			bridge.Use(a.interPluginAuthorizationRequired)
			bridge.POST("/completion/agent/:agent", ok)
			bridge.POST("/completion/service/:service", ok)
			router.Any("/mcp-server/mcp", ok)

			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.pluginID != "" {
				req.Header.Set("Mattermost-Plugin-ID", tc.pluginID)
			}
			router.ServeHTTP(httptest.NewRecorder(), req)

			spans := exporter.GetSpans()
			if tc.wantSpan == "" {
				require.Empty(t, spans)
				return
			}
			require.Len(t, spans, 1)
			require.Equal(t, tc.wantSpan, spans[0].Name)
			attrs := map[string]string{}
			for _, kv := range spans[0].Attributes {
				attrs[string(kv.Key)] = kv.Value.AsString()
			}
			for k, v := range tc.wantAttrs {
				require.Equal(t, v, attrs[k], k)
			}
		})
	}
}
