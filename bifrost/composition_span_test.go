// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/telemetry"
)

// TestSetCompositionSpanAttributes_EmitsAggregateBuckets pins the bifrost-side
// wiring: when a request carries a Composition and we have a real input-token
// total from the provider, the LLM-call span gets per-source token attributes
// (one per category) that downstream Grafana dashboards can histogram.
func TestSetCompositionSpanAttributes_EmitsAggregateBuckets(t *testing.T) {
	exporter := setupTracerProvider(t)

	ctx, span := telemetry.Tracer().Start(context.Background(), "llm chat completion")

	req := llm.CompletionRequest{
		Composition: []llm.CompositionInput{
			{Source: llm.SourceSystem, Text: "you are a helpful assistant"},
			{Source: llm.SourceHistory, Text: "user said hello"},
			{Source: llm.SourceToolDefs, Text: `{"name":"foo"}`},
			{Source: llm.SourceToolResults, Text: "tool returned this"},
			{Source: llm.SourceAttachment, ID: "f1", Name: "a.txt", Text: "lorem ipsum"},
			{Source: llm.SourceImage, ID: "i1", Name: "x.png"},
		},
	}
	usage := llm.TokenUsage{InputTokens: 10000, OutputTokens: 250}
	setCompositionSpanAttributes(span, req, usage)
	span.End()
	_ = ctx

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	keys := map[string]int64{}
	for _, a := range spans[0].Attributes {
		keys[string(a.Key)] = a.Value.AsInt64()
	}

	// All six buckets must be present because every category had at least
	// one input.
	for _, key := range []string{
		"agents.llm.tokens.system",
		"agents.llm.tokens.history",
		"agents.llm.tokens.tool_defs",
		"agents.llm.tokens.tool_results",
		"agents.llm.tokens.attachments",
		"agents.llm.tokens.images",
	} {
		assert.NotZerof(t, keys[key], "expected non-zero %s", key)
	}

	// Sum of per-bucket tokens should equal the provider's input total
	// (allowing for one token of rounding slack per bucket).
	var sum int64
	for _, v := range keys {
		sum += v
	}
	assert.InDelta(t, 10000, sum, 6,
		"per-source buckets must add up to the provider input total; "+
			"users will be confused if 'attachments=3000' but 'input=10000' doesn't roll up")
}

// TestSetCompositionSpanAttributes_NoCompositionNoAttrs guards the no-op path:
// when a request doesn't carry composition data (legacy callers, internal
// title-generation calls), the helper must not emit any token-source
// attributes — emitting zeros would still create the keys, polluting traces.
func TestSetCompositionSpanAttributes_NoCompositionNoAttrs(t *testing.T) {
	exporter := setupTracerProvider(t)
	_, span := telemetry.Tracer().Start(context.Background(), "llm chat completion")

	setCompositionSpanAttributes(span, llm.CompletionRequest{}, llm.TokenUsage{InputTokens: 100})
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	for _, a := range spans[0].Attributes {
		assert.NotContains(t, string(a.Key), "agents.llm.tokens.",
			"composition span attrs must be absent when the request has no composition")
	}
}

// TestSetCompositionSpanAttributes_ZeroInputTokensNoAttrs covers the other
// no-op case: composition data is present but the provider reported zero input
// tokens. Without a total to scale by, every bucket would be 0 anyway.
func TestSetCompositionSpanAttributes_ZeroInputTokensNoAttrs(t *testing.T) {
	exporter := setupTracerProvider(t)
	_, span := telemetry.Tracer().Start(context.Background(), "llm chat completion")

	req := llm.CompletionRequest{
		Composition: []llm.CompositionInput{
			{Source: llm.SourceSystem, Text: "sys"},
		},
	}
	setCompositionSpanAttributes(span, req, llm.TokenUsage{InputTokens: 0})
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	for _, a := range spans[0].Attributes {
		assert.NotContains(t, string(a.Key), "agents.llm.tokens.")
	}
}
