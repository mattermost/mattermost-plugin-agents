// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// proportionTolerance is the rounding slack for sum-to-1 assertions.
const proportionTolerance = 1e-9

func TestComputeComposition(t *testing.T) {
	t.Run("empty inputs returns empty composition", func(t *testing.T) {
		c := ComputeComposition(nil, 1000, CompositionTotalCounted)
		assert.Empty(t, c.Components)
		assert.Equal(t, 1000, c.Total)
		assert.Equal(t, CompositionTotalCounted, c.TotalSource)
	})

	t.Run("proportions sum to 1.0", func(t *testing.T) {
		inputs := []CompositionInput{
			{Source: SourceSystem, Text: "system prompt here"},
			{Source: SourceHistory, Text: "user said hello"},
			{Source: SourceHistory, Text: "bot responded with a longer reply that takes more tokens"},
			{Source: SourceToolDefs, Text: `{"name":"foo","schema":{}}`},
			{Source: SourceToolResults, Text: "tool returned this output"},
			{Source: SourceAttachment, ID: "f1", Name: "notes.txt", Text: "the file content"},
			{Source: SourceImage, ID: "i1", Name: "diagram.png"},
		}
		c := ComputeComposition(inputs, 10000, CompositionTotalCounted)

		var sum float64
		for _, comp := range c.Components {
			sum += comp.Proportion
		}
		assert.InDelta(t, 1.0, sum, proportionTolerance, "proportions should sum to ~1.0")
	})

	t.Run("tool_defs and tool_results are reported as separate aggregate buckets", func(t *testing.T) {
		inputs := []CompositionInput{
			{Source: SourceToolDefs, Text: "tool definition one"},
			{Source: SourceToolDefs, Text: "tool definition two"},
			{Source: SourceToolResults, Text: "tool result one"},
			{Source: SourceToolResults, Text: "tool result two"},
		}
		c := ComputeComposition(inputs, 1000, CompositionTotalCounted)

		var sawDefs, sawResults bool
		for _, comp := range c.Components {
			switch comp.Source {
			case SourceToolDefs:
				sawDefs = true
			case SourceToolResults:
				sawResults = true
			}
		}
		assert.True(t, sawDefs, "expected a tool_defs row")
		assert.True(t, sawResults, "expected a tool_results row")
	})

	t.Run("attachments and images get one row per id", func(t *testing.T) {
		inputs := []CompositionInput{
			{Source: SourceAttachment, ID: "f1", Name: "a.txt", Text: "alpha"},
			{Source: SourceAttachment, ID: "f2", Name: "b.txt", Text: "beta"},
			{Source: SourceImage, ID: "i1", Name: "x.png"},
			{Source: SourceImage, ID: "i2", Name: "y.png"},
		}
		c := ComputeComposition(inputs, 1000, CompositionTotalCounted)

		var attachmentRows, imageRows int
		ids := map[string]bool{}
		for _, comp := range c.Components {
			ids[comp.ID] = true
			if comp.Source == SourceAttachment {
				attachmentRows++
			}
			if comp.Source == SourceImage {
				imageRows++
			}
		}
		assert.Equal(t, 2, attachmentRows)
		assert.Equal(t, 2, imageRows)
		assert.True(t, ids["f1"] && ids["f2"] && ids["i1"] && ids["i2"])
	})

	t.Run("system + history aggregate into single rows", func(t *testing.T) {
		inputs := []CompositionInput{
			{Source: SourceSystem, Text: "sys part one"},
			{Source: SourceSystem, Text: "sys part two"}, // assembly only emits one but tolerate >1
			{Source: SourceHistory, Text: "u1"},
			{Source: SourceHistory, Text: "b1"},
			{Source: SourceHistory, Text: "u2"},
		}
		c := ComputeComposition(inputs, 1000, CompositionTotalCounted)
		var sys, hist int
		for _, comp := range c.Components {
			if comp.Source == SourceSystem {
				sys++
			}
			if comp.Source == SourceHistory {
				hist++
			}
		}
		assert.Equal(t, 1, sys)
		assert.Equal(t, 1, hist)
	})

	t.Run("tokens scale to total", func(t *testing.T) {
		inputs := []CompositionInput{
			{Source: SourceSystem, Text: "aaaa"},
			{Source: SourceHistory, Text: "aaaa"},
		}
		// Equal-sized text → ~50/50 split. Token total chosen so rounding is exact.
		c := ComputeComposition(inputs, 100, CompositionTotalCounted)
		var sumTokens int
		for _, comp := range c.Components {
			sumTokens += comp.Tokens
		}
		// Rounding can cost at most 1 token total
		assert.InDelta(t, 100, sumTokens, 1)
	})

	t.Run("zero total still returns proportions", func(t *testing.T) {
		inputs := []CompositionInput{
			{Source: SourceSystem, Text: "foo"},
			{Source: SourceHistory, Text: "bar baz"},
		}
		c := ComputeComposition(inputs, 0, CompositionTotalEstimated)
		assert.Equal(t, 0, c.Total)
		assert.Equal(t, CompositionTotalEstimated, c.TotalSource)
		var sum float64
		for _, comp := range c.Components {
			sum += comp.Proportion
			assert.Equal(t, 0, comp.Tokens, "no total -> no token attribution")
		}
		assert.InDelta(t, 1.0, sum, proportionTolerance)
	})

	t.Run("zero-estimate inputs are dropped to avoid NaN", func(t *testing.T) {
		// Empty Text and no Bytes ⇒ heuristic returns 0. Should not produce NaN
		// proportions even with multiple zero-weight inputs.
		inputs := []CompositionInput{
			{Source: SourceSystem, Text: ""},
			{Source: SourceHistory, Text: "real content here"},
			{Source: SourceImage, ID: "i1"}, // image without bytes still gets fallback weight
		}
		c := ComputeComposition(inputs, 100, CompositionTotalCounted)
		for _, comp := range c.Components {
			assert.False(t, math.IsNaN(comp.Proportion), "proportion should never be NaN")
		}
	})
}

func TestComposition_AggregateBySource(t *testing.T) {
	c := Composition{
		Components: []CompositionComponent{
			{Source: SourceSystem, Tokens: 100},
			{Source: SourceHistory, Tokens: 200},
			{Source: SourceToolDefs, Tokens: 50},
			{Source: SourceToolResults, Tokens: 75},
			{Source: SourceAttachment, ID: "f1", Tokens: 300},
			{Source: SourceAttachment, ID: "f2", Tokens: 150},
			{Source: SourceImage, ID: "i1", Tokens: 250},
		},
	}
	got := c.AggregateBySource()

	assert.Equal(t, 100, got[SourceSystem])
	assert.Equal(t, 200, got[SourceHistory])
	assert.Equal(t, 50, got[SourceToolDefs])
	assert.Equal(t, 75, got[SourceToolResults])
	assert.Equal(t, 450, got[SourceAttachment], "per-attachment rows must roll up to one bucket")
	assert.Equal(t, 250, got[SourceImage])
}

func TestComposition_SpanAttributes_OmitsZeroBuckets(t *testing.T) {
	c := Composition{
		Components: []CompositionComponent{
			{Source: SourceSystem, Tokens: 100},
			{Source: SourceImage, ID: "i1", Tokens: 250},
		},
	}
	attrs := c.SpanAttributes()
	keys := map[string]bool{}
	for _, a := range attrs {
		keys[string(a.Key)] = true
	}
	assert.True(t, keys["agents.llm.tokens.system"])
	assert.True(t, keys["agents.llm.tokens.images"])
	assert.False(t, keys["agents.llm.tokens.history"], "zero-token buckets must be omitted to keep cardinality bounded")
	assert.False(t, keys["agents.llm.tokens.tool_defs"])
}

func TestCompletionRequestCarriesComposition(t *testing.T) {
	// Pin down that CompletionRequest exposes a Composition slice so the
	// assembly path can populate it and downstream wrappers can read it.
	req := CompletionRequest{
		Composition: []CompositionInput{
			{Source: SourceSystem, Text: "hi"},
		},
	}
	assert.Len(t, req.Composition, 1)
	assert.Equal(t, SourceSystem, req.Composition[0].Source)
}
