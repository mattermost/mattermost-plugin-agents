// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

const exclusiveJobMutexKey = "ai_reindex_job"

var (
	// ErrRebuildIncompatible is returned when rebuild is requested after a
	// provider/model/dimension change. Search over mixed vectors is unsafe.
	ErrRebuildIncompatible = errors.New("cannot rebuild vector index while the embedding model is incompatible")
	// ErrCatchUpIncompatible is returned when catch-up is requested after a
	// provider/model/dimension/vector-precision change. Catch-up embeds with
	// the current model but does not rewrite stored identity, so mixed
	// vectors would be searchable after a config revert.
	ErrCatchUpIncompatible = errors.New("cannot catch up while the embedding model is incompatible")
	// ErrCannotResumeRebuild is returned when StartReindexJob(false) is
	// asked to resume a rebuild-vector-index job.
	ErrCannotResumeRebuild = errors.New("cannot resume a vector index rebuild; use rebuild vector index")
	// ErrRebuildIncompleteReindex is returned when rebuild is requested after
	// a failed or canceled full reindex. Rebuild does not re-embed.
	ErrRebuildIncompleteReindex = errors.New("cannot rebuild vector index after an incomplete reindex; finish or restart the reindex (rebuild does not re-embed)")
	// ErrJobAlreadyRunning is reported when an exclusive indexer job is
	// already active.
	ErrJobAlreadyRunning = errors.New("job already running")
	// ErrNotRunning is returned when a cancel is requested and no job is
	// running.
	ErrNotRunning = errors.New("not running")
	// ErrNoPreviousIndex is returned when catch-up is requested before any
	// full reindex has completed.
	ErrNoPreviousIndex = errors.New("no previous index found, run a full reindex first")
	// ErrNotConfigured is returned when search functionality is not
	// configured.
	ErrNotConfigured = errors.New("search functionality is not configured")
)

// jobAlreadyRunningError is returned when an exclusive indexer job is
// already active. It wraps ErrJobAlreadyRunning and carries the blocking
// job's status.
type jobAlreadyRunningError struct {
	status JobStatus
}

func (e *jobAlreadyRunningError) Error() string {
	return ErrJobAlreadyRunning.Error()
}

func (e *jobAlreadyRunningError) Unwrap() error {
	return ErrJobAlreadyRunning
}

func asJobAlreadyRunning(err error) (JobStatus, bool) {
	if conflict, ok := errors.AsType[*jobAlreadyRunningError](err); ok {
		return conflict.status, true
	}
	return JobStatus{}, false
}

// exclusiveJobSession holds the cluster mutex and the job row snapshot
// used as the CAS predicate.
type exclusiveJobSession struct {
	existing    JobStatus
	hasExisting bool
	unlock      func()
}

func (s *exclusiveJobSession) Unlock() {
	if s != nil && s.unlock != nil {
		s.unlock()
		s.unlock = nil
	}
}

func (s *Indexer) beginExclusiveJob() (*exclusiveJobSession, error) {
	var jobStatus JobStatus
	err := s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil && !mmapi.IsKVNotFound(err) {
		return nil, fmt.Errorf("failed to check job status: %w", err)
	}
	if isActiveJob(&jobStatus) && !s.isJobStale(&jobStatus) {
		return nil, &jobAlreadyRunningError{status: jobStatus}
	}

	mtx, err := cluster.NewMutex(s.clusterMutex, exclusiveJobMutexKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create mutex: %w", err)
	}
	mtx.Lock()

	err = s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil && !mmapi.IsKVNotFound(err) {
		mtx.Unlock()
		return nil, fmt.Errorf("failed to check job status: %w", err)
	}
	hasExisting := err == nil
	if !hasExisting {
		jobStatus = JobStatus{}
	}
	if hasExisting && isActiveJob(&jobStatus) && !s.isJobStale(&jobStatus) {
		mtx.Unlock()
		return nil, &jobAlreadyRunningError{status: jobStatus}
	}

	return &exclusiveJobSession{
		existing:    jobStatus,
		hasExisting: hasExisting,
		unlock:      mtx.Unlock,
	}, nil
}

func (s *exclusiveJobSession) commit(idx *Indexer, newStatus JobStatus) error {
	var oldValue any
	if s.hasExisting {
		oldValue = s.existing
	}
	ok, err := idx.pluginAPI.KVCompareAndSet(ReindexJobKey, oldValue, newStatus)
	if err != nil {
		return fmt.Errorf("failed to save job status: %w", err)
	}
	if !ok {
		return &jobAlreadyRunningError{}
	}
	return nil
}

func (s *Indexer) exclusiveJobConflict(err error) (JobStatus, error) {
	if status, ok := asJobAlreadyRunning(err); ok {
		return status, err
	}
	return JobStatus{}, err
}
