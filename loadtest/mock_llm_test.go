// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestLanguageModelAssertion(t *testing.T) {
	t.Parallel()
	var _ llm.LanguageModel = (*MockLLM)(nil)
}

func TestNewMockLLMValidatesAndCopiesProfile(t *testing.T) {
	t.Parallel()
	p := fastTestProfile()
	m := NewMockLLM(p)

	p.ProfileWeights["realistic_default"] = 0
	p.ToolArgumentProfiles["read_channel"] = ToolArgumentProfile{PostLimits: []int{999}}
	p.FinalResponseTemplates[0] = "mutated %d"

	require.NotZero(t, m.profile.ProfileWeights["realistic_default"])
	require.NotEqual(t, []int{999}, m.profile.ToolArgumentProfiles["read_channel"].PostLimits)
	require.NotEqual(t, "mutated %d", m.profile.FinalResponseTemplates[0])
}

func TestDeterministicRepeatNewInstances(t *testing.T) {
	t.Parallel()
	base := fastTestProfile()
	base.ToolUseProbability = 0
	base.ReasoningSkipProbability = 1.0

	var first []llm.EventType
	for i := 0; i < 2; i++ {
		m := NewMockLLM(base)
		res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{}, llm.WithReasoningDisabled())
		require.NoError(t, err)
		var types []llm.EventType
		for ev := range res.Stream {
			types = append(types, ev.Type)
		}
		if i == 0 {
			first = types
		} else {
			require.Equal(t, first, types)
		}
	}
}

func TestTextOnlyStreamEvents(t *testing.T) {
	t.Parallel()
	p := fastTestProfile()
	p.ToolUseProbability = 0
	p.ReasoningSkipProbability = 1.0
	for k := range p.LatencyProfiles {
		lp := p.LatencyProfiles[k]
		lp.ChunkCount = [2]int{1, 1}
		p.LatencyProfiles[k] = lp
	}
	m := NewMockLLM(p)
	res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{}, llm.WithReasoningDisabled())
	require.NoError(t, err)
	var types []llm.EventType
	for ev := range res.Stream {
		types = append(types, ev.Type)
	}
	require.Equal(t, []llm.EventType{
		llm.EventTypeText,
		llm.EventTypeUsage,
		llm.EventTypeEnd,
	}, types)
}

func TestStreamingDisabledOneChunk(t *testing.T) {
	t.Parallel()
	p := fastTestProfile()
	p.StreamingEnabled = false
	p.ToolUseProbability = 0
	p.ReasoningSkipProbability = 1.0
	m := NewMockLLM(p)
	res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{}, llm.WithReasoningDisabled())
	require.NoError(t, err)
	textChunks := 0
	for ev := range res.Stream {
		if ev.Type == llm.EventTypeText {
			textChunks++
		}
	}
	require.Equal(t, 1, textChunks)
}

func TestToolCallStreamTyping(t *testing.T) {
	t.Parallel()
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{Name: "read_channel"}})
	ctx := &llm.Context{
		Channel: &model.Channel{Id: model.NewId()},
		Tools:   store,
	}
	p := fastTestProfile()
	p.ToolUseProbability = 1.0
	p.ReasoningSkipProbability = 1.0
	m := NewMockLLM(p)
	res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx}, llm.WithReasoningDisabled())
	require.NoError(t, err)
	found := false
	for ev := range res.Stream {
		if ev.Type == llm.EventTypeToolCalls {
			tcs, ok := ev.Value.([]llm.ToolCall)
			require.True(t, ok)
			require.NotEmpty(t, tcs)
			found = true
		}
	}
	require.True(t, found)
}

func TestMockLLMSkipsUnbuildableWeightedTool(t *testing.T) {
	t.Parallel()
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{
		{Name: "group_message"},
		{Name: "read_channel"},
	})
	ctx := &llm.Context{
		Channel: &model.Channel{Id: model.NewId()},
		Tools:   store,
	}
	p := fastTestProfile()
	p.ToolUseProbability = 1.0
	p.ReasoningSkipProbability = 1.0
	p.ToolWeights = map[string]float64{
		"group_message": 1000,
		"read_channel":  1,
	}
	delete(p.ToolArgumentProfiles, "group_message")
	m := NewMockLLM(p)
	res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx}, llm.WithReasoningDisabled())
	require.NoError(t, err)

	for ev := range res.Stream {
		if ev.Type != llm.EventTypeToolCalls {
			continue
		}
		tcs := ev.Value.([]llm.ToolCall)
		require.Len(t, tcs, 1)
		require.Equal(t, "read_channel", tcs[0].Name)
		return
	}
	require.Fail(t, "expected eligible tool call")
}

func TestToolsDisabledForcesTextOnly(t *testing.T) {
	t.Parallel()
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{Name: "read_channel"}})
	ctx := &llm.Context{
		Channel: &model.Channel{Id: model.NewId()},
		Tools:   store,
	}
	p := fastTestProfile()
	p.ToolUseProbability = 1.0
	m := NewMockLLM(p)
	res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx}, llm.WithToolsDisabled())
	require.NoError(t, err)
	for ev := range res.Stream {
		require.NotEqual(t, llm.EventTypeToolCalls, ev.Type)
	}
}

func TestMaxToolRoundsBlocksTools(t *testing.T) {
	t.Parallel()
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{Name: "read_channel"}})
	ctx := &llm.Context{
		Channel: &model.Channel{Id: model.NewId()},
		Tools:   store,
	}
	p := fastTestProfile()
	p.ToolUseProbability = 1.0
	p.MaxToolRounds = 2
	p.ReasoningSkipProbability = 1.0

	posts := []llm.Post{
		{Role: llm.PostRoleBot, ToolUse: []llm.ToolCall{{ID: "x"}}},
		{Role: llm.PostRoleBot, ToolUse: []llm.ToolCall{{ID: "y"}}},
	}
	m := NewMockLLM(p)
	res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx, Posts: posts}, llm.WithReasoningDisabled())
	require.NoError(t, err)
	for ev := range res.Stream {
		require.NotEqual(t, llm.EventTypeToolCalls, ev.Type)
	}
}

func TestHistoricalToolRoundsDoNotBlockNewRequest(t *testing.T) {
	t.Parallel()
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{Name: "read_channel"}})
	ctx := &llm.Context{
		Channel: &model.Channel{Id: model.NewId()},
		Tools:   store,
	}
	p := fastTestProfile()
	p.ToolUseProbability = 1.0
	p.MaxToolRounds = 2
	p.ReasoningSkipProbability = 1.0

	posts := []llm.Post{
		{Role: llm.PostRoleBot, ToolUse: []llm.ToolCall{{ID: "historical-1"}}},
		{Role: llm.PostRoleBot, ToolUse: []llm.ToolCall{{ID: "historical-2"}}},
		{Role: llm.PostRoleUser, Message: "new request"},
	}
	m := NewMockLLM(p)
	res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx, Posts: posts}, llm.WithReasoningDisabled())
	require.NoError(t, err)
	for ev := range res.Stream {
		if ev.Type == llm.EventTypeToolCalls {
			require.NotEmpty(t, ev.Value)
			return
		}
	}
	require.Fail(t, "expected current request to remain eligible for tool calls")
}

func TestReasoningSkipProbabilityAllOrNothing(t *testing.T) {
	t.Parallel()
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{Name: "read_channel"}})
	ctx := &llm.Context{Tools: store, Channel: &model.Channel{Id: model.NewId()}}

	tests := []struct {
		name         string
		setupProfile func(*MockProfile)
		options      []llm.LanguageModelOption
		assertEvents func(*testing.T, []llm.EventType)
	}{
		{
			name: "skips reasoning when skip probability is one",
			setupProfile: func(p *MockProfile) {
				p.ToolUseProbability = 1.0
				p.ReasoningSkipProbability = 1.0
			},
			assertEvents: func(t *testing.T, events []llm.EventType) {
				for _, eventType := range events {
					require.NotEqual(t, llm.EventTypeReasoning, eventType)
					require.NotEqual(t, llm.EventTypeReasoningEnd, eventType)
				}
			},
		},
		{
			name: "emits reasoning and end when skip probability is zero",
			setupProfile: func(p *MockProfile) {
				p.ToolUseProbability = 1.0
				p.ReasoningSkipProbability = 0.0
			},
			assertEvents: func(t *testing.T, events []llm.EventType) {
				hasR := false
				hasREnd := false
				for _, eventType := range events {
					if eventType == llm.EventTypeReasoning {
						hasR = true
					}
					if eventType == llm.EventTypeReasoningEnd {
						hasREnd = true
					}
				}
				require.True(t, hasR)
				require.True(t, hasREnd)
			},
		},
		{
			name: "omits reasoning when reasoning is disabled",
			setupProfile: func(p *MockProfile) {
				p.ToolUseProbability = 0
				p.ReasoningSkipProbability = 0.0
			},
			options: []llm.LanguageModelOption{llm.WithReasoningDisabled()},
			assertEvents: func(t *testing.T, events []llm.EventType) {
				for _, eventType := range events {
					require.NotEqual(t, llm.EventTypeReasoning, eventType)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := fastTestProfile()
			tt.setupProfile(&p)
			m := NewMockLLM(p)
			res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx}, tt.options...)
			require.NoError(t, err)

			var events []llm.EventType
			for ev := range res.Stream {
				events = append(events, ev.Type)
			}
			tt.assertEvents(t, events)
		})
	}
}

func TestProfileWeightConvergence(t *testing.T) {
	t.Parallel()
	p := fastTestProfile()
	p.ToolUseProbability = 0
	p.ReasoningSkipProbability = 1.0
	p.LatencyProfiles["realistic_default"] = LatencyProfile{
		TTFTMs: [2]int{0, 0}, ChunkCount: [2]int{7, 7}, ChunkIntervalMs: [2]int{0, 0},
		TotalWallTimeMsPerRequest: [2]int{0, 0},
	}
	p.LatencyProfiles["realistic_fast"] = LatencyProfile{
		TTFTMs: [2]int{0, 0}, ChunkCount: [2]int{3, 3}, ChunkIntervalMs: [2]int{0, 0},
		TotalWallTimeMsPerRequest: [2]int{0, 0},
	}
	p.LatencyProfiles["realistic_slow"] = LatencyProfile{
		TTFTMs: [2]int{0, 0}, ChunkCount: [2]int{11, 11}, ChunkIntervalMs: [2]int{0, 0},
		TotalWallTimeMsPerRequest: [2]int{0, 0},
	}
	require.NoError(t, p.Validate())

	hist := map[int]int{}
	m := NewMockLLM(p)
	for i := 0; i < 4000; i++ {
		res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{}, llm.WithReasoningDisabled())
		require.NoError(t, err)
		n := 0
		for ev := range res.Stream {
			if ev.Type == llm.EventTypeText {
				n++
			}
		}
		hist[n]++
	}
	require.InDelta(t, 0.70, float64(hist[7])/4000, 0.04)
	require.InDelta(t, 0.20, float64(hist[3])/4000, 0.04)
	require.InDelta(t, 0.10, float64(hist[11])/4000, 0.04)
}

func TestConcurrentChatCompletionRace(t *testing.T) {
	t.Parallel()
	p := fastTestProfile()
	p.ToolUseProbability = 0
	p.ReasoningSkipProbability = 1.0
	m := NewMockLLM(p)
	var wg sync.WaitGroup
	errCh := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{}, llm.WithReasoningDisabled())
			if err != nil {
				errCh <- err
				return
			}
			for event := range res.Stream {
				_ = event
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
}

func TestChatCompletionNoStreamBlocksAndText(t *testing.T) {
	t.Parallel()
	p := fastTestProfile()
	p.LatencyProfiles["realistic_default"] = LatencyProfile{
		TTFTMs: [2]int{0, 0}, ChunkCount: [2]int{1, 1}, ChunkIntervalMs: [2]int{0, 0},
		TotalWallTimeMsPerRequest: [2]int{45, 45},
	}
	p.ToolUseProbability = 0
	p.ReasoningSkipProbability = 1.0
	m := NewMockLLM(p)
	start := time.Now()
	txt, err := m.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{}, llm.WithReasoningDisabled())
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond)
	require.NotEmpty(t, txt)
}

func TestChatCompletionClosesStreamOnContextCancellation(t *testing.T) {
	t.Parallel()
	p := fastTestProfile()
	for k := range p.LatencyProfiles {
		p.LatencyProfiles[k] = LatencyProfile{
			TTFTMs:                    [2]int{1000, 1000},
			ChunkCount:                [2]int{1, 1},
			ChunkIntervalMs:           [2]int{0, 0},
			TotalWallTimeMsPerRequest: [2]int{1000, 1000},
		}
	}
	p.ToolUseProbability = 0
	p.ReasoningSkipProbability = 1.0

	ctx, cancel := context.WithCancel(context.Background())
	m := NewMockLLM(p)
	res, err := m.ChatCompletion(ctx, llm.CompletionRequest{}, llm.WithReasoningDisabled())
	require.NoError(t, err)
	cancel()

	select {
	case _, ok := <-res.Stream:
		require.False(t, ok)
	case <-time.After(200 * time.Millisecond):
		require.Fail(t, "stream did not close after context cancellation")
	}
}

func TestChatCompletionNoStreamHonorsContextCancellation(t *testing.T) {
	t.Parallel()
	p := fastTestProfile()
	for k := range p.LatencyProfiles {
		p.LatencyProfiles[k] = LatencyProfile{
			TTFTMs:                    [2]int{0, 0},
			ChunkCount:                [2]int{1, 1},
			ChunkIntervalMs:           [2]int{0, 0},
			TotalWallTimeMsPerRequest: [2]int{1000, 1000},
		}
	}
	p.ToolUseProbability = 0
	p.ReasoningSkipProbability = 1.0

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	m := NewMockLLM(p)
	start := time.Now()
	txt, err := m.ChatCompletionNoStream(ctx, llm.CompletionRequest{}, llm.WithReasoningDisabled())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, txt)
	require.Less(t, time.Since(start), 200*time.Millisecond)
}

func TestToolArgumentsVaryBySeed(t *testing.T) {
	t.Parallel()
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{Name: "read_channel"}})
	ctx := &llm.Context{Channel: &model.Channel{Id: model.NewId()}, Tools: store}
	p := fastTestProfile()
	p.ToolUseProbability = 1.0
	p.ReasoningSkipProbability = 1.0
	args := map[string]struct{}{}
	limits := map[int]struct{}{}
	m := NewMockLLM(p)
	for i := 0; i < 20; i++ {
		res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx}, llm.WithReasoningDisabled())
		require.NoError(t, err)
		for ev := range res.Stream {
			if ev.Type == llm.EventTypeToolCalls {
				tcs := ev.Value.([]llm.ToolCall)
				args[string(tcs[0].Arguments)] = struct{}{}
				var decoded map[string]any
				require.NoError(t, json.Unmarshal(tcs[0].Arguments, &decoded))
				limit, ok := decoded["limit"]
				require.True(t, ok)
				limitFloat, ok := limit.(float64)
				require.True(t, ok)
				limits[int(limitFloat)] = struct{}{}
			}
		}
	}
	require.GreaterOrEqual(t, len(args), 2)
	require.GreaterOrEqual(t, len(limits), 2)
}

func TestCountTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "empty string", input: "", want: 0},
		{name: "three characters", input: "abc", want: 1},
		{name: "five characters", input: "abcde", want: 2},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, countTextTokens(tt.input))
		})
	}
}

func TestCountTokensSumsPosts(t *testing.T) {
	t.Parallel()
	m := NewMockLLM(fastTestProfile())
	n, err := m.CountTokens(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Message: "abcd"}, {Message: "efgh"}},
	})
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

func TestMockLLMTokenLimits(t *testing.T) {
	t.Parallel()
	m := NewMockLLM(fastTestProfile())
	require.Equal(t, 100000, m.InputTokenLimit())
	require.Equal(t, 100000, m.OutputTokenLimit())
}

func TestCountToolRoundsExportedViaBehavior(t *testing.T) {
	t.Parallel()
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{Name: "read_channel"}})
	ctx := &llm.Context{
		Channel: &model.Channel{Id: model.NewId()},
		Tools:   store,
	}
	p := fastTestProfile()
	p.ToolUseProbability = 1.0
	p.MaxToolRounds = 2
	p.ReasoningSkipProbability = 1.0

	posts := make([]llm.Post, 2)
	for i := range posts {
		posts[i] = llm.Post{
			Role:    llm.PostRoleBot,
			ToolUse: []llm.ToolCall{{ID: fmt.Sprintf("t%d", i), Name: "read_channel", Arguments: []byte(`{}`)}},
		}
	}
	m := NewMockLLM(p)
	res, err := m.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx, Posts: posts}, llm.WithReasoningDisabled())
	require.NoError(t, err)
	for ev := range res.Stream {
		require.NotEqual(t, llm.EventTypeToolCalls, ev.Type)
	}
}
