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
	defaultHeartbeatInterval   = time.Minute
	finishRetryBaseDelay       = 200 * time.Millisecond
)

// batch is a unit of work flowing through an index pass: dispatched to a
// worker for embedding and storage, and queued in fetch order for the
// committer, which waits on result (buffered(1), resolved exactly once).
type batch struct {
	posts  []PostRecord
	cursor Cursor // keyset position after this batch
	count  int64
	result chan error
}

// fetchFunc returns the next batch of posts after the given cursor. It must
// honor ctx so an aborting pass can interrupt an in-flight query.
type fetchFunc func(ctx context.Context, cursor Cursor, limit int) ([]PostRecord, error)

// reindexSettings resolves worker count and batch size from plugin config,
// falling back to defaults when unconfigured.
func (s *Indexer) reindexSettings() (workers, batchSize int) {
	if s.configGetter == nil {
		return embeddings.DefaultReindexWorkers, embeddings.DefaultReindexBatchSize
	}
	cfg := s.configGetter()
	return cfg.GetReindexWorkers(), cfg.GetReindexBatchSize()
}

// passOptions sizes an index pass's worker pool and fetch batches.
type passOptions struct {
	workers   int
	batchSize int
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
	opts passOptions,
) (int64, Cursor, error) {
	workers, batchSize := opts.workers, opts.batchSize

	parentCtx := ctx
	ctx, cancelPass := context.WithCancel(ctx)
	defer cancelPass()

	workCh := make(chan *batch)
	// Capacity = workers keeps all workers dispatchable while the committer
	// waits on the oldest batch; a smaller buffer would serialize dispatch.
	orderedCh := make(chan *batch, workers)

	// Fetcher: sequential keyset pagination; also polls for cancel requests.
	// On cancel it stops dispatching, but already-dispatched batches complete
	// and count. fetchErr is published before the deferred close(orderedCh),
	// so the committer may read it once its range over orderedCh finishes.
	var fetchErr error
	go func() {
		defer close(orderedCh)
		defer close(workCh)
		defer func() {
			if r := recover(); r != nil {
				fetchErr = fmt.Errorf("post fetcher panicked: %v", r)
			}
		}()
		cursor := startCursor
		for {
			if canceled, err := s.isCancelRequested(jobStatus.JobID); err == nil && canceled {
				fetchErr = errCancelRequested
				return
			}
			posts, err := fetch(ctx, cursor, batchSize)
			if err != nil {
				fetchErr = fmt.Errorf("failed to fetch posts: %w", err)
				return
			}
			if len(posts) == 0 {
				return
			}
			last := posts[len(posts)-1]
			cursor = Cursor{LastCreateAt: last.CreateAt, LastID: last.ID}

			b := &batch{posts: posts, cursor: cursor, count: int64(len(posts)), result: make(chan error, 1)}
			// Dispatch before queueing for commit: any batch the committer
			// sees is guaranteed to have a worker resolving it, so the
			// committer can never block on an undispatched batch.
			select {
			case workCh <- b:
			case <-ctx.Done():
				return
			}
			select {
			case orderedCh <- b:
			case <-ctx.Done():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range workCh {
				if err := s.safeStoreBatch(ctx, search, b.posts); err != nil {
					b.result <- err
					// Exit so this worker cannot store any batch after a
					// failed one: with a single worker this guarantees
					// nothing is ever stored ahead of the watermark, which
					// the catch-up pass's NOT EXISTS filter relies on.
					return
				}
				b.result <- nil
			}
		}()
	}

	// Committer: consume batches in fetch order, advancing the watermark one
	// contiguous batch at a time. While waiting — for the next batch or for a
	// slow oldest batch (e.g. retry backoff) — the heartbeat ticker keeps
	// stale-job detection at bay and cancellation responsive even if the
	// whole pipeline is stuck.
	heartbeat := time.NewTicker(s.heartbeatInterval)
	defer heartbeat.Stop()

	startProcessed := jobStatus.ProcessedRows
	processed := startProcessed
	lastSaved := startProcessed
	lastCheckpoint := time.Now()
	watermark := startCursor
	var firstErr error

	for {
		b, canceled, ok := s.waitForBatch(ctx, orderedCh, heartbeat.C, jobStatus)
		if !ok {
			if canceled {
				firstErr = errCancelRequested
				cancelPass()
			}
			break
		}

		canceled, err := s.waitForBatchResult(ctx, b, heartbeat.C, jobStatus)
		if canceled {
			firstErr = errCancelRequested
			cancelPass()
			break
		}
		if err != nil {
			firstErr = err
			cancelPass() // stop the fetcher and abort in-flight workers
			break
		}

		processed += b.count
		watermark = b.cursor
		jobStatus.ProcessedRows = processed
		jobStatus.LastUpdatedAt = time.Now()

		// Save checkpoint every 500 posts or every 2 minutes (whichever
		// comes first) so a resume never has to redo much work.
		if processed >= lastSaved+500 || time.Since(lastCheckpoint) > 2*time.Minute {
			s.saveCursor(watermark)
			s.saveJobStatus(jobStatus)
			s.pluginAPI.LogWarn("Reindexing progress",
				"processed", processed,
				"estimated_total", jobStatus.TotalRows)
			lastSaved = processed
			lastCheckpoint = time.Now()
		}
	}

	cancelPass()
	wg.Wait()

	if firstErr == nil {
		// orderedCh is closed and drained, so the fetcher has exited and
		// fetchErr is safe to read.
		firstErr = fetchErr
	}
	if firstErr == nil {
		// A canceled parent context truncates the fetch loop silently; it
		// must not be mistaken for a completed pass.
		firstErr = parentCtx.Err()
	}

	jobStatus.ProcessedRows = processed
	jobStatus.LastUpdatedAt = time.Now()
	return processed - startProcessed, watermark, firstErr
}

// waitForBatch waits for the next ordered batch while heartbeating. ok is
// false when orderedCh is closed, ctx is canceled, or cancel was requested.
func (s *Indexer) waitForBatch(
	ctx context.Context,
	orderedCh <-chan *batch,
	heartbeat <-chan time.Time,
	jobStatus *JobStatus,
) (b *batch, canceled, ok bool) {
	for {
		select {
		case next, open := <-orderedCh:
			if !open {
				return nil, false, false
			}
			return next, false, true
		case <-heartbeat:
			if s.heartbeatTick(jobStatus) {
				return nil, true, false
			}
		case <-ctx.Done():
			return nil, false, false
		}
	}
}

// waitForBatchResult waits for a worker to resolve b while heartbeating.
// canceled is true when cancel was requested during the wait.
func (s *Indexer) waitForBatchResult(
	ctx context.Context,
	b *batch,
	heartbeat <-chan time.Time,
	jobStatus *JobStatus,
) (canceled bool, err error) {
	for {
		select {
		case err = <-b.result:
			return false, err
		case <-heartbeat:
			if s.heartbeatTick(jobStatus) {
				return true, nil
			}
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// heartbeatTick refreshes the job's heartbeat so stale-job detection sees
// forward progress, and reports whether the admin requested cancellation.
func (s *Indexer) heartbeatTick(jobStatus *JobStatus) (cancelRequested bool) {
	jobStatus.LastUpdatedAt = time.Now()
	s.saveJobStatus(jobStatus)
	canceled, err := s.isCancelRequested(jobStatus.JobID)
	return err == nil && canceled
}

// safeStoreBatch guards a worker against panics in filtering, embedding, or
// storage so one bad batch fails the job instead of crashing the plugin.
func (s *Indexer) safeStoreBatch(ctx context.Context, search embeddings.EmbeddingSearch, posts []PostRecord) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("batch store panicked: %v", r)
		}
	}()
	return s.storeBatchWithRetry(ctx, search, posts)
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
// mirrors the terminal state onto the worker's local status. A worker-side
// error (e.g. a failed vector index rebuild on the cancel exit path) is
// carried onto the terminal row so it is visible beyond the server log.
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
	if jobStatus.Error != "" {
		canceledStatus.Error = jobStatus.Error
	}
	if ok, casErr := s.pluginAPI.KVCompareAndSet(ReindexJobKey, currentStatus, canceledStatus); casErr != nil {
		s.pluginAPI.LogError("Failed to record reindex cancellation", "error", casErr)
	} else if ok {
		jobStatus.Status = JobStatusCanceled
		jobStatus.CompletedAt = canceledStatus.CompletedAt
	}
	s.pluginAPI.LogWarn("Reindex job was canceled")
}
