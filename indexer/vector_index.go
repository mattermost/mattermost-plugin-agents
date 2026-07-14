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

// VectorIndexState is the durable record of a deferred reindex owning the
// ANN index lifecycle. The Postgres catalog is authoritative on conflict.
type VectorIndexState struct {
	JobID string `json:"job_id"` // owning job
	Phase string `json:"phase"`  // VectorIndexPhaseDropped or VectorIndexPhaseBuilding
}

// DeferredIndexRebuildActive reports whether a deferred reindex currently
// owns the ANN index lifecycle (the index is dropped or being rebuilt). Used
// by InitEmbeddingsSearch callers to skip constructor index creation, and by
// search gating.
func DeferredIndexRebuildActive(client mmapi.Client) bool {
	if client == nil {
		return false
	}
	var state VectorIndexState
	if err := client.KVGet(VectorIndexStateKey, &state); err != nil {
		return false
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

func (s *Indexer) saveVectorIndexState(state VectorIndexState) error {
	return s.pluginAPI.KVSet(VectorIndexStateKey, state)
}

func (s *Indexer) deleteVectorIndexState() {
	if err := s.pluginAPI.KVDelete(VectorIndexStateKey); err != nil {
		s.pluginAPI.LogError("Failed to clear vector index state", "error", err)
	}
}

// resolveDeferredRebuild decides whether this run defers the ANN index
// rebuild and persists/updates the phase state accordingly. Fresh full
// reindexes consult the configured strategy; resumes take ownership of any
// existing state regardless of the config value at resume time. Called under
// the job-start cluster mutex, before the background job runs.
func (s *Indexer) resolveDeferredRebuild(clearIndex bool, jobID string) bool {
	if !clearIndex {
		// Resume path: continue in defer mode only if a previous deferred
		// run left the index dropped.
		state, err := s.loadVectorIndexState()
		if err != nil {
			s.pluginAPI.LogError("Failed to read vector index state on resume", "error", err)
			return false
		}
		if state == nil {
			return false
		}
		if bulkIndexerFor(s.getSearch()) == nil {
			s.pluginAPI.LogError("Vector index was dropped by a previous reindex but the current vector store does not support rebuilding it; semantic search stays disabled",
				"previous_job_id", state.JobID)
			return false
		}
		// Take ownership: the index stays dropped until this run rebuilds it.
		if err := s.saveVectorIndexState(VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped}); err != nil {
			s.pluginAPI.LogError("Failed to take ownership of vector index state", "error", err)
		}
		return true
	}

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
	// Persist the state BEFORE the index is dropped so a crash in between
	// leaves a record; reconciliation clears it if the index still exists.
	if err := s.saveVectorIndexState(VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped}); err != nil {
		s.pluginAPI.LogError("Failed to persist vector index state; maintaining the index during reindex", "error", err)
		return false
	}
	return true
}

// finalizeDeferredIndex transitions the phase to building, rebuilds the ANN
// index, and clears the phase state on success. The build can run for hours,
// so the DDL runs in a goroutine while this method keeps the job heartbeat
// ticking — otherwise stale-job detection would reclaim the job and start a
// second reindex mid-build. Cancellation cannot interrupt in-flight DDL; a
// pending cancel is acknowledged by the caller after the build finishes.
func (s *Indexer) finalizeDeferredIndex(ctx context.Context, jobStatus *JobStatus, bulk embeddings.BulkIndexer) error {
	if err := s.saveVectorIndexState(VectorIndexState{JobID: jobStatus.JobID, Phase: VectorIndexPhaseBuilding}); err != nil {
		s.pluginAPI.LogError("Failed to record vector index building phase", "error", err)
	}

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
				// the owning job) so health checks and reconciliation
				// reflect reality and a resume can take ownership cleanly.
				if saveErr := s.saveVectorIndexState(VectorIndexState{JobID: jobStatus.JobID, Phase: VectorIndexPhaseDropped}); saveErr != nil {
					s.pluginAPI.LogError("Failed to revert vector index state after failed build", "error", saveErr)
				}
				return err
			}
			s.deleteVectorIndexState()
			return nil
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
	rebuildErr := s.finalizeDeferredIndex(ctx, jobStatus, bulk)
	if rebuildErr == nil {
		return errMsg
	}
	s.pluginAPI.LogError("Failed to rebuild vector index while terminating reindex job", "error", rebuildErr)
	if errMsg == "" {
		return fmt.Sprintf("Failed to rebuild vector index: %s", rebuildErr)
	}
	return fmt.Sprintf("%s; additionally failed to rebuild vector index: %s", errMsg, rebuildErr)
}

// ReconcileVectorIndexState checks a leftover phase state against the
// catalog and the job row on activation. If a valid index exists, the state
// is stale and cleared (catalog is authoritative). If the index is genuinely
// missing and no live job owns the state, the index is NOT rebuilt here — a
// build can take hours and must not run inside activation. Search stays
// gated until an admin starts or resumes a reindex, which rebuilds it.
func (s *Indexer) ReconcileVectorIndexState(ctx context.Context) error {
	state, err := s.loadVectorIndexState()
	if err != nil {
		return err
	}
	if state == nil {
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
			s.deleteVectorIndexState()
			return nil
		}
	}

	var jobStatus JobStatus
	jobErr := s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if jobErr == nil && jobStatus.JobID == state.JobID && isActiveJob(&jobStatus) && !s.isJobStale(&jobStatus) {
		// The owning job is still running; it will rebuild the index.
		return nil
	}

	s.pluginAPI.LogError("Vector index was dropped for a deferred reindex but no active job owns it; semantic search is disabled until a reindex is started or resumed",
		"job_id", state.JobID, "phase", state.Phase)
	return nil
}
