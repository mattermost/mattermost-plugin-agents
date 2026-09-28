// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"sync"
	"time"

	bschemas "github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// otelTracer adapts Bifrost's schemas.Tracer interface onto the plugin's
// OpenTelemetry tracer. It forwards Bifrost spans (provider call, plugin
// hooks, retries, fallbacks) into the same trace as the surrounding plugin
// span so a single end-to-end trace is exported via OTLP.
//
// The accumulator/deferred-span methods provide the bookkeeping Bifrost
// expects for streaming requests; they keep handles live so the final
// EndSpan call can stamp completion attributes.
type otelTracer struct {
	// NoOpTracer provides the stream pause/resume gate methods (PauseStream,
	// ResumeStream, EndStream, GateSend, ...). This plugin never engages the
	// gate, and NoOpTracer's GateSend forwards chunks directly to the channel,
	// so the no-op implementations preserve normal streaming behavior.
	bschemas.NoOpTracer

	mu            sync.Mutex
	deferredSpans map[string]bschemas.SpanHandle
	streams       map[string]*streamState
}

// streamState is the per-trace accumulator state for a streaming request.
type streamState struct {
	start    time.Time
	chunks   int
	firstAt  time.Time
	response *bschemas.BifrostResponse
}

// newOTelTracer returns a Bifrost Tracer that emits spans into the plugin's
// OpenTelemetry pipeline.
func newOTelTracer() bschemas.Tracer {
	return &otelTracer{
		deferredSpans: make(map[string]bschemas.SpanHandle),
		streams:       make(map[string]*streamState),
	}
}

// lockedStreamState returns the accumulator state for the trace, creating it
// if needed. Callers must hold t.mu.
func (t *otelTracer) lockedStreamState(traceID string) *streamState {
	s := t.streams[traceID]
	if s == nil {
		s = &streamState{}
		t.streams[traceID] = s
	}
	return s
}

// otelSpanHandle is the SpanHandle implementation backed by an OTel span.
type otelSpanHandle struct {
	span trace.Span
}

// spanOf extracts the OTel span from a Bifrost span handle, returning nil for
// foreign or empty handles.
func spanOf(handle bschemas.SpanHandle) trace.Span {
	h, ok := handle.(*otelSpanHandle)
	if !ok || h == nil || h.span == nil {
		return nil
	}
	return h.span
}

// CreateTrace is a no-op for the OTel adapter: OTel traces are identified by
// the active span's TraceID, which is created when the parent span starts.
// We return an empty string because Bifrost only uses the returned trace ID
// as an opaque key for its own bookkeeping (StoreDeferredSpan, etc.); it
// never round-trips back to OTel.
func (t *otelTracer) CreateTrace(_ string, requestID ...string) string {
	if len(requestID) > 0 && requestID[0] != "" {
		return requestID[0]
	}
	return ""
}

// EndTrace returns nil because we don't materialize Bifrost Trace objects;
// span lifetime is managed by OTel via StartSpan/EndSpan calls.
func (t *otelTracer) EndTrace(_ string) *bschemas.Trace { return nil }

// StartSpan opens a new OTel span as a child of the current span in ctx.
// The returned context carries the new span so subsequent Bifrost calls
// nest correctly.
func (t *otelTracer) StartSpan(ctx context.Context, name string, kind bschemas.SpanKind) (context.Context, bschemas.SpanHandle) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, span := telemetry.Tracer().Start(ctx, name,
		trace.WithAttributes(attribute.String("bifrost.span.kind", string(kind))),
	)
	return ctx, &otelSpanHandle{span: span}
}

// EndSpan closes the span with the given status.
func (t *otelTracer) EndSpan(handle bschemas.SpanHandle, status bschemas.SpanStatus, statusMsg string) {
	span := spanOf(handle)
	if span == nil {
		return
	}
	switch status {
	case bschemas.SpanStatusError:
		span.SetStatus(otelcodes.Error, statusMsg)
	case bschemas.SpanStatusOk:
		span.SetStatus(otelcodes.Ok, statusMsg)
	}
	span.End()
}

// SetAttribute records a single attribute on the span. We pass values
// through attributeFromAny which maps common Go scalars onto the OTel
// attribute type; anything else is stringified so traces still capture
// the value.
func (t *otelTracer) SetAttribute(handle bschemas.SpanHandle, key string, value any) {
	span := spanOf(handle)
	if span == nil {
		return
	}
	span.SetAttributes(attributeFromAny(key, value))
}

// AddEvent records a timestamped event on the span.
func (t *otelTracer) AddEvent(handle bschemas.SpanHandle, name string, attrs map[string]any) {
	span := spanOf(handle)
	if span == nil {
		return
	}
	otelAttrs := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		otelAttrs = append(otelAttrs, attributeFromAny(k, v))
	}
	span.AddEvent(name, trace.WithAttributes(otelAttrs...))
}

// PopulateLLMRequestAttributes pulls provider/model out of the Bifrost
// request and tags the span. Bifrost itself populates the `gen_ai.*`
// semantic-convention attributes via SetAttribute, so we just add provider
// and model here for direct correlation with the outer plugin span.
func (t *otelTracer) PopulateLLMRequestAttributes(handle bschemas.SpanHandle, req *bschemas.BifrostRequest) {
	span := spanOf(handle)
	if span == nil || req == nil {
		return
	}
	provider, model, _ := req.GetRequestFields()
	span.SetAttributes(
		telemetry.LLMProvider.String(string(provider)),
		telemetry.LLMModel.String(model),
	)
}

// PopulateLLMResponseAttributes captures token usage and any error.
func (t *otelTracer) PopulateLLMResponseAttributes(_ *bschemas.BifrostContext, handle bschemas.SpanHandle, resp *bschemas.BifrostResponse, bErr *bschemas.BifrostError) {
	span := spanOf(handle)
	if span == nil {
		return
	}
	if usage := chatUsage(resp); usage != nil {
		setUsageAttributes(span, usage)
	}
	if bErr != nil && bErr.Error != nil {
		// Sanitize before recording: provider error messages can echo back API
		// keys, which would otherwise be exported in the span status.
		span.SetStatus(otelcodes.Error, llm.SanitizeProviderErrorMessage(bErr.Error.Message, ""))
	}
}

// StoreDeferredSpan keeps a handle alive across the streaming lifecycle so
// the matching ProcessStreamingChunk/EndSpan calls can find it again.
func (t *otelTracer) StoreDeferredSpan(traceID string, handle bschemas.SpanHandle) {
	if traceID == "" {
		return
	}
	t.mu.Lock()
	t.deferredSpans[traceID] = handle
	t.mu.Unlock()
}

func (t *otelTracer) GetDeferredSpanHandle(traceID string) bschemas.SpanHandle {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.deferredSpans[traceID]
}

// GetSpanHandleByID returns the span handle stored for the trace. This OTel
// adapter keeps a single deferred (root) span per trace ID and does not index
// individual spans by ID, so spanID is ignored and the trace's stored handle is
// returned (nil if none), mirroring NoOpTracer's behavior.
func (t *otelTracer) GetSpanHandleByID(traceID string, _ *string) bschemas.SpanHandle {
	return t.GetDeferredSpanHandle(traceID)
}

func (t *otelTracer) ClearDeferredSpan(traceID string) {
	t.mu.Lock()
	delete(t.deferredSpans, traceID)
	t.mu.Unlock()
}

// GetDeferredSpanID returns the OTel SpanID hex, which is what other
// Bifrost subsystems use to correlate logs with the active span.
func (t *otelTracer) GetDeferredSpanID(traceID string) string {
	t.mu.Lock()
	handle := t.deferredSpans[traceID]
	t.mu.Unlock()
	span := spanOf(handle)
	if span == nil {
		return ""
	}
	return span.SpanContext().SpanID().String()
}

// CreateStreamAccumulator records the stream start time. We don't keep the
// chunks themselves — only enough state to compute time-to-first-token and
// chunk counts when ProcessStreamingChunk is called.
func (t *otelTracer) CreateStreamAccumulator(traceID string, startTime time.Time) {
	if traceID == "" {
		return
	}
	t.mu.Lock()
	s := t.lockedStreamState(traceID)
	s.start = startTime
	s.chunks = 0
	t.mu.Unlock()
}

func (t *otelTracer) CleanupStreamAccumulator(traceID string) {
	t.mu.Lock()
	delete(t.streams, traceID)
	t.mu.Unlock()
}

// AddStreamingChunk increments the chunk count and remembers the latest
// response so the final ProcessStreamingChunk call can return it.
func (t *otelTracer) AddStreamingChunk(traceID string, response *bschemas.BifrostResponse) {
	if traceID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.lockedStreamState(traceID)
	s.chunks++
	if s.firstAt.IsZero() {
		s.firstAt = time.Now()
	}
	if response != nil {
		s.response = response
	}
}

func (t *otelTracer) GetAccumulatedChunks(traceID string) (*bschemas.BifrostResponse, int64, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.streams[traceID]
	if s == nil {
		return nil, 0, 0
	}
	var ttft int64
	if !s.start.IsZero() && !s.firstAt.IsZero() {
		ttft = s.firstAt.Sub(s.start).Nanoseconds()
	}
	return s.response, ttft, s.chunks
}

// ProcessStreamingChunk forwards the chunk into the accumulator and
// returns a result on the final chunk. Bifrost's exporter plugins
// expect a non-nil result on the terminating call so they can flush;
// we only synthesize one when isFinalChunk is true.
func (t *otelTracer) ProcessStreamingChunk(_ *bschemas.BifrostContext, traceID string, isFinalChunk bool, result *bschemas.BifrostResponse, bErr *bschemas.BifrostError) *bschemas.StreamAccumulatorResult {
	if traceID == "" {
		return nil
	}
	t.AddStreamingChunk(traceID, result)
	if !isFinalChunk {
		return nil
	}

	var count int
	var start, first time.Time
	var last *bschemas.BifrostResponse
	t.mu.Lock()
	if s := t.streams[traceID]; s != nil {
		count, start, first, last = s.chunks, s.start, s.firstAt, s.response
	}
	t.mu.Unlock()

	out := &bschemas.StreamAccumulatorResult{
		ErrorDetails: bErr,
	}
	if !start.IsZero() {
		out.Latency = time.Since(start).Milliseconds()
		if !first.IsZero() {
			out.TimeToFirstToken = first.Sub(start).Milliseconds()
		}
	}
	if last != nil {
		if usage := chatUsage(last); usage != nil {
			out.TokenUsage = usage
		}
		if extras := last.GetExtraFields(); extras != nil {
			routing := extras.RoutingInfo
			requestedModel := routing.Model
			if routing.IsFallback && routing.PrimaryModel != nil {
				requestedModel = *routing.PrimaryModel
			}
			resolvedModel := routing.Model
			if routing.ResolvedKeyAlias != nil {
				resolvedModel = routing.ResolvedKeyAlias.ModelID
			}
			// traceID is the request ID: CreateTrace returns the requestID
			// Bifrost passes in, and Bifrost keys the whole streaming
			// lifecycle (including this call) by that value.
			out.RequestID = traceID
			out.RequestedModel = requestedModel
			out.ResolvedModel = resolvedModel
			out.Provider = routing.Provider
		}
	}
	if count > 0 {
		out.Status = "completed"
	}
	return out
}

// AttachPluginLogs is a no-op: Bifrost plugin logs are surfaced via its
// own logger which we already wrap with sanitizingLogger.
func (t *otelTracer) AttachPluginLogs(_ string, _ []bschemas.PluginLogEntry) {}

// CompleteAndFlushTrace closes any deferred span tied to the trace ID and
// removes the accumulator state. Called by transports that bypass the
// normal HTTP completion path.
func (t *otelTracer) CompleteAndFlushTrace(traceID string) {
	t.mu.Lock()
	handle := t.deferredSpans[traceID]
	delete(t.deferredSpans, traceID)
	delete(t.streams, traceID)
	t.mu.Unlock()
	if span := spanOf(handle); span != nil {
		span.End()
	}
}

// Stop releases tracker state. The OTel SDK shutdown is owned by
// telemetry.Init's returned shutdown func, not this adapter.
func (t *otelTracer) Stop() {
	t.mu.Lock()
	t.deferredSpans = map[string]bschemas.SpanHandle{}
	t.streams = map[string]*streamState{}
	t.mu.Unlock()
}

// setUsageAttributes only emits cached / reasoning / cost attributes when the
// value is non-zero so spans from providers that don't expose them stay clean.
func setUsageAttributes(span trace.Span, usage *bschemas.BifrostLLMUsage) {
	attrs := []attribute.KeyValue{
		telemetry.LLMInputTokens.Int64(int64(usage.PromptTokens)),
		telemetry.LLMOutputTokens.Int64(int64(usage.CompletionTokens)),
	}
	if d := usage.PromptTokensDetails; d != nil {
		if d.CachedReadTokens > 0 {
			attrs = append(attrs, telemetry.LLMCachedReadTokens.Int64(int64(d.CachedReadTokens)))
		}
		if d.CachedWriteTokens > 0 {
			attrs = append(attrs, telemetry.LLMCachedWriteTokens.Int64(int64(d.CachedWriteTokens)))
		}
	}
	if d := usage.CompletionTokensDetails; d != nil && d.ReasoningTokens > 0 {
		attrs = append(attrs, telemetry.LLMReasoningTokens.Int64(int64(d.ReasoningTokens)))
	}
	if usage.Cost != nil && usage.Cost.TotalCost > 0 {
		attrs = append(attrs, telemetry.LLMCost.Float64(usage.Cost.TotalCost))
	}
	span.SetAttributes(attrs...)
}

// chatUsage returns the chat-completion usage struct from a BifrostResponse,
// or nil if the response is not a chat completion or has no usage.
func chatUsage(resp *bschemas.BifrostResponse) *bschemas.BifrostLLMUsage {
	if resp == nil {
		return nil
	}
	if cr := resp.ChatResponse; cr != nil {
		return cr.Usage
	}
	return nil
}

// attributeFromAny maps a value of arbitrary type to an OTel KeyValue,
// falling back to a string representation for unknown types.
func attributeFromAny(key string, value any) attribute.KeyValue {
	k := attribute.Key(key)
	switch v := value.(type) {
	case string:
		return k.String(v)
	case bool:
		return k.Bool(v)
	case int:
		return k.Int64(int64(v))
	case int32:
		return k.Int64(int64(v))
	case int64:
		return k.Int64(v)
	case float32:
		return k.Float64(float64(v))
	case float64:
		return k.Float64(v)
	case []string:
		return k.StringSlice(v)
	default:
		return k.String(fmt.Sprintf("%v", v))
	}
}
