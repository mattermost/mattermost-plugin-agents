// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"fmt"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

var errVectorStoreNoBulkIndex = fmt.Errorf("vector store does not support rebuilding the index")

// StartRebuildVectorIndex drops and rebuilds the HNSW index with the current
// m without clearing or re-embedding posts. Search is gated while the index
// is dropped/building; live writes skip during building and are repaired after.
func (s *Indexer) StartRebuildVectorIndex(ctx context.Context) (JobStatus, error) {
	if s.getSearch == nil || s.getSearch() == nil {
		return JobStatus{}, fmt.Errorf("search functionality is not configured")
	}

	sess, err := s.beginExclusiveJob()
	if err != nil {
		return s.exclusiveJobConflict(err)
	}
	defer sess.Unlock()

	// Full reindex (clearIndex) may have emptied the embeddings table.
	// Rebuild finalizes HNSW only and must not paper over that.
	if sess.hasExisting && sess.existing.isUnfinishedFullReindex() {
		return JobStatus{}, ErrRebuildIncompleteReindex
	}

	if bulkIndexerFor(s.getSearch()) == nil {
		return JobStatus{}, errVectorStoreNoBulkIndex
	}

	if current := s.getModelInfoFromConfig(); current != nil {
		compat := s.CheckModelCompatibility(*current)
		if !compat.Compatible {
			return JobStatus{}, fmt.Errorf("%w: %s", ErrRebuildIncompatible, compat.Reason)
		}
	}

	newJobStatus := JobStatus{
		JobID:     model.NewId(),
		Status:    JobStatusRunning,
		StartedAt: time.Now(),
		Resumable: false,
		NodeID:    s.getNodeID(),
		CutoffAt:  time.Now().UnixMilli(),
		Operation: JobOperationRebuildVectorIndex,
		ModelInfo: s.getModelInfoFromConfig(),
	}

	if err := sess.commit(s, newJobStatus); err != nil {
		return s.exclusiveJobConflict(err)
	}

	deferRun, deferErr := s.claimRebuildVectorIndex(newJobStatus.JobID)
	if deferErr != nil {
		failedStatus := newJobStatus
		failedStatus.Status = JobStatusFailed
		failedStatus.Error = deferErr.Error()
		failedStatus.CompletedAt = time.Now()
		if _, casErr := s.pluginAPI.KVCompareAndSet(ReindexJobKey, newJobStatus, failedStatus); casErr != nil {
			s.pluginAPI.LogError("Failed to record vector index rebuild failure", "error", casErr)
		}
		return JobStatus{}, deferErr
	}

	returnStatus := newJobStatus
	jobCtx := context.WithoutCancel(ctx)
	go s.runRebuildVectorIndexJob(jobCtx, &newJobStatus, deferRun)

	return returnStatus, nil
}

func (s *Indexer) runRebuildVectorIndexJob(ctx context.Context, jobStatus *JobStatus, deferRun *deferredRun) {
	s.runIndexJob(ctx, jobStatus, deferRun, indexJobSpec{
		panicLog:              "Vector index rebuild panicked",
		completeLog:           "Vector index rebuild completed",
		clearIndex:            false,
		runMainPass:           false,
		saveLastIndexed:       false,
		deleteCursorOnSuccess: false,
		persistHNSWMOnly:      true,
	})
}

// claimRebuildVectorIndex takes ownership of the vector-index phase key so
// search gates and live writes skip during building. Leftover dropped/building
// claims are adopted; leftover repairing is converted to dropped (the index
// will be replaced) while preserving BuildStartedAt for the repair window.
func (s *Indexer) claimRebuildVectorIndex(jobID string) (*deferredRun, error) {
	if bulkIndexerFor(s.getSearch()) == nil {
		return nil, errVectorStoreNoBulkIndex
	}
	state, err := s.loadVectorIndexState()
	if err != nil {
		return nil, fmt.Errorf("failed to read vector index state: %w", err)
	}

	newState := VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped}
	if state == nil {
		ok, casErr := s.casVectorIndexState(nil, &newState)
		if casErr != nil {
			return nil, fmt.Errorf("failed to persist vector index state: %w", casErr)
		}
		if !ok {
			return nil, fmt.Errorf("vector index state was claimed concurrently")
		}
		return &deferredRun{state: newState, adopted: false}, nil
	}

	newState.BuildStartedAt = state.BuildStartedAt
	ok, casErr := s.casVectorIndexState(state, &newState)
	if casErr != nil {
		return nil, fmt.Errorf("failed to take ownership of vector index state: %w", casErr)
	}
	if !ok {
		return nil, fmt.Errorf("vector index state changed while taking ownership")
	}
	run := &deferredRun{state: newState, adopted: true}
	if state.Phase == VectorIndexPhaseRepairing {
		run.convertedFrom = state
	}
	return run, nil
}
