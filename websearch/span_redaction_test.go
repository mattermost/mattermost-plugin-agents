// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const (
	sentinelQuery = "SENTINEL-QUERY-7f3a"
	sentinelKey   = "SENTINEL-KEY-91bc"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type captureLogger struct {
	mu   sync.Mutex
	logs []string
}

func (l *captureLogger) record(message string, kv ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, message+" "+fmt.Sprint(kv...))
}
func (l *captureLogger) Debug(message string, kv ...any) { l.record(message, kv...) }
func (l *captureLogger) Info(message string, kv ...any)  { l.record(message, kv...) }
func (l *captureLogger) Warn(message string, kv ...any)  { l.record(message, kv...) }
func (l *captureLogger) Error(message string, kv ...any) { l.record(message, kv...) }

func TestSearchTransportErrorsDoNotLeakIntoSpansOrLogs(t *testing.T) {
	failingClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	tests := []struct {
		name   string
		search func(l Logger) (*SearchResponse, error)
	}{
		{
			name: "google",
			search: func(l Logger) (*SearchResponse, error) {
				return NewGoogleProvider(sentinelKey, "cx", "https://example.invalid/customsearch", failingClient, l).
					Search(context.Background(), sentinelQuery, 5)
			},
		},
		{
			name: "brave",
			search: func(l Logger) (*SearchResponse, error) {
				return NewBraveProvider(sentinelKey, "https://example.invalid", 1, 1, failingClient, l).
					Search(context.Background(), sentinelQuery, 5)
			},
		},
		{
			name: "searxng",
			search: func(l Logger) (*SearchResponse, error) {
				return NewSearXNGProvider("https://example.invalid", failingClient, l).
					Search(context.Background(), sentinelQuery, 5)
			},
		},
		{
			name: "serply",
			search: func(l Logger) (*SearchResponse, error) {
				return NewSerplyProvider(sentinelKey, "https://example.invalid/v1/search", failingClient, l).
					Search(context.Background(), sentinelQuery, 5)
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

			logger := &captureLogger{}
			_, err := tc.search(logger)
			require.Error(t, err)
			for _, secret := range []string{sentinelQuery, sentinelKey} {
				assert.NotContains(t, err.Error(), secret)
			}
			var urlErr *url.Error
			require.ErrorAs(t, err, &urlErr)

			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			assert.NotEmpty(t, spans[0].Events, "failure should still be recorded on the span")

			spanJSON, jsonErr := json.Marshal(spans[0])
			require.NoError(t, jsonErr)
			for _, secret := range []string{sentinelQuery, sentinelKey} {
				assert.NotContains(t, string(spanJSON), secret)
				assert.NotContains(t, strings.Join(logger.logs, "\n"), secret)
			}
		})
	}
}
