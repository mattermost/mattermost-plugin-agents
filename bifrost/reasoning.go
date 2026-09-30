// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"cmp"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// thinkingBlockedBySchema reports whether extended thinking must be dropped
// for this request: Anthropic rejects thinking combined with structured output.
func (b *LLM) thinkingBlockedBySchema(cfg llm.LanguageModelConfig) bool {
	return b.provider == schemas.Anthropic && cfg.JSONOutputFormat != nil
}

// providerReasoningBudget resolves the reasoning decision shared by the chat
// and Responses paths. Anthropic gets a native effort on adaptive-thinking
// models and an effort-derived token budget elsewhere; Gemini/Vertex get an
// explicit budget or effort fallback (Bifrost maps reasoning.max_tokens to
// thinkingConfig.thinkingBudget and reasoning.effort to
// thinkingConfig.thinkingLevel on 3.0+). ok=false means the reasoning block
// must be omitted; other providers reject reasoning parameters on these paths.
// OpenAI/Azure reasoning is Responses-API-only and handled directly by
// buildResponsesReasoning.
func (b *LLM) providerReasoningBudget(cfg llm.LanguageModelConfig) (effort *string, maxTokens *int, ok bool) {
	switch b.provider {
	case schemas.Anthropic:
		return b.anthropicReasoning(cfg)
	case schemas.Gemini, schemas.Vertex:
		if b.thinkingBudget > 0 {
			return nil, new(b.thinkingBudget), true
		}
		return new(cmp.Or(b.reasoningEffort, "medium")), nil, true
	default:
		return nil, nil, false
	}
}

// anthropicReasoning sends effort alone to adaptive-thinking models: Bifrost's
// chat converter prefers max_tokens when both are set and would drop the
// effort. Budget-only models (and Opus 4.5, whose Bifrost effort mapping scales
// the budget with max_tokens uncapped) get a budget from anthropicEffortBudgets.
func (b *LLM) anthropicReasoning(cfg llm.LanguageModelConfig) (effort *string, maxTokens *int, ok bool) {
	level := anthropicEffort(b.reasoningEffort)
	if anthropic.SupportsAdaptiveThinking(cfg.Model) {
		return new(level), nil, true
	}
	budget, ok := anthropicThinkingBudget(level, cfg.MaxGeneratedTokens)
	if !ok {
		return nil, nil, false
	}
	return nil, new(budget), true
}

// anthropicEffort normalizes the configured effort to a level every
// effort-capable Anthropic model accepts. Unset and unrecognized values
// resolve to high, the depth used before effort was configurable.
func anthropicEffort(effort string) string {
	switch effort {
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	default:
		return "high"
	}
}

// minThinkingBudget is Anthropic's floor for budget_tokens, which must also
// stay strictly below max_tokens.
const minThinkingBudget = 1024

// anthropicEffortBudgets maps an effort level to a thinking budget of
// max_tokens/divisor, capped at maxBudget. High matches the budget sent before
// effort was configurable.
var anthropicEffortBudgets = map[string]struct{ divisor, maxBudget int }{
	"low":    {divisor: 16, maxBudget: 2048},
	"medium": {divisor: 8, maxBudget: 4096},
	"high":   {divisor: 4, maxBudget: 8192},
}

// anthropicThinkingBudget returns the budget for an effort level, raised to
// minThinkingBudget. Returns ok=false when that leaves no room below
// max_tokens, in which case thinking is omitted.
func anthropicThinkingBudget(effort string, maxGeneratedTokens int) (int, bool) {
	scale, found := anthropicEffortBudgets[effort]
	if !found {
		scale = anthropicEffortBudgets["high"]
	}
	budget := max(min(maxGeneratedTokens/scale.divisor, scale.maxBudget), minThinkingBudget)
	if budget >= maxGeneratedTokens {
		return 0, false
	}
	return budget, true
}
