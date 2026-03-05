// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestInit_NoEndpoint(t *testing.T) {
	shutdown, err := Init(context.Background(), "test-svc", "1.0.0", "")
	if err != nil {
		t.Fatalf("Init with empty endpoint should not error, got: %v", err)
	}
	defer shutdown(context.Background()) //nolint:errcheck

	tp := otel.GetTracerProvider()
	if _, ok := tp.(noop.TracerProvider); !ok {
		t.Errorf("expected noop TracerProvider when endpoint is empty, got %T", tp)
	}
}

func TestInit_WithEndpoint(t *testing.T) {
	// Use a non-routable address so we don't actually connect
	shutdown, err := Init(context.Background(), "test-svc", "1.0.0", "192.0.2.1:4317")
	if err != nil {
		t.Fatalf("Init with endpoint should not error, got: %v", err)
	}
	defer shutdown(context.Background()) //nolint:errcheck

	tp := otel.GetTracerProvider()
	if _, ok := tp.(*sdktrace.TracerProvider); !ok {
		t.Errorf("expected SDK TracerProvider when endpoint is set, got %T", tp)
	}

	// Restore noop for other tests
	otel.SetTracerProvider(noop.NewTracerProvider())
}

func TestTracer(t *testing.T) {
	tracer := Tracer()
	if tracer == nil {
		t.Fatal("Tracer() returned nil")
	}
}

func TestSpanFromContext(t *testing.T) {
	span := SpanFromContext(context.Background())
	if span == nil {
		t.Fatal("SpanFromContext returned nil for background context")
	}
	if span.SpanContext().IsValid() {
		t.Error("expected invalid span context from background context")
	}
}

func TestWithLLMAttributes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background()) //nolint:errcheck

	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span",
		WithLLMAttributes("openai", "gpt-4", "conversation", true),
	)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	attrs := spans[0].Attributes
	expected := map[string]string{
		"ai.llm.provider":  "openai",
		"ai.llm.model":     "gpt-4",
		"ai.llm.operation": "conversation",
	}

	for key, want := range expected {
		found := false
		for _, attr := range attrs {
			if string(attr.Key) == key && attr.Value.AsString() == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected attribute %s=%s not found in span", key, want)
		}
	}

	// Check streaming bool attribute
	streamingFound := false
	for _, attr := range attrs {
		if string(attr.Key) == "ai.llm.streaming" && attr.Value.AsBool() {
			streamingFound = true
			break
		}
	}
	if !streamingFound {
		t.Error("expected ai.llm.streaming=true attribute not found")
	}
}

func TestSpanHierarchy(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background()) //nolint:errcheck

	tracer := tp.Tracer("test")
	ctx, parent := tracer.Start(context.Background(), "parent-span")
	_, child := tracer.Start(ctx, "child-span")
	child.End()
	parent.End()

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	var parentSpan, childSpan tracetest.SpanStub
	for _, s := range spans {
		if s.Name == "parent-span" {
			parentSpan = s
		}
		if s.Name == "child-span" {
			childSpan = s
		}
	}

	if !parentSpan.SpanContext.TraceID().IsValid() {
		t.Fatal("parent span has invalid trace ID")
	}
	if !childSpan.SpanContext.TraceID().IsValid() {
		t.Fatal("child span has invalid trace ID")
	}

	if parentSpan.SpanContext.TraceID() != childSpan.SpanContext.TraceID() {
		t.Error("parent and child spans should share the same trace ID")
	}

	if childSpan.Parent.SpanID() != parentSpan.SpanContext.SpanID() {
		t.Error("child span's parent should be the parent span")
	}
}

func TestContextPropagation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background()) //nolint:errcheck

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "root")

	extractedSpan := trace.SpanFromContext(ctx)
	if extractedSpan.SpanContext().SpanID() != span.SpanContext().SpanID() {
		t.Error("SpanFromContext should return the span stored in context")
	}

	span.End()
}
