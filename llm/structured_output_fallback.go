// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// StructuredOutputTarget is one provider attempt the wrapped model may make:
// the primary service or one entry of its fallback chain, paired with the
// model that attempt would use.
type StructuredOutputTarget struct {
	Service ServiceConfig
	Model   string // effective model when this target is attempted
}

// StructuredOutputFallbackWrapper wraps a LanguageModel and encapsulates the
// structured output capability decision. Requests without a JSON schema pass
// through untouched. When a schema is requested, the wrapper resolves a single
// mode for the whole request before calling the provider: either the schema is
// sent natively, or it is stripped from the provider request and converted
// into a prompt-level system instruction (with markdown code fencing stripped
// from non-streaming responses).
//
// The decision covers the primary target and every fallback target, because
// the prompt/schema transformation happens here, before Bifrost picks which
// provider actually serves the request. A chain is only eligible for native
// output when every attempt in it is capable.
type StructuredOutputFallbackWrapper struct {
	wrapped   LanguageModel
	primary   StructuredOutputTarget
	fallbacks []StructuredOutputTarget
	resolver  StructuredOutputCapabilityResolver
}

// NewStructuredOutputFallbackWrapper wraps llm with the structured-output
// decision for the given provider chain. resolver answers the auto policy; a
// nil resolver makes every auto target fall back to prompt instructions.
func NewStructuredOutputFallbackWrapper(wrapped LanguageModel, primary StructuredOutputTarget, fallbacks []StructuredOutputTarget, resolver StructuredOutputCapabilityResolver) *StructuredOutputFallbackWrapper {
	return &StructuredOutputFallbackWrapper{
		wrapped:   wrapped,
		primary:   primary,
		fallbacks: fallbacks,
		resolver:  resolver,
	}
}

func (w *StructuredOutputFallbackWrapper) ChatCompletion(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	request, opts, _ = w.applyFallback(request, opts)
	return w.wrapped.ChatCompletion(ctx, request, opts...)
}

func (w *StructuredOutputFallbackWrapper) ChatCompletionNoStream(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	downstreamRequest, downstreamOpts, fallbackApplied := w.applyFallback(request, opts)
	response, err := w.wrapped.ChatCompletionNoStream(ctx, downstreamRequest, downstreamOpts...)
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
	return w.wrapped.CountTokens(ctx, request, opts...)
}

func (w *StructuredOutputFallbackWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}

func (w *StructuredOutputFallbackWrapper) OutputTokenLimit() int {
	return w.wrapped.OutputTokenLimit()
}

// applyFallback strips the JSON output schema from the downstream request and
// injects an equivalent prompt-level instruction when the resolved chain
// cannot take a native schema. The caller's request and opts are never
// mutated. Returns whether the fallback was applied.
func (w *StructuredOutputFallbackWrapper) applyFallback(request CompletionRequest, opts []LanguageModelOption) (CompletionRequest, []LanguageModelOption, bool) {
	schema := resolveJSONOutputSchema(opts)
	if schema == nil {
		return request, opts, false
	}

	if w.chainSupportsNativeOutput(opts) {
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

// chainSupportsNativeOutput reports whether every provider attempt Bifrost may
// make for this request can take the schema natively. One incapable attempt
// puts the whole request on the prompt fallback, because the transformation
// has to be decided before the provider is chosen.
func (w *StructuredOutputFallbackWrapper) chainSupportsNativeOutput(opts []LanguageModelOption) bool {
	primary := w.primary
	// A per-call WithModel overrides the target's configured model, and only
	// for the primary: Bifrost fallbacks always run their own default model.
	if requested := extractRequestedModel(opts...); requested != "" {
		primary.Model = requested
	}

	if !w.targetSupportsNativeOutput(primary) {
		return false
	}
	for _, fallback := range w.fallbacks {
		if !w.targetSupportsNativeOutput(fallback) {
			return false
		}
	}
	return true
}

func (w *StructuredOutputFallbackWrapper) targetSupportsNativeOutput(target StructuredOutputTarget) bool {
	switch target.Service.EffectiveStructuredOutputPolicy() {
	case StructuredOutputPolicyNative:
		// Administratively asserted capable.
		return true
	case StructuredOutputPolicyPromptFallback:
		return false
	case StructuredOutputPolicyAuto:
		if w.resolver == nil {
			return false
		}
		// Only positive knowledge earns a native schema; unknown and
		// unsupported both take the prompt fallback.
		return w.resolver(target.Service, target.Model) == StructuredOutputCapabilitySupported
	default:
		// An unrecognized stored value must never send a native schema.
		return false
	}
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
