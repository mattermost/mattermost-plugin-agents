// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/mattermost/mattermost-plugin-agents/telemetry"
)

// TestDetachContext_NoCancelPropagation guards the merge bug where API handlers
// passed c.Request.Context() into StreamToNewDM. GetStreamingContext does
// context.WithCancel(inCtx), so request-context cancellation propagates into
// the streaming goroutine and the user sees truncated DMs as soon as the HTTP
// handler returns.
func TestDetachContext_NoCancelPropagation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	detached := telemetry.DetachContext(parent)
	cancel()

	select {
	case <-detached.Done():
		t.Fatal("detached context must not be canceled when its source is canceled")
	default:
	}
	require.NoError(t, detached.Err(), "detached context must not carry the parent's cancellation error")
}

// TestDetachContext_PreservesSpan ensures we can still create child spans on
// the detached context that nest under the original trace, so async work
// continues to show up in the same trace as the request that triggered it.
func TestDetachContext_PreservesSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})

	parent, parentSpan := telemetry.Tracer().Start(context.Background(), "parent")
	parentTraceID := parentSpan.SpanContext().TraceID()

	detached := telemetry.DetachContext(parent)
	_, childSpan := telemetry.Tracer().Start(detached, "child")
	require.Equal(t, parentTraceID, childSpan.SpanContext().TraceID(),
		"child span on detached context should share the parent trace")

	childSpan.End()
	parentSpan.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)

	var parentRecorded, childRecorded *tracetest.SpanStub
	for i := range spans {
		switch spans[i].Name {
		case "parent":
			parentRecorded = &spans[i]
		case "child":
			childRecorded = &spans[i]
		}
	}
	require.NotNil(t, parentRecorded)
	require.NotNil(t, childRecorded)
	require.Equal(t, parentRecorded.SpanContext.SpanID(), childRecorded.Parent.SpanID(),
		"child span's parent should be the original parent span, not a new root")
}
