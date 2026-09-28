// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticLanguageModel struct {
	mu       sync.Mutex
	calls    int
	started  chan struct{}
	release  chan struct{}
	block    bool
	response string
}

func (m *staticLanguageModel) ChatCompletion(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	return llm.NewStreamFromString(m.complete()), nil
}

func (m *staticLanguageModel) ChatCompletionNoStream(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (string, error) {
	return m.complete(), nil
}

func (m *staticLanguageModel) complete() string {
	m.mu.Lock()
	started := m.started
	release := m.release
	block := m.block
	m.calls++
	m.mu.Unlock()

	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if block && release != nil {
		<-release
	}
	if m.response != "" {
		return m.response
	}
	return "ok"
}

func (m *staticLanguageModel) CountTokens(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (int, error) {
	return 1, nil
}
func (m *staticLanguageModel) InputTokenLimit() int  { return 4096 }
func (m *staticLanguageModel) OutputTokenLimit() int { return 4096 }

// requireShutdownIDsAfterGC waits for retired clients to shut down. Agent
// handles release their lease when collected, so the wait forces GC.
func requireShutdownIDsAfterGC(t *testing.T, builder *fakeServiceLLMBuilder, want []string) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		runtime.GC()
		runtime.GC()
		assert.ElementsMatch(c, want, builder.shutdownIDs())
	}, 5*time.Second, 10*time.Millisecond)
}

func newAgentLLMTestBots(t *testing.T, agents int) (*MMBots, *mockConfig, *stubAgentStore, *fakeServiceLLMBuilder) {
	t.Helper()

	store := &stubAgentStore{agents: dbAgents(agents, "svc")}
	cfg := &mockConfig{services: []llm.ServiceConfig{openAIService("svc", "")}}
	mmBots := newEnsureBotsHarness(t, cfg, store, enterpriseAdvancedLicense())
	builder := &fakeServiceLLMBuilder{}
	mmBots.SetBaseLLMBuilderForTest(builder.build)
	return mmBots, cfg, store, builder
}

func TestEnsureBotsAgentsShareServiceClient(t *testing.T) {
	mmBots, cfg, _, builder := newAgentLLMTestBots(t, 3)

	require.NoError(t, mmBots.EnsureBots())
	require.Len(t, mmBots.GetAllBots(), 3)
	require.Equal(t, 1, builder.buildCount(), "agents on one service must share its provider client")

	_, release, err := mmBots.AcquireServiceLLM(cfg.services[0], nil)
	require.NoError(t, err)
	release()
	require.Equal(t, 1, builder.buildCount(), "direct service calls must share the agents' provider client")
}

func TestEnsureBotsAgentChangesReuseServiceClient(t *testing.T) {
	tests := []struct {
		name   string
		change func(*llm.BotConfig)
	}{
		{name: "custom instructions", change: func(a *llm.BotConfig) { a.CustomInstructions = "changed" }},
		{name: "model override", change: func(a *llm.BotConfig) { a.Model = "gpt-5" }},
		{name: "native tools", change: func(a *llm.BotConfig) { a.EnabledNativeTools = []string{llm.NativeToolWebSearch} }},
		{name: "reasoning", change: func(a *llm.BotConfig) { a.ReasoningEnabled = true; a.ReasoningEffort = "high" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mmBots, _, store, builder := newAgentLLMTestBots(t, 2)

			require.NoError(t, mmBots.EnsureBots())
			before := mmBots.GetAllBots()[0].LLM()

			tt.change(&store.agents[0])
			require.NoError(t, mmBots.EnsureBots())
			require.NotSame(t, before, mmBots.GetAllBots()[0].LLM(), "the agent must be rebuilt")

			require.Equal(t, 1, builder.buildCount())
			runtime.GC()
			runtime.GC()
			assert.Empty(t, builder.shutdownIDs())
		})
	}
}

func TestEnsureBotsServiceChangeKeepsClientForInFlightRequest(t *testing.T) {
	store := &stubAgentStore{agents: dbAgents(1, "svc")}
	cfg := &mockConfig{services: []llm.ServiceConfig{openAIService("svc", "")}}
	mmBots := newEnsureBotsHarness(t, cfg, store, enterpriseAdvancedLicense())

	first := &staticLanguageModel{response: "first", started: make(chan struct{}), release: make(chan struct{}), block: true}
	second := &staticLanguageModel{response: "second"}
	builder := &fakeServiceLLMBuilder{}
	mmBots.SetBaseLLMBuilderForTest(func(svc llm.ServiceConfig, fallbacks []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
		_, shutdown, err := builder.build(svc, fallbacks)
		if builder.buildCount() == 1 {
			return first, shutdown, err
		}
		return second, shutdown, err
	})

	require.NoError(t, mmBots.EnsureBots())
	held := mmBots.GetAllBots()[0].LLM()

	done := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		answer, err := held.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{})
		errCh <- err
		done <- answer
	}()
	<-first.started

	cfg.services[0].APIKey = "rotated"
	require.NoError(t, mmBots.EnsureBots())
	mmBots.ReconcileServiceLLMs(cfg.services)
	require.Equal(t, 2, builder.buildCount())

	runtime.GC()
	runtime.GC()
	assert.Empty(t, builder.shutdownIDs(), "an in-flight holder must keep the replaced client alive")

	close(first.release)
	require.NoError(t, <-errCh)
	require.Equal(t, "first", <-done)

	answer, err := mmBots.GetAllBots()[0].LLM().ChatCompletionNoStream(context.Background(), llm.CompletionRequest{})
	require.NoError(t, err)
	require.Equal(t, "second", answer)

	held = nil
	requireShutdownIDsAfterGC(t, builder, []string{"svc"})
}

func TestShutdownServiceLLMsReleasesAgentClients(t *testing.T) {
	mmBots, cfg, _, builder := newAgentLLMTestBots(t, 1)

	require.NoError(t, mmBots.EnsureBots())
	held := mmBots.GetAllBots()[0].LLM()

	cfg.services[0].APIKey = "rotated"
	require.NoError(t, mmBots.EnsureBots())

	mmBots.ShutdownServiceLLMs()
	require.ElementsMatch(t, []string{"svc", "svc"}, builder.shutdownIDs())

	runtime.KeepAlive(held)
}

func TestReleaseStreamWhenConsumedDropsLeaseOnCancel(t *testing.T) {
	inner := make(chan llm.TextStreamEvent)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	released := make(chan struct{})
	out := releaseStreamWhenConsumed(ctx, &llm.TextStreamResult{Stream: inner}, func() { close(released) })

	inner <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "a"}
	ev := <-out.Stream
	require.Equal(t, "a", ev.Value)

	cancel()
	inner <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "b"}
	close(inner)

	require.Eventually(t, func() bool {
		select {
		case <-released:
			return true
		default:
			return false
		}
	}, 5*time.Second, time.Millisecond)

	_, ok := <-out.Stream
	require.False(t, ok, "canceled relay must close the outbound stream")
}
