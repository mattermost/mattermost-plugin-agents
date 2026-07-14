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
)

// deleteStateRetries bounds the success-path retries when clearing the phase
// state; a persistent failure keeps search gated so it must surface.
const deleteStateRetries = 3

// VectorIndexState is the durable record of a deferred reindex owning the
// ANN index lifecycle. The Postgres catalog is authoritative on conflict.
type VectorIndexState struct {
	JobID string `json:"job_id"` // owning job
	Phase string `json:"phase"`  // VectorIndexPhaseDropped or VectorIndexPhaseBuilding
	// BuildStartedAt (Unix millis) records when the first build attempt began
	// (live indexing is gated from that point). It is preserved across failed
	// attempts and ownership handoffs so the post-build repair of posts
	// edited while gated covers the whole window.
	BuildStartedAt int64 `json:"build_started_at,omitempty"`
}

// DeferredIndexRebuildActive reports whether a deferred reindex currently
// owns the ANN index lifecycle (the index is dropped or being rebuilt). Used
// by InitEmbeddingsSearch callers to skip constructor index creation, and by
// search gating. Unexpected KV errors fail closed (treated as active): a
// wrongly-skipped constructor index creation is repaired by reconciliation,
// whereas a wrongly-run one can synchronously rebuild a huge index that was
// dropped on purpose.
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
	return state.Phase != ""
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

// deleteOwnedVectorIndexState clears the phase state if (and only if) jobID
// still owns it, retrying transient failures. Returns an error when the
// state could not be cleared while owned — search stays gated in that case,
// so the caller must surface it.
func (s *Indexer) deleteOwnedVectorIndexState(jobID string) error {
	var lastErr error
	for attempt := 0; attempt < deleteStateRetries; attempt++ {
		state, err := s.loadVectorIndexState()
		if err != nil {
			lastErr = err
			continue
		}
		if state == nil {
			return nil
		}
		if state.JobID != jobID {
			s.pluginAPI.LogWarn("Vector index state is owned by another run; skipping clear",
				"job_id", jobID, "owner_job_id", state.JobID)
			return nil
		}
		ok, casErr := s.casVectorIndexState(state, nil)
		if casErr != nil {
			lastErr = casErr
			continue
		}
		if ok {
			return nil
		}
		// Lost a CAS race; re-read and re-check ownership.
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("compare-and-delete kept losing races")
	}
	return fmt.Errorf("failed to clear vector index state: %w", lastErr)
}

// ownsVectorIndexState returns the current state when jobID owns it, or nil
// (with a log) when the state is absent or owned by another run.
func (s *Indexer) ownsVectorIndexState(jobID string) *VectorIndexState {
	state, err := s.loadVectorIndexState()
	if err != nil {
		s.pluginAPI.LogError("Failed to read vector index state for ownership check", "error", err)
		return nil
	}
	if state == nil {
		return nil
	}
	if state.JobID != jobID {
		s.pluginAPI.LogWarn("Vector index state is owned by another run",
			"job_id", jobID, "owner_job_id", state.JobID)
		return nil
	}
	return state
}

// resolveDeferredRebuild decides whether this run defers the ANN index
// rebuild and persists/updates the phase state accordingly. Any leftover
// state is adopted regardless of the configured strategy or fresh/resume
// mode — the invariant is that every full or resumed reindex rebuilds a
// dropped index, even if the admin switched the strategy back to maintain
// after a deferred run crashed. Without leftover state, fresh full reindexes
// consult the configured strategy. Called under the job-start cluster mutex,
// before the background job runs.
//
// Returns deferRebuild (run in defer mode), adopted (the claimed state was
// left over from a previous run, i.e. the index may already be genuinely
// dropped), and a non-nil error when the state could not be read or an
// adoption write failed — the caller must fail the job rather than silently
// running in maintain mode with the index possibly missing.
func (s *Indexer) resolveDeferredRebuild(clearIndex bool, jobID string) (deferRebuild bool, adopted bool, err error) {
	state, err := s.loadVectorIndexState()
	if err != nil {
		return false, false, fmt.Errorf("failed to read vector index state: %w", err)
	}

	if state != nil {
		// Leftover state: a previous deferred run left the index dropped (or
		// its build unfinished). This run must rebuild it.
		if bulkIndexerFor(s.getSearch()) == nil {
			s.pluginAPI.LogError("Vector index was dropped by a previous reindex but the current vector store does not support rebuilding it; semantic search stays disabled",
				"previous_job_id", state.JobID)
			return false, false, nil
		}
		newState := VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped, BuildStartedAt: state.BuildStartedAt}
		ok, casErr := s.casVectorIndexState(state, &newState)
		if casErr != nil {
			return false, false, fmt.Errorf("failed to take ownership of vector index state: %w", casErr)
		}
		if !ok {
			return false, false, fmt.Errorf("vector index state changed while taking ownership")
		}
		return true, true, nil
	}

	if !clearIndex {
		// Resume or catch-up without leftover state: nothing to defer.
		return false, false, nil
	}

	if s.configGetter == nil {
		return false, false, nil
	}
	cfg := s.configGetter()
	if cfg.EffectiveReindexIndexStrategy() != embeddings.ReindexIndexStrategyDefer {
		return false, false, nil
	}
	if bulkIndexerFor(s.getSearch()) == nil {
		s.pluginAPI.LogWarn("Reindex index strategy is 'defer' but the vector store does not support it; maintaining the index during reindex")
		return false, false, nil
	}
	// Persist the state BEFORE the index is dropped so a crash in between
	// leaves a record; reconciliation clears it if the index still exists.
	// The index is untouched at this point, so a claim failure can safely
	// fall back to maintain mode.
	ok, casErr := s.casVectorIndexState(nil, &VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped})
	if casErr != nil || !ok {
		s.pluginAPI.LogError("Failed to persist vector index state; maintaining the index during reindex", "error", casErr)
		return false, false, nil
	}
	return true, false, nil
}

// finalizeDeferredIndex transitions the phase to building, rebuilds the ANN
// index, and clears the phase state on success. The build can run for hours,
// so the DDL runs in a goroutine while this method keeps the job heartbeat
// ticking — otherwise stale-job detection would reclaim the job and start a
// second reindex mid-build. Cancellation cannot interrupt in-flight DDL; a
// pending cancel is acknowledged by the caller after the build finishes.
//
// Returns the Unix-millis timestamp from which live indexing was gated (for
// the edited-posts repair) and an error. The building transition is fenced
// on ownership and MUST succeed before the DDL runs: the IndexPost gate only
// engages on the building phase, and running a blocking CREATE INDEX without
// the gate would pile up hook goroutines on blocked writes for hours.
func (s *Indexer) finalizeDeferredIndex(ctx context.Context, jobStatus *JobStatus, bulk embeddings.BulkIndexer) (int64, error) {
	state := s.ownsVectorIndexState(jobStatus.JobID)
	if state == nil {
		return 0, fmt.Errorf("vector index state is no longer owned by this run; skipping index build")
	}

	buildingState := VectorIndexState{
		JobID:          jobStatus.JobID,
		Phase:          VectorIndexPhaseBuilding,
		BuildStartedAt: state.BuildStartedAt,
	}
	if buildingState.BuildStartedAt == 0 {
		buildingState.BuildStartedAt = time.Now().UnixMilli()
	}
	ok, casErr := s.casVectorIndexState(state, &buildingState)
	if casErr != nil {
		return 0, fmt.Errorf("failed to record vector index building phase: %w", casErr)
	}
	if !ok {
		return 0, fmt.Errorf("vector index state changed while entering the building phase")
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
				}
				return buildingState.BuildStartedAt, err
			}
			if delErr := s.deleteOwnedVectorIndexState(jobStatus.JobID); delErr != nil {
				// The index is built but search stays gated until the state
				// clears; surface the failure instead of silently completing.
				return buildingState.BuildStartedAt, fmt.Errorf("vector index was rebuilt but its state could not be cleared; semantic search stays disabled: %w", delErr)
			}
			return buildingState.BuildStartedAt, nil
		case <-heartbeat.C:
			// Keep the job fresh; a cancel request is intentionally not
			// acted on here since the DDL cannot be interrupted.
			s.heartbeatTick(jobStatus)
		}
	}
}

// restoreDeferredIndex attempts to rebuild the ANN index on a failure or
// cancel exit path, before the terminal status is recorded. It returns the
// (possibly augmented) error message; when the rebuild itself fails the
// phase state stays in place (reverted to dropped) so the condition stays
// visible and a resume can fix it.
func (s *Indexer) restoreDeferredIndex(ctx context.Context, jobStatus *JobStatus, bulk embeddings.BulkIndexer, errMsg string) string {
	_, rebuildErr := s.finalizeDeferredIndex(ctx, jobStatus, bulk)
	if rebuildErr == nil {
		return errMsg
	}
	s.pluginAPI.LogError("Failed to rebuild vector index while terminating reindex job", "error", rebuildErr)
	if errMsg == "" {
		return fmt.Sprintf("Failed to rebuild vector index: %s", rebuildErr)
	}
	return fmt.Sprintf("%s; additionally failed to rebuild vector index: %s", errMsg, rebuildErr)
}

// abandonUndroppedClaim clears a claimed phase state on an exit path where
// the ANN index was never actually dropped by this run. Adopted states are
// left in place instead: they were inherited from a previous run, so the
// index may genuinely be missing and clearing the state would ungate search
// against a missing index.
func (s *Indexer) abandonUndroppedClaim(jobID string, adopted bool) {
	if adopted {
		s.pluginAPI.LogError("Reindex job is exiting before rebuilding an inherited dropped vector index; semantic search stays disabled until a reindex rebuilds it",
			"job_id", jobID)
		return
	}
	if err := s.deleteOwnedVectorIndexState(jobID); err != nil {
		s.pluginAPI.LogError("Failed to clear vector index state on early exit", "error", err)
	}
}

// ReconcileVectorIndexState checks a leftover phase state against the job
// row and the catalog on activation. A fresh active owning job is checked
// FIRST: between claiming the state and dropping the index there is a window
// where the catalog still shows a valid index, and clearing a live job's
// claim there would let the constructor rebuild the index under the job or
// ungate search mid-drop. Only leftovers without a live owner use catalog
// evidence. If the index is genuinely missing and no live job owns the
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
		// The owning job is still running; it will rebuild the index.
		return nil
	}

	if bulk := bulkIndexerFor(s.getSearch()); bulk != nil {
		exists, exErr := bulk.VectorIndexExists(ctx)
		if exErr != nil {
			return exErr
		}
		if exists {
			s.pluginAPI.LogWarn("Vector index state says the index is dropped but a valid index exists; clearing stale state",
				"job_id", state.JobID, "phase", state.Phase)
			if ok, casErr := s.casVectorIndexState(state, nil); casErr != nil || !ok {
				s.pluginAPI.LogError("Failed to clear stale vector index state", "error", casErr)
			}
			return nil
		}
	}

	s.pluginAPI.LogError("Vector index was dropped for a deferred reindex but no active job owns it; semantic search is disabled until a reindex is started or resumed",
		"job_id", state.JobID, "phase", state.Phase)
	return nil
}
