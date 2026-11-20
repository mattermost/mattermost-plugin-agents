// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// tierLLM answers with fixed usage and records the service tier of each
// request. While flexFull is set it rejects flex requests the way Bifrost
// does: with an error event as the stream's first event, or with an error
// when flexErrReturned is set.
type tierLLM struct {
	llm.LanguageModel
	usage           llm.TokenUsage
	flexFull        bool
	flexErrReturned bool
	tiers           []string
}

func (l *tierLLM) ChatCompletion(_ context.Context, _ llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	var cfg llm.LanguageModelConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	l.tiers = append(l.tiers, cfg.ServiceTier)
	if cfg.ServiceTier == "flex" && l.flexFull {
		err := errors.New("bifrost error: Resource Unavailable (status=429 type=resource_unavailable)")
		if l.flexErrReturned {
			return nil, err
		}
		stream := make(chan llm.TextStreamEvent, 1)
		stream <- llm.TextStreamEvent{Type: llm.EventTypeError, Value: err}
		close(stream)
		return &llm.TextStreamResult{Stream: stream}, nil
	}
	stream := make(chan llm.TextStreamEvent, 3)
	stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "ok"}
	stream <- llm.TextStreamEvent{Type: llm.EventTypeUsage, Value: l.usage}
	stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	close(stream)
	return &llm.TextStreamResult{Stream: stream}, nil
}

func TestMeteredLLMRecordsUsageAndServiceTier(t *testing.T) {
	// 1M uncached input, 1M cached, 1M output on gpt-6.1-terra:
	// $1.00 + $0.05 + $5.00 at the standard tier.
	usage := llm.TokenUsage{InputTokens: 2_000_000, CachedReadTokens: 1_000_000, OutputTokens: 1_000_000}
	tests := []struct {
		name            string
		tier            string
		flexFull        bool
		flexErrReturned bool
		wantTiers       []string
		wantCost        float64
	}{
		{name: "standard tier", wantTiers: []string{""}, wantCost: 6.05},
		{name: "flex is half price", tier: "flex", wantTiers: []string{"flex"}, wantCost: 3.025},
		{name: "flex without capacity falls back to the standard tier", tier: "flex", flexFull: true, wantTiers: []string{"flex", ""}, wantCost: 6.05},
		{name: "a returned flex capacity error falls back to the standard tier", tier: "flex", flexFull: true, flexErrReturned: true, wantTiers: []string{"flex", ""}, wantCost: 6.05},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner := &tierLLM{usage: usage, flexFull: tc.flexFull, flexErrReturned: tc.flexErrReturned}
			meter := &usageMeter{}
			agent := &meteredLLM{LanguageModel: inner, meter: meter, model: "gpt-6.1-terra", serviceTier: tc.tier}
			grader := &meteredLLM{LanguageModel: &tierLLM{usage: llm.TokenUsage{InputTokens: 1_000_000}}, meter: meter, grader: true, model: "gpt-6-luna"}

			out, err := agent.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{})
			require.NoError(t, err)
			assert.Equal(t, "ok", out)
			_, err = grader.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{})
			require.NoError(t, err)

			got := meter.snapshot()
			assert.Equal(t, tc.wantTiers, inner.tiers)
			assert.Equal(t, 1, got.Agent.Calls)
			assert.Equal(t, usage.InputTokens, got.Agent.InputTokens)
			assert.Equal(t, usage.CachedReadTokens, got.Agent.CachedReadTokens)
			assert.InDelta(t, tc.wantCost, got.Agent.CostUSD, 1e-9)
			assert.InDelta(t, 0.10, got.Grader.CostUSD, 1e-9, "grader usage is recorded separately")

			meter.reset()
			assert.Equal(t, TrialUsage{}, meter.snapshot())
		})
	}
}

func TestUsageCostOfUnknownModel(t *testing.T) {
	_, ok := usageCost("some-local-model", llm.TokenUsage{InputTokens: 10}, false)
	assert.False(t, ok)
}
