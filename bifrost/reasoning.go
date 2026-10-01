// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"cmp"
	"regexp"
	"strconv"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// thinkingOff reports whether this request should run without reasoning.
func (b *LLM) thinkingOff(cfg llm.LanguageModelConfig) bool {
	return !b.reasoningEnabled || cfg.ReasoningDisabled
}

// reasoningParams is the provider-neutral reasoning block shared by the chat
// and Responses paths.
type reasoningParams struct {
	effort    *string
	maxTokens *int
	// hideThinking asks the provider not to return reasoning text.
	hideThinking bool
}

// providerReasoning resolves the reasoning block shared by the chat and
// Responses paths. ok=false means the reasoning block must be omitted; other
// providers reject reasoning parameters on these paths. Gemini/Vertex get an
// explicit budget or effort fallback (Bifrost maps reasoning.max_tokens to
// thinkingConfig.thinkingBudget and reasoning.effort to
// thinkingConfig.thinkingLevel on 3.0+). OpenAI/Azure reasoning is
// Responses-API-only and handled directly by buildResponsesReasoning.
func (b *LLM) providerReasoning(cfg llm.LanguageModelConfig) (reasoningParams, bool) {
	switch b.provider {
	case schemas.Anthropic:
		if b.thinkingOff(cfg) {
			return anthropicThinkingOff(cfg.Model)
		}
		return b.anthropicReasoning(cfg)
	case schemas.Gemini, schemas.Vertex:
		if b.thinkingOff(cfg) {
			return reasoningParams{}, false
		}
		if b.thinkingBudget > 0 {
			return reasoningParams{maxTokens: new(b.thinkingBudget)}, true
		}
		return reasoningParams{effort: new(cmp.Or(b.reasoningEffort, "medium"))}, true
	default:
		return reasoningParams{}, false
	}
}

// anthropicReasoning maps the configured effort onto the model. Adaptive
// models get the effort alone: Bifrost's chat converter prefers max_tokens
// when both are set and would drop the effort. Budget-only models (and Opus
// 4.5, whose Bifrost effort mapping scales the budget with max_tokens
// uncapped) get a budget from anthropicEffortBudgets.
func (b *LLM) anthropicReasoning(cfg llm.LanguageModelConfig) (reasoningParams, bool) {
	level := anthropicEffort(b.reasoningEffort)
	if anthropic.SupportsAdaptiveThinking(cfg.Model) {
		if level != "" {
			return reasoningParams{effort: new(clampAnthropicEffort(cfg.Model, level))}, true
		}
		if !anthropic.IsAdaptiveOnlyThinkingModel(cfg.Model) {
			// Opus/Sonnet 4.6 need an effort to select adaptive over the
			// deprecated budget mode; high is their documented default.
			return reasoningParams{effort: new("high")}, true
		}
		// A budget makes Bifrost send adaptive thinking without an effort, so
		// the model applies its own default (medium on Opus 5.5, high elsewhere).
	}
	budget, ok := anthropicThinkingBudget(cmp.Or(level, "high"), cfg.MaxGeneratedTokens)
	if !ok {
		return reasoningParams{}, false
	}
	return reasoningParams{maxTokens: new(budget)}, true
}

// anthropicThinkingOff turns thinking off as far as the model allows. Models
// that think without a thinking parameter need an explicit "disabled" (sent as
// effort "none"); models that reject "disabled" run at the lowest effort with
// their reasoning hidden. Older models think only when asked.
func anthropicThinkingOff(model string) (reasoningParams, bool) {
	switch {
	case anthropicThinkingAlwaysOn(model):
		return reasoningParams{effort: new("low"), hideThinking: true}, true
	case anthropicThinksByDefault(model):
		return reasoningParams{effort: new("none")}, true
	default:
		return reasoningParams{}, false
	}
}

var claudeVersionPattern = regexp.MustCompile(`(?i)(opus|sonnet)-(\d{1,2})(?:[-.](\d{1,2}))?(?:\D|$)`)

// claudeVersion extracts the Opus/Sonnet version from IDs such as
// claude-opus-5-5, claude-sonnet-5-20260101 or claude-opus-5-5@20260101. A
// long trailing number is a date, not a minor version. ok=false for other
// families and legacy names like claude-3-7-sonnet.
func claudeVersion(model string) (major, minor int, ok bool) {
	m := claudeVersionPattern.FindStringSubmatch(model)
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		minor, _ = strconv.Atoi(m[3])
	}
	return major, minor, true
}

// anthropicThinksByDefault reports whether the model runs adaptive thinking
// when the request has no thinking parameter: Opus 5+, Sonnet 5+ and the
// Fable/Mythos family.
func anthropicThinksByDefault(model string) bool {
	if anthropic.IsFableFamily(model) {
		return true
	}
	major, _, ok := claudeVersion(model)
	return ok && major >= 5
}

// anthropicThinkingAlwaysOn reports whether the model rejects
// thinking:{type:"disabled"} outright: Opus 5.5+, Sonnet 5.5+ and the
// Fable/Mythos family.
func anthropicThinkingAlwaysOn(model string) bool {
	if anthropic.IsFableFamily(model) {
		return true
	}
	major, minor, ok := claudeVersion(model)
	return ok && (major > 5 || (major == 5 && minor >= 5))
}

// anthropicEffort normalizes the configured effort to an Anthropic level.
// Unset and unrecognized values return "", the model's default.
func anthropicEffort(effort string) string {
	switch effort {
	case "minimal", "low":
		return "low"
	case "medium", "high", "xhigh", "max":
		return effort
	default:
		return ""
	}
}

// clampAnthropicEffort lowers xhigh to high on the adaptive models that don't
// offer it (Opus/Sonnet 4.6 and Mythos Preview). Every adaptive model accepts
// max.
func clampAnthropicEffort(model, effort string) string {
	if effort == "xhigh" && (!anthropic.IsAdaptiveOnlyThinkingModel(model) || anthropic.IsMythosPreview(model)) {
		return "high"
	}
	return effort
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
	"xhigh":  {divisor: 3, maxBudget: 16384},
	"max":    {divisor: 2, maxBudget: 32768},
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
