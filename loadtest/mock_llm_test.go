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

	"github.com/mattermost/mattermost-plugin-agents/llm"
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

	pSkip := fastTestProfile()
	pSkip.ToolUseProbability = 1.0
	pSkip.ReasoningSkipProbability = 1.0
	mSkip := NewMockLLM(pSkip)
	res, err := mSkip.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx})
	require.NoError(t, err)
	for ev := range res.Stream {
		require.NotEqual(t, llm.EventTypeReasoning, ev.Type)
		require.NotEqual(t, llm.EventTypeReasoningEnd, ev.Type)
	}

	pAll := fastTestProfile()
	pAll.ToolUseProbability = 1.0
	pAll.ReasoningSkipProbability = 0.0
	mAll := NewMockLLM(pAll)
	res2, err := mAll.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx})
	require.NoError(t, err)
	hasR := false
	hasREnd := false
	for ev := range res2.Stream {
		if ev.Type == llm.EventTypeReasoning {
			hasR = true
		}
		if ev.Type == llm.EventTypeReasoningEnd {
			hasREnd = true
		}
	}
	require.True(t, hasR)
	require.True(t, hasREnd)

	pAll2 := fastTestProfile()
	pAll2.ToolUseProbability = 0
	pAll2.ReasoningSkipProbability = 0.0
	mAll2 := NewMockLLM(pAll2)
	res3, err := mAll2.ChatCompletion(context.Background(), llm.CompletionRequest{Context: ctx}, llm.WithReasoningDisabled())
	require.NoError(t, err)
	for ev := range res3.Stream {
		require.NotEqual(t, llm.EventTypeReasoning, ev.Type)
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
			for range res.Stream {
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
				limits[int(decoded["limit"].(float64))] = struct{}{}
			}
		}
	}
	require.GreaterOrEqual(t, len(args), 2)
	require.GreaterOrEqual(t, len(limits), 2)
}

func TestCountTokens(t *testing.T) {
	t.Parallel()
	m := NewMockLLM(fastTestProfile())
	require.Equal(t, 0, m.CountTokens(""))
	require.Equal(t, 1, m.CountTokens("abc"))
	require.Equal(t, 2, m.CountTokens("abcde"))
	require.Equal(t, 100000, m.InputTokenLimit())
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
