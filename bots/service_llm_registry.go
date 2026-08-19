// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"reflect"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// serviceLLMEntry is one lazily built service-backed language model plus the
// configuration it was built from and the number of in-flight requests holding
// it.
type serviceLLMEntry struct {
	model     llm.LanguageModel
	shutdown  func()
	svc       llm.ServiceConfig
	fallbacks []llm.ServiceConfig

	// leases counts requests currently using the model. retired entries are no
	// longer handed out and shut down when their last lease is released.
	leases  int
	retired bool

	shutdownOnce sync.Once
}

// shutdownNow releases the underlying client at most once, so a plugin
// deactivation racing a lease release cannot shut the same client down twice.
func (e *serviceLLMEntry) shutdownNow() {
	e.shutdownOnce.Do(func() {
		if e.shutdown != nil {
			e.shutdown()
		}
	})
}

// AcquireServiceLLM returns a language model for direct (agent-less) calls
// against svc, building it on first use and caching it by service ID.
//
// Models are cached rather than built per request because each Bifrost client
// starts a worker pool (1,000 goroutines and a 5,000-item queue per registered
// provider by default). The caller must invoke release exactly once when the
// request is done; the model may be shut down immediately afterwards if the
// configuration changed in the meantime.
func (b *MMBots) AcquireServiceLLM(svc llm.ServiceConfig, fallbacks []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
	if entry, ok := b.leaseCachedServiceLLM(svc, fallbacks); ok {
		return entry.model, b.releaseFunc(entry), nil
	}

	// Serialize builds per service ID so a burst of first requests creates one
	// client, not one per request. The global registry lock is never held
	// across a build.
	buildMu := b.serviceLLMBuildMutex(svc.ID)
	buildMu.Lock()
	defer buildMu.Unlock()

	// Another builder may have populated the cache while we waited.
	if entry, ok := b.leaseCachedServiceLLM(svc, fallbacks); ok {
		return entry.model, b.releaseFunc(entry), nil
	}

	model, shutdown, err := b.buildServiceLLM(svc, fallbacks)
	if err != nil {
		// Build failures are never cached: the next request retries, which
		// matters when the failure is transient or the admin just fixed the
		// configuration.
		return nil, nil, err
	}

	entry := &serviceLLMEntry{
		model:     model,
		shutdown:  shutdown,
		svc:       svc,
		fallbacks: fallbacks,
		leases:    1,
	}

	// Only publish the entry when it still matches the live configuration. The
	// request may have carried a snapshot that was already stale when the build
	// started, and a generation counter sampled at build start cannot see a
	// change that lands mid-build. Reading the live configuration after the
	// build is sufficient because the configuration is stored before update
	// listeners run: either this read sees the new value and refuses to
	// publish, or it precedes the store and the listener's reconciliation
	// happens after the entry is published.
	if b.serviceLLMMatchesCurrentConfig(svc, fallbacks) {
		var supersededEntry *serviceLLMEntry

		b.serviceLLMMu.Lock()
		if existing, ok := b.serviceLLMs[svc.ID]; ok {
			if reflect.DeepEqual(existing.svc, svc) && serviceConfigSlicesEqual(existing.fallbacks, fallbacks) {
				// Lost a race against another writer for the same
				// configuration; keep the published entry and discard ours.
				existing.leases++
				b.serviceLLMMu.Unlock()
				entry.shutdownNow()
				return existing.model, b.releaseFunc(existing), nil
			}
			// The cached entry was built from a configuration that is no longer
			// current, so it must not stay in the cache.
			existing.retired = true
			if existing.leases > 0 {
				b.retiredServiceLLMs = append(b.retiredServiceLLMs, existing)
			} else {
				supersededEntry = existing
			}
		}
		if b.serviceLLMs == nil {
			b.serviceLLMs = make(map[string]*serviceLLMEntry)
		}
		b.serviceLLMs[svc.ID] = entry
		b.serviceLLMMu.Unlock()

		if supersededEntry != nil {
			supersededEntry.shutdownNow()
		}
		return entry.model, b.releaseFunc(entry), nil
	}

	// Stale snapshot: serve this request from the uncached model and shut it
	// down when the lease is released.
	entry.retired = true
	b.serviceLLMMu.Lock()
	b.retiredServiceLLMs = append(b.retiredServiceLLMs, entry)
	b.serviceLLMMu.Unlock()

	return entry.model, b.releaseFunc(entry), nil
}

// ReconcileServiceLLMs drops cached models whose service or fallback chain
// changed or disappeared. Entries still in use are retired and shut down when
// their last lease is released.
func (b *MMBots) ReconcileServiceLLMs(services []llm.ServiceConfig) {
	byID := make(map[string]llm.ServiceConfig, len(services))
	for _, svc := range services {
		if _, exists := byID[svc.ID]; !exists {
			byID[svc.ID] = svc
		}
	}
	lookup := func(id string) (llm.ServiceConfig, bool) {
		svc, ok := byID[id]
		return svc, ok
	}

	var toShutdown []*serviceLLMEntry

	b.serviceLLMMu.Lock()
	for id, entry := range b.serviceLLMs {
		svc, ok := byID[id]
		if ok && reflect.DeepEqual(svc, entry.svc) {
			// A fallback chain resolution error means the chain is no longer
			// usable, which is itself a change.
			fallbacks, err := llm.ResolveFallbackChain(id, lookup)
			if err == nil && serviceConfigSlicesEqual(fallbacks, entry.fallbacks) {
				continue
			}
		}

		delete(b.serviceLLMs, id)
		entry.retired = true
		if entry.leases <= 0 {
			toShutdown = append(toShutdown, entry)
			continue
		}
		b.retiredServiceLLMs = append(b.retiredServiceLLMs, entry)
	}
	b.serviceLLMMu.Unlock()

	for _, entry := range toShutdown {
		entry.shutdownNow()
	}
}

// ShutdownServiceLLMs shuts down every service model, cached or retired,
// regardless of outstanding leases. Called on plugin deactivation.
func (b *MMBots) ShutdownServiceLLMs() {
	b.serviceLLMMu.Lock()
	entries := make([]*serviceLLMEntry, 0, len(b.serviceLLMs)+len(b.retiredServiceLLMs))
	for id, entry := range b.serviceLLMs {
		entry.retired = true
		entries = append(entries, entry)
		delete(b.serviceLLMs, id)
	}
	entries = append(entries, b.retiredServiceLLMs...)
	b.retiredServiceLLMs = nil
	b.serviceLLMMu.Unlock()

	for _, entry := range entries {
		entry.shutdownNow()
	}
}

// leaseCachedServiceLLM takes a lease on the cached entry for svc when it was
// built from exactly this configuration.
func (b *MMBots) leaseCachedServiceLLM(svc llm.ServiceConfig, fallbacks []llm.ServiceConfig) (*serviceLLMEntry, bool) {
	b.serviceLLMMu.Lock()
	defer b.serviceLLMMu.Unlock()

	entry, ok := b.serviceLLMs[svc.ID]
	if !ok || entry.retired {
		return nil, false
	}
	if !reflect.DeepEqual(entry.svc, svc) || !serviceConfigSlicesEqual(entry.fallbacks, fallbacks) {
		return nil, false
	}

	entry.leases++
	return entry, true
}

// releaseFunc returns the one-shot lease release for entry. A retired entry
// shuts down once its last lease goes away.
func (b *MMBots) releaseFunc(entry *serviceLLMEntry) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			shutdown := false

			b.serviceLLMMu.Lock()
			entry.leases--
			if entry.retired && entry.leases <= 0 {
				shutdown = true
				b.retiredServiceLLMs = removeServiceLLMEntry(b.retiredServiceLLMs, entry)
			}
			b.serviceLLMMu.Unlock()

			if shutdown {
				entry.shutdownNow()
			}
		})
	}
}

// serviceLLMBuildMutex returns the per-service-ID build lock, creating it on
// first use.
func (b *MMBots) serviceLLMBuildMutex(serviceID string) *sync.Mutex {
	b.serviceLLMMu.Lock()
	defer b.serviceLLMMu.Unlock()

	if b.serviceLLMBuildMutexes == nil {
		b.serviceLLMBuildMutexes = make(map[string]*sync.Mutex)
	}
	mutex, ok := b.serviceLLMBuildMutexes[serviceID]
	if !ok {
		mutex = &sync.Mutex{}
		b.serviceLLMBuildMutexes[serviceID] = mutex
	}
	return mutex
}

// serviceLLMMatchesCurrentConfig reports whether the configuration a model was
// built from is still the live one.
func (b *MMBots) serviceLLMMatchesCurrentConfig(svc llm.ServiceConfig, fallbacks []llm.ServiceConfig) bool {
	if b.config == nil {
		return false
	}

	current, ok := b.config.GetServiceByID(svc.ID)
	if !ok || !reflect.DeepEqual(current, svc) {
		return false
	}

	currentFallbacks, err := llm.ResolveFallbackChain(svc.ID, b.config.GetServiceByID)
	if err != nil {
		return false
	}
	return serviceConfigSlicesEqual(currentFallbacks, fallbacks)
}

// buildServiceLLM constructs a service-backed model, honoring the test seam
// when one is installed.
func (b *MMBots) buildServiceLLM(svc llm.ServiceConfig, fallbacks []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
	if b.serviceLLMBuilderForTest != nil {
		return b.serviceLLMBuilderForTest(svc, fallbacks)
	}
	return b.buildLLM(svc, nil, fallbacks, serviceTokenUsageIdentity(svc))
}

// serviceConfigSlicesEqual compares two fallback chains, treating nil and empty
// as the same "no fallbacks".
func serviceConfigSlicesEqual(a, b []llm.ServiceConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func removeServiceLLMEntry(entries []*serviceLLMEntry, target *serviceLLMEntry) []*serviceLLMEntry {
	for i, entry := range entries {
		if entry == target {
			return append(entries[:i], entries[i+1:]...)
		}
	}
	return entries
}
