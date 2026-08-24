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
// fallback works everywhere at the cost of some accuracy. So a combination is
// only "supported" when both the request shape we send and the provider's
// acceptance of it are positively known; everything model-dependent stays
// "unknown", which routes to the prompt fallback while remaining
// distinguishable from a known-incapable provider.
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
//	openai            supported for the model families known to implement
//	                  strict json_schema; unknown otherwise.
//	azure             unknown. Deployment names are operator-chosen, so the
//	                  model string carries no reliable model identity, and
//	                  strict json_schema needs a recent model + API version.
//	openaicompatible  unknown, always. The endpoint is an arbitrary
//	                  operator-run server (Ollama, vLLM, LiteLLM, a proxy);
//	                  some ignore response_format, some reject it.
//	anthropic         unknown. Native structured output (output_config.format)
//	                  exists only on recent Claude models; older ones reject
//	                  it. Sending a schema also disables extended thinking
//	                  (thinkingBlockedBySchema), so guessing here is costly.
//	gemini            supported for gemini-* models of the 1.5+ generations
//	                  (response_schema is part of the Gemini API from 1.5 on);
//	                  unknown for the retired 1.0 generation, its legacy
//	                  gemini-pro / gemini-ultra aliases, and anything else
//	                  served by the same endpoint (Gemma models in particular
//	                  do not support response schemas).
//	vertex            unknown. The same endpoint serves Gemini, Anthropic and
//	                  partner models, and Bifrost downgrades Anthropic-on-Vertex
//	                  structured output to a forced tool call.
//	bedrock           unknown. Bifrost converts the schema into a forced tool,
//	                  which only works for tool-capable models on Bedrock.
//	cohere, mistral   unknown. Only the newer model generations accept a
//	                  json_schema response format.
//	scale             unsupported: the Bifrost adapter cannot build this type
//	                  at all, so no native path exists.
//	loadtest_mock     unsupported: loadtest.MockLLM ignores JSONOutputFormat
//	                  entirely and emits templated prose, so it can never
//	                  honor a native schema. Prompt fallback also keeps the
//	                  mock exercising the same request transformation as a real
//	                  prompt-fallback provider.
//	anything else     unsupported: not constructible by Bifrost.
func ResolveStructuredOutputCapability(svc llm.ServiceConfig, model string) llm.StructuredOutputCapability {
	switch svc.Type {
	case llm.ServiceTypeOpenAI:
		// Direct OpenAI always takes the Responses API path, so the only open
		// question is the model.
		if !llm.ServiceUsesResponsesAPI(svc) {
			return llm.StructuredOutputCapabilityUnknown
		}
		if openAIModelSupportsStrictJSONSchema(model) {
			return llm.StructuredOutputCapabilitySupported
		}
		return llm.StructuredOutputCapabilityUnknown

	case llm.ServiceTypeGemini:
		if geminiModelSupportsResponseSchema(model) {
			return llm.StructuredOutputCapabilitySupported
		}
		return llm.StructuredOutputCapabilityUnknown

	case llm.ServiceTypeAzure,
		llm.ServiceTypeOpenAICompatible,
		llm.ServiceTypeAnthropic,
		llm.ServiceTypeVertex,
		llm.ServiceTypeBedrock,
		llm.ServiceTypeCohere,
		llm.ServiceTypeMistral:
		return llm.StructuredOutputCapabilityUnknown

	default:
		// Includes llm.ServiceTypeScale and llm.ServiceTypeLoadTestMock: no
		// Bifrost provider path exists, so there is nothing to send a schema
		// to.
		return llm.StructuredOutputCapabilityUnsupported
	}
}

// openAIModelSupportsStrictJSONSchema reports whether an OpenAI model family is
// known to implement Structured Outputs (strict json_schema). Matching is by
// family prefix so new dated snapshots of a supported family keep working
// without a code change; unrecognized names stay unknown so a private or
// future model never silently starts receiving native schemas.
//
// Exclusions within otherwise-supported families, per OpenAI's Structured
// Outputs guide (which gates support at o1-2024-12-17 / gpt-4o-2024-08-06 and
// later):
//   - gpt-4o-2024-05-13 predates Structured Outputs.
//   - o1-mini and o1-preview predate the full o1 release and are not listed as
//     supporting strict json_schema; only the o1 (2024-12-17+) snapshots are.
//   - chatgpt-4o-latest is a ChatGPT-serving alias that is not listed in the
//     Structured Outputs guide, so it stays unknown.
func openAIModelSupportsStrictJSONSchema(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return false
	}

	unsupportedExceptions := []string{
		"gpt-4o-2024-05-13",
		"o1-mini",
		"o1-preview",
	}
	for _, exception := range unsupportedExceptions {
		if normalized == exception || strings.HasPrefix(normalized, exception+"-") {
			return false
		}
	}

	supportedFamilies := []string{
		"gpt-4o",
		"gpt-4.1",
		"gpt-4.5",
		"gpt-5",
		"o1",
		"o3",
		"o4",
	}
	for _, family := range supportedFamilies {
		if normalized == family || strings.HasPrefix(normalized, family+"-") {
			return true
		}
	}
	return false
}

// geminiModelSupportsResponseSchema reports whether the model name identifies a
// Gemini model generation with response_schema support (1.5 and later). The
// Gemini API also serves Gemma models, which do not support response schemas,
// and the retired 1.0 generation (including its legacy gemini-pro /
// gemini-ultra aliases) predates them.
func geminiModelSupportsResponseSchema(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if !strings.HasPrefix(normalized, "gemini-") {
		return false
	}

	legacyGenerations := []string{
		"gemini-1.0",
		"gemini-pro",
		"gemini-ultra",
	}
	for _, legacy := range legacyGenerations {
		if normalized == legacy || strings.HasPrefix(normalized, legacy+"-") {
			return false
		}
	}
	return true
}
