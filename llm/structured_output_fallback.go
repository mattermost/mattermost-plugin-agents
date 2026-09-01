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
// structured output capability decision. Requests without a JSON schema pass
// through untouched. When a schema is requested, the wrapper resolves a single
// mode for the whole request before calling the provider: either the schema is
// sent natively, or it is stripped from the provider request and converted
// into a prompt-level system instruction (with markdown code fencing stripped
// from non-streaming responses).
type StructuredOutputFallbackWrapper struct {
	wrapped LanguageModel
	// nativeAllowed answers, for the model a request will actually run, whether
	// its schema may be sent natively. See NewNativeStructuredOutputDecision.
	nativeAllowed func(requestedModel string) bool
}

// NewStructuredOutputFallbackWrapper wraps llm with the structured-output
// decision in nativeAllowed. A nil nativeAllowed keeps every schema request on
// the prompt fallback.
func NewStructuredOutputFallbackWrapper(wrapped LanguageModel, nativeAllowed func(requestedModel string) bool) *StructuredOutputFallbackWrapper {
	return &StructuredOutputFallbackWrapper{
		wrapped:       wrapped,
		nativeAllowed: nativeAllowed,
	}
}

// NewNativeStructuredOutputDecision builds the native-schema decision for one
// provider chain: the primary service with the model it would run, plus the
// services Bifrost may fail over to. resolver answers the auto policy; a nil
// resolver puts every auto target on the prompt fallback.
//
// The decision has to cover every attempt Bifrost may make, because the
// prompt/schema transformation happens before Bifrost picks which provider
// actually serves the request: one incapable attempt puts the whole request on
// the prompt fallback. Only the primary's model can change per request (a
// per-call WithModel override), so the fallbacks' verdict is fixed and is
// computed once here rather than on every call.
func NewNativeStructuredOutputDecision(primary ServiceConfig, primaryModel string, fallbacks []ServiceConfig, resolver StructuredOutputCapabilityResolver) func(requestedModel string) bool {
	fallbacksAllowNative := true
	for _, fallback := range fallbacks {
		// A Bifrost fallback always runs its own default model.
		if !serviceAllowsNativeOutput(fallback, fallback.DefaultModel, resolver) {
			fallbacksAllowNative = false
			break
		}
	}

	return func(requestedModel string) bool {
		if !fallbacksAllowNative {
			return false
		}
		model := primaryModel
		if requestedModel != "" {
			model = requestedModel
		}
		return serviceAllowsNativeOutput(primary, model, resolver)
	}
}

// serviceAllowsNativeOutput applies svc's structured-output policy to the model
// this attempt would run.
func serviceAllowsNativeOutput(svc ServiceConfig, model string, resolver StructuredOutputCapabilityResolver) bool {
	switch svc.EffectiveStructuredOutputPolicy() {
	case StructuredOutputPolicyNative:
		// Administratively asserted capable.
		return true
	case StructuredOutputPolicyPromptFallback:
		return false
	case StructuredOutputPolicyAuto:
		if resolver == nil {
			return false
		}
		// Only positive knowledge earns a native schema.
		return resolver(svc, model)
	default:
		// An unrecognized stored value must never send a native schema.
		return false
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
	schema, requestedModel := resolveOutputOptions(opts)
	if schema == nil {
		return request, opts, false
	}

	if w.nativeAllowed != nil && w.nativeAllowed(requestedModel) {
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

// resolveOutputOptions applies the caller's options once and returns the two
// values the structured-output decision needs: the requested JSON schema, if
// any, and the per-call model override.
func resolveOutputOptions(opts []LanguageModelOption) (*jsonschema.Schema, string) {
	var cfg LanguageModelConfig
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&cfg)
	}
	return cfg.JSONOutputFormat, cfg.Model
}
