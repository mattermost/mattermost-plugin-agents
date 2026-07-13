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
	seq    int64
	posts  []PostRecord
	cursor Cursor // keyset position after this batch
}

// batchDone reports a processed batch back to the watermark tracker.
type batchDone struct {
	seq    int64
	cursor Cursor
	count  int64
	err    error
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
// embed, and store them concurrently. The persisted checkpoint (cursor +
// processed count) only ever advances across a contiguous prefix of completed
// batches, so a resume after failure can never skip a batch that was still in
// flight. Batches committed ahead of the watermark may be re-processed on
// resume, which Store makes idempotent.
//
// Returns the processed count and watermark cursor (both reflecting only the
// contiguous completed prefix) and the first error encountered, which is
// errCancelRequested when the admin canceled the job.
func (s *Indexer) runIndexPass(
	ctx context.Context,
	jobStatus *JobStatus,
	search embeddings.EmbeddingSearch,
	fetch fetchFunc,
	startCursor Cursor,
	startProcessed int64,
) (int64, Cursor, error) {
	workers, batchSize := s.reindexSettings()

	ctx, cancelPass := context.WithCancel(ctx)
	defer cancelPass()

	workCh := make(chan batchWork)
	doneCh := make(chan batchDone)
	fetchErrCh := make(chan error, 1)

	// Fetcher: sequential keyset pagination; also polls for cancel requests.
	// On cancel, dispatching stops but in-flight batches complete and count.
	go func() {
		defer close(workCh)
		cursor := startCursor
		var seq int64
		for {
			if canceled, err := s.isCancelRequested(jobStatus.JobID); err == nil && canceled {
				fetchErrCh <- errCancelRequested
				return
			}
			posts, err := fetch(cursor, batchSize)
			if err != nil {
				fetchErrCh <- fmt.Errorf("failed to fetch posts: %w", err)
				return
			}
			if len(posts) == 0 {
				return
			}
			last := posts[len(posts)-1]
			cursor = Cursor{LastCreateAt: last.CreateAt, LastID: last.ID}
			select {
			case workCh <- batchWork{seq: seq, posts: posts, cursor: cursor}:
			case <-ctx.Done():
				return
			}
			seq++
		}
	}()

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workCh {
				err := s.storeBatchWithRetry(ctx, search, work.posts)
				select {
				case doneCh <- batchDone{seq: work.seq, cursor: work.cursor, count: int64(len(work.posts)), err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	// Watermark tracker: the checkpoint advances over the contiguous prefix
	// of completed batches; out-of-order completions wait in pending.
	pending := make(map[int64]batchDone)
	var nextSeq int64
	processed := startProcessed
	lastSaved := startProcessed
	lastHeartbeatSave := time.Now()
	watermark := startCursor
	var firstErr error

	for done := range doneCh {
		if done.err != nil {
			if firstErr == nil {
				firstErr = done.err
				cancelPass() // stop the fetcher and abort in-flight workers
			}
			continue
		}

		pending[done.seq] = done
		for {
			d, ok := pending[nextSeq]
			if !ok {
				break
			}
			delete(pending, nextSeq)
			processed += d.count
			watermark = d.cursor
			nextSeq++
		}

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

	select {
	case err := <-fetchErrCh:
		if firstErr == nil {
			firstErr = err
		}
	default:
	}

	jobStatus.ProcessedRows = processed
	jobStatus.LastUpdatedAt = time.Now()
	return processed, watermark, firstErr
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
