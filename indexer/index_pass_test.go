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
	return func(cursor Cursor, limit int) ([]PostRecord, error) {
		if idx >= len(batches) {
			return nil, nil
		}
		b := batches[idx]
		idx++
		return b, nil
	}
}

func newPassTestIndexer(t *testing.T, workers, batchSize int) (*Indexer, *mocks.MockClient) {
	mockClient := mocks.NewMockClient(t)
	mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
	mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
	mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
	mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

	idx := New(nil, testConfigGetter(workers, batchSize), mockClient, &bots.MMBots{}, nil, nil)
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
			idx, _ := newPassTestIndexer(t, 1, 100)

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

func TestRunIndexPassWatermark(t *testing.T) {
	t.Run("out-of-order completion advances watermark contiguously", func(t *testing.T) {
		idx, _ := newPassTestIndexer(t, 2, 100)

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
			batchedFetch([][]PostRecord{b0, b1}), Cursor{}, 0)

		require.NoError(t, err)
		assert.Equal(t, int64(20), processed, "all posts from both batches should be counted")
		assert.Equal(t, b1[len(b1)-1].ID, watermark.LastID, "watermark should reach the end of the last batch")
		assert.Equal(t, int64(20), jobStatus.ProcessedRows)
	})

	t.Run("watermark holds at failed batch while later batches complete", func(t *testing.T) {
		idx, _ := newPassTestIndexer(t, 2, 100)
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
		jobStatus := &JobStatus{JobID: "watermark-fail-test", Status: JobStatusRunning}
		processed, watermark, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch,
			batchedFetch([][]PostRecord{b0, b1}), startCursor, 5)

		require.Error(t, err)
		assert.Equal(t, int64(5), processed, "no batch at or after the failure may count toward the checkpoint")
		assert.Equal(t, startCursor, watermark, "watermark must not advance past the failed batch")
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

		idx := New(nil, testConfigGetter(2, 100), mockClient, &bots.MMBots{}, nil, nil)

		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

		jobStatus := &JobStatus{JobID: "cancel-test", Status: JobStatusRunning}
		processed, watermark, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch,
			batchedFetch([][]PostRecord{makeTestPosts("b0", 5, 1000)}), Cursor{}, 0)

		require.ErrorIs(t, err, errCancelRequested)
		assert.Equal(t, int64(0), processed)
		assert.Equal(t, Cursor{}, watermark)
		mockSearch.AssertNotCalled(t, "Store", mock.Anything, mock.Anything)
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

		idx := New(nil, testConfigGetter(2, 100), mockClient, &bots.MMBots{}, nil, nil)

		mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
		mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil)

		batches := make([][]PostRecord, 6)
		for i := range batches {
			batches[i] = makeTestPosts(fmt.Sprintf("b%d", i), 100, int64((i+1)*1000))
		}

		jobStatus := &JobStatus{JobID: "checkpoint-test", Status: JobStatusRunning}
		processed, _, err := idx.runIndexPass(
			context.Background(), jobStatus, mockSearch,
			batchedFetch(batches), Cursor{}, 0)

		require.NoError(t, err)
		assert.Equal(t, int64(600), processed)
		require.NotEmpty(t, savedCursors, "checkpoint cursor should be saved once 500 posts completed")
	})
}
