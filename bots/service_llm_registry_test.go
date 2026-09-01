// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServiceLLMBuilder stands in for provider client construction so registry
// behavior can be exercised without starting Bifrost worker pools.
type fakeServiceLLMBuilder struct {
	mu        sync.Mutex
	builds    []llm.ServiceConfig
	shutdowns []string
	// beforeReturn runs inside the build, after it has been counted, so tests
	// can hold builds open or mutate configuration mid-build.
	beforeReturn func(svc llm.ServiceConfig)
	failWith     error
}

func (f *fakeServiceLLMBuilder) build(svc llm.ServiceConfig, _ llm.BotConfig, _ []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
	f.mu.Lock()
	f.builds = append(f.builds, svc)
	beforeReturn := f.beforeReturn
	failWith := f.failWith
	f.mu.Unlock()

	if beforeReturn != nil {
		beforeReturn(svc)
	}
	if failWith != nil {
		return nil, nil, failWith
	}

	return &recordingLanguageModel{}, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.shutdowns = append(f.shutdowns, svc.ID)
	}, nil
}

func (f *fakeServiceLLMBuilder) buildCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.builds)
}

func (f *fakeServiceLLMBuilder) shutdownIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.shutdowns...)
}

func newRegistryTestBots(t *testing.T, services []llm.ServiceConfig) (*MMBots, *mockConfig, *fakeServiceLLMBuilder) {
	t.Helper()

	cfg := &mockConfig{services: services}
	mmBots := newTestMMBots(t, cfg)
	builder := &fakeServiceLLMBuilder{}
	mmBots.baseLLMBuilderForTest = builder.build
	return mmBots, cfg, builder
}

// requireShutdownIDs waits for the models that have been retired to shut down.
// Retirement drains an entry's leases on its own goroutine, so the shutdown
// lands shortly after the last release rather than inside it.
func requireShutdownIDs(t *testing.T, builder *fakeServiceLLMBuilder, want []string) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, want, builder.shutdownIDs())
	}, 5*time.Second, time.Millisecond)
}

func TestAcquireServiceLLMCachesPerService(t *testing.T) {
	svc := openAIService("a")
	mmBots, _, builder := newRegistryTestBots(t, []llm.ServiceConfig{svc})

	first, releaseFirst, err := mmBots.AcquireServiceLLM(svc, nil)
	require.NoError(t, err)
	releaseFirst()

	second, releaseSecond, err := mmBots.AcquireServiceLLM(svc, nil)
	require.NoError(t, err)
	releaseSecond()

	assert.Same(t, first, second, "identical configuration must reuse the cached model")
	assert.Equal(t, 1, builder.buildCount())
	assert.Empty(t, builder.shutdownIDs())
}

func TestAcquireServiceLLMRebuildsOnConfigChange(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *mockConfig) (llm.ServiceConfig, []llm.ServiceConfig)
		initial []llm.ServiceConfig
	}{
		{
			name: "primary configuration changed",
			initial: []llm.ServiceConfig{
				openAIService("a"),
			},
			mutate: func(cfg *mockConfig) (llm.ServiceConfig, []llm.ServiceConfig) {
				changed := openAIService("a")
				changed.DefaultModel = "gpt-4.1"
				cfg.services = []llm.ServiceConfig{changed}
				return changed, nil
			},
		},
		{
			name: "fallback configuration changed",
			initial: func() []llm.ServiceConfig {
				primary := openAIService("a")
				primary.FallbackServiceID = "b"
				return []llm.ServiceConfig{primary, openAIService("b")}
			}(),
			mutate: func(cfg *mockConfig) (llm.ServiceConfig, []llm.ServiceConfig) {
				primary := openAIService("a")
				primary.FallbackServiceID = "b"
				fallback := openAIService("b")
				fallback.DefaultModel = "gpt-4.1"
				cfg.services = []llm.ServiceConfig{primary, fallback}
				return primary, []llm.ServiceConfig{fallback}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mmBots, cfg, builder := newRegistryTestBots(t, tt.initial)

			primary := tt.initial[0]
			fallbacks, err := ResolveBridgeFallbacks(tt.initial, primary)
			require.NoError(t, err)

			first, release, err := mmBots.AcquireServiceLLM(primary, fallbacks)
			require.NoError(t, err)
			release()

			newPrimary, newFallbacks := tt.mutate(cfg)
			mmBots.ReconcileServiceLLMs(cfg.services)

			second, releaseSecond, err := mmBots.AcquireServiceLLM(newPrimary, newFallbacks)
			require.NoError(t, err)
			releaseSecond()

			assert.NotSame(t, first, second, "a changed configuration must produce a new model")
			assert.Equal(t, 2, builder.buildCount())
			requireShutdownIDs(t, builder, []string{primary.ID})
		})
	}
}

// TestAcquireServiceLLMReplacesSupersededCacheEntry covers a request that
// arrives with a newer configuration before reconciliation has run: the caller
// must get a model built from its own configuration, never the cached one.
func TestAcquireServiceLLMReplacesSupersededCacheEntry(t *testing.T) {
	tests := []struct {
		name           string
		holdOldLease   bool
		wantShutdownAt string
	}{
		{
			name:           "idle superseded entry shuts down immediately",
			holdOldLease:   false,
			wantShutdownAt: "replace",
		},
		{
			name:           "leased superseded entry shuts down on release",
			holdOldLease:   true,
			wantShutdownAt: "release",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := openAIService("a")
			mmBots, cfg, builder := newRegistryTestBots(t, []llm.ServiceConfig{old})

			oldModel, releaseOld, err := mmBots.AcquireServiceLLM(old, nil)
			require.NoError(t, err)
			if !tt.holdOldLease {
				releaseOld()
			}

			updated := openAIService("a")
			updated.DefaultModel = "gpt-4.1"
			cfg.services = []llm.ServiceConfig{updated}

			newModel, releaseNew, err := mmBots.AcquireServiceLLM(updated, nil)
			require.NoError(t, err)
			assert.NotSame(t, oldModel, newModel, "the caller must get a model built from its own configuration")
			assert.Equal(t, 2, builder.buildCount())

			if tt.wantShutdownAt == "replace" {
				requireShutdownIDs(t, builder, []string{"a"})
			} else {
				assert.Empty(t, builder.shutdownIDs())
				releaseOld()
				requireShutdownIDs(t, builder, []string{"a"})
			}

			// The replacement is cached: a second acquire reuses it.
			cachedModel, releaseCached, err := mmBots.AcquireServiceLLM(updated, nil)
			require.NoError(t, err)
			assert.Same(t, newModel, cachedModel)
			releaseCached()
			releaseNew()
			assert.Equal(t, 2, builder.buildCount())
		})
	}
}

func TestAcquireServiceLLMBuildsOnceUnderConcurrency(t *testing.T) {
	svc := openAIService("a")
	mmBots, _, builder := newRegistryTestBots(t, []llm.ServiceConfig{svc})

	start := make(chan struct{})
	builder.beforeReturn = func(llm.ServiceConfig) {
		// Hold the build open so every goroutine is inside AcquireServiceLLM.
		<-start
	}

	const callers = 16
	models := make([]llm.LanguageModel, callers)
	releases := make([]func(), callers)
	errs := make([]error, callers)

	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			models[i], releases[i], errs[i] = mmBots.AcquireServiceLLM(svc, nil)
		}()
	}
	close(start)
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		require.NotNil(t, models[i])
		assert.Same(t, models[0], models[i], "every caller must share one model")
		releases[i]()
	}

	assert.Equal(t, 1, builder.buildCount())
	assert.Empty(t, builder.shutdownIDs(), "a live model must not be shut down while cached")
}

func TestAcquireServiceLLMDoesNotCacheBuildFailures(t *testing.T) {
	svc := openAIService("a")
	mmBots, _, builder := newRegistryTestBots(t, []llm.ServiceConfig{svc})
	builder.failWith = errors.New("provider unavailable")

	_, _, err := mmBots.AcquireServiceLLM(svc, nil)
	require.Error(t, err)

	builder.mu.Lock()
	builder.failWith = nil
	builder.mu.Unlock()

	model, release, err := mmBots.AcquireServiceLLM(svc, nil)
	require.NoError(t, err)
	require.NotNil(t, model)
	release()

	assert.Equal(t, 2, builder.buildCount(), "a failed build must be retried, not cached")
}

func TestReconcileServiceLLMs(t *testing.T) {
	tests := []struct {
		name           string
		nextServices   func(svc llm.ServiceConfig) []llm.ServiceConfig
		wantShutdown   bool
		wantRebuildIDs int
	}{
		{
			name: "unchanged service keeps its model",
			nextServices: func(svc llm.ServiceConfig) []llm.ServiceConfig {
				return []llm.ServiceConfig{svc}
			},
			wantShutdown:   false,
			wantRebuildIDs: 1,
		},
		{
			name: "changed service is dropped",
			nextServices: func(svc llm.ServiceConfig) []llm.ServiceConfig {
				changed := svc
				changed.DefaultModel = "gpt-4.1"
				return []llm.ServiceConfig{changed}
			},
			wantShutdown:   true,
			wantRebuildIDs: 2,
		},
		{
			name: "deleted service is dropped",
			nextServices: func(llm.ServiceConfig) []llm.ServiceConfig {
				return nil
			},
			wantShutdown:   true,
			wantRebuildIDs: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := openAIService("a")
			mmBots, cfg, builder := newRegistryTestBots(t, []llm.ServiceConfig{svc})

			_, release, err := mmBots.AcquireServiceLLM(svc, nil)
			require.NoError(t, err)
			release()

			next := tt.nextServices(svc)
			cfg.services = next
			mmBots.ReconcileServiceLLMs(next)

			if tt.wantShutdown {
				requireShutdownIDs(t, builder, []string{"a"})
			} else {
				assert.Empty(t, builder.shutdownIDs())
			}

			// Re-acquiring the original configuration rebuilds only when the
			// entry was dropped.
			cfg.services = []llm.ServiceConfig{svc}
			_, releaseAgain, err := mmBots.AcquireServiceLLM(svc, nil)
			require.NoError(t, err)
			releaseAgain()
			assert.Equal(t, tt.wantRebuildIDs, builder.buildCount())
		})
	}
}

func TestReconcileServiceLLMsWaitsForOutstandingLeases(t *testing.T) {
	svc := openAIService("a")
	mmBots, cfg, builder := newRegistryTestBots(t, []llm.ServiceConfig{svc})

	_, releaseFirst, err := mmBots.AcquireServiceLLM(svc, nil)
	require.NoError(t, err)
	_, releaseSecond, err := mmBots.AcquireServiceLLM(svc, nil)
	require.NoError(t, err)
	require.Equal(t, 1, builder.buildCount())

	changed := openAIService("a")
	changed.DefaultModel = "gpt-4.1"
	cfg.services = []llm.ServiceConfig{changed}
	mmBots.ReconcileServiceLLMs(cfg.services)

	assert.Empty(t, builder.shutdownIDs(), "a retired model with live leases must stay up")

	releaseFirst()
	assert.Empty(t, builder.shutdownIDs(), "the model must stay up until the last lease is released")

	releaseSecond()
	requireShutdownIDs(t, builder, []string{"a"})
}

func TestShutdownServiceLLMs(t *testing.T) {
	first := openAIService("a")
	second := openAIService("b")
	mmBots, cfg, builder := newRegistryTestBots(t, []llm.ServiceConfig{first, second})

	_, releaseFirst, err := mmBots.AcquireServiceLLM(first, nil)
	require.NoError(t, err)
	releaseFirst()

	// Keep a lease open on a retired entry so shutdown has to cover both maps.
	_, releaseSecond, err := mmBots.AcquireServiceLLM(second, nil)
	require.NoError(t, err)
	changedSecond := openAIService("b")
	changedSecond.DefaultModel = "gpt-4.1"
	cfg.services = []llm.ServiceConfig{first, changedSecond}
	mmBots.ReconcileServiceLLMs(cfg.services)
	require.Empty(t, builder.shutdownIDs())

	mmBots.ShutdownServiceLLMs()
	assert.ElementsMatch(t, []string{"a", "b"}, builder.shutdownIDs())

	// Releasing afterwards must not shut the same client down twice.
	releaseSecond()
	assert.ElementsMatch(t, []string{"a", "b"}, builder.shutdownIDs())
}
