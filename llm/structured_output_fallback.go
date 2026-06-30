// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"context"
	"strings"
)

// StructuredOutputFallbackWrapper wraps a LanguageModel and applies fallback
// structured output handling when a JSON schema is requested but the upstream
// LLM does not support native structured outputs. Currently the fallback
// strips markdown code fencing that LLMs frequently wrap around JSON responses.
type StructuredOutputFallbackWrapper struct {
	wrapped                 LanguageModel
	structuredOutputEnabled bool
}

func NewStructuredOutputFallbackWrapper(llm LanguageModel, structuredOutputEnabled bool) *StructuredOutputFallbackWrapper {
	return &StructuredOutputFallbackWrapper{
		wrapped:                 llm,
		structuredOutputEnabled: structuredOutputEnabled,
	}
}

func (w *StructuredOutputFallbackWrapper) ChatCompletion(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	result, err := w.wrapped.ChatCompletion(ctx, request, opts...)
	if err != nil {
		return result, err
	}

	if w.structuredOutputEnabled || !hasJSONOutputSchema(opts) {
		return result, nil
	}

	// Providers without native structured output frequently wrap JSON in
	// markdown code fences. The fence only resolves once the whole response is
	// known, so buffer the text chunks and emit the stripped JSON as a single
	// text event at the end. Non-text events pass through unchanged.
	return stripFencingFromStream(result), nil
}

func stripFencingFromStream(source *TextStreamResult) *TextStreamResult {
	out := make(chan TextStreamEvent)

	go func() {
		defer close(out)

		var text strings.Builder
		for event := range source.Stream {
			switch event.Type {
			case EventTypeText:
				if chunk, ok := event.Value.(string); ok {
					text.WriteString(chunk)
				}
			case EventTypeEnd:
				if cleaned := StripMarkdownCodeFencing(text.String()); cleaned != "" {
					out <- TextStreamEvent{Type: EventTypeText, Value: cleaned}
				}
				out <- event
				return
			case EventTypeError:
				out <- event
				return
			default:
				out <- event
			}
		}

		// Source closed without an explicit end/error event: flush whatever
		// text was buffered so callers still receive the (stripped) response.
		if cleaned := StripMarkdownCodeFencing(text.String()); cleaned != "" {
			out <- TextStreamEvent{Type: EventTypeText, Value: cleaned}
		}
	}()

	return &TextStreamResult{Stream: out}
}

func (w *StructuredOutputFallbackWrapper) ChatCompletionNoStream(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	response, err := w.wrapped.ChatCompletionNoStream(ctx, request, opts...)
	if err != nil {
		return response, err
	}

	if !w.structuredOutputEnabled && hasJSONOutputSchema(opts) {
		response = StripMarkdownCodeFencing(response)
	}

	return response, nil
}

func (w *StructuredOutputFallbackWrapper) CountTokens(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (int, error) {
	return w.wrapped.CountTokens(ctx, request, opts...)
}

func (w *StructuredOutputFallbackWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}

func (w *StructuredOutputFallbackWrapper) OutputTokenLimit() int {
	return w.wrapped.OutputTokenLimit()
}

func hasJSONOutputSchema(opts []LanguageModelOption) bool {
	var cfg LanguageModelConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg.JSONOutputFormat != nil
}
