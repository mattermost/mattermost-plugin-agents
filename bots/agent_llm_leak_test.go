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

// measureEnsureBotsGoroutineGrowth drives EnsureBots through an initial build
// plus refreshRounds effective config changes and returns goroutine counts.
func measureEnsureBotsGoroutineGrowth(t *testing.T, svc llm.ServiceConfig, agents int, refreshRounds int) (baseline, afterFirst, afterUnchanged, afterRefreshes int) {
	t.Helper()

	store := &stubAgentStore{agents: dbAgents(agents, svc.ID)}
	cfg := &mockConfig{
		bots:     nil,
		services: []llm.ServiceConfig{svc},
	}
	mmBots := newEnsureBotsHarness(t, cfg, store)

	baseline = settleGoroutines()

	require.NoError(t, mmBots.EnsureBots())
	require.Len(t, mmBots.GetAllBots(), agents)
	afterFirst = settleGoroutines()

	require.NoError(t, mmBots.EnsureBots())
	afterUnchanged = settleGoroutines()

	for round := 1; round <= refreshRounds; round++ {
		for i := range store.agents {
			store.agents[i].CustomInstructions = fmt.Sprintf("round-%d", round)
		}
		require.NoError(t, mmBots.EnsureBots())
	}

	// Retired clients shut down once their handles are unreachable. GC runs
	// the AddCleanup lease; the retire goroutine then releases the pool.
	require.Eventually(t, func() bool {
		afterRefreshes = settleGoroutines()
		return afterRefreshes-afterFirst < perProviderWorkers/2
	}, 15*time.Second, 50*time.Millisecond)
	return baseline, afterFirst, afterUnchanged, afterRefreshes
}

// TestEnsureBotsOpenAIWorkerPoolDoesNotLeakOnConfigChange is the empirical
// check that replaced agent Bifrost clients are shut down. Each real OpenAI
// client starts DefaultConcurrency workers at Init, so a leak scales with
// agent count × effective config changes. Before the lifecycle fix this grew
// by ~1000 goroutines per agent per refresh.
func TestEnsureBotsOpenAIWorkerPoolDoesNotLeakOnConfigChange(t *testing.T) {
	const agents = 2
	const refreshRounds = 3

	svc := llm.ServiceConfig{
		ID:           "openai-svc",
		Name:         "OpenAI",
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "sk-test",
		DefaultModel: "gpt-4o",
	}

	baseline, afterFirst, afterUnchanged, afterRefreshes := measureEnsureBotsGoroutineGrowth(t, svc, agents, refreshRounds)

	firstBuildGrowth := afterFirst - baseline
	unchangedDelta := afterUnchanged - afterFirst
	refreshGrowth := afterRefreshes - afterFirst

	t.Logf("goroutines: baseline=%d afterFirst=%d afterUnchanged=%d afterRefreshes=%d",
		baseline, afterFirst, afterUnchanged, afterRefreshes)
	t.Logf("first-build growth=%d (agents=%d, ~%d workers/client)", firstBuildGrowth, agents, perProviderWorkers)
	t.Logf("unchanged EnsureBots delta=%d", unchangedDelta)
	t.Logf("refresh growth over %d rounds=%d (agents=%d)", refreshRounds, refreshGrowth, agents)

	// Construction must actually start the worker pool; otherwise this test
	// cannot detect a leak.
	require.Greater(t, firstBuildGrowth, agents*perProviderWorkers/2,
		"OpenAI Bifrost clients did not start worker pools at construction; leak measurement is invalid")

	require.Less(t, unchangedDelta, perProviderWorkers/2,
		"EnsureBots early-exit must not rebuild clients when config is unchanged")

	require.Less(t, refreshGrowth, perProviderWorkers/2,
		"replaced agent Bifrost clients leaked worker-pool goroutines across effective config changes")
}

// TestEnsureBotsLoadTestMockDoesNotGrowGoroutines is the control: getBaseLLM
// returns a no-op shutdown for the load-test mock, so config changes must not
// grow a Bifrost worker pool.
func TestEnsureBotsLoadTestMockDoesNotGrowGoroutines(t *testing.T) {
	const agents = 2
	const refreshRounds = 3

	svc := loadTestService(buildTinyLoadTestProfile(t, nil))
	baseline, afterFirst, afterUnchanged, afterRefreshes := measureEnsureBotsGoroutineGrowth(t, svc, agents, refreshRounds)

	firstBuildGrowth := afterFirst - baseline
	refreshGrowth := afterRefreshes - afterFirst

	t.Logf("loadtest goroutines: baseline=%d afterFirst=%d afterUnchanged=%d afterRefreshes=%d",
		baseline, afterFirst, afterUnchanged, afterRefreshes)
	t.Logf("loadtest first-build growth=%d refresh growth=%d", firstBuildGrowth, refreshGrowth)

	require.Less(t, firstBuildGrowth, perProviderWorkers/2,
		"load-test mock must not start a Bifrost worker pool")
	require.Less(t, afterUnchanged-afterFirst, perProviderWorkers/2)
	require.Less(t, refreshGrowth, perProviderWorkers/2,
		"load-test mock must not leak goroutines across config changes")
}
