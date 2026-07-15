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

const (
	// reconcileQueueSize bounds the background queue. When it is full new
	// requests are dropped: self-healing is best-effort and a later decision
	// on the same resource re-enqueues it.
	reconcileQueueSize = 256

	// defaultReconcileCooldown is the minimum interval between two background
	// reconciliations of the same resource.
	defaultReconcileCooldown = time.Minute

	// reconcileLastRunHighWater triggers pruning of expired cooldown entries
	// so the map cannot grow unboundedly with resource churn.
	reconcileLastRunHighWater = 1024
)

type reconcileRequest struct {
	resourceType string
	resourceID   string
	outcome      Outcome
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
func (r *reconciler) enqueue(resourceType, resourceID string, outcome Outcome) {
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
	case r.ch <- reconcileRequest{resourceType: resourceType, resourceID: resourceID, outcome: outcome}:
		r.enqueued.Add(1)
	default:
		r.mu.Lock()
		delete(r.pending, key)
		r.mu.Unlock()
		r.dropped.Add(1)
	}
}

// run processes queued requests until stopped. work is
// Checker.reconcileIndex in production.
func (r *reconciler) run(work func(resourceType, resourceID string, outcome Outcome)) {
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return
		case req := <-r.ch:
			key := req.resourceType + "/" + req.resourceID
			r.mu.Lock()
			delete(r.pending, key)
			r.lastRun[key] = time.Now()
			if len(r.lastRun) > reconcileLastRunHighWater {
				for k, at := range r.lastRun {
					if time.Since(at) >= r.cooldown {
						delete(r.lastRun, k)
					}
				}
			}
			r.mu.Unlock()

			work(req.resourceType, req.resourceID, req.outcome)
			r.processed.Add(1)
		}
	}
}

// close stops the worker and waits for the in-flight request (if any) to
// finish. Idempotent.
func (r *reconciler) close() {
	r.stopOnce.Do(func() { close(r.stop) })
	<-r.done
}
