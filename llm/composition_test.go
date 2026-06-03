// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"math"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			{Source: SourceToolDefs, Text: `{"name":"foo","schema":{}}`},
			{Source: SourceToolResults, Text: "tool returned this output"},
			{Source: SourceImage},
		}
		c := ComputeComposition(inputs, 10000, CompositionTotalCounted)

		var sum float64
		for _, comp := range c.Components {
			sum += comp.Proportion
		}
		assert.InDelta(t, 1.0, sum, proportionTolerance, "proportions should sum to ~1.0")
	})

	t.Run("each source collapses to a single row", func(t *testing.T) {
		inputs := []CompositionInput{
			{Source: SourceSystem, Text: "you are a helpful assistant"},
			{Source: SourceSystem, Text: "always be concise and clear"},
			{Source: SourceHistory, Text: "what is the capital of france"},
			{Source: SourceHistory, Text: "the capital of france is paris"},
			{Source: SourceToolResults, Text: "the weather today is sunny"},
			{Source: SourceToolResults, Text: "the temperature is seventy two"},
		}
		c := ComputeComposition(inputs, 1000, CompositionTotalCounted)

		counts := map[CompositionSource]int{}
		for _, comp := range c.Components {
			counts[comp.Source]++
		}
		assert.Equal(t, 1, counts[SourceSystem])
		assert.Equal(t, 1, counts[SourceHistory])
		assert.Equal(t, 1, counts[SourceToolResults])
	})

	t.Run("rows are emitted in compositionOrder", func(t *testing.T) {
		// Scrambled input order must not change output order.
		inputs := []CompositionInput{
			{Source: SourceImage},
			{Source: SourceToolResults, Text: "tool result text here"},
			{Source: SourceHistory, Text: "some conversation history here"},
			{Source: SourceToolDefs, Text: "tool definition schema here"},
			{Source: SourceSystem, Text: "system prompt text here"},
		}
		c := ComputeComposition(inputs, 1000, CompositionTotalCounted)

		got := make([]CompositionSource, 0, len(c.Components))
		for _, comp := range c.Components {
			got = append(got, comp.Source)
		}
		assert.Equal(t, []CompositionSource{
			SourceSystem, SourceHistory, SourceToolDefs, SourceToolResults, SourceImage,
		}, got)
	})

	t.Run("tokens scale to total", func(t *testing.T) {
		inputs := []CompositionInput{
			{Source: SourceSystem, Text: "aaaa"},
			{Source: SourceHistory, Text: "aaaa"},
		}
		c := ComputeComposition(inputs, 100, CompositionTotalCounted)
		var sumTokens int
		for _, comp := range c.Components {
			sumTokens += comp.Tokens
		}
		assert.Equal(t, 100, sumTokens)
	})

	t.Run("buckets sum to total exactly despite rounding", func(t *testing.T) {
		// Three equal sources with total=2 would overshoot to 3 under naive rounding.
		inputs := []CompositionInput{
			{Source: SourceSystem, Text: "aaaa"},
			{Source: SourceHistory, Text: "aaaa"},
			{Source: SourceToolResults, Text: "aaaa"},
		}
		c := ComputeComposition(inputs, 2, CompositionTotalCounted)
		var sumTokens int
		for _, comp := range c.Components {
			sumTokens += comp.Tokens
		}
		assert.Equal(t, 2, sumTokens)
	})

	t.Run("zero total still returns proportions", func(t *testing.T) {
		inputs := []CompositionInput{
			{Source: SourceSystem, Text: "foo"},
			{Source: SourceHistory, Text: "bar baz"},
		}
		c := ComputeComposition(inputs, 0, CompositionTotalEstimated)
		assert.Equal(t, 0, c.Total)
		var sum float64
		for _, comp := range c.Components {
			sum += comp.Proportion
			assert.Equal(t, 0, comp.Tokens, "no total -> no token attribution")
		}
		assert.InDelta(t, 1.0, sum, proportionTolerance)
	})

	t.Run("zero-weight inputs don't produce NaN", func(t *testing.T) {
		inputs := []CompositionInput{
			{Source: SourceSystem, Text: ""},
			{Source: SourceHistory, Text: "real content here"},
			{Source: SourceImage},
		}
		c := ComputeComposition(inputs, 100, CompositionTotalCounted)
		for _, comp := range c.Components {
			assert.False(t, math.IsNaN(comp.Proportion), "proportion should never be NaN")
		}
	})

	t.Run("image-only request yields a single image row", func(t *testing.T) {
		c := ComputeComposition([]CompositionInput{{Source: SourceImage}}, 500, CompositionTotalCounted)
		require.Len(t, c.Components, 1)
		assert.Equal(t, SourceImage, c.Components[0].Source)
		assert.InDelta(t, 1.0, c.Components[0].Proportion, proportionTolerance)
		assert.Equal(t, 500, c.Components[0].Tokens)
	})
}

func TestEstimateRequestTokens(t *testing.T) {
	tests := []struct {
		name   string
		inputs []CompositionInput
		want   int
	}{
		{
			name:   "image-only returns the image placeholder weight",
			inputs: []CompositionInput{{Source: SourceImage}},
			want:   imageWeightPlaceholder,
		},
		{
			name: "text plus images sums weights via inputWeight",
			inputs: []CompositionInput{
				{Source: SourceSystem, Text: "system prompt"},
				{Source: SourceImage},
				{Source: SourceImage},
			},
			want: EstimateTokens("system prompt") + 2*imageWeightPlaceholder,
		},
		{
			name:   "empty inputs returns zero",
			inputs: nil,
			want:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, EstimateRequestTokens(tt.inputs))
		})
	}
}

// TestCompletionRequestComposition pins that the breakdown is derived from the
// request itself — posts (role, message, tool results, image files) and the
// tool context — so it stays consistent through truncation with no stored state.
func TestCompletionRequestComposition(t *testing.T) {
	tools := NewToolStore()
	tools.AddTools([]Tool{
		{Name: "get_weather", Description: "Returns weather for a city", Schema: &jsonschema.Schema{}},
	})

	req := CompletionRequest{
		Posts: []Post{
			{Role: PostRoleSystem, Message: "you are helpful"},
			{Role: PostRoleUser, Message: "have a look"},
			{Role: PostRoleBot, Message: "let me check", ToolUse: []ToolCall{{ID: "t1", Result: "72F, sunny"}}},
			{Role: PostRoleUser, Files: []File{{MimeType: "image/png"}}},
		},
		Context: &Context{Tools: tools},
	}

	bySource := map[CompositionSource][]CompositionInput{}
	for _, in := range req.Composition() {
		bySource[in.Source] = append(bySource[in.Source], in)
	}

	require.Len(t, bySource[SourceSystem], 1)
	assert.Equal(t, "you are helpful", bySource[SourceSystem][0].Text)

	require.GreaterOrEqual(t, len(bySource[SourceHistory]), 2, "user + assistant text both count as history")

	require.Len(t, bySource[SourceToolResults], 1)
	assert.Equal(t, "72F, sunny", bySource[SourceToolResults][0].Text)

	require.Len(t, bySource[SourceImage], 1)

	require.Len(t, bySource[SourceToolDefs], 1)
	assert.Contains(t, bySource[SourceToolDefs][0].Text, "get_weather")
}

func TestCompletionRequestComposition_NoToolsNoPanic(t *testing.T) {
	req := CompletionRequest{
		Posts:   []Post{{Role: PostRoleUser, Message: "hi"}},
		Context: &Context{},
	}
	for _, in := range req.Composition() {
		assert.NotEqual(t, SourceToolDefs, in.Source, "no Context.Tools ⇒ no tool_defs entries")
	}
}

func TestComposition_SpanAttributes_OmitsZeroBuckets(t *testing.T) {
	c := Composition{
		Components: []CompositionComponent{
			{Source: SourceSystem, Tokens: 100},
			{Source: SourceImage, Tokens: 250},
		},
	}
	attrs := c.SpanAttributes()
	keys := map[string]bool{}
	for _, a := range attrs {
		keys[string(a.Key)] = true
	}
	assert.True(t, keys["agents.llm.tokens.system"])
	assert.True(t, keys["agents.llm.tokens.images"])
	assert.False(t, keys["agents.llm.tokens.history"], "absent sources must not emit attributes")
	assert.False(t, keys["agents.llm.tokens.tool_defs"])
}
