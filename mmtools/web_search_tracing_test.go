// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const (
	tracingSentinelQuery = "SENTINEL-QUERY-7f3a"
	tracingSentinelKey   = "SENTINEL-KEY-91bc"
)

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

func TestWebSearchTransportErrorDoesNotLeakIntoToolSpans(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.WebSearchConfig
	}{
		{
			name: "google",
			cfg: config.WebSearchConfig{
				Enabled:  true,
				Provider: "google",
				Google:   config.WebSearchGoogleConfig{APIKey: tracingSentinelKey, SearchEngineID: "cx", APIURL: "https://example.invalid/customsearch"},
			},
		},
		{
			name: "brave",
			cfg: config.WebSearchConfig{
				Enabled:  true,
				Provider: "brave",
				Brave:    config.WebSearchBraveConfig{APIKey: tracingSentinelKey, APIURL: "https://example.invalid"},
			},
		},
		{
			name: "searxng",
			cfg: config.WebSearchConfig{
				Enabled:  true,
				Provider: "searxng",
				SearXNG:  config.WebSearchSearXNGConfig{BaseURL: "https://example.invalid"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			prev := otel.GetTracerProvider()
			otel.SetTracerProvider(tp)
			t.Cleanup(func() {
				_ = tp.Shutdown(context.Background())
				otel.SetTracerProvider(prev)
			})

			service := NewWebSearchService(
				func() *config.Config { return &config.Config{WebSearch: tc.cfg} },
				&mockLogger{},
				&http.Client{Transport: failingTransport{}},
				enterprisetest.CheckerAt(enterprise.LevelEnterprise),
			)
			tool := service.Tool()
			require.NotNil(t, tool)

			store := llm.NewToolStore()
			store.AddTools([]llm.Tool{*tool})

			_, err := store.ResolveToolCall(context.Background(), "WebSearch", llm.ToolCallInfo{ID: "call-1"},
				func(args any) error {
					a, ok := args.(*WebSearchToolArgs)
					require.True(t, ok)
					a.Query = tracingSentinelQuery
					return nil
				}, &llm.Context{Parameters: map[string]any{}})
			require.Error(t, err)

			spans := exporter.GetSpans()
			names := make([]string, 0, len(spans))
			for _, s := range spans {
				names = append(names, s.Name)
			}
			require.Contains(t, names, "resolve tool")

			for _, secret := range []string{tracingSentinelQuery, tracingSentinelKey} {
				assert.NotContains(t, err.Error(), secret)
				spanJSON, jsonErr := json.Marshal(spans)
				require.NoError(t, jsonErr)
				assert.NotContains(t, string(spanJSON), secret)
			}
		})
	}
}
