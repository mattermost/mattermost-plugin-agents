// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
)

// errCancelRequested signals that the admin requested cancellation; the
// caller acknowledges it by CASing the job status to canceled.
var errCancelRequested = errors.New("reindex job cancel requested")

const (
	defaultStoreRetryAttempts  = 5
	defaultStoreRetryBaseDelay = 2 * time.Second
)

// batchWork is one fetched batch dispatched to a worker.
type batchWork struct {
	posts []PostRecord
	errCh chan error // buffered(1); receives the batch's store result
}

// batchHandle is the committer's view of a dispatched batch, queued in fetch
// order so results are committed in that same order.
type batchHandle struct {
	cursor Cursor // keyset position after this batch
	count  int64
	errCh  chan error
}

// fetchFunc returns the next batch of posts after the given cursor.
type fetchFunc func(cursor Cursor, limit int) ([]PostRecord, error)

// reindexSettings resolves worker count and batch size from plugin config,
// falling back to defaults when unconfigured.
func (s *Indexer) reindexSettings() (workers, batchSize int) {
	if s.configGetter == nil {
		return embeddings.DefaultReindexWorkers, embeddings.DefaultReindexBatchSize
	}
	cfg := s.configGetter()
	return cfg.GetReindexWorkers(), cfg.GetReindexBatchSize()
}

// runIndexPass streams post batches through a pool of workers that filter,
// embed, and store them concurrently, while committing results strictly in
// fetch order: the committer waits on each batch's result before counting the
// next, so the persisted checkpoint (cursor + processed count) is always a
// contiguous prefix of completed batches and a resume after failure can never
// skip a batch that was still in flight. Batches stored ahead of a failed one
// may be re-processed on resume, which Store makes idempotent.
//
// The pass starts from startCursor with jobStatus.ProcessedRows as the
// processed-count base. It returns the number of posts committed by this
// pass, the final watermark cursor, and the first error encountered, which is
// errCancelRequested when the admin canceled the job.
func (s *Indexer) runIndexPass(
	ctx context.Context,
	jobStatus *JobStatus,
	search embeddings.EmbeddingSearch,
	fetch fetchFunc,
	startCursor Cursor,
) (int64, Cursor, error) {
	workers, batchSize := s.reindexSettings()

	ctx, cancelPass := context.WithCancel(ctx)
	defer cancelPass()

	workCh := make(chan batchWork)
	// Capacity = workers keeps all workers dispatchable while the committer
	// waits on the oldest batch; a smaller buffer would serialize dispatch.
	orderedCh := make(chan batchHandle, workers)

	// Fetcher: sequential keyset pagination; also polls for cancel requests.
	// On cancel it stops dispatching, but already-dispatched batches complete
	// and count. fetchErr is published before orderedCh closes, so the
	// committer may read it once its range over orderedCh finishes.
	var fetchErr error
	go func() {
		defer close(orderedCh)
		defer close(workCh)
		cursor := startCursor
		for {
			if canceled, err := s.isCancelRequested(jobStatus.JobID); err == nil && canceled {
				fetchErr = errCancelRequested
				return
			}
			posts, err := fetch(cursor, batchSize)
			if err != nil {
				fetchErr = fmt.Errorf("failed to fetch posts: %w", err)
				return
			}
			if len(posts) == 0 {
				return
			}
			last := posts[len(posts)-1]
			cursor = Cursor{LastCreateAt: last.CreateAt, LastID: last.ID}

			handle := batchHandle{cursor: cursor, count: int64(len(posts)), errCh: make(chan error, 1)}
			select {
			case orderedCh <- handle:
			case <-ctx.Done():
				return
			}
			select {
			case workCh <- batchWork{posts: posts, errCh: handle.errCh}:
			case <-ctx.Done():
				// The handle is already queued; resolve it so the committer
				// can never block on a batch that was never dispatched.
				handle.errCh <- ctx.Err()
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workCh {
				work.errCh <- s.storeBatchWithRetry(ctx, search, work.posts)
			}
		}()
	}

	// Committer: consume handles in fetch order, advancing the watermark one
	// contiguous batch at a time.
	startProcessed := jobStatus.ProcessedRows
	processed := startProcessed
	lastSaved := startProcessed
	lastHeartbeatSave := time.Now()
	watermark := startCursor
	var firstErr error

	for handle := range orderedCh {
		if err := <-handle.errCh; err != nil {
			firstErr = err
			cancelPass() // stop the fetcher and abort in-flight workers
			break
		}

		processed += handle.count
		watermark = handle.cursor
		jobStatus.ProcessedRows = processed
		jobStatus.LastUpdatedAt = time.Now()

		// Save checkpoint every 500 posts or every 2 minutes (whichever
		// comes first) to prevent false stale detection with slow
		// embedding providers.
		if processed >= lastSaved+500 || time.Since(lastHeartbeatSave) > 2*time.Minute {
			s.saveCursor(watermark)
			s.saveJobStatus(jobStatus)
			s.pluginAPI.LogWarn("Reindexing progress",
				"processed", processed,
				"estimated_total", jobStatus.TotalRows)
			lastSaved = processed
			lastHeartbeatSave = time.Now()
		}
	}

	cancelPass()
	wg.Wait()

	if firstErr == nil {
		// orderedCh is closed, so the fetcher has exited and fetchErr is safe
		// to read.
		firstErr = fetchErr
	}

	jobStatus.ProcessedRows = processed
	jobStatus.LastUpdatedAt = time.Now()
	return processed - startProcessed, watermark, firstErr
}

// storeBatchWithRetry filters a batch into documents and stores them,
// retrying failures (rate limits, network blips) with exponential backoff and
// jitter so a transient error doesn't fail a long-running job.
func (s *Indexer) storeBatchWithRetry(ctx context.Context, search embeddings.EmbeddingSearch, posts []PostRecord) error {
	docs := s.filterAndCreateDocs(posts)
	if len(docs) == 0 {
		return nil
	}

	delay := s.storeRetryBaseDelay
	var err error
	for attempt := 1; ; attempt++ {
		err = search.Store(ctx, docs)
		if err == nil || ctx.Err() != nil || attempt >= s.storeRetryAttempts {
			return err
		}
		s.pluginAPI.LogWarn("Reindex batch store failed, retrying",
			"attempt", attempt,
			"max_attempts", s.storeRetryAttempts,
			"error", err.Error())
		var jitter time.Duration
		if half := int64(delay / 2); half > 0 {
			jitter = time.Duration(rand.Int64N(half)) // #nosec G404 -- backoff jitter does not need cryptographic randomness.
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(delay + jitter):
		}
		delay *= 2
	}
}

// isCancelRequested reports whether the admin requested cancellation of this
// run. Scoped to jobID so a stale read for a different run is ignored.
func (s *Indexer) isCancelRequested(jobID string) (bool, error) {
	var currentStatus JobStatus
	if err := s.pluginAPI.KVGet(ReindexJobKey, &currentStatus); err != nil {
		return false, err
	}
	return currentStatus.JobID == jobID && currentStatus.Status == JobStatusCancelRequested, nil
}

// acknowledgeCancel CASes cancel_requested -> canceled for this run and
// mirrors the terminal state onto the worker's local status.
func (s *Indexer) acknowledgeCancel(jobStatus *JobStatus) {
	var currentStatus JobStatus
	if err := s.pluginAPI.KVGet(ReindexJobKey, &currentStatus); err != nil {
		s.pluginAPI.LogError("Failed to read job status for cancellation", "error", err)
		return
	}
	if currentStatus.JobID != jobStatus.JobID || currentStatus.Status != JobStatusCancelRequested {
		return
	}

	canceledStatus := currentStatus
	canceledStatus.Status = JobStatusCanceled
	canceledStatus.CompletedAt = time.Now()
	canceledStatus.ProcessedRows = jobStatus.ProcessedRows
	if ok, casErr := s.pluginAPI.KVCompareAndSet(ReindexJobKey, currentStatus, canceledStatus); casErr != nil {
		s.pluginAPI.LogError("Failed to record reindex cancellation", "error", casErr)
	} else if ok {
		jobStatus.Status = JobStatusCanceled
		jobStatus.CompletedAt = canceledStatus.CompletedAt
	}
	s.pluginAPI.LogWarn("Reindex job was canceled")
}
