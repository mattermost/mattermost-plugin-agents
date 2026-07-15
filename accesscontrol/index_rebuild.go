// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
)

// rebuildConcurrency bounds the parallel GetAccessControlPolicy fetches of
// the truth-gathering phase.
const rebuildConcurrency = 4

type rebuildRef struct {
	resourceType string
	resourceID   string
}

type rebuildTruth struct {
	wantMarker bool
	readable   bool
}

// RebuildIndex reconciles the fail-closed policy index against server truth
// for every provided resource ID. Run on plugin activation (best-effort,
// non-fatal) so index divergence caused by a lost mutex lease (see
// Checker.reconcileIndex) never survives a restart.
//
// Server truth is fetched OUTSIDE the cluster mutex with bounded concurrency;
// the mutex is taken only per divergent resource, re-confirming the
// divergence under the lock before writing, so the global PAP lease is never
// held across the whole sweep. ctx must be a plugin-lifecycle context:
// cancellation (deactivation) abandons the remaining work, and because the
// context is re-checked immediately before every index write
// (healDivergenceUnderLock), no index mutation happens after cancellation —
// even for a straggler that outlived the deactivation join timeout. Only
// in-flight policy reads may briefly outlive shutdown.
//
// resourceIDsByType maps ResourceTypeAgent/Service/MCP to all resource IDs
// known to the plugin at the time of the call. IDs that are not
// policy-addressable (non-26-char config bot IDs) are skipped. Markers for
// resource IDs no longer enumerated (deleted resources) are left alone: they
// can only fail closed for resources that no longer resolve anyway.
func (c *Checker) RebuildIndex(ctx context.Context, resourceIDsByType map[string][]string) {
	_, span := telemetry.Tracer().Start(ctx, "abac rebuild_index")
	defer span.End()

	if c.papi == nil {
		return
	}

	var refs []rebuildRef
	for _, resourceType := range []string{ResourceTypeAgent, ResourceTypeService, ResourceTypeMCP} {
		for _, resourceID := range resourceIDsByType[resourceType] {
			if model.IsValidId(resourceID) {
				refs = append(refs, rebuildRef{resourceType: resourceType, resourceID: resourceID})
			}
		}
	}

	// Phase 1: gather server truth outside the mutex, bounded concurrency.
	// Semaphore acquisition selects on ctx so a deactivation never leaves
	// this goroutine parked on a slot; the caller joins on the whole rebuild
	// (see server/main.go), so no fetch can outlive the plugin.
	truths := make([]rebuildTruth, len(refs))
	var failed atomic.Int64
	sem := make(chan struct{}, rebuildConcurrency)
	var wg sync.WaitGroup
gather:
	for i := range refs {
		// Checked before the select too: with the context already done both
		// select cases are ready and the pick would be random.
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			break gather
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			ref := refs[i]
			_, appErr := c.papi.GetAccessControlPolicy(ref.resourceID)
			switch {
			case appErr == nil:
				truths[i] = rebuildTruth{wantMarker: true, readable: true}
			case isNotFoundAppErr(appErr):
				truths[i] = rebuildTruth{wantMarker: false, readable: true}
			default:
				// Debug, not Warn: on servers without ABAC every Get can fail
				// and per-resource warnings would spam each activation.
				failed.Add(1)
				logDebug(c.log, "ABAC index rebuild could not read the policy for a resource; leaving its marker unchanged",
					"resource_type", ref.resourceType, "resource_id", ref.resourceID, "error", appErr.Error())
			}
		}(i)
	}
	wg.Wait()

	// Phase 2: heal divergences. The cheap divergence check runs without the
	// mutex against phase-one truth; only an apparently divergent resource
	// takes the lock, where BOTH the index state and the server truth are
	// re-derived fresh (a policy mutated between the phases must never be
	// healed from the stale phase-one snapshot, in either direction).
	var healed int64
	for i, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		if !truths[i].readable {
			continue
		}
		want := truths[i].wantMarker
		has, err := c.index.Has(ref.resourceType, ref.resourceID)
		if err != nil {
			failed.Add(1)
			logWarn(c.log, "ABAC index rebuild could not read the policy index",
				"resource_type", ref.resourceType, "resource_id", ref.resourceID, "error", err.Error())
			continue
		}
		if has == want {
			continue
		}
		if c.healDivergenceUnderLock(ctx, ref) {
			healed++
		} else {
			failed.Add(1)
		}
	}

	if ctx.Err() != nil {
		logInfo(c.log, "ABAC policy index rebuild abandoned: plugin is shutting down",
			"scanned", len(refs), "healed", healed, "failed", failed.Load())
		return
	}
	if healed > 0 || failed.Load() > 0 {
		logWarn(c.log, "ABAC policy index rebuild finished",
			"scanned", len(refs), "healed", healed, "failed", failed.Load())
		return
	}
	logInfo(c.log, "ABAC policy index rebuild finished: no divergences",
		"scanned", len(refs), "healed", 0, "failed", 0)
}

// healDivergenceUnderLock heals one apparently divergent marker under the
// mutation mutex, re-deriving BOTH sides of the comparison under the lock:
// the index state (a concurrent PAP mutation may have already corrected it)
// and the server truth via a fresh Get. Re-confirming the truth matters in
// both directions — a pre-lock 404 may predate a save that committed in the
// meantime (dropping the marker would fail open), and a pre-lock hit may
// predate a delete (re-adding the marker would fail closed for a resource
// with no policy). Reports true when the resource is confirmed converged
// (no divergence remained, or the heal write succeeded); false when the
// truth could not be re-read or the write failed.
//
// ctx is the governing lifecycle context and is re-checked immediately
// before the index mutation: the guarantee is that NO index mutation happens
// after cancellation — a straggler that outlived a deactivation join timeout
// (blocked on the mutex or mid-Get) degrades to a no-op. Only its in-flight
// reads may briefly outlive shutdown.
func (c *Checker) healDivergenceUnderLock(ctx context.Context, ref rebuildRef) bool {
	c.mutationMu.Lock()
	defer c.mutationMu.Unlock()

	has, err := c.index.Has(ref.resourceType, ref.resourceID)
	if err != nil {
		logWarn(c.log, "ABAC policy index heal could not re-read the policy index under the lock",
			"resource_type", ref.resourceType, "resource_id", ref.resourceID, "error", err.Error())
		return false
	}
	want, readable := c.policyMarkerTruth(ref.resourceID)
	if !readable {
		logDebug(c.log, "ABAC policy index heal could not re-read the policy under the lock; leaving the marker unchanged",
			"resource_type", ref.resourceType, "resource_id", ref.resourceID)
		return false
	}
	if has == want {
		return true
	}

	// Last gate before mutating: a context canceled between the truth fetch
	// and the write (plugin shutdown) must make the heal a no-op.
	if ctx.Err() != nil {
		logDebug(c.log, "ABAC policy index heal abandoned before its write: lifecycle context canceled",
			"resource_type", ref.resourceType, "resource_id", ref.resourceID)
		return false
	}

	if want {
		if err := c.index.Add(ref.resourceType, ref.resourceID); err != nil {
			logWarn(c.log, "ABAC policy index heal failed to restore a marker",
				"resource_type", ref.resourceType, "resource_id", ref.resourceID, "error", err.Error())
			return false
		}
		logWarn(c.log, "ABAC policy index self-healed: restored a missing fail-closed marker for an enforced policy",
			"resource_type", ref.resourceType, "resource_id", ref.resourceID)
		return true
	}

	if err := c.index.Remove(ref.resourceType, ref.resourceID); err != nil {
		logWarn(c.log, "ABAC policy index heal failed to drop a stale marker",
			"resource_type", ref.resourceType, "resource_id", ref.resourceID, "error", err.Error())
		return false
	}
	logWarn(c.log, "ABAC policy index self-healed: dropped a stale fail-closed marker with no stored policy",
		"resource_type", ref.resourceType, "resource_id", ref.resourceID)
	return true
}
