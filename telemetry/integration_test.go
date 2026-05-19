// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/telemetry"
)

// setupTestTracing installs an in-memory exporter as the global TracerProvider
// and returns the exporter plus a cleanup function.
func setupTestTracing(t *testing.T) (*tracetest.InMemoryExporter, func()) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return exporter, func() {
		tp.Shutdown(context.Background()) //nolint:errcheck
		otel.SetTracerProvider(prev)
	}
}

// spanByName finds a span by name in the exporter output.
func spanByName(spans []tracetest.SpanStub, name string) *tracetest.SpanStub {
	for i := range spans {
		if spans[i].Name == name {
			return &spans[i]
		}
	}
	return nil
}

// attrString extracts a string attribute from a span.
func attrString(span *tracetest.SpanStub, key string) string {
	for _, a := range span.Attributes {
		if string(a.Key) == key {
			return a.Value.AsString()
		}
	}
	return ""
}

// attrBool extracts a bool attribute from a span.
func attrBool(span *tracetest.SpanStub, key string) bool {
	for _, a := range span.Attributes {
		if string(a.Key) == key {
			return a.Value.AsBool()
		}
	}
	return false
}

// attrInt64 extracts an int64 attribute from a span.
func attrInt64(span *tracetest.SpanStub, key string) int64 {
	for _, a := range span.Attributes {
		if string(a.Key) == key {
			return a.Value.AsInt64()
		}
	}
	return 0
}

// fakeLLM simulates an LLM that emits text and usage events, producing spans
// via the telemetry package just like bifrost.LLM does.
type fakeLLM struct {
	wg sync.WaitGroup
}

func (f *fakeLLM) ChatCompletion(ctx context.Context, request llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	_, span := telemetry.Tracer().Start(ctx, "llm chat completion",
		telemetry.WithLLMAttributes("test-provider", "test-model", request.Operation, true),
	)

	stream := make(chan llm.TextStreamEvent)
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer close(stream)
		defer span.End()

		stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Hello"}
		stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: " world"}

		usage := llm.TokenUsage{InputTokens: 10, OutputTokens: 5}
		span.SetAttributes(
			telemetry.LLMInputTokens.Int64(usage.InputTokens),
			telemetry.LLMOutputTokens.Int64(usage.OutputTokens),
		)
		stream <- llm.TextStreamEvent{Type: llm.EventTypeUsage, Value: usage}
		stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	}()

	return &llm.TextStreamResult{Stream: stream}, nil
}

func (f *fakeLLM) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	result, err := f.ChatCompletion(ctx, request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (f *fakeLLM) CountTokens(text string) int { return len(text) / 4 }
func (f *fakeLLM) InputTokenLimit() int        { return 100000 }

// fakeLLMError simulates an LLM that produces an error span.
type fakeLLMError struct {
	wg sync.WaitGroup
}

func (f *fakeLLMError) ChatCompletion(ctx context.Context, request llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	_, span := telemetry.Tracer().Start(ctx, "llm chat completion",
		telemetry.WithLLMAttributes("test-provider", "test-model", request.Operation, true),
	)

	stream := make(chan llm.TextStreamEvent)
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer close(stream)
		defer span.End()

		err := fmt.Errorf("upstream provider error: rate limited")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		stream <- llm.TextStreamEvent{Type: llm.EventTypeError, Value: err}
	}()

	return &llm.TextStreamResult{Stream: stream}, nil
}

func (f *fakeLLMError) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	result, err := f.ChatCompletion(ctx, request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (f *fakeLLMError) CountTokens(text string) int { return len(text) / 4 }
func (f *fakeLLMError) InputTokenLimit() int        { return 100000 }

func TestLLMChatCompletionSpan(t *testing.T) {
	exporter, cleanup := setupTestTracing(t)
	defer cleanup()

	model := &fakeLLM{}
	result, err := model.ChatCompletion(context.Background(), llm.CompletionRequest{
		Operation: "conversation",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text, err := result.ReadAll()
	if err != nil {
		t.Fatalf("reading stream: %v", err)
	}
	if text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", text)
	}

	model.wg.Wait()
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := &spans[0]
	if span.Name != "llm chat completion" {
		t.Errorf("expected span name 'llm chat completion', got %q", span.Name)
	}
	if v := attrString(span, "agents.llm.provider"); v != "test-provider" {
		t.Errorf("agents.llm.provider = %q, want 'test-provider'", v)
	}
	if v := attrString(span, "agents.llm.model"); v != "test-model" {
		t.Errorf("agents.llm.model = %q, want 'test-model'", v)
	}
	if v := attrString(span, "agents.llm.operation"); v != "conversation" {
		t.Errorf("agents.llm.operation = %q, want 'conversation'", v)
	}
	if v := attrBool(span, "agents.llm.streaming"); !v {
		t.Error("agents.llm.streaming should be true")
	}
	if v := attrInt64(span, "agents.llm.input_tokens"); v != 10 {
		t.Errorf("agents.llm.input_tokens = %d, want 10", v)
	}
	if v := attrInt64(span, "agents.llm.output_tokens"); v != 5 {
		t.Errorf("agents.llm.output_tokens = %d, want 5", v)
	}
	if span.Status.Code != codes.Unset {
		t.Errorf("expected unset status, got %v", span.Status.Code)
	}
}

func TestLLMChatCompletionErrorSpan(t *testing.T) {
	exporter, cleanup := setupTestTracing(t)
	defer cleanup()

	model := &fakeLLMError{}
	result, err := model.ChatCompletion(context.Background(), llm.CompletionRequest{
		Operation: "conversation",
	})
	if err != nil {
		t.Fatalf("unexpected error from ChatCompletion: %v", err)
	}

	// Drain the stream (should contain the error event)
	_, _ = result.ReadAll()
	model.wg.Wait()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := &spans[0]
	if span.Status.Code != codes.Error {
		t.Errorf("expected error status, got %v", span.Status.Code)
	}
	if len(span.Events) == 0 {
		t.Error("expected at least one event (error), got none")
	}
	foundException := false
	for _, event := range span.Events {
		if event.Name == "exception" {
			foundException = true
			break
		}
	}
	if !foundException {
		t.Error("expected 'exception' event on error span")
	}
}

func TestToolResolveSpan(t *testing.T) {
	exporter, cleanup := setupTestTracing(t)
	defer cleanup()

	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{
		{
			Name:        "test_tool",
			Description: "A test tool",
			Resolver: func(_ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				return "tool result", nil
			},
		},
	})

	result, err := store.ResolveTool(context.Background(), "test_tool", func(args any) error {
		return nil
	}, &llm.Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "tool result" {
		t.Errorf("expected 'tool result', got %q", result)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := &spans[0]
	if span.Name != "resolve tool" {
		t.Errorf("expected span name 'resolve tool', got %q", span.Name)
	}
	if v := attrString(span, "agents.tool.name"); v != "test_tool" {
		t.Errorf("agents.tool.name = %q, want 'test_tool'", v)
	}
}

func TestToolResolveUnknownSpan(t *testing.T) {
	exporter, cleanup := setupTestTracing(t)
	defer cleanup()

	store := llm.NewToolStore()

	_, err := store.ResolveTool(context.Background(), "nonexistent", func(args any) error {
		return nil
	}, &llm.Context{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := &spans[0]
	if span.Status.Code != codes.Error {
		t.Errorf("expected error status for unknown tool, got %v", span.Status.Code)
	}
}

func TestParentChildSpanHierarchy(t *testing.T) {
	exporter, cleanup := setupTestTracing(t)
	defer cleanup()

	// Simulate HTTP handler -> LLM call -> tool resolve hierarchy
	tracer := telemetry.Tracer()

	ctx, httpSpan := tracer.Start(context.Background(), "HTTP POST /post/:postid/react")

	model := &fakeLLM{}
	result, err := model.ChatCompletion(ctx, llm.CompletionRequest{Operation: "conversation"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = result.ReadAll()
	model.wg.Wait()

	httpSpan.End()

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	httpStub := spanByName(spans, "HTTP POST /post/:postid/react")
	llmStub := spanByName(spans, "llm chat completion")

	if httpStub == nil {
		t.Fatal("HTTP span not found")
	}
	if llmStub == nil {
		t.Fatal("LLM span not found")
	}

	// Verify same trace
	if httpStub.SpanContext.TraceID() != llmStub.SpanContext.TraceID() {
		t.Error("HTTP and LLM spans should share the same trace ID")
	}

	// Verify parent-child relationship
	if llmStub.Parent.SpanID() != httpStub.SpanContext.SpanID() {
		t.Error("LLM span should be a child of HTTP span")
	}
}

func TestFullRequestTrace(t *testing.T) {
	exporter, cleanup := setupTestTracing(t)
	defer cleanup()

	tracer := telemetry.Tracer()

	// Simulate: HTTP -> ProcessUserRequest -> ChatCompletion -> tool resolve
	ctx, httpSpan := tracer.Start(context.Background(), "HTTP handler")

	ctx, processSpan := tracer.Start(ctx, "process user request",
		trace.WithAttributes(telemetry.UserID.String("user123")),
	)

	// LLM call with tool calls response
	_, llmSpan := tracer.Start(ctx, "llm chat completion",
		telemetry.WithLLMAttributes("openai", "gpt-4o", "conversation", true),
	)
	llmSpan.SetAttributes(
		telemetry.LLMInputTokens.Int64(150),
		telemetry.LLMOutputTokens.Int64(42),
	)
	llmSpan.End()

	// Tool resolution
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{
		{
			Name: "web_search",
			Resolver: func(_ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				var args struct {
					Query string `json:"query"`
				}
				if err := argsGetter(&args); err != nil {
					return "", err
				}
				return "search results for: " + args.Query, nil
			},
		},
	})

	result, err := store.ResolveTool(ctx, "web_search", func(args any) error {
		return json.Unmarshal([]byte(`{"query":"test"}`), args)
	}, &llm.Context{})
	if err != nil {
		t.Fatalf("unexpected tool error: %v", err)
	}
	if result != "search results for: test" {
		t.Errorf("unexpected tool result: %q", result)
	}

	processSpan.End()
	httpSpan.End()

	spans := exporter.GetSpans()

	// Verify we got all 4 spans
	names := make(map[string]bool)
	for _, s := range spans {
		names[s.Name] = true
	}
	for _, expected := range []string{"HTTP handler", "process user request", "llm chat completion", "resolve tool"} {
		if !names[expected] {
			t.Errorf("missing expected span: %q", expected)
		}
	}

	// Verify all spans share the same trace ID
	traceID := spans[0].SpanContext.TraceID()
	for _, s := range spans {
		if s.SpanContext.TraceID() != traceID {
			t.Errorf("span %q has different trace ID", s.Name)
		}
	}

	// Verify LLM span has token attributes
	llmStub := spanByName(spans, "llm chat completion")
	if llmStub == nil {
		t.Fatal("LLM span not found")
	}
	if v := attrInt64(llmStub, "agents.llm.input_tokens"); v != 150 {
		t.Errorf("agents.llm.input_tokens = %d, want 150", v)
	}
	if v := attrInt64(llmStub, "agents.llm.output_tokens"); v != 42 {
		t.Errorf("agents.llm.output_tokens = %d, want 42", v)
	}

	// Verify tool span has name attribute
	toolStub := spanByName(spans, "resolve tool")
	if toolStub == nil {
		t.Fatal("tool span not found")
	}
	if v := attrString(toolStub, "agents.tool.name"); v != "web_search" {
		t.Errorf("agents.tool.name = %q, want 'web_search'", v)
	}
}
