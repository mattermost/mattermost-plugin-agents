// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// A canceled reply stops reading mid-stream; releasing must still export the
// span and stop the forwarder instead of leaving it blocked on a send.
func TestEndSpanOnStreamCloseReleaseAfterAbandon(t *testing.T) {
	previous := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})

	upstream := make(chan llm.TextStreamEvent, 2)
	upstream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "a"}
	upstream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "b"}
	// The producer keeps going until it notices the cancel that release
	// triggers; it closes upstream below, after release.

	_, span := telemetry.Tracer().Start(context.Background(), "process dm request")
	wrapped, release := endSpanOnStreamClose(&llm.TextStreamResult{Stream: upstream}, span)

	<-wrapped.Stream // the consumer reads one event, then is canceled
	require.Empty(t, exporter.GetSpans(), "span must stay open while the reply streams")

	release()
	require.Len(t, exporter.GetSpans(), 1, "release must export the span at once")

	close(upstream) // the canceled producer finishes
	select {
	case _, open := <-wrapped.Stream:
		require.False(t, open, "nothing may be forwarded after release")
	case <-time.After(time.Second):
		require.FailNow(t, "forwarder still blocked after release")
	}
}

// Released while the forwarder waits on a producer that has gone quiet (a
// tool call in progress): the span must end now, not at the next event.
func TestEndSpanOnStreamCloseReleaseWhileUpstreamIdle(t *testing.T) {
	previous := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})

	upstream := make(chan llm.TextStreamEvent) // never sends, never closes
	_, span := telemetry.Tracer().Start(context.Background(), "process dm request")
	_, release := endSpanOnStreamClose(&llm.TextStreamResult{Stream: upstream}, span)

	release()

	require.Len(t, exporter.GetSpans(), 1)
}

// After release the forwarder must keep draining upstream: an earlier stage
// with a plain blocking send (the citation decorator) would otherwise hang
// forever once the forwarder stops reading.
func TestEndSpanOnStreamCloseReleaseDrainsUpstreamStage(t *testing.T) {
	src := make(chan llm.TextStreamEvent)
	mid := make(chan llm.TextStreamEvent)
	stageDone := make(chan struct{})
	go func() { // a decorator-like stage: blocking sends, no cancellation
		defer close(stageDone)
		defer close(mid)
		for e := range src {
			mid <- e
		}
	}()

	_, span := telemetry.Tracer().Start(context.Background(), "process dm request")
	wrapped, release := endSpanOnStreamClose(&llm.TextStreamResult{Stream: mid}, span)

	src <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "a"}
	<-wrapped.Stream // the reply reads one chunk, then is stopped
	release()

	// The producer still emits before it notices the cancel, then closes.
	src <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "b"}
	src <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "c"}
	close(src)

	select {
	case <-stageDone:
	case <-time.After(time.Second):
		require.FailNow(t, "upstream stage blocked: forwarder stopped reading after release")
	}
	select {
	case _, open := <-wrapped.Stream:
		require.False(t, open, "nothing may be forwarded after release")
	case <-time.After(time.Second):
		require.FailNow(t, "forwarder did not close its output")
	}
}
