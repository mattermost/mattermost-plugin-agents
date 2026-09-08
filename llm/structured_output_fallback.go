// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// StructuredOutputFallbackWrapper wraps a LanguageModel and encapsulates the
// structured output capability decision. When structured output is enabled,
// requests pass through untouched and any JSON schema is sent natively to the
// provider. When disabled and a JSON schema is requested, the schema is
// stripped from the provider request and converted into a prompt-level system
// instruction, and markdown code fencing is stripped from non-streaming
// responses.
type StructuredOutputFallbackWrapper struct {
	LanguageModel
	structuredOutputEnabled bool
}

func NewStructuredOutputFallbackWrapper(llm LanguageModel, structuredOutputEnabled bool) *StructuredOutputFallbackWrapper {
	return &StructuredOutputFallbackWrapper{
		LanguageModel:           llm,
		structuredOutputEnabled: structuredOutputEnabled,
	}
}

func (w *StructuredOutputFallbackWrapper) ChatCompletion(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	request, opts, _ = w.applyFallback(request, opts)
	return w.LanguageModel.ChatCompletion(ctx, request, opts...)
}

func (w *StructuredOutputFallbackWrapper) ChatCompletionNoStream(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	downstreamRequest, downstreamOpts, fallbackApplied := w.applyFallback(request, opts)
	response, err := w.LanguageModel.ChatCompletionNoStream(ctx, downstreamRequest, downstreamOpts...)
	if err != nil {
		return response, err
	}

	if fallbackApplied {
		response = StripMarkdownCodeFencing(response)
	}

	return response, nil
}

// CountTokens applies the fallback so counts reflect the request actually sent.
func (w *StructuredOutputFallbackWrapper) CountTokens(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (int, error) {
	request, opts, _ = w.applyFallback(request, opts)
	return w.LanguageModel.CountTokens(ctx, request, opts...)
}

// applyFallback strips the JSON output schema from the downstream request and
// injects an equivalent prompt-level instruction when structured output is
// disabled but a schema is requested. The caller's request and opts are never
// mutated. Returns whether the fallback was applied.
func (w *StructuredOutputFallbackWrapper) applyFallback(request CompletionRequest, opts []LanguageModelOption) (CompletionRequest, []LanguageModelOption, bool) {
	if w.structuredOutputEnabled {
		return request, opts, false
	}

	schema := resolveJSONOutputSchema(opts)
	if schema == nil {
		return request, opts, false
	}

	instruction := jsonOutputInstruction(schema)

	if idx := firstSystemPostIndex(request.Posts); idx >= 0 {
		posts := make([]Post, len(request.Posts))
		copy(posts, request.Posts)
		posts[idx].Message += "\n\n" + instruction
		request.Posts = posts
	} else {
		posts := make([]Post, 0, len(request.Posts)+1)
		posts = append(posts, Post{Role: PostRoleSystem, Message: instruction})
		posts = append(posts, request.Posts...)
		request.Posts = posts
	}

	newOpts := make([]LanguageModelOption, 0, len(opts)+1)
	newOpts = append(newOpts, opts...)
	newOpts = append(newOpts, func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = nil
	})

	return request, newOpts, true
}

func firstSystemPostIndex(posts []Post) int {
	for i, post := range posts {
		if post.Role == PostRoleSystem {
			return i
		}
	}
	return -1
}

// jsonOutputInstruction builds a system instruction asking the model to emit
// raw JSON conforming to the given schema. If the schema cannot be serialized,
// a schema-less generic instruction is returned instead of failing the request.
func jsonOutputInstruction(schema *jsonschema.Schema) string {
	shape := "value"
	if schema.Type == "object" {
		shape = "object"
	}
	base := fmt.Sprintf("Respond with a single valid JSON %s. Output raw JSON only: no markdown code fences, no explanation, no text before or after.", shape)
	if shape == "object" {
		base += " Your response must start with { and end with }."
	}

	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return base
	}

	return fmt.Sprintf("%s The JSON must conform to the following JSON schema:\n%s", base, schemaJSON)
}

func resolveJSONOutputSchema(opts []LanguageModelOption) *jsonschema.Schema {
	var cfg LanguageModelConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg.JSONOutputFormat
}
