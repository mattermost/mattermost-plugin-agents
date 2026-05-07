// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// LogService is the subset of pluginapi.LogService that the log span
// processor needs. Defined locally so tests can supply a fake without
// pulling in the plugin API.
type LogService interface {
	Info(message string, keyValuePairs ...any)
	Error(message string, keyValuePairs ...any)
}

// logSpanProcessor writes finished spans to a LogService. It is the
// "Server Logs" output mode for admins who don't run an OTLP collector.
type logSpanProcessor struct {
	log LogService
}

// NewLogSpanProcessor returns a SpanProcessor that emits one log entry
// per finished span. Spans whose status code is Error are logged at
// Error level; everything else at Info.
func NewLogSpanProcessor(log LogService) sdktrace.SpanProcessor {
	return &logSpanProcessor{log: log}
}

func (p *logSpanProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (p *logSpanProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	sc := s.SpanContext()
	kv := []any{
		"span", s.Name(),
		"trace_id", sc.TraceID().String(),
		"span_id", sc.SpanID().String(),
		"duration_ms", s.EndTime().Sub(s.StartTime()).Milliseconds(),
	}
	if parent := s.Parent(); parent.IsValid() {
		kv = append(kv, "parent_span_id", parent.SpanID().String())
	}
	for _, a := range s.Attributes() {
		kv = append(kv, string(a.Key), a.Value.AsInterface())
	}
	for _, e := range s.Events() {
		kv = append(kv, "event", formatEvent(e))
	}

	status := s.Status()
	if status.Code == codes.Error {
		if status.Description != "" {
			kv = append(kv, "error", status.Description)
		}
		p.log.Error("agents trace span", kv...)
		return
	}
	p.log.Info("agents trace span", kv...)
}

func (p *logSpanProcessor) Shutdown(context.Context) error   { return nil }
func (p *logSpanProcessor) ForceFlush(context.Context) error { return nil }

func formatEvent(e sdktrace.Event) map[string]any {
	out := map[string]any{
		"name": e.Name,
		"time": e.Time.Format(time.RFC3339Nano),
	}
	for _, a := range e.Attributes {
		out[string(a.Key)] = a.Value.AsInterface()
	}
	return out
}
