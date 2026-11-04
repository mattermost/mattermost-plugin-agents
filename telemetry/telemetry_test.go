// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestInit_Off(t *testing.T) {
	shutdown, err := Init(context.Background(), Options{ServiceName: "test-svc", ServiceVersion: "1.0.0", Mode: OutputModeOff})
	if err != nil {
		t.Fatalf("Init off should not error, got: %v", err)
	}
	defer shutdown(context.Background()) //nolint:errcheck

	tp := otel.GetTracerProvider()
	if _, ok := tp.(noop.TracerProvider); !ok {
		t.Errorf("expected noop TracerProvider for off mode, got %T", tp)
	}
}

func TestInit_EmptyMode(t *testing.T) {
	// Empty string should behave like OutputModeOff for backward compat with
	// existing config installations.
	shutdown, err := Init(context.Background(), Options{ServiceName: "test-svc", ServiceVersion: "1.0.0"})
	if err != nil {
		t.Fatalf("Init empty mode should not error, got: %v", err)
	}
	defer shutdown(context.Background()) //nolint:errcheck

	if _, ok := otel.GetTracerProvider().(noop.TracerProvider); !ok {
		t.Errorf("expected noop TracerProvider for empty mode")
	}
}

func TestInit_Logs(t *testing.T) {
	fake := &fakeLog{}
	shutdown, err := Init(context.Background(), Options{ServiceName: "test-svc", ServiceVersion: "1.0.0", Mode: OutputModeLogs, Log: fake})
	if err != nil {
		t.Fatalf("Init logs should not error, got: %v", err)
	}
	defer shutdown(context.Background()) //nolint:errcheck

	tp := otel.GetTracerProvider()
	if _, ok := tp.(*sdktrace.TracerProvider); !ok {
		t.Fatalf("expected SDK TracerProvider for logs mode, got %T", tp)
	}

	_, span := Tracer().Start(context.Background(), "logs-mode-span")
	span.End()

	if err := tp.(*sdktrace.TracerProvider).Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if len(fake.entries) == 0 {
		t.Error("expected at least one log entry from logs mode span")
	}

	otel.SetTracerProvider(noop.NewTracerProvider())
}

func TestInit_LogsRequiresLogger(t *testing.T) {
	if _, err := Init(context.Background(), Options{ServiceName: "test-svc", ServiceVersion: "1.0.0", Mode: OutputModeLogs}); err == nil {
		t.Error("expected error when logs mode is given a nil LogService")
	}
}

func TestInit_OTLP(t *testing.T) {
	// Use a non-routable address so we don't actually connect
	shutdown, err := Init(context.Background(), Options{ServiceName: "test-svc", ServiceVersion: "1.0.0", Mode: OutputModeOTLP, OTLP: OTLPOptions{Endpoint: "192.0.2.1:4317"}})
	if err != nil {
		t.Fatalf("Init OTLP should not error, got: %v", err)
	}
	defer shutdown(context.Background()) //nolint:errcheck

	tp := otel.GetTracerProvider()
	if _, ok := tp.(*sdktrace.TracerProvider); !ok {
		t.Errorf("expected SDK TracerProvider for otlp mode, got %T", tp)
	}

	otel.SetTracerProvider(noop.NewTracerProvider())
}

func TestInit_OTLPRequiresEndpoint(t *testing.T) {
	if _, err := Init(context.Background(), Options{ServiceName: "test-svc", ServiceVersion: "1.0.0", Mode: OutputModeOTLP}); err == nil {
		t.Error("expected error when otlp mode is given an empty endpoint")
	}
}

func TestInit_UnknownMode(t *testing.T) {
	if _, err := Init(context.Background(), Options{ServiceName: "test-svc", ServiceVersion: "1.0.0", Mode: OutputMode("bogus")}); err == nil {
		t.Error("expected error for unknown mode")
	}
}

func TestTracer(t *testing.T) {
	tracer := Tracer()
	require.NotNil(t, tracer, "Tracer() returned nil")
}

func TestSpanFromContext(t *testing.T) {
	span := SpanFromContext(context.Background())
	require.NotNil(t, span, "SpanFromContext returned nil for background context")
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
		"agents.llm.provider":  "openai",
		"agents.llm.model":     "gpt-4",
		"agents.llm.operation": "conversation",
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
		if string(attr.Key) == "agents.llm.streaming" && attr.Value.AsBool() {
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

	require.True(t, parentSpan.SpanContext.TraceID().IsValid(), "parent span has invalid trace ID")
	require.True(t, childSpan.SpanContext.TraceID().IsValid(), "child span has invalid trace ID")

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

func TestParseHeaders(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    map[string]string
		wantErr bool
	}{
		{name: "empty", raw: "", want: map[string]string{}},
		{name: "single", raw: "Authorization=Basic abc==", want: map[string]string{"Authorization": "Basic abc=="}},
		{name: "multiple with spaces", raw: " X-Scope-OrgID = tenant , Authorization=Bearer t ", want: map[string]string{"X-Scope-OrgID": "tenant", "Authorization": "Bearer t"}},
		{name: "percent-encoded value", raw: "X-Token=a%2Cb%3Dc", want: map[string]string{"X-Token": "a,b=c"}},
		{name: "bad percent-encoding", raw: "X-Token=a%zz", wantErr: true},
		{name: "missing separator", raw: "Authorization", wantErr: true},
		{name: "missing key", raw: "=value", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseHeaders(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParseHeadersErrorDoesNotEchoSecret(t *testing.T) {
	const secret = "sentinel-secret-value"
	_, err := ParseHeaders("Authorization=ok," + secret)
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)
}

type headerCapturingCollector struct {
	coltracepb.UnimplementedTraceServiceServer
	got chan metadata.MD
}

func (c *headerCapturingCollector) Export(ctx context.Context, _ *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	select {
	case c.got <- md:
	default:
	}
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

func TestInitOTLPSendsHeaders(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	collector := &headerCapturingCollector{got: make(chan metadata.MD, 1)}
	server := grpc.NewServer()
	coltracepb.RegisterTraceServiceServer(server, collector)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	previous := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	shutdown, err := Init(context.Background(), Options{
		ServiceName:    "test-svc",
		ServiceVersion: "1.0.0",
		Mode:           OutputModeOTLP,
		OTLP: OTLPOptions{
			Endpoint: lis.Addr().String(),
			Headers:  map[string]string{"x-scope-orgid": "tenant-1"},
		},
	})
	require.NoError(t, err)

	_, span := Tracer().Start(context.Background(), "agent run")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, shutdown(ctx))

	select {
	case md := <-collector.got:
		require.Equal(t, []string{"tenant-1"}, md.Get("x-scope-orgid"))
	case <-ctx.Done():
		require.FailNow(t, "collector received no export")
	}
}
