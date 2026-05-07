// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
	"context"
	"errors"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type capturedLog struct {
	level   string
	message string
	kvs     []any
}

type fakeLog struct {
	mu      sync.Mutex
	entries []capturedLog
}

func (f *fakeLog) Info(message string, kvs ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, capturedLog{level: "info", message: message, kvs: kvs})
}

func (f *fakeLog) Error(message string, kvs ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, capturedLog{level: "error", message: message, kvs: kvs})
}

func kvLookup(kvs []any, key string) (any, bool) {
	for i := 0; i+1 < len(kvs); i += 2 {
		if k, ok := kvs[i].(string); ok && k == key {
			return kvs[i+1], true
		}
	}
	return nil, false
}

func TestLogSpanProcessor(t *testing.T) {
	tests := []struct {
		name         string
		recordError  error
		setStatus    codes.Code
		statusDesc   string
		wantLevel    string
		wantHasError bool
	}{
		{
			name:         "ok span logs at info",
			setStatus:    codes.Ok,
			wantLevel:    "info",
			wantHasError: false,
		},
		{
			name:         "error span logs at error with description",
			recordError:  errors.New("boom"),
			setStatus:    codes.Error,
			statusDesc:   "boom",
			wantLevel:    "error",
			wantHasError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeLog{}
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(NewLogSpanProcessor(fake)))
			defer tp.Shutdown(context.Background()) //nolint:errcheck

			tracer := tp.Tracer("test")
			_, span := tracer.Start(context.Background(), "unit-span")
			span.SetAttributes(
				attribute.String("agents.llm.provider", "openai"),
				attribute.Int("agents.llm.input_tokens", 42),
			)
			if tc.recordError != nil {
				span.RecordError(tc.recordError)
			}
			if tc.setStatus != codes.Unset {
				span.SetStatus(tc.setStatus, tc.statusDesc)
			}
			span.End()

			if got := len(fake.entries); got != 1 {
				t.Fatalf("expected 1 log entry, got %d", got)
			}
			entry := fake.entries[0]
			if entry.level != tc.wantLevel {
				t.Errorf("level: got %q, want %q", entry.level, tc.wantLevel)
			}
			if v, ok := kvLookup(entry.kvs, "span"); !ok || v != "unit-span" {
				t.Errorf("span key: got %v ok=%v, want unit-span", v, ok)
			}
			if v, ok := kvLookup(entry.kvs, "agents.llm.provider"); !ok || v != "openai" {
				t.Errorf("provider attr: got %v ok=%v", v, ok)
			}
			if v, ok := kvLookup(entry.kvs, "agents.llm.input_tokens"); !ok || v != int64(42) {
				t.Errorf("tokens attr: got %v ok=%v", v, ok)
			}
			if _, ok := kvLookup(entry.kvs, "trace_id"); !ok {
				t.Error("trace_id not present")
			}
			if _, ok := kvLookup(entry.kvs, "duration_ms"); !ok {
				t.Error("duration_ms not present")
			}
			_, hasError := kvLookup(entry.kvs, "error")
			if hasError != tc.wantHasError {
				t.Errorf("error key presence: got %v, want %v", hasError, tc.wantHasError)
			}
		})
	}
}

func TestLogSpanProcessor_ParentSpanID(t *testing.T) {
	fake := &fakeLog{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(NewLogSpanProcessor(fake)))
	defer tp.Shutdown(context.Background()) //nolint:errcheck

	tracer := tp.Tracer("test")
	ctx, parent := tracer.Start(context.Background(), "parent")
	_, child := tracer.Start(ctx, "child")
	child.End()
	parent.End()

	if len(fake.entries) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(fake.entries))
	}

	var childEntry *capturedLog
	for i := range fake.entries {
		if v, _ := kvLookup(fake.entries[i].kvs, "span"); v == "child" {
			childEntry = &fake.entries[i]
		}
	}
	if childEntry == nil {
		t.Fatal("child entry not found")
	}
	if _, ok := kvLookup(childEntry.kvs, "parent_span_id"); !ok {
		t.Error("child entry missing parent_span_id")
	}

	for i := range fake.entries {
		if v, _ := kvLookup(fake.entries[i].kvs, "span"); v == "parent" {
			if _, ok := kvLookup(fake.entries[i].kvs, "parent_span_id"); ok {
				t.Error("root span should not have parent_span_id")
			}
		}
	}
}
