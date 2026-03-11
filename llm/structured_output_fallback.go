// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

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

func (w *StructuredOutputFallbackWrapper) ChatCompletion(request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	return w.wrapped.ChatCompletion(request, opts...)
}

func (w *StructuredOutputFallbackWrapper) ChatCompletionNoStream(request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	response, err := w.wrapped.ChatCompletionNoStream(request, opts...)
	if err != nil {
		return response, err
	}

	if !w.structuredOutputEnabled && hasJSONOutputSchema(opts) {
		response = StripMarkdownCodeFencing(response)
	}

	return response, nil
}

func (w *StructuredOutputFallbackWrapper) CountTokens(text string) int {
	return w.wrapped.CountTokens(text)
}

func (w *StructuredOutputFallbackWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}

func hasJSONOutputSchema(opts []LanguageModelOption) bool {
	var cfg LanguageModelConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg.JSONOutputFormat != nil
}
