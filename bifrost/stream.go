// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"cmp"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// toolArgsToJSON ensures tool arguments are valid JSON.
// Tools with no parameters produce an empty string which is not valid JSON,
// so we default to "{}".
func toolArgsToJSON(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(s)
}

// setTokenUsageSpanAttributes is the converted-TokenUsage counterpart of
// setUsageAttributes in tracer.go.
func setTokenUsageSpanAttributes(span trace.Span, usage llm.TokenUsage) {
	attrs := []attribute.KeyValue{
		telemetry.LLMInputTokens.Int64(usage.InputTokens),
		telemetry.LLMOutputTokens.Int64(usage.OutputTokens),
	}
	if usage.CachedReadTokens > 0 {
		attrs = append(attrs, telemetry.LLMCachedReadTokens.Int64(usage.CachedReadTokens))
	}
	if usage.CachedWriteTokens > 0 {
		attrs = append(attrs, telemetry.LLMCachedWriteTokens.Int64(usage.CachedWriteTokens))
	}
	if usage.ReasoningTokens > 0 {
		attrs = append(attrs, telemetry.LLMReasoningTokens.Int64(usage.ReasoningTokens))
	}
	if usage.Cost > 0 {
		attrs = append(attrs, telemetry.LLMCost.Float64(usage.Cost))
	}
	span.SetAttributes(attrs...)
}

// setCompositionSpanAttributes attaches per-source token attribution to the
// span, derived from the request's posts and tools and scaled to the
// provider's input-token total. One attribute per source.
func setCompositionSpanAttributes(span trace.Span, request llm.CompletionRequest, usage llm.TokenUsage) {
	if usage.InputTokens <= 0 {
		return
	}
	inputs := request.Composition()
	if len(inputs) == 0 {
		return
	}
	composition := llm.ComputeComposition(inputs, int(usage.InputTokens), llm.CompositionTotalProvider)
	attrs := composition.SpanAttributes()
	if len(attrs) == 0 {
		return
	}
	span.SetAttributes(attrs...)
}

func convertChatUsage(u *schemas.BifrostLLMUsage) llm.TokenUsage {
	if u == nil {
		return llm.TokenUsage{}
	}
	usage := llm.TokenUsage{
		InputTokens:  int64(u.PromptTokens),
		OutputTokens: int64(u.CompletionTokens),
	}
	if u.PromptTokensDetails != nil {
		usage.CachedReadTokens = int64(u.PromptTokensDetails.CachedReadTokens)
		usage.CachedWriteTokens = int64(u.PromptTokensDetails.CachedWriteTokens)
	}
	if u.CompletionTokensDetails != nil {
		usage.ReasoningTokens = int64(u.CompletionTokensDetails.ReasoningTokens)
	}
	if u.Cost != nil {
		usage.Cost = u.Cost.TotalCost
	}
	return usage
}

func convertResponsesUsage(u *schemas.ResponsesResponseUsage) llm.TokenUsage {
	if u == nil {
		return llm.TokenUsage{}
	}
	usage := llm.TokenUsage{
		InputTokens:  int64(u.InputTokens),
		OutputTokens: int64(u.OutputTokens),
	}
	if u.InputTokensDetails != nil {
		usage.CachedReadTokens = int64(u.InputTokensDetails.CachedReadTokens)
		usage.CachedWriteTokens = int64(u.InputTokensDetails.CachedWriteTokens)
	}
	if u.OutputTokensDetails != nil {
		usage.ReasoningTokens = int64(u.OutputTokensDetails.ReasoningTokens)
	}
	if u.Cost != nil {
		usage.Cost = u.Cost.TotalCost
	}
	return usage
}

// startStreamWatchdog starts a timer goroutine that calls cancel when no chunk
// arrives within the streaming timeout. The returned ping resets the timer and
// never blocks; the goroutine exits when done closes or the timer fires.
func (b *LLM) startStreamWatchdog(done <-chan struct{}, cancel func()) (ping func()) {
	watchdog := make(chan struct{})

	go func() {
		timer := time.NewTimer(b.streamingTimeout)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				cancel()
				return
			case <-done:
				return
			case <-watchdog:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(b.streamingTimeout)
			}
		}
	}()

	return func() {
		select {
		case watchdog <- struct{}{}:
		default:
		}
	}
}

// toolCallBuffer accumulates a streamed tool call's fields across delta chunks.
type toolCallBuffer struct {
	id        string
	name      string
	arguments strings.Builder
}

// flushToolCallBuffers converts buffered tool calls into llm.ToolCall values in
// sorted key order. When requireName is set, entries that never received a
// name are dropped.
func flushToolCallBuffers[K cmp.Ordered](buffers map[K]*toolCallBuffer, requireName bool) []llm.ToolCall {
	var toolCalls []llm.ToolCall
	for _, k := range slices.Sorted(maps.Keys(buffers)) {
		buf := buffers[k]
		if requireName && buf.name == "" {
			continue
		}
		toolCalls = append(toolCalls, llm.ToolCall{
			ID:        buf.id,
			Name:      buf.name,
			Arguments: toolArgsToJSON(buf.arguments.String()),
		})
	}
	return toolCalls
}

// reasoningAccumulator collects streamed reasoning text and its signature so a
// single EventTypeReasoningEnd carrying the full reasoning can be emitted.
type reasoningAccumulator struct {
	buffer    strings.Builder
	signature string
	complete  bool
}

// emitEnd emits an EventTypeReasoningEnd carrying the accumulated reasoning,
// unless nothing accumulated or the end event was already sent.
func (r *reasoningAccumulator) emitEnd(output chan<- llm.TextStreamEvent) {
	if r.complete || r.buffer.Len() == 0 {
		return
	}
	output <- llm.TextStreamEvent{
		Type: llm.EventTypeReasoningEnd,
		Value: llm.ReasoningData{
			Text:      r.buffer.String(),
			Signature: r.signature,
		},
	}
	r.complete = true
}
