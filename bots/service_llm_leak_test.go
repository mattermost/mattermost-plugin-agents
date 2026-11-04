// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

func settleGoroutines() int {
	runtime.GC()
	runtime.Gosched()
	return runtime.NumGoroutine()
}

// TestEnsureBotsOpenAIWorkerPoolIsSharedAndReleased measures real Bifrost
// clients, each of which starts schemas.DefaultConcurrency workers at Init:
// agents on one service share one pool, agent edits start no new pool, and a
// service change releases the pool it replaces.
func TestEnsureBotsOpenAIWorkerPoolIsSharedAndReleased(t *testing.T) {
	const workers = schemas.DefaultConcurrency
	const agents = 3

	store := &stubAgentStore{agents: dbAgents(agents, "openai-svc")}
	cfg := &mockConfig{services: []llm.ServiceConfig{{
		ID:           "openai-svc",
		Name:         "OpenAI",
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "sk-test",
		DefaultModel: "gpt-4o",
	}}}
	mmBots := newEnsureBotsHarness(t, cfg, store, enterpriseAdvancedLicense())

	baseline := settleGoroutines()
	require.NoError(t, mmBots.EnsureBots())
	require.Len(t, mmBots.GetAllBots(), agents)
	afterFirst := settleGoroutines()
	t.Logf("first build: +%d goroutines for %d agents", afterFirst-baseline, agents)
	require.Greater(t, afterFirst-baseline, workers/2, "no worker pool started; the measurement is invalid")
	require.Less(t, afterFirst-baseline, workers*3/2, "agents on one service must share one worker pool")

	for round := range 3 {
		for i := range store.agents {
			store.agents[i].CustomInstructions = fmt.Sprintf("round-%d", round)
		}
		require.NoError(t, mmBots.EnsureBots())
	}
	afterAgentChanges := settleGoroutines()
	t.Logf("after agent changes: %+d goroutines", afterAgentChanges-afterFirst)
	require.Less(t, afterAgentChanges-afterFirst, workers/2, "agent changes must not start another worker pool")

	for round := range 3 {
		cfg.services[0].APIKey = fmt.Sprintf("sk-round-%d", round)
		require.NoError(t, mmBots.EnsureBots())
		mmBots.ReconcileServiceLLMs(cfg.services)
	}
	// Retirement shuts the replaced client down on its own goroutine.
	require.Eventually(t, func() bool {
		return settleGoroutines()-afterFirst < workers/2
	}, 15*time.Second, 50*time.Millisecond, "replaced service clients leaked their worker pools")
	t.Logf("after service changes: %+d goroutines", settleGoroutines()-afterFirst)
}

// TestEnsureBotsRejectedAgentDoesNotPinClient covers an agent whose settings
// the service rejects after its client was built: the failed build must not
// keep that client alive once the service is gone.
func TestEnsureBotsRejectedAgentDoesNotPinClient(t *testing.T) {
	const workers = schemas.DefaultConcurrency

	agents := dbAgents(1, "openai-svc")
	agents[0].EnabledNativeTools = []string{llm.NativeToolWebSearch}
	store := &stubAgentStore{agents: agents}
	cfg := &mockConfig{services: []llm.ServiceConfig{
		{ID: "openai-svc", Name: "OpenAI", Type: llm.ServiceTypeOpenAI, APIKey: "sk-test", DefaultModel: "gpt-4o", FallbackServiceID: "north-svc"},
		{ID: "north-svc", Name: "North", Type: llm.ServiceTypeNorth, APIKey: "north-key", APIURL: "http://localhost", DefaultModel: "command-a"},
	}}
	mmBots := newEnsureBotsHarness(t, cfg, store, enterpriseAdvancedLicense())

	baseline := settleGoroutines()
	err := mmBots.EnsureBots()
	require.ErrorContains(t, err, "does not support provider-native tools")
	require.Greater(t, settleGoroutines()-baseline, workers/2, "no client was built; the measurement is invalid")

	mmBots.ReconcileServiceLLMs(nil)
	require.Eventually(t, func() bool {
		return settleGoroutines()-baseline < workers/2
	}, 15*time.Second, 50*time.Millisecond, "the rejected agent kept the retired client alive")
}
