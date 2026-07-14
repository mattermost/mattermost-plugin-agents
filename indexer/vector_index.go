// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
)

// VectorIndexStateKey is the KV key tracking the ANN index lifecycle during a
// deferred reindex. The key is absent when the index is present/normal.
const VectorIndexStateKey = "indexer_vector_index_state"

// Vector index phases while the state key exists.
const (
	// VectorIndexPhaseDropped: the ANN index was dropped for a bulk load.
	VectorIndexPhaseDropped = "dropped"
	// VectorIndexPhaseBuilding: the post-load index build is running.
	VectorIndexPhaseBuilding = "building"
	// VectorIndexPhaseRepairing: the index is valid again, but posts edited
	// while live indexing was gated (the building phase) still carry stale
	// embeddings and are pending re-indexing. Search works in this phase;
	// slightly stale rows are the maintain-mode norm.
	VectorIndexPhaseRepairing = "repairing"
)

// clearStateRetries bounds retries of the state-clearing CAS on transient KV
// errors; a persistent failure keeps the marker in place so it must surface.
const clearStateRetries = 3

// VectorIndexState is the durable record of a deferred reindex owning the
// ANN index lifecycle. The Postgres catalog is authoritative on conflict.
type VectorIndexState struct {
	JobID string `json:"job_id"` // owning job
	Phase string `json:"phase"`  // VectorIndexPhase* value
	// BuildStartedAt (Unix millis) records when the first build attempt began
	// (live indexing is gated from that point). It is preserved across failed
	// attempts and resume adoptions so the repair of posts edited while gated
	// covers the whole window; a fresh full reindex resets it (everything is
	// re-embedded anyway).
	BuildStartedAt int64 `json:"build_started_at,omitempty"`
}

// deferredRun carries a run's ownership of the vector index lifecycle. state
// is the exact value last written by a successful CAS — that CAS is the
// ownership proof (KV reads may hit a lagging replica in HA, CAS goes to
// master), so the state is threaded forward in memory and never re-read for
// ownership checks.
type deferredRun struct {
	state VectorIndexState
	// adopted reports the claim was inherited from a previous run, so the
	// index may already be genuinely dropped before this run touches it.
	adopted bool
	// convertedFrom is the original repairing marker a fresh full reindex
	// atomically replaced with its dropped claim. The index is still valid
	// at that point, so early pre-DDL exits must restore the marker (with
	// the original BuildStartedAt, which the conversion reset to 0) instead
	// of leaving a bogus dropped state that gates search over a valid index.
	convertedFrom *VectorIndexState
}

// deferredIndexGated reports whether a phase makes the ANN index unusable:
// dropped and building gate search and the constructor index creation.
// repairing does not — the index is valid, only some rows are stale. Unknown
// phases gate conservatively.
func deferredIndexGated(phase string) bool {
	return phase != "" && phase != VectorIndexPhaseRepairing
}

// DeferredIndexRebuildActive reports whether a deferred reindex currently
// has the ANN index dropped or being rebuilt. Used by InitEmbeddingsSearch
// callers to skip constructor index creation, and by search gating.
// Unexpected KV errors fail closed (treated as active): a wrongly-skipped
// constructor index creation is repaired by reconciliation, whereas a
// wrongly-run one can synchronously rebuild a huge index that was dropped on
// purpose.
//
// This gate is best-effort across nodes: the KV read may hit a lagging
// replica for a few seconds after a phase transition. That window is
// accepted — the strict fencing that matters (who may run index DDL) is
// enforced with compare-and-set through the master, not with this read.
func DeferredIndexRebuildActive(client mmapi.Client) bool {
	if client == nil {
		return false
	}
	var state VectorIndexState
	if err := client.KVGet(VectorIndexStateKey, &state); err != nil {
		if mmapi.IsKVNotFound(err) {
			return false
		}
		client.LogError("Failed to read vector index state; treating a deferred rebuild as active", "error", err)
		return true
	}
	return deferredIndexGated(state.Phase)
}

// bulkIndexerFor resolves the bulk index control from a search snapshot,
// either through the BulkIndexerProvider accessor or a direct implementation.
// Returns nil when the search/store does not support deferred indexing.
func bulkIndexerFor(search embeddings.EmbeddingSearch) embeddings.BulkIndexer {
	if search == nil {
		return nil
	}
	if provider, ok := search.(embeddings.BulkIndexerProvider); ok {
		return provider.BulkIndexer()
	}
	if bulk, ok := search.(embeddings.BulkIndexer); ok {
		return bulk
	}
	return nil
}

// loadVectorIndexState reads the phase state, returning nil when absent.
func (s *Indexer) loadVectorIndexState() (*VectorIndexState, error) {
	var state VectorIndexState
	err := s.pluginAPI.KVGet(VectorIndexStateKey, &state)
	if err != nil {
		if mmapi.IsKVNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &state, nil
}

// casVectorIndexState atomically replaces the phase state: the write only
// applies when the stored value still equals old (nil old = key must be
// absent; nil new = delete). All mutations go through this so a
// stale-reclaimed worker can never clobber a successor's claim.
func (s *Indexer) casVectorIndexState(old, updated *VectorIndexState) (bool, error) {
	var oldValue, newValue interface{}
	if old != nil {
		oldValue = *old
	}
	if updated != nil {
		newValue = *updated
	}
	return s.pluginAPI.KVCompareAndSet(VectorIndexStateKey, oldValue, newValue)
}

// clearVectorIndexState CAS-deletes the given owned state, retrying transient
// KV errors. A CAS conflict means ownership was lost to another run — the
// state is left alone and an error is returned so the caller surfaces it.
func (s *Indexer) clearVectorIndexState(state VectorIndexState) error {
	var lastErr error
	for attempt := 0; attempt < clearStateRetries; attempt++ {
		ok, err := s.casVectorIndexState(&state, nil)
		if err != nil {
			lastErr = err
			continue
		}
		if !ok {
			return fmt.Errorf("vector index state is no longer owned by this run")
		}
		return nil
	}
	return fmt.Errorf("failed to clear vector index state: %w", lastErr)
}

// deferStrategyUsable reports whether a fresh full reindex should run in
// defer mode: the strategy is configured AND the current vector store can
// actually drop/rebuild the index. Logs a warning when the strategy is set
// but unusable.
func (s *Indexer) deferStrategyUsable() bool {
	if s.configGetter == nil {
		return false
	}
	cfg := s.configGetter()
	if cfg.EffectiveReindexIndexStrategy() != embeddings.ReindexIndexStrategyDefer {
		return false
	}
	if bulkIndexerFor(s.getSearch()) == nil {
		s.pluginAPI.LogWarn("Reindex index strategy is 'defer' but the vector store does not support it; maintaining the index during reindex")
		return false
	}
	return true
}

// resolveDeferredRebuild decides whether this run participates in the
// deferred index lifecycle and claims/adopts the durable phase state
// accordingly. Any leftover state is handled — the invariant is that every
// full or resumed reindex either rebuilds a dropped index or completes a
// pending repair, even if the admin changed the strategy since. Without
// leftover state, fresh full reindexes consult the configured strategy.
// Called under the job-start cluster mutex, before the background job runs.
//
// Returns nil when the run proceeds in plain maintain mode. A non-nil error
// means the state could not be read or a claim/adoption CAS did not apply —
// the caller must fail the job rather than silently running in maintain mode
// with the lifecycle in an unknown state.
func (s *Indexer) resolveDeferredRebuild(clearIndex bool, jobID string) (*deferredRun, error) {
	state, err := s.loadVectorIndexState()
	if err != nil {
		return nil, fmt.Errorf("failed to read vector index state: %w", err)
	}

	if state != nil && clearIndex && state.Phase == VectorIndexPhaseRepairing {
		// A fresh full reindex re-embeds everything, so a pending repair is
		// moot. The leftover marker is resolved in a SINGLE atomic CAS per
		// strategy — a delete-then-create pair could strand the system with
		// neither marker nor claim if it failed in between.
		if s.deferStrategyUsable() {
			newState := VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped, BuildStartedAt: 0}
			ok, casErr := s.casVectorIndexState(state, &newState)
			if casErr != nil {
				return nil, fmt.Errorf("failed to replace leftover vector index repair state: %w", casErr)
			}
			if !ok {
				return nil, fmt.Errorf("vector index state changed while replacing leftover repair state")
			}
			return &deferredRun{state: newState, adopted: true, convertedFrom: state}, nil
		}
		// Maintain-mode fresh run: delete the marker outright, the full
		// re-embed supersedes the pending repair. Accepted residual: if this
		// run then dies before Clear() completes and is never resumed,
		// build-window stale edits persist without a marker — acceptable
		// because the failed job row stays visible and any subsequent full
		// reindex re-embeds everything.
		ok, casErr := s.casVectorIndexState(state, nil)
		if casErr != nil {
			return nil, fmt.Errorf("failed to clear leftover vector index repair state: %w", casErr)
		}
		if !ok {
			return nil, fmt.Errorf("vector index state changed while clearing leftover repair state")
		}
		return nil, nil
	}

	if state != nil {
		if state.Phase == VectorIndexPhaseRepairing {
			// Resume: the index is intact, only the edited-posts repair (and
			// the rest of the run) is outstanding. Adopt the marker; the run
			// must NOT drop the index in this mode.
			newState := VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseRepairing, BuildStartedAt: state.BuildStartedAt}
			ok, casErr := s.casVectorIndexState(state, &newState)
			if casErr != nil {
				return nil, fmt.Errorf("failed to take ownership of vector index repair state: %w", casErr)
			}
			if !ok {
				return nil, fmt.Errorf("vector index state changed while taking ownership")
			}
			return &deferredRun{state: newState, adopted: true}, nil
		}

		// Leftover dropped/building state: a previous deferred run left the
		// index dropped (or its build unfinished). This run must rebuild it.
		if bulkIndexerFor(s.getSearch()) == nil {
			s.pluginAPI.LogError("Vector index was dropped by a previous reindex but the current vector store does not support rebuilding it; semantic search stays disabled",
				"previous_job_id", state.JobID)
			return nil, nil
		}
		newState := VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped, BuildStartedAt: state.BuildStartedAt}
		if clearIndex {
			// A fresh full reindex re-embeds everything up to its cutoff, so
			// the gated-edits window restarts with the new build.
			newState.BuildStartedAt = 0
		}
		ok, casErr := s.casVectorIndexState(state, &newState)
		if casErr != nil {
			return nil, fmt.Errorf("failed to take ownership of vector index state: %w", casErr)
		}
		if !ok {
			return nil, fmt.Errorf("vector index state changed while taking ownership")
		}
		return &deferredRun{state: newState, adopted: true}, nil
	}

	if !clearIndex {
		// Resume or catch-up without leftover state: nothing to defer.
		return nil, nil
	}

	if !s.deferStrategyUsable() {
		return nil, nil
	}
	// Persist the state BEFORE the index is dropped so a crash in between
	// leaves a record; reconciliation clears it if the index still exists. A
	// failed or conflicting claim fails the job start — falling back to
	// maintain mode silently would leave the lifecycle in an unknown state.
	claim := VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped}
	ok, casErr := s.casVectorIndexState(nil, &claim)
	if casErr != nil {
		return nil, fmt.Errorf("failed to persist vector index state: %w", casErr)
	}
	if !ok {
		return nil, fmt.Errorf("vector index state was claimed concurrently")
	}
	return &deferredRun{state: claim, adopted: false}, nil
}

// finalizeDeferredIndex transitions the phase to building, rebuilds the ANN
// index, and transitions to repairing on success (the pending-repair marker
// for posts edited while gated; it is cleared only after the repair pass).
// The build can run for hours, so the DDL runs in a goroutine while this
// method keeps the job heartbeat ticking — otherwise stale-job detection
// would reclaim the job and start a second reindex mid-build. Cancellation
// cannot interrupt in-flight DDL; a pending cancel is acknowledged by the
// caller after the build finishes.
//
// owned is the state as last written by this run's CAS; the dropped→building
// CAS below both fences ownership and engages the IndexPost gate, and it
// MUST succeed before the DDL runs — a blocking CREATE INDEX without the
// gate would pile up hook goroutines on blocked writes for hours. Returns
// the updated in-memory state alongside any error.
func (s *Indexer) finalizeDeferredIndex(ctx context.Context, jobStatus *JobStatus, bulk embeddings.BulkIndexer, owned VectorIndexState) (VectorIndexState, error) {
	buildingState := owned
	buildingState.Phase = VectorIndexPhaseBuilding
	if buildingState.BuildStartedAt == 0 {
		buildingState.BuildStartedAt = time.Now().UnixMilli()
	}
	ok, casErr := s.casVectorIndexState(&owned, &buildingState)
	if casErr != nil {
		return owned, fmt.Errorf("failed to record vector index building phase: %w", casErr)
	}
	if !ok {
		return owned, fmt.Errorf("vector index state is no longer owned by this run; skipping index build")
	}

	// A small TOCTOU window remains between the ownership CAS above and the
	// DDL below: a concurrent claimant could take the state before our DDL
	// reaches Postgres. Fully serializing the DDL would need a Postgres
	// advisory lock, which is out of scope; the CAS fence removes the
	// realistic stale-worker case.
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("vector index build panicked: %v", r)
			}
		}()
		done <- bulk.FinalizeBulkIndex(ctx)
	}()

	heartbeat := time.NewTicker(s.heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case err := <-done:
			if err != nil {
				// Nothing is building anymore: revert to dropped (keeping
				// the owning job and build timestamp) so health checks and
				// reconciliation reflect reality and a resume can take
				// ownership cleanly.
				droppedState := buildingState
				droppedState.Phase = VectorIndexPhaseDropped
				if reverted, revertErr := s.casVectorIndexState(&buildingState, &droppedState); revertErr != nil || !reverted {
					s.pluginAPI.LogError("Failed to revert vector index state after failed build", "error", revertErr)
					return buildingState, err
				}
				return droppedState, err
			}
			// The index is valid again, but edits made during the build are
			// still stale: record the pending repair durably so it can never
			// be silently skipped (a resume adopts it).
			repairingState := buildingState
			repairingState.Phase = VectorIndexPhaseRepairing
			var lastErr error
			for attempt := 0; attempt < clearStateRetries; attempt++ {
				applied, transErr := s.casVectorIndexState(&buildingState, &repairingState)
				if transErr != nil {
					lastErr = transErr
					continue
				}
				if !applied {
					return buildingState, fmt.Errorf("vector index state is no longer owned by this run after the build")
				}
				return repairingState, nil
			}
			return buildingState, fmt.Errorf("vector index was rebuilt but the repair phase could not be recorded: %w", lastErr)
		case <-heartbeat.C:
			// Keep the job fresh; a cancel request is intentionally not
			// acted on here since the DDL cannot be interrupted.
			s.heartbeatTick(jobStatus)
		}
	}
}

// pendingRepairNote is appended to terminal job errors when the run ends
// with the repairing marker still in place.
const pendingRepairNote = "Posts edited during the index rebuild are pending repair; resume the job or run a reindex to re-index them"

// appendPendingRepairNote joins the pending-repair note onto an existing
// error message (or stands alone when there is none).
func appendPendingRepairNote(errMsg string) string {
	if errMsg == "" {
		return pendingRepairNote
	}
	return errMsg + "; " + pendingRepairNote
}

// restoreDeferredIndex attempts to rebuild the ANN index on a failure or
// cancel exit path, before the terminal status is recorded. On a successful
// rebuild the state transitions to repairing (not deleted): the edits gated
// during the build are still pending, and the marker must survive so a
// resume repairs them and the health check shows the condition. It returns
// the (possibly augmented) error message; when the rebuild itself fails the
// phase state stays in place (reverted to dropped) so a resume can fix it.
func (s *Indexer) restoreDeferredIndex(ctx context.Context, jobStatus *JobStatus, bulk embeddings.BulkIndexer, owned VectorIndexState, errMsg string) string {
	_, rebuildErr := s.finalizeDeferredIndex(ctx, jobStatus, bulk, owned)
	if rebuildErr == nil {
		s.pluginAPI.LogWarn("Vector index was rebuilt on the exit path; edits from the build window are pending repair",
			"job_id", jobStatus.JobID)
		return appendPendingRepairNote(errMsg)
	}
	s.pluginAPI.LogError("Failed to rebuild vector index while terminating reindex job", "error", rebuildErr)
	if errMsg == "" {
		return fmt.Sprintf("Failed to rebuild vector index: %s", rebuildErr)
	}
	return fmt.Sprintf("%s; additionally failed to rebuild vector index: %s", errMsg, rebuildErr)
}

// abandonUndroppedClaim releases a claimed phase state on an exit path where
// the ANN index was never actually dropped by this run. A claim converted
// from a leftover repairing marker is restored to repairing: the index is
// still valid (repairing implies a completed build), so keeping the bogus
// dropped state would gate search forever over a working index and silently
// rewrite the pending-repair obligation. The restore keeps this run's JobID
// but brings back the original BuildStartedAt (the conversion reset it to
// 0), so the repair window is intact for a later resume. Genuinely-adopted
// dropped/building states are left in place: they were inherited from a
// previous run, so the index may genuinely be missing and clearing the
// state would lose that marker. Fresh claims are simply deleted.
//
// Returns an error when the release CAS conflicts or fails: the claim was
// superseded (or KV is down) and the state key was NOT touched — callers
// must surface this on the job rather than proceed as if the claim were
// cleanly released.
func (s *Indexer) abandonUndroppedClaim(run *deferredRun) error {
	if run.convertedFrom != nil {
		restored := VectorIndexState{
			JobID:          run.state.JobID,
			Phase:          VectorIndexPhaseRepairing,
			BuildStartedAt: run.convertedFrom.BuildStartedAt,
		}
		ok, err := s.casVectorIndexState(&run.state, &restored)
		if err != nil {
			return fmt.Errorf("failed to restore vector index repair marker: %w", err)
		}
		if !ok {
			return fmt.Errorf("vector index state is no longer owned by this run")
		}
		return nil
	}
	if run.adopted {
		s.pluginAPI.LogError("Reindex job is exiting before completing an inherited vector index lifecycle; the state marker is kept",
			"job_id", run.state.JobID, "phase", run.state.Phase)
		return nil
	}
	if err := s.clearVectorIndexState(run.state); err != nil {
		return fmt.Errorf("failed to clear vector index state: %w", err)
	}
	return nil
}

// ReconcileVectorIndexState checks a leftover phase state against the job
// row and the catalog on activation. A fresh active owning job is checked
// FIRST: between claiming the state and dropping the index there is a window
// where the catalog still shows a valid index, and clearing a live job's
// claim there would let the constructor rebuild the index under the job or
// ungate search mid-drop. A leftover repairing state is always kept — it is
// the pending-repair marker and the index is SUPPOSED to exist in that
// phase, so catalog evidence must not clear it. Only ownerless
// dropped/building leftovers use catalog evidence: a dropped leftover with a
// valid index means the pre-drop crash left nothing to repair, so the state
// is cleared; a building leftover with a valid index means the CREATE
// committed but the building→repairing transition never did, so the state
// converts to repairing (preserving JobID and BuildStartedAt) — the
// gated-window edits still need repair and deleting the marker would lose
// them silently. If the index is genuinely missing and no live job owns the
// state, the index is NOT rebuilt here — a build can take hours and must not
// run inside activation. Search stays gated until an admin starts or resumes
// a reindex, which rebuilds it.
func (s *Indexer) ReconcileVectorIndexState(ctx context.Context) error {
	state, err := s.loadVectorIndexState()
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}

	var jobStatus JobStatus
	jobErr := s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if jobErr == nil && jobStatus.JobID == state.JobID && isActiveJob(&jobStatus) && !s.isJobStale(&jobStatus) {
		// The owning job is still running; it will finish the lifecycle.
		return nil
	}

	if state.Phase == VectorIndexPhaseRepairing {
		s.pluginAPI.LogWarn("Vector index repair is pending from a previous reindex; resume the job or run a reindex to re-index posts edited during the rebuild",
			"job_id", state.JobID)
		return nil
	}

	if bulk := bulkIndexerFor(s.getSearch()); bulk != nil {
		exists, exErr := bulk.VectorIndexExists(ctx)
		if exErr != nil {
			return exErr
		}
		if exists {
			switch state.Phase {
			case VectorIndexPhaseBuilding:
				// The build committed but its completion was never recorded:
				// live indexing was gated during the build, so a repair is
				// still pending. Convert the marker instead of deleting it.
				repairing := *state
				repairing.Phase = VectorIndexPhaseRepairing
				s.pluginAPI.LogWarn("Vector index build completed but the repair of gated-window edits is pending; resume the job or run a reindex to re-index them",
					"job_id", state.JobID)
				if ok, casErr := s.casVectorIndexState(state, &repairing); casErr != nil || !ok {
					s.pluginAPI.LogError("Failed to convert vector index state to repairing", "error", casErr)
				}
			case VectorIndexPhaseDropped:
				// Pre-drop crash: the index was never touched and nothing
				// was gated, so the leftover claim is safe to delete.
				s.pluginAPI.LogWarn("Vector index state says the index is dropped but a valid index exists; clearing stale state",
					"job_id", state.JobID, "phase", state.Phase)
				if ok, casErr := s.casVectorIndexState(state, nil); casErr != nil || !ok {
					s.pluginAPI.LogError("Failed to clear stale vector index state", "error", casErr)
				}
			default:
				// Unknown phase (newer plugin version?): keep it — deleting
				// could discard an obligation this version cannot interpret.
				s.pluginAPI.LogWarn("Vector index state has an unknown phase; leaving it in place",
					"job_id", state.JobID, "phase", state.Phase)
			}
			return nil
		}
	}

	s.pluginAPI.LogError("Vector index was dropped for a deferred reindex but no active job owns it; semantic search is disabled until a reindex is started or resumed",
		"job_id", state.JobID, "phase", state.Phase)
	return nil
}
