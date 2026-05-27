// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"go.opentelemetry.io/otel/attribute"

	"github.com/mattermost/mattermost-plugin-agents/telemetry"
)

// CompositionSource labels where a piece of an LLM request came from. Used to
// attribute token cost back to its origin (system prompt, history, attachments,
// images, tool definitions, tool results) without changing how a request is
// assembled or billed.
type CompositionSource string

const (
	SourceSystem      CompositionSource = "system"
	SourceHistory     CompositionSource = "history"
	SourceToolDefs    CompositionSource = "tool_defs"
	SourceToolResults CompositionSource = "tool_results"
	SourceAttachment  CompositionSource = "attachment"
	SourceImage       CompositionSource = "image"
)

// CompositionInput is a single piece of content contributed to a request,
// captured during assembly. The token cost is derived later from these inputs
// using EstimateTokens for proportions multiplied by the request's authoritative
// total. ID and Name are populated for per-file sources (attachment, image).
type CompositionInput struct {
	Source CompositionSource
	ID     string
	Name   string
	Text   string
}

// CompositionComponent is a single row in a composition breakdown.
//
// system, history, tool_defs, and tool_results are aggregated into one row
// per source. attachment and image are reported one row per file. Proportion
// is normalized so Components sum to 1.0 (modulo rounding); Tokens is
// round(Proportion * Total).
type CompositionComponent struct {
	Source     CompositionSource `json:"source"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Proportion float64           `json:"proportion"`
	Tokens     int               `json:"tokens"`
}

// CompositionTotalSource enumerates the provenance of Composition.Total.
const (
	// CompositionTotalCounted means the total came from a provider
	// CountTokens / CountRequestTokens call (most accurate, pre-call).
	CompositionTotalCounted = "counted"
	// CompositionTotalProvider means the total came from the provider's
	// post-call usage report (most accurate, post-call).
	CompositionTotalProvider = "provider"
	// CompositionTotalEstimated means we fell back to EstimateTokens because
	// neither a counter nor a provider report was available.
	CompositionTotalEstimated = "estimated"
)

// Composition is the per-request, per-source token breakdown that powers the
// /context endpoint and the components field on TokenUsageLog.
type Composition struct {
	Components      []CompositionComponent `json:"components"`
	Total           int                    `json:"total"`
	TotalSource     string                 `json:"total_source"`
	InputTokenLimit int                    `json:"input_token_limit,omitempty"`
	Model           string                 `json:"model,omitempty"`
}

// imageWeightPlaceholder is the heuristic weight for an image with no
// available text proxy. Vision providers price images very differently from
// text; this is a deliberately coarse stand-in so an image counts for
// *something* in the proportion math without claiming exact tokens. The
// authoritative total still comes from the provider/counter.
const imageWeightPlaceholder = 250

// ComputeComposition returns the per-source breakdown for a set of inputs,
// scaled to the given total. The heuristic estimator's absolute numbers are
// not exposed — only the ratios — so the published total always matches
// `total`. Aggregates system/history/tool_defs/tool_results into single rows;
// keeps attachment/image as one row per ID.
func ComputeComposition(inputs []CompositionInput, total int, totalSource string) Composition {
	c := Composition{Total: total, TotalSource: totalSource}
	if len(inputs) == 0 {
		return c
	}

	// Per-input raw weights from the cheap heuristic.
	weights := make([]float64, len(inputs))
	var totalWeight float64
	for i, in := range inputs {
		w := inputWeight(in)
		weights[i] = w
		totalWeight += w
	}
	if totalWeight == 0 {
		return c
	}

	// Aggregate by (Source, ID). Aggregate sources have an empty ID so they
	// collapse to one row; per-file sources keep their ID and stay distinct.
	type key struct {
		Source CompositionSource
		ID     string
	}
	type acc struct {
		Source CompositionSource
		ID     string
		Name   string
		Weight float64
		Order  int
	}
	rows := map[key]*acc{}
	order := 0
	for i, in := range inputs {
		k := key{Source: in.Source}
		if in.Source == SourceAttachment || in.Source == SourceImage {
			k.ID = in.ID
		}
		row, ok := rows[k]
		if !ok {
			row = &acc{Source: in.Source, ID: k.ID, Name: in.Name, Order: order}
			rows[k] = row
			order++
		}
		row.Weight += weights[i]
	}

	// Stable output: iterate by insertion order.
	ordered := make([]*acc, 0, len(rows))
	for _, r := range rows {
		ordered = append(ordered, r)
	}
	// Insertion sort by Order keeps this allocation small and stable.
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j-1].Order > ordered[j].Order; j-- {
			ordered[j-1], ordered[j] = ordered[j], ordered[j-1]
		}
	}

	c.Components = make([]CompositionComponent, 0, len(ordered))
	for _, r := range ordered {
		prop := r.Weight / totalWeight
		c.Components = append(c.Components, CompositionComponent{
			Source:     r.Source,
			ID:         r.ID,
			Name:       r.Name,
			Proportion: prop,
			Tokens:     int(prop*float64(total) + 0.5),
		})
	}
	return c
}

func inputWeight(in CompositionInput) float64 {
	if in.Source == SourceImage {
		// Images contribute a flat placeholder; provider-specific pricing
		// varies too much for a heuristic to be meaningful.
		return float64(imageWeightPlaceholder)
	}
	if in.Text == "" {
		return 0
	}
	return float64(EstimateTokens(in.Text))
}

// AggregateBySource returns the total tokens per source, rolling up
// per-file attachment / image rows into single buckets. Used to populate
// span attributes (one value per category) and to summarize the per-call
// token log.
func (c Composition) AggregateBySource() map[CompositionSource]int {
	out := map[CompositionSource]int{}
	for _, comp := range c.Components {
		out[comp.Source] += comp.Tokens
	}
	return out
}

// SpanAttributes returns OTel attribute key/value pairs for the per-source
// token totals, one per category. Zero-token buckets are omitted to keep
// trace cardinality bounded.
func (c Composition) SpanAttributes() []attribute.KeyValue {
	if len(c.Components) == 0 {
		return nil
	}
	sums := c.AggregateBySource()
	mapping := []struct {
		Source CompositionSource
		Key    attribute.Key
	}{
		{SourceSystem, telemetry.LLMTokensSystem},
		{SourceHistory, telemetry.LLMTokensHistory},
		{SourceToolDefs, telemetry.LLMTokensToolDefs},
		{SourceToolResults, telemetry.LLMTokensToolResults},
		{SourceAttachment, telemetry.LLMTokensAttachments},
		{SourceImage, telemetry.LLMTokensImages},
	}
	attrs := make([]attribute.KeyValue, 0, len(mapping))
	for _, m := range mapping {
		if tokens := sums[m.Source]; tokens > 0 {
			attrs = append(attrs, m.Key.Int(tokens))
		}
	}
	return attrs
}
