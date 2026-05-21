// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/telemetry"
)

// installTurnIDProvider builds a TracerProvider whose IDGenerator routes
// turn-tagged contexts to deterministic IDs and everything else to the
// SDK's random generator. Returns the in-memory exporter.
func installTurnIDProvider(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithIDGenerator(telemetry.NewTurnIDGenerator()),
	)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})
	return exporter
}

func TestTurnIDGenerator_DeterministicForTurnID(t *testing.T) {
	exporter := installTurnIDProvider(t)

	const turnID = "user-turn-abc123"
	ctx := telemetry.WithTurnID(context.Background(), turnID)

	_, span1 := telemetry.Tracer().Start(ctx, "run 1", trace.WithNewRoot())
	span1.End()
	_, span2 := telemetry.Tracer().Start(ctx, "run 2", trace.WithNewRoot())
	span2.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	assert.Equal(t, spans[0].SpanContext.TraceID(), spans[1].SpanContext.TraceID(),
		"the same turn ID must map to the same TraceID across calls")
}

func TestTurnIDGenerator_DifferentTurnsDifferentTraces(t *testing.T) {
	exporter := installTurnIDProvider(t)

	ctxA := telemetry.WithTurnID(context.Background(), "turn-A")
	ctxB := telemetry.WithTurnID(context.Background(), "turn-B")

	_, span1 := telemetry.Tracer().Start(ctxA, "A", trace.WithNewRoot())
	span1.End()
	_, span2 := telemetry.Tracer().Start(ctxB, "B", trace.WithNewRoot())
	span2.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	assert.NotEqual(t, spans[0].SpanContext.TraceID(), spans[1].SpanContext.TraceID(),
		"different turn IDs must produce different TraceIDs")
}

func TestTurnIDGenerator_FallsBackToRandom(t *testing.T) {
	exporter := installTurnIDProvider(t)

	// No WithTurnID — the generator should produce random IDs that vary.
	_, span1 := telemetry.Tracer().Start(context.Background(), "x", trace.WithNewRoot())
	span1.End()
	_, span2 := telemetry.Tracer().Start(context.Background(), "y", trace.WithNewRoot())
	span2.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	assert.NotEqual(t, spans[0].SpanContext.TraceID(), spans[1].SpanContext.TraceID(),
		"contexts without WithTurnID must use random TraceIDs")
}

func TestTurnIDGenerator_EmptyTurnIDFallsBack(t *testing.T) {
	exporter := installTurnIDProvider(t)

	// WithTurnID("") must not deterministically map all empty-key contexts
	// to the same trace — that would collide all "no turn" runs.
	ctx := telemetry.WithTurnID(context.Background(), "")
	_, span1 := telemetry.Tracer().Start(ctx, "x", trace.WithNewRoot())
	span1.End()
	_, span2 := telemetry.Tracer().Start(ctx, "y", trace.WithNewRoot())
	span2.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	assert.NotEqual(t, spans[0].SpanContext.TraceID(), spans[1].SpanContext.TraceID(),
		"empty turn IDs must fall back to random IDs, not collide")
}

func TestSpanContextForTurn_ValidAndMatchesGeneratedTraceID(t *testing.T) {
	exporter := installTurnIDProvider(t)

	const turnID = "user-turn-xyz"
	sc := telemetry.SpanContextForTurn(turnID)
	require.True(t, sc.IsValid(), "SpanContextForTurn must produce a valid SpanContext")

	// A span started with WithTurnID(turnID) + WithNewRoot must end up in
	// the same TraceID as SpanContextForTurn returns — that's the whole
	// point: a link to that SpanContext jumps to the run's trace.
	ctx := telemetry.WithTurnID(context.Background(), turnID)
	_, span := telemetry.Tracer().Start(ctx, "run", trace.WithNewRoot())
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, sc.TraceID(), spans[0].SpanContext.TraceID(),
		"SpanContextForTurn TraceID must match the generator's output for the same turn ID")
}

func TestWithNewRoot_UsesDerivedTraceIDFromCtxKey(t *testing.T) {
	exporter := installTurnIDProvider(t)

	// Even when ctx already carries a parent span (e.g. otelgin), WithNewRoot
	// must drop that parent and the IDGenerator must produce the derived
	// TraceID. This is the resume case (HandleToolCall on node B).
	parentCtx, parentSpan := telemetry.Tracer().Start(context.Background(), "parent")
	defer parentSpan.End()

	const turnID = "resume-turn"
	resumeCtx := telemetry.WithTurnID(parentCtx, turnID)
	_, resumeSpan := telemetry.Tracer().Start(resumeCtx, "resume", trace.WithNewRoot())
	resumeSpan.End()

	expected := telemetry.SpanContextForTurn(turnID).TraceID()
	var resumeRecorded *tracetest.SpanStub
	for i, s := range exporter.GetSpans() {
		if s.Name == "resume" {
			resumeRecorded = &exporter.GetSpans()[i]
			break
		}
	}
	require.NotNil(t, resumeRecorded)
	assert.Equal(t, expected, resumeRecorded.SpanContext.TraceID(),
		"WithNewRoot must use the derived TraceID even with an existing parent span in ctx")
	assert.NotEqual(t, parentSpan.SpanContext().TraceID(), resumeRecorded.SpanContext.TraceID(),
		"the parent span's TraceID must not be inherited when WithNewRoot is set")
}
