// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// CompositionSource labels where a piece of an LLM request came from. Used to
// attribute token cost back to its origin (system prompt, history, tool
// definitions, tool results, images) without changing how a request is
// assembled or billed. Attachment text is part of the message, so it counts
// as history.
type CompositionSource string

const (
	SourceSystem      CompositionSource = "system"
	SourceHistory     CompositionSource = "history"
	SourceToolDefs    CompositionSource = "tool_defs"
	SourceToolResults CompositionSource = "tool_results"
	SourceImage       CompositionSource = "image"
)

// compositionOrder is the fixed display/aggregation order for sources.
var compositionOrder = []CompositionSource{
	SourceSystem,
	SourceHistory,
	SourceToolDefs,
	SourceToolResults,
	SourceImage,
}

// CompositionInput is a single piece of content captured for attribution.
type CompositionInput struct {
	Source CompositionSource
	Text   string
}

// CompositionComponent is a single per-source row in a composition breakdown.
// Proportion is normalized so Components sum to 1.0 (modulo rounding); Tokens
// is round(Proportion * Total).
type CompositionComponent struct {
	Source     CompositionSource `json:"source"`
	Proportion float64           `json:"proportion"`
	Tokens     int               `json:"tokens"`
}

// CompositionTotalSource enumerates the provenance of Composition.Total.
const (
	// CompositionTotalCounted means the total came from a provider
	// CountTokens call (most accurate, pre-call).
	CompositionTotalCounted = "counted"
	// CompositionTotalProvider means the total came from the provider's
	// post-call usage report (most accurate, post-call).
	CompositionTotalProvider = "provider"
	// CompositionTotalEstimated means we fell back to EstimateTokens because
	// neither a counter nor a provider report was available.
	CompositionTotalEstimated = "estimated"
)

// Composition is the per-request, per-source token breakdown that powers the
// /context endpoint and the LLM-call span attributes.
type Composition struct {
	Components      []CompositionComponent `json:"components"`
	Total           int                    `json:"total"`
	TotalSource     string                 `json:"total_source"`
	InputTokenLimit int                    `json:"input_token_limit,omitempty"`
	Model           string                 `json:"model,omitempty"`
}

// imageWeightPlaceholder is the heuristic weight for an image. Vision providers
// price images very differently from text; this is a deliberately coarse
// stand-in so an image counts for *something* in the proportion math without
// claiming exact tokens. The authoritative total still comes from the
// provider/counter.
const imageWeightPlaceholder = 250

// ComputeComposition returns the per-source breakdown for a set of inputs,
// scaled to the given total. Only the ratios from the cheap estimator are
// exposed — the published total always matches `total`. One row per source,
// emitted in compositionOrder.
func ComputeComposition(inputs []CompositionInput, total int, totalSource string) Composition {
	c := Composition{Total: total, TotalSource: totalSource}

	weights := map[CompositionSource]float64{}
	var totalWeight float64
	for _, in := range inputs {
		w := inputWeight(in)
		weights[in.Source] += w
		totalWeight += w
	}
	if totalWeight == 0 {
		return c
	}

	// A running remainder keeps the per-source buckets summing to exactly total.
	c.Components = make([]CompositionComponent, 0, len(compositionOrder))
	remaining := total
	remainingWeight := totalWeight
	for _, src := range compositionOrder {
		w := weights[src]
		if w == 0 {
			continue
		}
		tokens := 0
		if remainingWeight > 0 {
			tokens = int(float64(remaining)*w/remainingWeight + 0.5)
			if tokens > remaining {
				tokens = remaining
			}
		}
		c.Components = append(c.Components, CompositionComponent{
			Source:     src,
			Proportion: w / totalWeight,
			Tokens:     tokens,
		})
		remaining -= tokens
		remainingWeight -= w
	}
	return c
}

func inputWeight(in CompositionInput) float64 {
	if in.Source == SourceImage {
		return float64(imageWeightPlaceholder)
	}
	if in.Text == "" {
		return 0
	}
	return float64(EstimateTokens(in.Text))
}

// EstimateRequestTokens is the fallback total used when no provider counter is
// available. It mirrors the same weighting as ComputeComposition, so an
// image-heavy request still contributes via imageWeightPlaceholder rather than
// silently rounding to zero (image CompositionInputs carry no Text).
func EstimateRequestTokens(inputs []CompositionInput) int {
	var sum float64
	for _, in := range inputs {
		sum += inputWeight(in)
	}
	return int(sum + 0.5)
}

// Composition derives the per-source attribution for this request from its
// posts and tool context. It is a pure function of the request, so it stays
// consistent through truncation and needs no stored state.
func (r CompletionRequest) Composition() []CompositionInput {
	var out []CompositionInput
	for _, p := range r.Posts {
		source := SourceHistory
		if p.Role == PostRoleSystem {
			source = SourceSystem
		}
		if p.Message != "" {
			out = append(out, CompositionInput{Source: source, Text: p.Message})
		}
		for _, tc := range p.ToolUse {
			if tc.Result != "" {
				out = append(out, CompositionInput{Source: SourceToolResults, Text: tc.Result})
			}
		}
		// Every entry in Post.Files is an image (only image blocks populate it).
		for range p.Files {
			out = append(out, CompositionInput{Source: SourceImage})
		}
	}
	if r.Context != nil && r.Context.Tools != nil {
		for _, t := range r.Context.Tools.GetTools() {
			out = append(out, CompositionInput{Source: SourceToolDefs, Text: toolDefText(t)})
		}
	}
	return out
}

// toolDefText approximates a tool definition's wire size. Only the relative
// length feeds proportion math, not the exact provider format.
func toolDefText(t Tool) string {
	var schema string
	if t.Schema != nil {
		if b, err := json.Marshal(t.Schema); err == nil {
			schema = string(b)
		}
	}
	return t.Name + "\n" + t.Description + "\n" + schema
}

// SpanAttributes returns OTel attribute key/value pairs for the per-source
// token totals, one per category. Zero-token buckets are omitted to keep
// trace cardinality bounded.
func (c Composition) SpanAttributes() []attribute.KeyValue {
	keyBySource := map[CompositionSource]attribute.Key{
		SourceSystem:      telemetry.LLMTokensSystem,
		SourceHistory:     telemetry.LLMTokensHistory,
		SourceToolDefs:    telemetry.LLMTokensToolDefs,
		SourceToolResults: telemetry.LLMTokensToolResults,
		SourceImage:       telemetry.LLMTokensImages,
	}
	attrs := make([]attribute.KeyValue, 0, len(c.Components))
	for _, comp := range c.Components {
		if key, ok := keyBySource[comp.Source]; ok && comp.Tokens > 0 {
			attrs = append(attrs, key.Int(comp.Tokens))
		}
	}
	return attrs
}
