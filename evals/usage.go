// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// RunUsage is the token usage, cost, and latency of the LLM calls one role
// (the model under test or the grader) made during an eval run.
type RunUsage struct {
	Model            string `json:"model"`
	Calls            int    `json:"calls"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CachedReadTokens int64  `json:"cached_read_tokens,omitempty"`
	ReasoningTokens  int64  `json:"reasoning_tokens,omitempty"`
	// CostUSD is the provider-reported cost when available, otherwise an
	// estimate from modelPrices. Zero means the price is unknown.
	CostUSD float64 `json:"cost_usd,omitempty"`
	// LatencyMS is the summed wall time of the calls, from request to the
	// end of the stream.
	LatencyMS int64 `json:"latency_ms"`
	// FirstTokenMS is the time to the first text of the run's first call.
	FirstTokenMS int64 `json:"first_token_ms,omitempty"`
}

// UsageRecord is written once per eval run, alongside its graded results.
type UsageRecord struct {
	Agent  RunUsage `json:"agent"`
	Grader RunUsage `json:"grader"`
}

// usageMeter accumulates usage for one role across a run. It is shared by
// concurrent streams, so every access is locked.
type usageMeter struct {
	mu           sync.Mutex
	model        string
	usage        RunUsage
	reportedCost float64
}

func newUsageMeter(model string) *usageMeter {
	return &usageMeter{model: model}
}

func (m *usageMeter) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage = RunUsage{}
	m.reportedCost = 0
}

func (m *usageMeter) record(latency, firstToken time.Duration, usage llm.TokenUsage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.usage.Calls == 0 && firstToken > 0 {
		m.usage.FirstTokenMS = firstToken.Milliseconds()
	}
	m.usage.Calls++
	m.usage.LatencyMS += latency.Milliseconds()
	m.usage.InputTokens += usage.InputTokens
	m.usage.OutputTokens += usage.OutputTokens
	m.usage.CachedReadTokens += usage.CachedReadTokens
	m.usage.ReasoningTokens += usage.ReasoningTokens
	m.reportedCost += usage.Cost
}

func (m *usageMeter) snapshot() RunUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	usage := m.usage
	usage.Model = m.model
	usage.CostUSD = m.reportedCost
	if usage.CostUSD == 0 {
		usage.CostUSD = estimateCost(m.model, usage)
	}
	return usage
}

// meteredLLM records the usage and latency of every call into a meter.
type meteredLLM struct {
	llm.LanguageModel
	meter *usageMeter
}

func (m *meteredLLM) ChatCompletion(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	start := time.Now()
	result, err := m.LanguageModel.ChatCompletion(ctx, request, opts...)
	if err != nil {
		m.meter.record(time.Since(start), 0, llm.TokenUsage{})
		return nil, err
	}

	out := make(chan llm.TextStreamEvent)
	go func() {
		defer close(out)
		var firstToken time.Duration
		var usage llm.TokenUsage
		recorded := false
		finish := func() {
			if !recorded {
				recorded = true
				m.meter.record(time.Since(start), firstToken, usage)
			}
		}
		defer finish()

		for event := range result.Stream {
			switch event.Type {
			case llm.EventTypeText:
				if firstToken == 0 {
					firstToken = time.Since(start)
				}
			case llm.EventTypeUsage:
				if u, ok := event.Value.(llm.TokenUsage); ok {
					usage.InputTokens += u.InputTokens
					usage.OutputTokens += u.OutputTokens
					usage.CachedReadTokens += u.CachedReadTokens
					usage.CachedWriteTokens += u.CachedWriteTokens
					usage.ReasoningTokens += u.ReasoningTokens
					usage.Cost += u.Cost
				}
			case llm.EventTypeEnd:
				// Readers such as ReadAll stop at the end event, so record before
				// forwarding it rather than when the stream closes.
				finish()
			}
			out <- event
		}
	}()
	return &llm.TextStreamResult{Stream: out}, nil
}

// ChatCompletionNoStream reads the metered stream so non-streaming calls are
// counted too; the wrapped providers implement it the same way.
func (m *meteredLLM) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	result, err := m.ChatCompletion(ctx, request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

// modelPrice is a list price in USD per million tokens.
type modelPrice struct {
	input       float64
	cachedInput float64
	output      float64
}

// modelPrices holds list prices for the default eval models, matched by
// substring so provider prefixes (Bedrock inference profiles, dated snapshots)
// resolve to the same entry. Add an entry when bumping a default model; a
// model without one reports no cost. Checked 2026-10-14.
var modelPrices = []struct {
	model string
	price modelPrice
}{
	{"gpt-6.1-sol", modelPrice{input: 2.00, cachedInput: 0.10, output: 10.00}},
	{"gpt-5.6-sol", modelPrice{input: 4.00, cachedInput: 0.40, output: 20.00}},
	{"gpt-5.6-terra", modelPrice{input: 2.00, cachedInput: 0.20, output: 12.00}},
	{"claude-sonnet-5-5", modelPrice{input: 2.00, cachedInput: 0.10, output: 10.00}},
	{"command-a-plus", modelPrice{input: 0.30, cachedInput: 0.15, output: 1.50}},
}

// estimateCost prices input at the cached rate for cache reads; input token
// counts include cached tokens for every provider bifrost normalizes.
func estimateCost(model string, usage RunUsage) float64 {
	for _, entry := range modelPrices {
		if !strings.Contains(model, entry.model) {
			continue
		}
		uncached := usage.InputTokens - usage.CachedReadTokens
		return (float64(uncached)*entry.price.input +
			float64(usage.CachedReadTokens)*entry.price.cachedInput +
			float64(usage.OutputTokens)*entry.price.output) / 1e6
	}
	return 0
}
