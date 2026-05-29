// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/telemetry"
)

// TestSetCompositionSpanAttributes_EmitsAggregateBuckets pins the bifrost-side
// wiring: when a request's derived composition is non-empty and we have a real
// input-token total from the provider, the LLM-call span gets per-source token
// attributes (one per category) that downstream Grafana dashboards can histogram.
func TestSetCompositionSpanAttributes_EmitsAggregateBuckets(t *testing.T) {
	exporter := setupTracerProvider(t)

	ctx, span := telemetry.Tracer().Start(context.Background(), "llm chat completion")

	tools := llm.NewToolStore()
	tools.AddTools([]llm.Tool{{Name: "foo", Description: "does foo", Schema: &jsonschema.Schema{}}})
	req := llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleSystem, Message: "you are a helpful assistant"},
			{Role: llm.PostRoleUser, Message: "user said hello"},
			{Role: llm.PostRoleBot, Message: "let me check", ToolUse: []llm.ToolCall{{ID: "t1", Result: "tool returned this"}}},
			{Role: llm.PostRoleUser, Files: []llm.File{{MimeType: "image/png"}}},
		},
		Context: &llm.Context{Tools: tools},
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

	// All five buckets must be present because every category had at least
	// one input.
	for _, key := range []string{
		"agents.llm.tokens.system",
		"agents.llm.tokens.history",
		"agents.llm.tokens.tool_defs",
		"agents.llm.tokens.tool_results",
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
	assert.InDelta(t, 10000, sum, 5,
		"per-source buckets must add up to the provider input total; "+
			"users will be confused if 'images=3000' but 'input=10000' doesn't roll up")
}

// TestSetCompositionSpanAttributes_NoCompositionNoAttrs guards the no-op path:
// when a request has no derivable composition (legacy callers, internal
// title-generation calls with no posts), the helper must not emit any
// token-source attributes — emitting zeros would still create the keys.
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
// no-op case: composition is derivable but the provider reported zero input
// tokens. Without a total to scale by, every bucket would be 0 anyway.
func TestSetCompositionSpanAttributes_ZeroInputTokensNoAttrs(t *testing.T) {
	exporter := setupTracerProvider(t)
	_, span := telemetry.Tracer().Start(context.Background(), "llm chat completion")

	req := llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleSystem, Message: "sys"}},
	}
	setCompositionSpanAttributes(span, req, llm.TokenUsage{InputTokens: 0})
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	for _, a := range spans[0].Attributes {
		assert.NotContains(t, string(a.Key), "agents.llm.tokens.")
	}
}
