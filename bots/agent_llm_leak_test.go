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

// perProviderWorkers is the Bifrost default worker-pool size started at client
// construction (schemas.DefaultConcurrency). A leaked client shows up as a
// jump of this many goroutines.
const perProviderWorkers = schemas.DefaultConcurrency

func settleGoroutines() int {
	runtime.GC()
	runtime.GC()
	runtime.Gosched()
	return runtime.NumGoroutine()
}

type goroutineGrowth struct {
	firstBuild, unchanged, agentChanges, serviceChanges int
}

// measureEnsureBotsGoroutineGrowth drives EnsureBots through an initial build,
// an unchanged rerun, rounds of agent-only changes, and rounds of service
// changes, and returns the goroutine growth each step left behind.
func measureEnsureBotsGoroutineGrowth(t *testing.T, svc llm.ServiceConfig, agents, rounds int) goroutineGrowth {
	t.Helper()

	store := &stubAgentStore{agents: dbAgents(agents, svc.ID)}
	cfg := &mockConfig{services: []llm.ServiceConfig{svc}}
	mmBots := newEnsureBotsHarness(t, cfg, store, enterpriseAdvancedLicense())

	baseline := settleGoroutines()
	require.NoError(t, mmBots.EnsureBots())
	require.Len(t, mmBots.GetAllBots(), agents)
	afterFirst := settleGoroutines()

	require.NoError(t, mmBots.EnsureBots())
	afterUnchanged := settleGoroutines()

	for round := 1; round <= rounds; round++ {
		for i := range store.agents {
			store.agents[i].CustomInstructions = fmt.Sprintf("round-%d", round)
		}
		require.NoError(t, mmBots.EnsureBots())
	}
	afterAgentChanges := settleGoroutines()

	for round := 1; round <= rounds; round++ {
		cfg.services[0].APIKey = fmt.Sprintf("sk-round-%d", round)
		require.NoError(t, mmBots.EnsureBots())
		mmBots.ReconcileServiceLLMs(cfg.services)
	}
	// A replaced client shuts down once the old agents' handles are collected.
	var afterServiceChanges int
	require.Eventually(t, func() bool {
		afterServiceChanges = settleGoroutines()
		return afterServiceChanges-afterFirst < perProviderWorkers/2
	}, 15*time.Second, 50*time.Millisecond)

	t.Logf("goroutines: baseline=%d afterFirst=%d afterUnchanged=%d afterAgentChanges=%d afterServiceChanges=%d",
		baseline, afterFirst, afterUnchanged, afterAgentChanges, afterServiceChanges)
	return goroutineGrowth{
		firstBuild:     afterFirst - baseline,
		unchanged:      afterUnchanged - afterFirst,
		agentChanges:   afterAgentChanges - afterFirst,
		serviceChanges: afterServiceChanges - afterFirst,
	}
}

// TestEnsureBotsOpenAIWorkerPoolIsSharedAndReleased is the empirical check
// against real Bifrost clients, each of which starts DefaultConcurrency workers
// at Init. Agents on one service share one pool, agent edits start no new
// pool, and a service change releases the pool it replaces.
func TestEnsureBotsOpenAIWorkerPoolIsSharedAndReleased(t *testing.T) {
	const agents = 3
	const rounds = 3

	svc := llm.ServiceConfig{
		ID:           "openai-svc",
		Name:         "OpenAI",
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "sk-test",
		DefaultModel: "gpt-4o",
	}

	growth := measureEnsureBotsGoroutineGrowth(t, svc, agents, rounds)
	t.Logf("first build=%d (agents=%d, ~%d workers/client) unchanged=%d agent changes=%d service changes=%d",
		growth.firstBuild, agents, perProviderWorkers, growth.unchanged, growth.agentChanges, growth.serviceChanges)

	// Construction must actually start the worker pool; otherwise this test
	// cannot detect a leak.
	require.Greater(t, growth.firstBuild, perProviderWorkers/2,
		"the OpenAI Bifrost client did not start a worker pool at construction; leak measurement is invalid")
	require.Less(t, growth.firstBuild, perProviderWorkers*3/2,
		"agents on one service must share a single Bifrost worker pool")
	require.Less(t, growth.unchanged, perProviderWorkers/2,
		"EnsureBots early-exit must not rebuild clients when config is unchanged")
	require.Less(t, growth.agentChanges, perProviderWorkers/2,
		"agent-only changes must not start another worker pool")
	require.Less(t, growth.serviceChanges, perProviderWorkers/2,
		"replaced service clients leaked worker-pool goroutines")
}

// TestEnsureBotsLoadTestMockDoesNotGrowGoroutines is the control: the load-test
// mock starts no worker pool, so no step may grow goroutines.
func TestEnsureBotsLoadTestMockDoesNotGrowGoroutines(t *testing.T) {
	growth := measureEnsureBotsGoroutineGrowth(t, loadTestService(buildTinyLoadTestProfile(t, nil)), 2, 3)

	require.Less(t, growth.firstBuild, perProviderWorkers/2, "load-test mock must not start a Bifrost worker pool")
	require.Less(t, growth.unchanged, perProviderWorkers/2)
	require.Less(t, growth.agentChanges, perProviderWorkers/2)
	require.Less(t, growth.serviceChanges, perProviderWorkers/2)
}
