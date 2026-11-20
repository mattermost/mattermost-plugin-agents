// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"context"
	"strings"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// RoleUsage is the token usage of one role (model under test or grader) in a
// trial, with its list-price cost when the model's price is known.
type RoleUsage struct {
	Model             string  `json:"model,omitempty"`
	Calls             int     `json:"calls"`
	InputTokens       int64   `json:"input_tokens"`
	CachedReadTokens  int64   `json:"cached_read_tokens,omitempty"`
	CachedWriteTokens int64   `json:"cached_write_tokens,omitempty"`
	OutputTokens      int64   `json:"output_tokens"`
	ReasoningTokens   int64   `json:"reasoning_tokens,omitempty"`
	CostUSD           float64 `json:"cost_usd,omitempty"`
}

// TrialUsage is the token usage of one trial of an eval.
type TrialUsage struct {
	Agent  RoleUsage `json:"agent"`
	Grader RoleUsage `json:"grader"`
}

// modelPrice is a model's list price in USD per million tokens.
type modelPrice struct {
	input, cachedInput, cacheWrite, output float64
}

// modelPrices covers the default eval models. Others are reported without a
// cost.
var modelPrices = map[string]modelPrice{
	"gpt-6.1-terra":                      {input: 1.00, cachedInput: 0.05, cacheWrite: 1.25, output: 5.00},
	"gpt-6-luna":                         {input: 0.10, cachedInput: 0.01, cacheWrite: 0.125, output: 0.50},
	"claude-sonnet-5-5":                  {input: 2.00, cachedInput: 0.10, cacheWrite: 2.50, output: 10.00},
	"global.anthropic.claude-sonnet-5-5": {input: 2.00, cachedInput: 0.10, cacheWrite: 2.50, output: 10.00},
}

// flexDiscount is the price multiplier of OpenAI's flex processing tier.
const flexDiscount = 0.5

// usageCost returns the list-price cost of usage on model, and false when the
// model's price is unknown. Input tokens include cache reads and writes.
func usageCost(model string, usage llm.TokenUsage, flex bool) (float64, bool) {
	price, ok := modelPrices[model]
	if !ok {
		return 0, false
	}
	uncached := max(usage.InputTokens-usage.CachedReadTokens-usage.CachedWriteTokens, 0)
	cost := (float64(uncached)*price.input +
		float64(usage.CachedReadTokens)*price.cachedInput +
		float64(usage.CachedWriteTokens)*price.cacheWrite +
		float64(usage.OutputTokens)*price.output) / 1e6
	if flex {
		cost *= flexDiscount
	}
	return cost, true
}

// usageMeter accumulates the token usage of the current trial.
type usageMeter struct {
	mu    sync.Mutex
	trial TrialUsage
}

func (m *usageMeter) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trial = TrialUsage{}
}

func (m *usageMeter) snapshot() TrialUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.trial
}

func (m *usageMeter) add(grader bool, model string, usage llm.TokenUsage, flex bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	role := &m.trial.Agent
	if grader {
		role = &m.trial.Grader
	}
	role.Model = model
	role.Calls++
	role.InputTokens += usage.InputTokens
	role.CachedReadTokens += usage.CachedReadTokens
	role.CachedWriteTokens += usage.CachedWriteTokens
	role.OutputTokens += usage.OutputTokens
	role.ReasoningTokens += usage.ReasoningTokens
	if cost, ok := usageCost(model, usage, flex); ok {
		role.CostUSD += cost
	}
}

// meteredLLM records the token usage of every request in its meter and sends
// requests at the configured OpenAI service tier.
type meteredLLM struct {
	llm.LanguageModel
	meter       *usageMeter
	grader      bool
	model       string
	serviceTier string
}

func (m *meteredLLM) ChatCompletion(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	flex := m.serviceTier == "flex"
	stream, err := startStream(ctx, m.LanguageModel, request, append(opts, llm.WithServiceTier(m.serviceTier))...)
	if flex && flexUnavailable(err, stream) {
		// Flex capacity is not guaranteed; the standard tier always serves.
		flex = false
		stream, err = startStream(ctx, m.LanguageModel, request, opts...)
	}
	if err != nil {
		return nil, err
	}

	out := make(chan llm.TextStreamEvent)
	go func() {
		defer close(out)
		for {
			event, ok := stream.next(ctx)
			if !ok {
				return
			}
			if event.Type == llm.EventTypeUsage {
				if usage, ok := event.Value.(llm.TokenUsage); ok {
					m.meter.add(m.grader, m.model, usage, flex)
				}
			}
			select {
			case <-ctx.Done():
				return
			case out <- event:
			}
		}
	}()
	return &llm.TextStreamResult{Stream: out}, nil
}

// peekedStream is a response stream whose first event was read ahead, since a
// request the provider rejects arrives as an error event, not as an error.
type peekedStream struct {
	first    *llm.TextStreamEvent
	upstream <-chan llm.TextStreamEvent
}

func startStream(ctx context.Context, model llm.LanguageModel, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*peekedStream, error) {
	result, err := model.ChatCompletion(ctx, request, opts...)
	if err != nil {
		return nil, err
	}
	stream := &peekedStream{upstream: result.Stream}
	if first, ok := stream.next(ctx); ok {
		stream.first = &first
	}
	return stream, nil
}

func (s *peekedStream) next(ctx context.Context) (llm.TextStreamEvent, bool) {
	if s.first != nil {
		first := *s.first
		s.first = nil
		return first, true
	}
	select {
	case <-ctx.Done():
		return llm.TextStreamEvent{}, false
	case event, ok := <-s.upstream:
		return event, ok
	}
}

func (m *meteredLLM) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	result, err := m.ChatCompletion(ctx, request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

// flexUnavailable reports OpenAI's "no flex capacity" rejection, returned as
// an error or as the stream's first event.
func flexUnavailable(err error, stream *peekedStream) bool {
	if err == nil && stream != nil && stream.first != nil && stream.first.Type == llm.EventTypeError {
		err, _ = stream.first.Value.(error)
	}
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "resource_unavailable") || strings.Contains(msg, "flex_unavailable")
}
