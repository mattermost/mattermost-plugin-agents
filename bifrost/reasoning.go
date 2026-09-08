// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"cmp"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// thinkingBlockedBySchema reports whether extended thinking must be dropped
// for this request: Anthropic rejects thinking combined with structured output.
func (b *LLM) thinkingBlockedBySchema(cfg llm.LanguageModelConfig) bool {
	return b.provider == schemas.Anthropic && cfg.JSONOutputFormat != nil
}

// providerReasoningBudget resolves the reasoning decision shared by the chat
// and Responses paths: a clamped token budget for Anthropic, and an explicit
// budget or effort fallback for Gemini/Vertex (Bifrost maps
// reasoning.max_tokens to thinkingConfig.thinkingBudget and reasoning.effort
// to thinkingConfig.thinkingLevel on 3.0+). ok=false means the reasoning block
// must be omitted; other providers reject reasoning parameters on these paths.
// OpenAI/Azure reasoning is Responses-API-only and handled directly by
// buildResponsesReasoning.
func (b *LLM) providerReasoningBudget(cfg llm.LanguageModelConfig) (effort *string, maxTokens *int, ok bool) {
	switch b.provider {
	case schemas.Anthropic:
		budget, budgetOK := b.anthropicThinkingBudget(cfg.MaxGeneratedTokens)
		if !budgetOK {
			return nil, nil, false
		}
		return nil, new(budget), true
	case schemas.Gemini, schemas.Vertex:
		if b.thinkingBudget > 0 {
			return nil, new(b.thinkingBudget), true
		}
		return new(cmp.Or(b.reasoningEffort, "medium")), nil, true
	default:
		return nil, nil, false
	}
}

// Anthropic budget-based extended thinking requires
// minThinkingBudget <= budget < max_tokens.
const (
	minThinkingBudget        = 1024
	defaultMaxThinkingBudget = 8192
)

// calculateThinkingBudget computes the thinking budget for Anthropic models.
func (b *LLM) calculateThinkingBudget(maxGeneratedTokens int) int {
	if b.thinkingBudget > 0 {
		return max(b.thinkingBudget, minThinkingBudget)
	}
	budget := maxGeneratedTokens / 4
	return max(min(budget, defaultMaxThinkingBudget), minThinkingBudget)
}

// anthropicThinkingBudget returns the thinking budget to send for an Anthropic
// request, clamped into the provider's valid range (minThinkingBudget <=
// budget < max_tokens). Clamping — rather than gating on the primary model's
// capabilities — keeps the value valid for every Anthropic model that may see
// it: models that only support adaptive thinking ignore the budget entirely,
// while budget-based models (including any Anthropic fallback in the request's
// fallback chain) reject an out-of-range value with a 400. Returns ok=false
// when no valid budget exists, in which case thinking must be omitted.
func (b *LLM) anthropicThinkingBudget(maxGeneratedTokens int) (int, bool) {
	budget := b.calculateThinkingBudget(maxGeneratedTokens)
	if budget >= maxGeneratedTokens {
		budget = maxGeneratedTokens - 1
	}
	if budget < minThinkingBudget {
		return 0, false
	}
	return budget, true
}
