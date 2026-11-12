// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	bschemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

func setupTracerProvider(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		tp.Shutdown(context.Background()) //nolint:errcheck
		otel.SetTracerProvider(prev)
	})
	return exporter
}

func TestOTelTracer_SpanLifecycleEmitsOTelSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	tracer := newOTelTracer()

	ctx, handle := tracer.StartSpan(context.Background(), "bifrost provider call", bschemas.SpanKindLLMCall)
	require.NotNil(t, handle)
	require.NotNil(t, ctx)

	tracer.SetAttribute(handle, "gen_ai.provider.name", "openai")
	tracer.AddEvent(handle, "first_token", map[string]any{"latency_ms": int64(120)})
	tracer.EndSpan(handle, bschemas.SpanStatusOk, "")

	spans := exporter.GetSpans()
	require.Len(t, spans, 1, "expected one exported span")
	span := spans[0]
	assert.Equal(t, "bifrost provider call", span.Name)

	var foundProvider, foundKind bool
	for _, a := range span.Attributes {
		switch string(a.Key) {
		case "gen_ai.provider.name":
			assert.Equal(t, "openai", a.Value.AsString())
			foundProvider = true
		case "bifrost.span.kind":
			assert.Equal(t, string(bschemas.SpanKindLLMCall), a.Value.AsString())
			foundKind = true
		}
	}
	assert.True(t, foundProvider, "expected gen_ai.provider.name attribute")
	assert.True(t, foundKind, "expected bifrost.span.kind attribute")

	require.Len(t, span.Events, 1)
	assert.Equal(t, "first_token", span.Events[0].Name)
}

func TestOTelTracer_PopulateLLMResponseAttributesGatesZeroRichUsage(t *testing.T) {
	// Input/output token attributes always emit. Cached / reasoning / cost
	// attributes are gated behind a > 0 check so spans from providers that
	// don't expose them stay clean.
	exporter := setupTracerProvider(t)
	tracer := newOTelTracer()

	_, handle := tracer.StartSpan(context.Background(), "bifrost call", bschemas.SpanKindLLMCall)
	tracer.PopulateLLMResponseAttributes(nil, handle, &bschemas.BifrostResponse{
		ChatResponse: &bschemas.BifrostChatResponse{
			Usage: &bschemas.BifrostLLMUsage{
				PromptTokens:     1200,
				CompletionTokens: 350,
				// No PromptTokensDetails / CompletionTokensDetails / Cost.
			},
		},
	}, nil)
	tracer.EndSpan(handle, bschemas.SpanStatusOk, "")

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	attrs := map[string]any{}
	for _, a := range spans[0].Attributes {
		attrs[string(a.Key)] = a.Value.AsInterface()
	}

	assert.Equal(t, int64(1200), attrs["agents.llm.input_tokens"])
	assert.Equal(t, int64(350), attrs["agents.llm.output_tokens"])
	for _, key := range []string{
		"agents.llm.cached_read_tokens",
		"agents.llm.cached_write_tokens",
		"agents.llm.reasoning_tokens",
		"agents.llm.cost",
	} {
		_, present := attrs[key]
		assert.False(t, present, "%s must not be emitted when the provider reports zero", key)
	}
}

func TestOTelTracer_ErrorStatusPropagates(t *testing.T) {
	exporter := setupTracerProvider(t)
	tracer := newOTelTracer()

	_, handle := tracer.StartSpan(context.Background(), "bifrost call", bschemas.SpanKindLLMCall)
	tracer.EndSpan(handle, bschemas.SpanStatusError, "rate limited")

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	// Error status maps to OTel codes.Error == 1
	assert.Equal(t, uint32(1), uint32(spans[0].Status.Code))
	assert.Equal(t, "rate limited", spans[0].Status.Description)
}

func TestOTelTracer_DeferredSpanRoundTrip(t *testing.T) {
	tracer := newOTelTracer().(*otelTracer)

	_, handle := tracer.StartSpan(context.Background(), "stream", bschemas.SpanKindLLMCall)
	tracer.StoreDeferredSpan("trace-1", handle)

	got := tracer.GetDeferredSpanHandle("trace-1")
	assert.Equal(t, handle, got)

	id := tracer.GetDeferredSpanID("trace-1")
	assert.NotEmpty(t, id, "deferred span ID should be the OTel SpanID hex")

	tracer.ClearDeferredSpan("trace-1")
	assert.Nil(t, tracer.GetDeferredSpanHandle("trace-1"))

	tracer.EndSpan(handle, bschemas.SpanStatusOk, "")
}

func TestOTelTracer_StreamAccumulatorTracksTTFTAndCount(t *testing.T) {
	tracer := newOTelTracer().(*otelTracer)
	start := time.Now().Add(-50 * time.Millisecond)
	tracer.CreateStreamAccumulator("trace-2", start)

	tracer.AddStreamingChunk("trace-2", &bschemas.BifrostResponse{})
	tracer.AddStreamingChunk("trace-2", &bschemas.BifrostResponse{})

	resp, ttft, count := tracer.GetAccumulatedChunks("trace-2")
	assert.NotNil(t, resp, "should expose the latest response")
	assert.Equal(t, 2, count)
	assert.Greater(t, ttft, int64(0), "TTFT should be set after first chunk")

	tracer.CleanupStreamAccumulator("trace-2")
	_, _, count = tracer.GetAccumulatedChunks("trace-2")
	assert.Equal(t, 0, count, "cleanup clears state")
}

func TestOTelTracer_ProcessStreamingChunkOnlyReturnsOnFinal(t *testing.T) {
	tracer := newOTelTracer().(*otelTracer)
	tracer.CreateStreamAccumulator("trace-3", time.Now())

	intermediate := tracer.ProcessStreamingChunk(nil, "trace-3", false, &bschemas.BifrostResponse{}, nil)
	assert.Nil(t, intermediate, "non-final chunks should not synthesize a result")

	final := tracer.ProcessStreamingChunk(nil, "trace-3", true, &bschemas.BifrostResponse{}, nil)
	require.NotNil(t, final, "final chunk should produce an accumulator result")
	assert.Equal(t, "completed", final.Status)
}

func TestOTelTracer_ProcessStreamingChunkRoutingInfo(t *testing.T) {
	tests := []struct {
		name          string
		routing       bschemas.RoutingInfo
		wantRequested string
		wantResolved  string
		wantProvider  bschemas.ModelProvider
	}{
		{
			name: "normal routing",
			routing: bschemas.RoutingInfo{
				Provider: bschemas.Anthropic,
				Model:    "claude-opus-5",
			},
			wantRequested: "claude-opus-5",
			wantResolved:  "claude-opus-5",
			wantProvider:  bschemas.Anthropic,
		},
		{
			name: "fallback keeps the caller's primary model as requested",
			routing: bschemas.RoutingInfo{
				Provider:     bschemas.OpenAI,
				Model:        "gpt-4o",
				IsFallback:   true,
				PrimaryModel: new("claude-opus-5"),
			},
			wantRequested: "claude-opus-5",
			wantResolved:  "gpt-4o",
			wantProvider:  bschemas.OpenAI,
		},
		{
			name: "key alias resolves to the wire model",
			routing: bschemas.RoutingInfo{
				Provider: bschemas.Anthropic,
				Model:    "my-alias",
				ResolvedKeyAlias: &bschemas.ResolvedKeyAlias{
					ModelID: "claude-sonnet-5",
				},
			},
			wantRequested: "my-alias",
			wantResolved:  "claude-sonnet-5",
			wantProvider:  bschemas.Anthropic,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracer := newOTelTracer().(*otelTracer)
			traceID := "req-" + tt.name
			tracer.CreateStreamAccumulator(traceID, time.Now())

			resp := &bschemas.BifrostResponse{
				ChatResponse: &bschemas.BifrostChatResponse{
					ExtraFields: bschemas.BifrostResponseExtraFields{
						RoutingInfo: tt.routing,
					},
				},
			}
			result := tracer.ProcessStreamingChunk(nil, traceID, true, resp, nil)
			require.NotNil(t, result)
			assert.Equal(t, traceID, result.RequestID, "RequestID must carry the request identifier, not a model name")
			assert.Equal(t, tt.wantRequested, result.RequestedModel)
			assert.Equal(t, tt.wantResolved, result.ResolvedModel)
			assert.Equal(t, tt.wantProvider, result.Provider)
		})
	}
}

func TestOTelTracer_NilHandleIsSafe(t *testing.T) {
	tracer := newOTelTracer()
	// Calls with nil/wrong-typed handles must be no-ops, not panics.
	tracer.SetAttribute(nil, "k", "v")
	tracer.AddEvent(nil, "evt", nil)
	tracer.EndSpan(nil, bschemas.SpanStatusOk, "")
	tracer.PopulateLLMRequestAttributes(nil, &bschemas.BifrostRequest{})
	tracer.PopulateLLMResponseAttributes(nil, nil, nil, nil)
}

// TestProviderCallEmitsSpan drives a request through Bifrost and checks its
// provider call and plugin hooks reach the exporter.
func TestProviderCallEmitsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","model":"text-embedding-3-small","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`)
	}))
	defer backend.Close()

	provider, err := NewEmbeddingProvider(EmbeddingConfig{
		Provider: bschemas.OpenAI,
		APIKey:   "test-key",
		APIURL:   backend.URL,
		Model:    "text-embedding-3-small",
	})
	require.NoError(t, err)
	defer provider.Shutdown()

	_, err = provider.CreateEmbedding(context.Background(), "hi")
	require.NoError(t, err)

	kinds := map[string][]tracetest.SpanStub{}
	for _, span := range exporter.GetSpans() {
		attrs := attribute.NewSet(span.Attributes...)
		kind, _ := attrs.Value("bifrost.span.kind")
		kinds[kind.AsString()] = append(kinds[kind.AsString()], span)
	}
	require.Len(t, kinds[string(bschemas.SpanKindLLMCall)], 1)
	callAttrs := attribute.NewSet(kinds[string(bschemas.SpanKindLLMCall)][0].Attributes...)
	model, _ := callAttrs.Value(telemetry.LLMModel)
	assert.Equal(t, "text-embedding-3-small", model.AsString())
	assert.NotEmpty(t, kinds[string(bschemas.SpanKindPlugin)], "plugin hook spans")
	for _, span := range kinds[string(bschemas.SpanKindInternal)] {
		assert.NotEqual(t, "attribute-population", span.Name)
	}
}
