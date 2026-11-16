// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/lifecycle"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/credentials"
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

// OTLPOptions configures the OTLP gRPC exporter.
type OTLPOptions struct {
	// Endpoint is the collector's host:port.
	Endpoint string
	// TLS dials the endpoint over TLS (system root CAs) instead of plaintext.
	TLS bool
	// Headers are sent as gRPC metadata on every export, typically an
	// Authorization header for a hosted collector. They are credentials:
	// never log them.
	Headers map[string]string
}

// Options selects and configures the trace output.
type Options struct {
	ServiceName    string
	ServiceVersion string
	Mode           OutputMode
	// OTLP is required for OutputModeOTLP.
	OTLP OTLPOptions
	// Log is required for OutputModeLogs.
	Log LogService
}

// ParseHeaders parses OTLP headers in the OTEL_EXPORTER_OTLP_HEADERS format:
// comma-separated key=value pairs with percent-encoded values. The error never echoes a value, since
// values are credentials.
func ParseHeaders(raw string) (map[string]string, error) {
	headers := map[string]string{}
	for i, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid OTLP header at position %d: expected key=value", i+1)
		}
		// Values are percent-encoded in this format (e.g. %2C for a comma).
		decoded, err := url.PathUnescape(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid OTLP header at position %d: bad percent-encoding", i+1)
		}
		headers[key] = decoded
	}
	return headers, nil
}

// Init sets up the global TracerProvider for the selected output mode.
//   - OutputModeOff registers a no-op provider.
//   - OutputModeLogs writes finished spans through opts.Log.
//   - OutputModeOTLP exports spans to the OTLP gRPC endpoint.
func Init(ctx context.Context, opts Options) (ShutdownFunc, error) {
	mode, serviceName, serviceVersion, log := opts.Mode, opts.ServiceName, opts.ServiceVersion, opts.Log
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

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithIDGenerator(NewTurnIDGenerator()),
	}

	switch mode {
	case OutputModeLogs:
		if log == nil {
			return nil, fmt.Errorf("log mode requires a LogService")
		}
		tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(NewLogSpanProcessor(log)))
	case OutputModeOTLP:
		if opts.OTLP.Endpoint == "" {
			return nil, fmt.Errorf("otlp mode requires an endpoint")
		}
		exporter, err := otlptracegrpc.New(ctx, otlpExporterOptions(opts.OTLP)...)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(newFilteringExporter(exporter), sdktrace.WithBatchTimeout(5*time.Second)))
	default:
		return nil, fmt.Errorf("unknown telemetry output mode: %q", mode)
	}

	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp.Shutdown, nil
}

func otlpExporterOptions(o OTLPOptions) []otlptracegrpc.Option {
	out := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(o.Endpoint)}
	if o.TLS {
		out = append(out, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
	} else {
		out = append(out, otlptracegrpc.WithInsecure())
	}
	if len(o.Headers) > 0 {
		out = append(out, otlptracegrpc.WithHeaders(o.Headers))
	}
	return out
}

// Tracer returns a named tracer from the global provider.
func Tracer() trace.Tracer {
	return otel.Tracer(TracerName)
}

// SpanFromContext extracts the current span from context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// DetachContext returns a context whose lifetime is independent of ctx (it
// derives from the plugin root context, canceled on deactivation) but
// preserves the active OpenTelemetry span. Use this when handing a request
// context to background work (post streaming, async processing) that must
// continue after the originating HTTP handler returns. Without it, the
// request context's cancellation propagates into the background goroutine
// and truncates the work.
func DetachContext(ctx context.Context) context.Context {
	return trace.ContextWithSpan(lifecycle.Context(), trace.SpanFromContext(ctx))
}
