// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

var errVectorStoreNoBulkIndex = errors.New("vector store does not support rebuilding the index")

// StartRebuildVectorIndex drops and rebuilds the HNSW index with the current
// m without clearing or re-embedding posts. Search is gated while the index
// is dropped/building; live writes skip during building and are repaired after.
func (s *Indexer) StartRebuildVectorIndex(ctx context.Context) (JobStatus, error) {
	if s.getSearch == nil || s.getSearch() == nil {
		return JobStatus{}, fmt.Errorf("search functionality is not configured")
	}

	var jobStatus JobStatus
	err := s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil && !mmapi.IsKVNotFound(err) {
		return JobStatus{}, fmt.Errorf("failed to check job status: %w", err)
	}
	if isActiveJob(&jobStatus) && !s.isJobStale(&jobStatus) {
		return jobStatus, fmt.Errorf("job already running")
	}

	mtx, err := cluster.NewMutex(s.clusterMutex, "ai_reindex_job")
	if err != nil {
		return JobStatus{}, fmt.Errorf("failed to create mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	err = s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil && !mmapi.IsKVNotFound(err) {
		return JobStatus{}, fmt.Errorf("failed to check job status: %w", err)
	}
	hasExisting := err == nil
	if !hasExisting {
		jobStatus = JobStatus{}
	}
	if hasExisting && isActiveJob(&jobStatus) && !s.isJobStale(&jobStatus) {
		return jobStatus, fmt.Errorf("job already running")
	}

	if bulkIndexerFor(s.getSearch()) == nil {
		return JobStatus{}, errVectorStoreNoBulkIndex
	}

	newJobStatus := JobStatus{
		JobID:     model.NewId(),
		Status:    JobStatusRunning,
		StartedAt: time.Now(),
		Resumable: false,
		NodeID:    s.getNodeID(),
		CutoffAt:  time.Now().UnixMilli(),
		ModelInfo: s.getModelInfoFromConfig(),
	}

	var oldValue interface{}
	if hasExisting {
		oldValue = jobStatus
	}
	ok, err := s.pluginAPI.KVCompareAndSet(ReindexJobKey, oldValue, newJobStatus)
	if err != nil {
		return JobStatus{}, fmt.Errorf("failed to save job status: %w", err)
	}
	if !ok {
		return JobStatus{}, fmt.Errorf("job already running")
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

// runRebuildVectorIndexJob drops the HNSW index, rebuilds it with the current
// m, then repairs gated-window edits and catch-up for posts skipped during
// building. It never Clear()s the embeddings table.
func (s *Indexer) runRebuildVectorIndexJob(ctx context.Context, jobStatus *JobStatus, deferRun *deferredRun) {
	var bulk embeddings.BulkIndexer
	var ownedState VectorIndexState
	deferPending := false
	repairPending := false

	defer func() {
		if r := recover(); r != nil {
			s.pluginAPI.LogError("Vector index rebuild panicked", "panic", r)
			errMsg := fmt.Sprintf("Job panicked: %v", r)
			if deferPending {
				errMsg = appendDroppedIndexNote(errMsg)
			} else if repairPending {
				errMsg = appendPendingRepairNote(errMsg)
			}
			jobStatus.Status = JobStatusFailed
			jobStatus.Error = errMsg
			jobStatus.CompletedAt = time.Now()
			s.saveJobStatus(jobStatus)
		}
	}()

	var search embeddings.EmbeddingSearch
	if s.getSearch != nil {
		search = s.getSearch()
	}
	if search == nil {
		errMsg := "Search not configured"
		if deferRun != nil {
			if abandonErr := s.abandonUndroppedClaim(deferRun); abandonErr != nil {
				s.pluginAPI.LogError("Failed to release vector index claim on early exit", "error", abandonErr)
				errMsg = fmt.Sprintf("%s; additionally failed to release the vector index claim: %s", errMsg, abandonErr)
			}
		}
		jobStatus.Status = JobStatusFailed
		jobStatus.Error = errMsg
		jobStatus.CompletedAt = time.Now()
		s.saveJobStatus(jobStatus)
		return
	}

	ownedState = deferRun.state
	bulk = bulkIndexerFor(search)
	if bulk == nil {
		errMsg := errVectorStoreNoBulkIndex.Error()
		if abandonErr := s.abandonUndroppedClaim(deferRun); abandonErr != nil {
			s.pluginAPI.LogError("Failed to release vector index claim", "error", abandonErr)
			errMsg = fmt.Sprintf("%s (additionally failed to release the vector index claim: %s)", errMsg, abandonErr)
		}
		jobStatus.Status = JobStatusFailed
		jobStatus.Error = errMsg
		jobStatus.CompletedAt = time.Now()
		s.saveJobStatus(jobStatus)
		return
	}

	if ok, casErr := s.casVectorIndexState(&ownedState, &ownedState); casErr != nil || !ok {
		s.pluginAPI.LogError("Vector index rebuild claim is no longer current; aborting before DROP",
			"job_id", jobStatus.JobID, "error", casErr)
		jobStatus.Status = JobStatusFailed
		jobStatus.Error = "Vector index claim lost before rebuild began"
		jobStatus.CompletedAt = time.Now()
		s.saveJobStatus(jobStatus)
		return
	}

	deferPending = true
	if err := bulk.PrepareBulkIndex(ctx); err != nil {
		jobStatus.Status = JobStatusFailed
		jobStatus.Error = appendDroppedIndexNote(fmt.Sprintf("Failed to drop vector index: %s", err))
		jobStatus.CompletedAt = time.Now()
		s.saveJobStatus(jobStatus)
		return
	}

	newState, buildErr := s.finalizeDeferredIndex(ctx, jobStatus, bulk, ownedState)
	ownedState = newState
	if buildErr != nil {
		deferPending = false
		s.handleJobError(jobStatus, appendDroppedIndexNote(fmt.Sprintf("Failed to rebuild vector index: %s", buildErr)), 0, "")
		return
	}
	deferPending = false
	repairPending = true

	if canceled, cancelErr := s.isCancelRequested(jobStatus.JobID); cancelErr == nil && canceled {
		jobStatus.Error = appendPendingRepairNote(jobStatus.Error)
		s.acknowledgeCancel(jobStatus)
		return
	}

	if repairErr := s.reindexEditedPosts(ctx, jobStatus, search, ownedState.BuildStartedAt); repairErr != nil {
		if errors.Is(repairErr, errCancelRequested) {
			jobStatus.Error = appendPendingRepairNote(jobStatus.Error)
			s.acknowledgeCancel(jobStatus)
			return
		}
		s.handleJobError(jobStatus, fmt.Sprintf("Failed to re-index posts edited during the index build: %s", repairErr), 0, "")
		return
	}
	if clearErr := s.clearVectorIndexState(ownedState); clearErr != nil {
		s.handleJobError(jobStatus, fmt.Sprintf("Repair completed but the vector index state could not be cleared: %s", clearErr), 0, "")
		return
	}
	repairPending = false

	_, catchUpCursor, catchUpErr := s.runCatchUpPass(ctx, jobStatus, search)
	if errors.Is(catchUpErr, errCancelRequested) {
		s.acknowledgeCancel(jobStatus)
		return
	}
	if catchUpErr != nil {
		s.handleJobError(jobStatus, fmt.Sprintf("Catch-up pass failed: %s", catchUpErr), catchUpCursor.LastCreateAt, catchUpCursor.LastID)
		return
	}

	if !s.finishJob(jobStatus) {
		return
	}

	if jobStatus.ModelInfo != nil {
		if err := s.SaveModelInfo(*jobStatus.ModelInfo); err != nil {
			s.pluginAPI.LogError("Failed to save model info after vector index rebuild", "error", err)
		}
	}

	s.pluginAPI.LogWarn("Vector index rebuild completed", "job_id", jobStatus.JobID)
}
