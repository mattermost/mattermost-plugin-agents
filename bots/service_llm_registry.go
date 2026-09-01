// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"maps"
	"reflect"
	"slices"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// serviceLLMEntry is one lazily built service-backed language model plus the
// configuration it was built from.
type serviceLLMEntry struct {
	model     llm.LanguageModel
	shutdown  func()
	svc       llm.ServiceConfig
	fallbacks []llm.ServiceConfig

	// inUse counts the requests currently holding the model. Retired entries
	// shut down once it drains.
	inUse sync.WaitGroup

	shutdownOnce sync.Once
}

// shutdownNow releases the underlying client at most once, so a plugin
// deactivation racing a retirement cannot shut the same client down twice.
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
// provider by default). The caller must invoke release when the request is
// done; the model may be shut down shortly afterwards if the configuration
// changed in the meantime.
func (b *MMBots) AcquireServiceLLM(svc llm.ServiceConfig, fallbacks []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
	if entry, ok := b.leaseCachedServiceLLM(svc, fallbacks); ok {
		return entry.model, releaseLease(entry), nil
	}

	// Serialize builds so a burst of first requests creates one client, not one
	// per request. The registry lock is never held across a build.
	b.serviceLLMBuildMu.Lock()
	defer b.serviceLLMBuildMu.Unlock()

	// Another builder may have populated the cache while we waited.
	if entry, ok := b.leaseCachedServiceLLM(svc, fallbacks); ok {
		return entry.model, releaseLease(entry), nil
	}

	model, shutdown, err := b.buildLLM(svc, nil, fallbacks)
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
	}
	entry.inUse.Add(1)

	// The entry is published unconditionally, even if the caller's snapshot went
	// stale while the build ran: such an entry is evicted by the next
	// reconciliation, or superseded by the next request whose snapshot differs.
	// No request is ever served a stale model, because leaseCachedServiceLLM
	// only hands out an entry whose service and fallback chain equal the ones
	// the caller resolved.
	b.serviceLLMMu.Lock()
	defer b.serviceLLMMu.Unlock()

	if existing, ok := b.serviceLLMs[svc.ID]; ok {
		// Only a configuration this request did not resolve can still be
		// cached here — an equal one would have been leased above — so it must
		// not stay in the cache.
		b.retire(existing)
	}
	if b.serviceLLMs == nil {
		b.serviceLLMs = make(map[string]*serviceLLMEntry)
	}
	b.serviceLLMs[svc.ID] = entry
	return entry.model, releaseLease(entry), nil
}

// ReconcileServiceLLMs drops cached models whose service or fallback chain
// changed or disappeared. Entries still in use are retired and shut down once
// their last lease is released.
func (b *MMBots) ReconcileServiceLLMs(services []llm.ServiceConfig) {
	lookup := llm.ServiceLookup(services)

	b.serviceLLMMu.Lock()
	defer b.serviceLLMMu.Unlock()

	for id, entry := range b.serviceLLMs {
		svc, ok := lookup(id)
		if ok && reflect.DeepEqual(svc, entry.svc) {
			// A fallback chain resolution error means the chain is no longer
			// usable, which is itself a change.
			fallbacks, err := llm.ResolveFallbackChain(id, lookup)
			if err == nil && serviceConfigSlicesEqual(fallbacks, entry.fallbacks) {
				continue
			}
		}

		delete(b.serviceLLMs, id)
		b.retire(entry)
	}
}

// ShutdownServiceLLMs shuts every service model down, cached or retired,
// regardless of outstanding leases. Called on plugin deactivation.
func (b *MMBots) ShutdownServiceLLMs() {
	b.serviceLLMMu.Lock()
	entries := slices.Collect(maps.Values(b.serviceLLMs))
	entries = append(entries, slices.Collect(maps.Keys(b.retiredServiceLLMs))...)
	clear(b.serviceLLMs)
	clear(b.retiredServiceLLMs)
	b.serviceLLMMu.Unlock()

	// Retired entries may still have a goroutine waiting for their leases to
	// drain; shutdownOnce makes its later call a no-op.
	for _, entry := range entries {
		entry.shutdownNow()
	}
}

// retire stops handing entry out and shuts it down once its leases drain. It
// must be called with serviceLLMMu held and after entry is no longer reachable
// from b.serviceLLMs, so that no new lease can be taken: that is what makes
// inUse.Add and inUse.Wait mutually exclusive, and the "Add after Wait" hazard
// impossible.
func (b *MMBots) retire(entry *serviceLLMEntry) {
	if b.retiredServiceLLMs == nil {
		b.retiredServiceLLMs = make(map[*serviceLLMEntry]struct{})
	}
	b.retiredServiceLLMs[entry] = struct{}{}

	go func() {
		entry.inUse.Wait()
		entry.shutdownNow()

		b.serviceLLMMu.Lock()
		delete(b.retiredServiceLLMs, entry)
		b.serviceLLMMu.Unlock()
	}()
}

// leaseCachedServiceLLM takes a lease on the cached entry for svc when it was
// built from exactly this configuration.
func (b *MMBots) leaseCachedServiceLLM(svc llm.ServiceConfig, fallbacks []llm.ServiceConfig) (*serviceLLMEntry, bool) {
	b.serviceLLMMu.Lock()
	defer b.serviceLLMMu.Unlock()

	entry, ok := b.serviceLLMs[svc.ID]
	if !ok {
		return nil, false
	}
	if !reflect.DeepEqual(entry.svc, svc) || !serviceConfigSlicesEqual(entry.fallbacks, fallbacks) {
		return nil, false
	}

	entry.inUse.Add(1)
	return entry, true
}

// releaseLease returns the one-shot release for a lease already taken on entry.
// Extra calls are ignored so a caller that releases twice cannot drive the
// lease count negative.
func releaseLease(entry *serviceLLMEntry) func() {
	return sync.OnceFunc(entry.inUse.Done)
}

// serviceConfigSlicesEqual compares two fallback chains, treating nil and empty
// as the same "no fallbacks".
func serviceConfigSlicesEqual(a, b []llm.ServiceConfig) bool {
	return slices.EqualFunc(a, b, func(x, y llm.ServiceConfig) bool {
		return reflect.DeepEqual(x, y)
	})
}
