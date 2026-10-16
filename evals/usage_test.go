// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

type cannedStreamLLM struct {
	llm.LanguageModel
	events []llm.TextStreamEvent
}

func (c *cannedStreamLLM) ChatCompletion(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	stream := make(chan llm.TextStreamEvent, len(c.events))
	for _, event := range c.events {
		stream <- event
	}
	close(stream)
	return &llm.TextStreamResult{Stream: stream}, nil
}

func TestMeteredLLMRecordsUsageWhenReaderStopsAtEnd(t *testing.T) {
	meter := newUsageMeter("gpt-6.1-sol")
	model := &meteredLLM{
		LanguageModel: &cannedStreamLLM{events: []llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "hello"},
			{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 1000, OutputTokens: 200, CachedReadTokens: 400}},
			{Type: llm.EventTypeEnd},
		}},
		meter: meter,
	}

	for range 2 {
		text, err := model.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{})
		require.NoError(t, err)
		assert.Equal(t, "hello", text)
	}

	usage := meter.snapshot()
	assert.Equal(t, 2, usage.Calls)
	assert.Equal(t, int64(2000), usage.InputTokens)
	assert.Equal(t, int64(400), usage.OutputTokens)
	assert.Equal(t, int64(800), usage.CachedReadTokens)
	assert.Equal(t, "gpt-6.1-sol", usage.Model)
	assert.InDelta(t, (1200*2.00+800*0.10+400*10.00)/1e6, usage.CostUSD, 1e-12)
}

func TestEstimateCost(t *testing.T) {
	tests := []struct {
		name  string
		model string
		usage RunUsage
		want  float64
	}{
		{
			name:  "uncached input and output",
			model: "claude-sonnet-5-5",
			usage: RunUsage{InputTokens: 1_000_000, OutputTokens: 100_000},
			want:  2.00 + 1.00,
		},
		{
			name:  "cache reads priced at the cached rate",
			model: "gpt-5.6-terra",
			usage: RunUsage{InputTokens: 1_000_000, CachedReadTokens: 500_000},
			want:  0.5*2.00 + 0.5*0.20,
		},
		{
			name:  "provider prefix resolves to the base model",
			model: "global.anthropic.claude-sonnet-5-5",
			usage: RunUsage{OutputTokens: 1_000_000},
			want:  10.00,
		},
		{
			name:  "unknown model has no cost",
			model: "mistral-large-latest",
			usage: RunUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000},
			want:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.InDelta(t, tc.want, estimateCost(tc.model, tc.usage), 1e-9)
		})
	}
}
