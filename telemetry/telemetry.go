// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const TracerName = "github.com/mattermost/mattermost-plugin-ai"

// OutputMode selects where finished spans are sent.
type OutputMode string

const (
	// OutputModeOff disables tracing entirely (no-op TracerProvider).
	OutputModeOff OutputMode = "off"
	// OutputModeLogs writes spans to the Mattermost server log via
	// pluginapi.LogService. No collector required.
	OutputModeLogs OutputMode = "logs"
	// OutputModeOTLP exports spans to an OTLP gRPC endpoint such as
	// Grafana Tempo or Jaeger.
	OutputModeOTLP OutputMode = "otlp"
)

// ShutdownFunc is returned by Init and must be called to flush pending spans.
type ShutdownFunc func(context.Context) error

// Init sets up the global TracerProvider for the selected output mode.
//   - OutputModeOff registers a no-op provider.
//   - OutputModeLogs writes finished spans through log.
//   - OutputModeOTLP exports spans to the OTLP gRPC endpoint.
//
// log is required for OutputModeLogs; endpoint is required for OutputModeOTLP.
func Init(ctx context.Context, serviceName, serviceVersion string, mode OutputMode, endpoint string, log LogService) (ShutdownFunc, error) {
	if mode == "" || mode == OutputModeOff {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithIDGenerator(NewTurnIDGenerator()),
	}

	switch mode {
	case OutputModeLogs:
		if log == nil {
			return nil, fmt.Errorf("log mode requires a LogService")
		}
		opts = append(opts, sdktrace.WithSpanProcessor(NewLogSpanProcessor(log)))
	case OutputModeOTLP:
		if endpoint == "" {
			return nil, fmt.Errorf("otlp mode requires an endpoint")
		}
		exporter, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)))
	default:
		return nil, fmt.Errorf("unknown telemetry output mode: %q", mode)
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp.Shutdown, nil
}

// Tracer returns a named tracer from the global provider.
func Tracer() trace.Tracer {
	return otel.Tracer(TracerName)
}

// SpanFromContext extracts the current span from context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// DetachContext returns a context whose lifetime is independent of ctx but
// preserves the active OpenTelemetry span. Use this when handing a request
// context to background work (post streaming, async processing) that must
// continue after the originating HTTP handler returns. Without it, the
// request context's cancellation propagates into the background goroutine
// and truncates the work.
func DetachContext(ctx context.Context) context.Context {
	return trace.ContextWithSpan(context.Background(), trace.SpanFromContext(ctx))
}
