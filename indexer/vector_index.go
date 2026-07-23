// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
)

// VectorIndexStateKey tracks ANN index lifecycle during a deferred reindex.
// Absent means the index is present/normal.
const VectorIndexStateKey = "indexer_vector_index_state"

// Vector index phases while the state key exists.
const (
	// Index dropped for bulk load.
	VectorIndexPhaseDropped = "dropped"
	// Post-load CREATE INDEX is running.
	VectorIndexPhaseBuilding = "building"
	// Index is valid; posts edited while gated still need re-embedding.
	// Search works; slight staleness matches maintain-mode norms.
	VectorIndexPhaseRepairing = "repairing"
)

// clearStateRetries bounds CAS retries on transient KV errors; persistent
// failure leaves the marker so the condition stays visible.
const clearStateRetries = 3

// VectorIndexState is the durable deferred-reindex claim. On conflict the
// Postgres catalog is authoritative.
type VectorIndexState struct {
	JobID string `json:"job_id"` // owning job
	Phase string `json:"phase"`  // VectorIndexPhase* value
	// BuildStartedAt (Unix millis) is when the first build began (writes gated
	// from then). Preserved across failed builds/adoptions so repair covers
	// the whole window; a fresh full reindex resets it.
	BuildStartedAt int64 `json:"build_started_at,omitempty"`
}

// deferredRun is this run's in-memory ownership of the index lifecycle.
// state is exactly what the last successful CAS wrote — never re-read for
// ownership (KVGet may hit a lagging replica; CAS goes to master).
type deferredRun struct {
	state VectorIndexState
	// adopted: claim inherited; index may already be dropped.
	adopted bool
	// convertedFrom: repairing marker replaced by a fresh dropped claim.
	// Pre-DDL exits must restore it (with original BuildStartedAt) instead
	// of leaving a dropped claim over a still-valid index.
	convertedFrom *VectorIndexState
}

// deferredIndexGated: dropped/building (and unknown) gate search and
// constructor index creation; repairing does not.
func deferredIndexGated(phase string) bool {
	return phase != "" && phase != VectorIndexPhaseRepairing
}

// DeferredIndexRebuildActive reports a deferred reindex with the ANN index
// dropped or rebuilding. Fail-closed on KV errors (wrong skip is repaired;
// wrong CREATE can rebuild a huge intentionally-dropped index). Best-effort
// across nodes — DDL fencing is CAS, not this read.
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

// deferredBuildWriteGated skips hook-driven embeddings writes during the
// building phase: non-concurrent CREATE INDEX blocks writers and would pile
// up hook goroutines. Fail-closed on KV errors (skip is repairable). The
// reindex job must not use this gate — it coordinates the build itself.
func (s *Indexer) deferredBuildWriteGated() bool {
	if s.pluginAPI == nil {
		return false
	}
	var state VectorIndexState
	if err := s.pluginAPI.KVGet(VectorIndexStateKey, &state); err != nil {
		if mmapi.IsKVNotFound(err) {
			return false
		}
		s.pluginAPI.LogError("Failed to read vector index state; skipping embeddings write", "error", err)
		return true
	}
	return state.Phase == VectorIndexPhaseBuilding
}

// bulkIndexerFor returns bulk index control from a search snapshot, or nil.
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

// loadVectorIndexState returns nil when the key is absent.
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

// casVectorIndexState replaces the phase state iff it still equals old
// (nil old = absent; nil new = delete). Fences stale workers from clobbering
// a successor's claim.
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

// clearVectorIndexState CAS-deletes owned state, retrying transient KV errors.
// CAS conflict means ownership was lost — leave the key alone and error.
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

// deferStrategyUsable is true when strategy is defer and the store supports it.
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

// resolveDeferredRebuild claims/adopts deferred-index phase state under the
// job-start mutex. Leftover state always rebuilds or finishes repair (even if
// strategy changed); otherwise fresh full reindexes follow config. Nil =
// maintain mode. Error = unknown lifecycle — caller must fail the job, not
// fall through to maintain.
func (s *Indexer) resolveDeferredRebuild(clearIndex bool, jobID string) (*deferredRun, error) {
	state, err := s.loadVectorIndexState()
	if err != nil {
		return nil, fmt.Errorf("failed to read vector index state: %w", err)
	}

	if state != nil && clearIndex && state.Phase == VectorIndexPhaseRepairing {
		// Full reindex supersedes pending repair. Resolve in one CAS —
		// delete-then-create can leave neither marker nor claim.
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
		// Maintain: delete marker. Residual if we die before Clear(): stale
		// edits may lack a marker; failed job stays visible and a later full
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
			// Resume repair only — index is intact; do not drop it.
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

		// Leftover dropped/building: must rebuild.
		if bulkIndexerFor(s.getSearch()) == nil {
			s.pluginAPI.LogError("Vector index was dropped by a previous reindex but the current vector store does not support rebuilding it; semantic search stays disabled",
				"previous_job_id", state.JobID)
			return nil, nil
		}
		newState := VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped, BuildStartedAt: state.BuildStartedAt}
		if clearIndex {
			// Fresh full reindex: restart the gated-edits window.
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
		return nil, nil
	}

	if !s.deferStrategyUsable() {
		return nil, nil
	}
	// Claim before DROP so a crash leaves a record (reconcile clears if the
	// index still exists). Claim failure must fail the job, not silent maintain.
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

// finalizeDeferredIndex CAS-fences dropped→building (engages write gate),
// rebuilds the ANN index in a goroutine (heartbeat ticks so stale-job
// reclaim cannot start a second mid-build), then CAS to repairing. DDL is
// not interruptible; caller acknowledges cancel after return. owned must be
// this run's last CAS-written state.
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

	// Persist immediately so the UI's short poll sees the phase before the
	// first heartbeat tick.
	jobStatus.Phase = JobPhaseBuildingIndex
	s.saveJobStatus(jobStatus)
	defer func() {
		jobStatus.Phase = ""
		s.saveJobStatus(jobStatus)
	}()

	// Accepted CAS→DDL TOCTOU (ms-wide); advisory lock would fully serialize.
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
				// Revert to dropped so health/resume see a rebuildable claim.
				droppedState := buildingState
				droppedState.Phase = VectorIndexPhaseDropped
				if reverted, revertErr := s.casVectorIndexState(&buildingState, &droppedState); revertErr != nil || !reverted {
					s.pluginAPI.LogError("Failed to revert vector index state after failed build", "error", revertErr)
					return buildingState, err
				}
				return droppedState, err
			}
			// Index valid; keep repairing marker until edit repair finishes.
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
			// Do not cancel mid-DDL; caller acknowledges after build.
			s.heartbeatTick(jobStatus)
		}
	}
}

const pendingRepairNote = "Posts edited during the index rebuild are pending repair; resume the job or run a reindex to re-index them"

func appendPendingRepairNote(errMsg string) string {
	if errMsg == "" {
		return pendingRepairNote
	}
	return errMsg + "; " + pendingRepairNote
}

const droppedIndexNote = "The vector index remains dropped for the deferred rebuild; semantic search is unavailable until you resume the job or run a full reindex"

func appendDroppedIndexNote(errMsg string) string {
	if errMsg == "" {
		return droppedIndexNote
	}
	return errMsg + "; " + droppedIndexNote
}

// abandonUndroppedClaim releases a claim when this run never dropped the
// index: restore converted repairing markers (valid index + pending repair),
// keep adopted dropped/building (index may be missing), delete fresh claims.
// CAS conflict/error leaves the key untouched — surface on the job.
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

// ReconcileVectorIndexState cleans leftover phase state on activation.
// Check a live owning job first (claim→DROP window still shows a valid
// catalog index). Keep repairing always. Ownerless dropped+valid → clear;
// building+valid → convert to repairing (CREATE committed, transition lost).
// Never rebuild here — hours-long builds stay admin-driven.
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
				// CREATE done, repairing CAS lost — keep the repair obligation.
				repairing := *state
				repairing.Phase = VectorIndexPhaseRepairing
				s.pluginAPI.LogWarn("Vector index build completed but the repair of gated-window edits is pending; resume the job or run a reindex to re-index them",
					"job_id", state.JobID)
				if ok, casErr := s.casVectorIndexState(state, &repairing); casErr != nil || !ok {
					s.pluginAPI.LogError("Failed to convert vector index state to repairing", "error", casErr)
				}
			case VectorIndexPhaseDropped:
				// Pre-DROP crash: index untouched, claim is stale.
				s.pluginAPI.LogWarn("Vector index state says the index is dropped but a valid index exists; clearing stale state",
					"job_id", state.JobID, "phase", state.Phase)
				if ok, casErr := s.casVectorIndexState(state, nil); casErr != nil || !ok {
					s.pluginAPI.LogError("Failed to clear stale vector index state", "error", casErr)
				}
			default:
				// Unknown phase: keep — may be from a newer plugin version.
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
