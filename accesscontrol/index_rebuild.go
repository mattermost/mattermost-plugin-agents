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
// held across the whole sweep. ctx should be a plugin-lifecycle context:
// cancellation (deactivation) abandons the remaining work cleanly.
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
	truths := make([]rebuildTruth, len(refs))
	var failed atomic.Int64
	sem := make(chan struct{}, rebuildConcurrency)
	var wg sync.WaitGroup
	for i := range refs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
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
	// mutex; only a divergent resource takes the lock, and the divergence is
	// re-confirmed under it (a concurrent PAP mutation may have already
	// corrected the marker, and dropping a marker is additionally re-gated on
	// fresh server truth, mirroring reconcileIndex).
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
		if c.healDivergenceUnderLock(ref, want) {
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

// healDivergenceUnderLock re-confirms and heals one divergent marker under
// the mutation mutex. Reports true when the marker was changed; false when
// the write failed. A divergence that disappeared under the lock (concurrent
// PAP mutation, or fresher server truth for a removal) is reported as true
// without a write: it is no longer divergent.
func (c *Checker) healDivergenceUnderLock(ref rebuildRef, wantMarker bool) bool {
	c.mutationMu.Lock()
	defer c.mutationMu.Unlock()

	has, err := c.index.Has(ref.resourceType, ref.resourceID)
	if err != nil {
		logWarn(c.log, "ABAC index rebuild could not re-read the policy index under the lock",
			"resource_type", ref.resourceType, "resource_id", ref.resourceID, "error", err.Error())
		return false
	}
	if has == wantMarker {
		return true
	}

	if wantMarker {
		// Adding is the fail-closed direction and needs no re-confirmation.
		if err := c.index.Add(ref.resourceType, ref.resourceID); err != nil {
			logWarn(c.log, "ABAC index rebuild failed to restore a marker",
				"resource_type", ref.resourceType, "resource_id", ref.resourceID, "error", err.Error())
			return false
		}
		return true
	}

	// Dropping a marker fails open, so re-confirm against fresh server truth
	// under the lock: the pre-lock 404 may predate a save that committed in
	// the meantime.
	if _, appErr := c.papi.GetAccessControlPolicy(ref.resourceID); !isNotFoundAppErr(appErr) {
		return true
	}
	if err := c.index.Remove(ref.resourceType, ref.resourceID); err != nil {
		logWarn(c.log, "ABAC index rebuild failed to drop a stale marker",
			"resource_type", ref.resourceType, "resource_id", ref.resourceID, "error", err.Error())
		return false
	}
	return true
}
