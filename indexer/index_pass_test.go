// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	embeddingsmocks "github.com/mattermost/mattermost-plugin-agents/v2/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// testConfigGetter returns a configGetter with explicit reindex throughput
// settings so tests control batching and concurrency deterministically.
func testConfigGetter(workers, batchSize int) func() embeddings.EmbeddingSearchConfig {
	return func() embeddings.EmbeddingSearchConfig {
		return embeddings.EmbeddingSearchConfig{
			ReindexWorkers:   workers,
			ReindexBatchSize: batchSize,
		}
	}
}

func makeTestPosts(prefix string, n int, createAtBase int64) []PostRecord {
	posts := make([]PostRecord, n)
	for i := range posts {
		posts[i] = PostRecord{
			ID:          fmt.Sprintf("%s-post%03d", prefix, i),
			Message:     fmt.Sprintf("message %d", i),
			UserID:      "user1",
			ChannelID:   "channel1",
			ChannelType: string(model.ChannelTypeOpen),
			TeamID:      "team1",
			ChannelName: "town-square",
			CreateAt:    createAtBase + int64(i),
		}
	}
	return posts
}

// batchedFetch returns a fetchFunc serving the given batches in order,
// ignoring the cursor (the pass runner drives strictly forward).
func batchedFetch(batches [][]PostRecord) fetchFunc {
	idx := 0
	return func(ctx context.Context, cursor Cursor, limit int) ([]PostRecord, error) {
		if idx >= len(batches) {
			return nil, nil
		}
		b := batches[idx]
		idx++
		return b, nil
	}
}

func newPassTestIndexer(t *testing.T) (*Indexer, *mocks.MockClient) {
	mockClient := mocks.NewMockClient(t)
	mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
	mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
	mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
	mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

	idx := New(nil, nil, mockClient, &bots.MMBots{}, nil, nil)
	idx.storeRetryAttempts = 3
	idx.storeRetryBaseDelay = time.Millisecond
	return idx, mockClient
}

func TestStoreBatchWithRetry(t *testing.T) {
	posts := makeTestPosts("retry", 3, 1000)

	tests := []struct {
		name          string
		failuresFirst int32 // number of leading Store calls that fail
		posts         []PostRecord
		wantErr       bool
		wantCalls     int32
	}{
		{
			name:      "succeeds on first attempt",
			posts:     posts,
			wantCalls: 1,
		},
		{
			name:          "recovers from transient failures",
			failuresFirst: 2,
			posts:         posts,
			wantCalls:     3,
		},
		{
			name:          "fails after exhausting attempts",
			failuresFirst: 99,
			posts:         posts,
			wantErr:       true,
			wantCalls:     3, // storeRetryAttempts
		},
		{
			name: "skips store entirely when all posts are filtered",
			posts: []PostRecord{
				{ID: "empty", Message: "", ChannelID: "channel1", ChannelType: string(model.ChannelTypeOpen)},
			},
			wantCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, _ := newPassTestIndexer(t)

			var calls int32
			mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
			mockSearch.On("Store", mock.Anything, mock.Anything).
				Return(func(ctx context.Context, docs []embeddings.PostDocument) error {
					if atomic.AddInt32(&calls, 1) <= tt.failuresFirst {
						return errors.New("transient store failure")
					}
					return nil
				}).Maybe()

			err := idx.storeBatchWithRetry(context.Background(), mockSearch, tt.posts)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantCalls, atomic.LoadInt32(&calls))
		})
	}
}

func TestFinishJob(t *testing.T) {
	tests := []struct {
		name          string
		currentStatus string
		currentJobID  string
		wantCompleted bool
		wantWritten   string // expected terminal status CASed to KV; "" = no write
	}{
		{
			name:          "running resolves to completed",
			currentStatus: JobStatusRunning,
			currentJobID:  "job-1",
			wantCompleted: true,
			wantWritten:   JobStatusCompleted,
		},
		{
			name:          "late cancel_requested wins over completion",
			currentStatus: JobStatusCancelRequested,
			currentJobID:  "job-1",
			wantCompleted: false,
			wantWritten:   JobStatusCanceled,
		},
		{
			name:          "superseded run writes nothing",
			currentStatus: JobStatusRunning,
			currentJobID:  "successor-job",
			wantCompleted: false,
			wantWritten:   "",
		},
		{
			name:          "existing failed state is preserved",
			currentStatus: JobStatusFailed,
			currentJobID:  "job-1",
			wantCompleted: false,
			wantWritten:   "",
		},
		{
			name:          "existing canceled state is preserved",
			currentStatus: JobStatusCanceled,
			currentJobID:  "job-1",
			wantCompleted: false,
			wantWritten:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockClient(t)
			mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
				Run(func(args mock.Arguments) {
					status := args.Get(1).(*JobStatus)
					status.JobID = tt.currentJobID
					status.Status = tt.currentStatus
				}).
				Return(nil)

			var written *JobStatus
			mockClient.On("KVCompareAndSet", ReindexJobKey, mock.Anything, mock.AnythingOfType("indexer.JobStatus")).
				Run(func(args mock.Arguments) {
					status := args.Get(2).(JobStatus)
					written = &status
				}).
				Return(true, nil).Maybe()
			mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()

			idx := New(nil, nil, mockClient, &bots.MMBots{}, nil, nil)
			jobStatus := &JobStatus{JobID: "job-1", Status: JobStatusRunning, ProcessedRows: 7}

			completed := idx.finishJob(jobStatus)

			assert.Equal(t, tt.wantCompleted, completed)
			if tt.wantWritten == "" {
				assert.Nil(t, written, "no terminal write may happen for this state")
			} else {
				require.NotNil(t, written)
				assert.Equal(t, tt.wantWritten, written.Status)
				assert.Equal(t, tt.wantWritten, jobStatus.Status, "local status must mirror the persisted terminal state")
				assert.False(t, written.CompletedAt.IsZero())
			}
		})
	}

	t.Run("lost CAS against a racing cancel resolves to canceled on retry", func(t *testing.T) {
		// First read sees running (e.g. a stale replica), so the completion
		// CAS loses; the re-read observes the admin's cancel_requested and
		// must land in canceled rather than exhausting into a wedged row.
		var reads atomic.Int32
		mockClient := mocks.NewMockClient(t)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.JobID = "job-1"
				if reads.Add(1) == 1 {
					status.Status = JobStatusRunning
				} else {
					status.Status = JobStatusCancelRequested
				}
			}).
			Return(nil)

		var written *JobStatus
		mockClient.On("KVCompareAndSet", ReindexJobKey, mock.Anything, mock.AnythingOfType("indexer.JobStatus")).
			Return(func(key string, oldValue any, newValue any) (bool, error) {
				status := newValue.(JobStatus)
				if status.Status == JobStatusCompleted {
					return false, nil // predicate lost against the cancel
				}
				written = &status
				return true, nil
			})
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(nil, nil, mockClient, &bots.MMBots{}, nil, nil)
		jobStatus := &JobStatus{JobID: "job-1", Status: JobStatusRunning}

		completed := idx.finishJob(jobStatus)

		assert.False(t, completed)
		require.NotNil(t, written)
		assert.Equal(t, JobStatusCanceled, written.Status)
		assert.Equal(t, JobStatusCanceled, jobStatus.Status)
	})
}

func TestRunIndexPassWatermark(t *testing.T) {
	twoWorkers := passOptions{workers: 2, batchSize: 100}

	t.Run("out-of-order completion advances watermark contiguously", func(t *testing.T) {
		idx, _ := newPassTestIndexer(t)

		b0 := makeTestPosts("b0", 10, 1000)
		b1 := makeTestPosts("b1", 10, 2000)

		// Force b1 to complete before b0: b0's Store blocks until b1 is done.
		b1Done := make(chan struct{})
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Return(func(ctx context.Context, docs []embeddings.PostDocument) error {
				switch docs[0].PostID[:2] {
				case "b0":
					<-b1Done
				case "b1":
					defer close(b1Done)
				}
				return nil
			})

		jobStatus := &JobStatus{JobID: "watermark-test", Status: JobStatusRunning}
		processed, watermark, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch,
			batchedFetch([][]PostRecord{b0, b1}), Cursor{}, twoWorkers)

		require.NoError(t, err)
		assert.Equal(t, int64(20), processed, "all posts from both batches should be counted")
		assert.Equal(t, b1[len(b1)-1].ID, watermark.LastID, "watermark should reach the end of the last batch")
		assert.Equal(t, int64(20), jobStatus.ProcessedRows)
	})

	t.Run("watermark holds at failed batch while later batches complete", func(t *testing.T) {
		idx, _ := newPassTestIndexer(t)
		idx.storeRetryAttempts = 1 // fail fast, no retries

		b0 := makeTestPosts("b0", 10, 1000)
		b1 := makeTestPosts("b1", 10, 2000)

		// b1 succeeds while b0 fails after waiting for b1 to finish, so a
		// later batch has definitely committed when the earlier one errors.
		b1Done := make(chan struct{})
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Return(func(ctx context.Context, docs []embeddings.PostDocument) error {
				switch docs[0].PostID[:2] {
				case "b0":
					<-b1Done
					return errors.New("batch b0 store failure")
				case "b1":
					defer close(b1Done)
				}
				return nil
			})

		startCursor := Cursor{LastCreateAt: 42, LastID: "start"}
		jobStatus := &JobStatus{JobID: "watermark-fail-test", Status: JobStatusRunning, ProcessedRows: 5}
		processed, watermark, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch,
			batchedFetch([][]PostRecord{b0, b1}), startCursor, twoWorkers)

		require.Error(t, err)
		assert.Equal(t, int64(0), processed, "no batch at or after the failure may count toward the checkpoint")
		assert.Equal(t, int64(5), jobStatus.ProcessedRows, "resumed base count must be preserved")
		assert.Equal(t, startCursor, watermark, "watermark must not advance past the failed batch")
	})

	t.Run("single worker never stores past a failed batch", func(t *testing.T) {
		// The catch-up pass relies on this: its NOT EXISTS filter would
		// permanently skip (and undercount) any post stored ahead of the
		// failure watermark, so a worker must stop after its first error.
		idx, _ := newPassTestIndexer(t)
		idx.storeRetryAttempts = 1 // fail fast, no retries

		var stored []string
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Return(func(ctx context.Context, docs []embeddings.PostDocument) error {
				stored = append(stored, docs[0].PostID)
				return errors.New("store failure")
			})

		jobStatus := &JobStatus{JobID: "no-store-past-failure-test", Status: JobStatusRunning}
		processed, _, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch,
			batchedFetch([][]PostRecord{
				makeTestPosts("b0", 5, 1000),
				makeTestPosts("b1", 5, 2000),
			}), Cursor{}, passOptions{workers: 1, batchSize: 100})

		require.Error(t, err)
		assert.Equal(t, int64(0), processed)
		require.Len(t, stored, 1, "no store attempt may happen after a failed batch")
		assert.Contains(t, stored[0], "b0")
	})

	t.Run("worker panic fails the pass instead of crashing", func(t *testing.T) {
		idx, _ := newPassTestIndexer(t)

		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Return(func(ctx context.Context, docs []embeddings.PostDocument) error {
				panic("store exploded")
			})

		jobStatus := &JobStatus{JobID: "worker-panic-test", Status: JobStatusRunning}
		processed, watermark, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch,
			batchedFetch([][]PostRecord{makeTestPosts("b0", 5, 1000)}), Cursor{}, twoWorkers)

		require.ErrorContains(t, err, "store exploded")
		assert.Equal(t, int64(0), processed)
		assert.Equal(t, Cursor{}, watermark)
	})

	t.Run("fetcher panic fails the pass instead of crashing", func(t *testing.T) {
		idx, _ := newPassTestIndexer(t)

		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		panickyFetch := func(ctx context.Context, cursor Cursor, limit int) ([]PostRecord, error) {
			panic("fetch exploded")
		}

		jobStatus := &JobStatus{JobID: "fetch-panic-test", Status: JobStatusRunning}
		processed, _, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch, panickyFetch, Cursor{}, twoWorkers)

		require.ErrorContains(t, err, "fetch exploded")
		assert.Equal(t, int64(0), processed)
	})

	t.Run("cancel request stops dispatch and surfaces errCancelRequested", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.JobID = "cancel-test"
				status.Status = JobStatusCancelRequested
			}).
			Return(nil)
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(nil, nil, mockClient, &bots.MMBots{}, nil, nil)

		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		jobStatus := &JobStatus{JobID: "cancel-test", Status: JobStatusRunning}
		processed, watermark, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch,
			batchedFetch([][]PostRecord{makeTestPosts("b0", 5, 1000)}), Cursor{}, twoWorkers)

		require.ErrorIs(t, err, errCancelRequested)
		assert.Equal(t, int64(0), processed)
		assert.Equal(t, Cursor{}, watermark)
		mockSearch.AssertNotCalled(t, "Store", mock.Anything, mock.Anything)
	})

	t.Run("heartbeat and cancel polling continue while the oldest batch is slow", func(t *testing.T) {
		// The single worker is stuck in a store that only ends on abort, and
		// the fetcher is blocked dispatching the next batch — so neither can
		// observe the cancel. The committer's heartbeat loop must keep the
		// job status fresh and honor the cancel on its own.
		var cancelRequested atomic.Bool
		var heartbeats atomic.Int32

		mockClient := mocks.NewMockClient(t)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.JobID = "slow-batch-test"
				if cancelRequested.Load() {
					status.Status = JobStatusCancelRequested
				} else {
					status.Status = JobStatusRunning
				}
			}).
			Return(nil)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		// JobID-scoped status saves go through CAS; count them as heartbeats.
		mockClient.On("KVCompareAndSet", ReindexJobKey, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) { heartbeats.Add(1) }).
			Return(true, nil).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(nil, nil, mockClient, &bots.MMBots{}, nil, nil)
		idx.heartbeatInterval = 5 * time.Millisecond

		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Return(func(ctx context.Context, docs []embeddings.PostDocument) error {
				<-ctx.Done() // hang until the pass aborts us
				return ctx.Err()
			}).Maybe()

		// Endless supply of batches: the fetcher blocks dispatching the
		// second one, so its own cancel poll never runs again.
		fetch := func(ctx context.Context, cursor Cursor, limit int) ([]PostRecord, error) {
			return makeTestPosts("slow", 5, 1000), nil
		}

		go func() {
			time.Sleep(30 * time.Millisecond)
			cancelRequested.Store(true)
		}()

		jobStatus := &JobStatus{JobID: "slow-batch-test", Status: JobStatusRunning}
		processed, _, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch,
			fetch, Cursor{}, passOptions{workers: 1, batchSize: 100})

		require.ErrorIs(t, err, errCancelRequested,
			"the committer must observe the cancel when fetcher and workers are stuck")
		assert.Equal(t, int64(0), processed)
		assert.Positive(t, heartbeats.Load(),
			"job status must be heartbeated while waiting on a slow batch")
	})

	t.Run("terminates when context is canceled while a batch is stuck", func(t *testing.T) {
		// With one worker busy on b0, the fetcher blocks dispatching b1.
		// Canceling the parent context must unwind the fetcher, workers,
		// and committer without hanging, and must surface as an error so
		// the truncated pass isn't mistaken for completion.
		idx, _ := newPassTestIndexer(t)

		b0 := makeTestPosts("b0", 10, 1000)
		b1 := makeTestPosts("b1", 10, 2000)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// The worker stays busy on b0 until released, which happens only
		// after cancellation — so the fetcher cannot dispatch b1.
		b0Started := make(chan struct{})
		b0Release := make(chan struct{})
		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockSearch.On("Store", mock.Anything, mock.Anything).
			Return(func(storeCtx context.Context, docs []embeddings.PostDocument) error {
				if docs[0].PostID[:2] == "b0" {
					close(b0Started)
					<-b0Release
				}
				return nil
			}).Maybe()

		go func() {
			<-b0Started
			// Give the fetcher time to block dispatching b1.
			time.Sleep(50 * time.Millisecond)
			cancel()
			time.Sleep(50 * time.Millisecond)
			close(b0Release)
		}()

		jobStatus := &JobStatus{JobID: "stuck-cancel-test", Status: JobStatusRunning}
		processed, watermark, err := idx.runIndexPass(
			ctx, jobStatus, mockSearch,
			batchedFetch([][]PostRecord{b0, b1}), Cursor{}, passOptions{workers: 1, batchSize: 100})

		// b0 completes successfully (its store returns nil after release);
		// b1 was never dispatched, so the cancellation surfaces as the
		// pass error instead of a phantom completion.
		assert.Equal(t, int64(10), processed)
		assert.Equal(t, b0[len(b0)-1].ID, watermark.LastID)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("checkpoint saved after 500 contiguous posts", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		var savedCursors []Cursor
		mockClient.On("KVSet", IndexerCursorKey, mock.AnythingOfType("indexer.Cursor")).
			Run(func(args mock.Arguments) {
				savedCursors = append(savedCursors, args.Get(1).(Cursor))
			}).
			Return(nil)
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(nil, nil, mockClient, &bots.MMBots{}, nil, nil)

		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil)

		batches := make([][]PostRecord, 6)
		for i := range batches {
			batches[i] = makeTestPosts(fmt.Sprintf("b%d", i), 100, int64((i+1)*1000))
		}

		jobStatus := &JobStatus{JobID: "checkpoint-test", Status: JobStatusRunning}
		processed, _, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch,
			batchedFetch(batches), Cursor{}, twoWorkers)

		require.NoError(t, err)
		assert.Equal(t, int64(600), processed)
		require.NotEmpty(t, savedCursors, "checkpoint cursor should be saved once 500 posts completed")
	})
}
