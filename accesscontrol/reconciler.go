// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"sync"
	"sync/atomic"
	"time"
)

// Decision-time index self-healing runs in a single background worker so the
// authorization hot path never blocks on the KV-backed policy index for
// definitive outcomes (allow/deny/no_policy). Synchronous, authoritative
// index reads remain only on the unavailable/error fail-closed paths
// (Checker.indexGated). Requests are deduplicated while queued and throttled
// with a per-resource cooldown, so a burst of decisions against one resource
// costs at most one background reconciliation per cooldown window.
//
// A request carries only the resource identity, never the decision outcome:
// the worker reconciles against fresh server truth at processing time, so
// deduplication can never suppress a newer truth behind a stale queued
// outcome.

const (
	// reconcileQueueSize bounds the background queue. When it is full new
	// requests are dropped: self-healing is best-effort and a later decision
	// on the same resource re-enqueues it.
	reconcileQueueSize = 256

	// defaultReconcileCooldown is the minimum interval between two background
	// reconciliations of the same resource.
	defaultReconcileCooldown = time.Minute

	// cooldownPruneInterval is how often the worker sweeps expired entries
	// out of the cooldown map, keeping it bounded under resource churn
	// without per-request full scans.
	cooldownPruneInterval = time.Minute
)

type reconcileRequest struct {
	resourceType string
	resourceID   string
}

// reconciler is the deduplicated, rate-limited background reconciliation
// worker. Its lifecycle is owned by the Checker: started in New, stopped by
// Checker.Close.
type reconciler struct {
	ch       chan reconcileRequest
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}

	cooldown time.Duration

	mu      sync.Mutex
	pending map[string]struct{}
	lastRun map[string]time.Time

	// enqueued/dropped/processed satisfy enqueued == processed once the
	// worker drains, which tests use to wait deterministically.
	enqueued  atomic.Int64
	dropped   atomic.Int64
	processed atomic.Int64
}

func newReconciler(cooldown time.Duration) *reconciler {
	return &reconciler{
		ch:       make(chan reconcileRequest, reconcileQueueSize),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		cooldown: cooldown,
		pending:  map[string]struct{}{},
		lastRun:  map[string]time.Time{},
	}
}

// enqueue is called from the decision hot path. It never blocks and never
// touches the index: in-memory bookkeeping plus a buffered channel send.
func (r *reconciler) enqueue(resourceType, resourceID string) {
	key := resourceType + "/" + resourceID

	r.mu.Lock()
	if _, queued := r.pending[key]; queued {
		r.mu.Unlock()
		r.dropped.Add(1)
		return
	}
	if last, ok := r.lastRun[key]; ok && time.Since(last) < r.cooldown {
		r.mu.Unlock()
		r.dropped.Add(1)
		return
	}
	r.pending[key] = struct{}{}
	r.mu.Unlock()

	select {
	case r.ch <- reconcileRequest{resourceType: resourceType, resourceID: resourceID}:
		r.enqueued.Add(1)
	default:
		r.mu.Lock()
		delete(r.pending, key)
		r.mu.Unlock()
		r.dropped.Add(1)
	}
}

// run processes queued requests until stopped. work is Checker.reconcileIndex
// in production; its return value reports confirmed convergence. The
// per-resource cooldown starts only after confirmed convergence, so a failed
// or inconclusive reconciliation never suppresses the next attempt.
func (r *reconciler) run(work func(resourceType, resourceID string) bool) {
	defer close(r.done)
	prune := time.NewTicker(cooldownPruneInterval)
	defer prune.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-prune.C:
			r.pruneExpiredCooldowns()
		case req := <-r.ch:
			// The pending entry survives until the work is done (requests
			// arriving mid-reconcile stay deduplicated) and is cleared in
			// the same critical section that starts the cooldown — which
			// starts only on confirmed convergence, so a failed or
			// inconclusive reconciliation never suppresses the retry.
			converged := work(req.resourceType, req.resourceID)
			key := req.resourceType + "/" + req.resourceID
			r.mu.Lock()
			delete(r.pending, key)
			if converged {
				r.lastRun[key] = time.Now()
			}
			r.mu.Unlock()
			r.processed.Add(1)
		}
	}
}

// pruneExpiredCooldowns drops cooldown entries that can no longer throttle
// anything. Runs on the worker's ticker so the map stays bounded by the
// number of resources touched within one cooldown window.
func (r *reconciler) pruneExpiredCooldowns() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, at := range r.lastRun {
		if time.Since(at) >= r.cooldown {
			delete(r.lastRun, key)
		}
	}
}

// close stops the worker and waits for the in-flight request (if any) to
// finish. Idempotent.
func (r *reconciler) close() {
	r.stopOnce.Do(func() { close(r.stop) })
	<-r.done
}
