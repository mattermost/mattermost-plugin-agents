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

func requireAgentShutdownIDs(t *testing.T, builder *fakeServiceLLMBuilder, want []string) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		runtime.GC()
		runtime.GC()
		assert.Equal(c, want, builder.shutdownIDs())
	}, 5*time.Second, 10*time.Millisecond)
}

func newAgentLLMTestBots(t *testing.T) (*MMBots, *stubAgentStore, *fakeServiceLLMBuilder) {
	t.Helper()

	store := &stubAgentStore{
		agents: dbAgents(1, "svc"),
	}
	cfg := &mockConfig{
		services: []llm.ServiceConfig{openAIService("svc")},
	}
	mmBots := newEnsureBotsHarness(t, cfg, store)
	builder := &fakeServiceLLMBuilder{}
	mmBots.SetBaseLLMBuilderForTest(builder.build)
	return mmBots, store, builder
}

func TestEnsureBotsShutsDownReplacedAgentLLM(t *testing.T) {
	mmBots, store, builder := newAgentLLMTestBots(t)

	require.NoError(t, mmBots.EnsureBots())
	require.Equal(t, 1, builder.buildCount())
	assert.Empty(t, builder.shutdownIDs())

	store.agents[0].CustomInstructions = "changed"
	require.NoError(t, mmBots.EnsureBots())
	require.Equal(t, 2, builder.buildCount())

	requireAgentShutdownIDs(t, builder, []string{"svc"})
}

func TestEnsureBotsUnchangedSkipsRebuildAndShutdown(t *testing.T) {
	mmBots, _, builder := newAgentLLMTestBots(t)

	require.NoError(t, mmBots.EnsureBots())
	initial := mmBots.GetAllBots()[0].LLM()

	require.NoError(t, mmBots.EnsureBots())
	require.Same(t, initial, mmBots.GetAllBots()[0].LLM())
	require.Equal(t, 1, builder.buildCount())
	assert.Empty(t, builder.shutdownIDs())
}

func TestEnsureBotsInFlightRequestSurvivesReplace(t *testing.T) {
	store := &stubAgentStore{agents: dbAgents(1, "svc")}
	cfg := &mockConfig{services: []llm.ServiceConfig{openAIService("svc")}}
	mmBots := newEnsureBotsHarness(t, cfg, store)

	first := &staticLanguageModel{response: "first", started: make(chan struct{}), release: make(chan struct{}), block: true}
	second := &staticLanguageModel{response: "second"}
	var builds int
	var shutdowns int
	var shutdownMu sync.Mutex
	mmBots.SetBaseLLMBuilderForTest(func(llm.ServiceConfig, llm.BotConfig, []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
		builds++
		n := builds
		model := second
		if n == 1 {
			model = first
		}
		return model, func() {
			shutdownMu.Lock()
			shutdowns++
			shutdownMu.Unlock()
		}, nil
	})

	require.NoError(t, mmBots.EnsureBots())
	held := mmBots.GetAllBots()[0].LLM()

	done := make(chan string, 1)
	go func() {
		answer, err := held.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{})
		require.NoError(t, err)
		done <- answer
	}()
	<-first.started

	store.agents[0].CustomInstructions = "changed"
	require.NoError(t, mmBots.EnsureBots())
	require.Equal(t, 2, builds)

	shutdownMu.Lock()
	assert.Equal(t, 0, shutdowns, "in-flight holder must keep the replaced client alive")
	shutdownMu.Unlock()

	close(first.release)
	require.Equal(t, "first", <-done)

	held = nil
	require.Eventually(t, func() bool {
		runtime.GC()
		runtime.GC()
		shutdownMu.Lock()
		defer shutdownMu.Unlock()
		return shutdowns == 1
	}, 5*time.Second, time.Millisecond)
}

func TestShutdownAgentLLMsReleasesLiveAndRetired(t *testing.T) {
	mmBots, store, builder := newAgentLLMTestBots(t)

	require.NoError(t, mmBots.EnsureBots())
	held := mmBots.GetAllBots()[0].LLM()

	store.agents[0].CustomInstructions = "changed"
	require.NoError(t, mmBots.EnsureBots())

	mmBots.ShutdownAgentLLMs()
	require.ElementsMatch(t, []string{"svc", "svc"}, builder.shutdownIDs())

	_ = held
}

func TestEnsureBotsFailedBuildShutsDownAlreadyBuilt(t *testing.T) {
	store := &stubAgentStore{agents: dbAgents(2, "svc")}
	cfg := &mockConfig{services: []llm.ServiceConfig{openAIService("svc")}}
	mmBots := newEnsureBotsHarness(t, cfg, store)

	builder := &fakeServiceLLMBuilder{}
	mmBots.SetBaseLLMBuilderForTest(func(svc llm.ServiceConfig, botCfg llm.BotConfig, fallbacks []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
		if botCfg.Name == "agent2" {
			return nil, nil, assert.AnError
		}
		return builder.build(svc, botCfg, fallbacks)
	})

	require.Error(t, mmBots.EnsureBots())
	require.Empty(t, mmBots.GetAllBots())
	require.Equal(t, []string{"svc"}, builder.shutdownIDs())
}
