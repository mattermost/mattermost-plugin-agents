// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// ResolveStructuredOutputCapability reports whether a native JSON schema can be
// sent to the given service/model combination. It is the single source of
// truth for the `auto` structured-output policy.
//
// The table below is deliberately conservative: a native schema that the
// provider rejects turns every structured-output feature (title generation,
// thread analysis, emoji selection, evals) into a hard error, while the prompt
// fallback works everywhere at the cost of some accuracy. So this reports true
// only when both the request shape we send and the provider's acceptance of it
// are positively known; everything model-dependent reports false and takes the
// prompt fallback. A combination that is merely not positively known and one
// that is known to have no native path at all are therefore treated
// identically — the difference is documented below rather than reported.
//
// How the schema is actually sent (see bifrost.go) determines what "capable"
// means per provider:
//   - Responses API path (llm.ServiceUsesResponsesAPI): buildResponsesTextConfig
//     sets text.format = json_schema with strict: true.
//   - Chat Completions path: buildChatResponseFormat sets
//     response_format = json_schema with strict: true.
//   - Bifrost then translates that per provider: Gemini/Vertex map it onto
//     response_schema, Anthropic onto output_config.format (or a forced tool on
//     Vertex/Azure/Bedrock Mantle), Bedrock onto a forced tool.
//
// The table:
//
//	openai            capable for the model families known to implement strict
//	                  json_schema; any other model name is not positively
//	                  known to be capable, so it takes the prompt fallback.
//	azure             not positively known to be capable. Deployment names are
//	                  operator-chosen, so the model string carries no reliable
//	                  model identity, and strict json_schema needs a recent
//	                  model + API version.
//	openaicompatible  never positively known to be capable. The endpoint is an
//	                  arbitrary operator-run server (Ollama, vLLM, LiteLLM, a
//	                  proxy); some ignore response_format, some reject it.
//	anthropic         not positively known to be capable. Native structured
//	                  output (output_config.format) exists only on recent
//	                  Claude models; older ones reject it. Sending a schema
//	                  also disables extended thinking (thinkingBlockedBySchema),
//	                  so guessing here is costly.
//	gemini            capable for gemini-* models of the 1.5+ generations
//	                  (response_schema is part of the Gemini API from 1.5 on).
//	                  The retired 1.0 generation, its legacy gemini-pro /
//	                  gemini-ultra aliases, embedding models, and anything else
//	                  served by the same endpoint (Gemma models in particular
//	                  do not support response schemas) are not.
//	vertex            not positively known to be capable. The same endpoint
//	                  serves Gemini, Anthropic and partner models, and Bifrost
//	                  downgrades Anthropic-on-Vertex structured output to a
//	                  forced tool call.
//	bedrock           not positively known to be capable. Bifrost converts the
//	                  schema into a forced tool, which only works for
//	                  tool-capable models on Bedrock.
//	cohere, mistral   not positively known to be capable. Only the newer model
//	                  generations accept a json_schema response format.
//	scale             has no native path: the Bifrost adapter cannot build
//	                  this type at all.
//	loadtest_mock     has no native path: loadtest.MockLLM ignores
//	                  JSONOutputFormat entirely and emits templated prose, so
//	                  it can never honor a native schema. Prompt fallback also
//	                  keeps the mock exercising the same request
//	                  transformation as a real prompt-fallback provider.
//	anything else     has no native path: not constructible by Bifrost.
func ResolveStructuredOutputCapability(svc llm.ServiceConfig, model string) bool {
	switch svc.Type {
	case llm.ServiceTypeOpenAI:
		// Direct OpenAI always takes the Responses API path
		// (llm.ServiceUsesResponsesAPI is unconditionally true for it), so the
		// only open question is the model.
		return openAIModelSupportsStrictJSONSchema(model)

	case llm.ServiceTypeGemini:
		return geminiModelSupportsResponseSchema(model)

	default:
		// Everything else is either model-dependent behind an identity we
		// cannot read (azure, openaicompatible, anthropic, vertex, bedrock,
		// cohere, mistral) or has no native path at all (scale,
		// loadtest_mock, unrecognized types).
		return false
	}
}

// matchesModelFamily reports whether the normalized model name is one of the
// given families exactly, or a variant of one (a dated snapshot, a size, or any
// other `family-suffix` name).
func matchesModelFamily(normalized string, families ...string) bool {
	for _, family := range families {
		if normalized == family || strings.HasPrefix(normalized, family+"-") {
			return true
		}
	}
	return false
}

// openAIModelSupportsStrictJSONSchema reports whether an OpenAI model family is
// known to implement Structured Outputs (strict json_schema). Matching is by
// family prefix so new dated snapshots of a supported family keep working
// without a code change; unrecognized names stay unsupported so a private or
// future model never silently starts receiving native schemas.
//
// Exclusions within otherwise-supported families, per OpenAI's Structured
// Outputs guide (which gates support at o1-2024-12-17 / gpt-4o-2024-08-06 and
// later):
//   - gpt-4o-2024-05-13 predates Structured Outputs.
//   - o1-mini and o1-preview predate the full o1 release and are not listed as
//     supporting strict json_schema; only the o1 (2024-12-17+) snapshots are.
//   - chatgpt-4o-latest is a ChatGPT-serving alias that is not listed in the
//     Structured Outputs guide, so it stays unsupported.
//   - the audio, realtime and search variants of a supported family are
//     separate models with their own request shapes and do not implement
//     strict json_schema. They are matched anywhere in the name so both
//     gpt-4o-audio-preview and gpt-4o-mini-audio-preview, plus any dated
//     snapshot of either, stay excluded.
func openAIModelSupportsStrictJSONSchema(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return false
	}

	if matchesModelFamily(normalized, "gpt-4o-2024-05-13", "o1-mini", "o1-preview") {
		return false
	}
	for _, modality := range []string{"-audio", "-realtime", "-search"} {
		if strings.Contains(normalized, modality) {
			return false
		}
	}

	return matchesModelFamily(normalized,
		"gpt-4o",
		"gpt-4.1",
		"gpt-4.5",
		"gpt-5",
		"o1",
		"o3",
		"o4",
	)
}

// geminiModelSupportsResponseSchema reports whether the model name identifies a
// Gemini model generation with response_schema support (1.5 and later). The
// Gemini API also serves Gemma models, which do not support response schemas,
// embedding models, which produce vectors rather than text, and the retired 1.0
// generation (including its legacy gemini-pro / gemini-ultra aliases), which
// predates them.
func geminiModelSupportsResponseSchema(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if !strings.HasPrefix(normalized, "gemini-") {
		return false
	}
	if strings.Contains(normalized, "embedding") {
		return false
	}

	return !matchesModelFamily(normalized, "gemini-1.0", "gemini-pro", "gemini-ultra")
}
