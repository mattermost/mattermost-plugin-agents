// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
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

// callRecorder captures the order of vector store and search operations so
// tests can assert the deferred-index lifecycle ordering.
type callRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *callRecorder) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *callRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// firstIndex returns the position of the first occurrence of call, or -1.
func (r *callRecorder) firstIndex(call string) int {
	return slices.Index(r.snapshot(), call)
}

// fakeBulkIndexer implements embeddings.BulkIndexer, recording calls and
// failing or blocking on demand.
type fakeBulkIndexer struct {
	rec           *callRecorder
	prepareErr    error
	finalizeErr   error
	finalizeDelay time.Duration
	indexExists   bool
	existsErr     error
}

func (f *fakeBulkIndexer) PrepareBulkIndex(ctx context.Context) error {
	f.rec.record("prepare")
	return f.prepareErr
}

func (f *fakeBulkIndexer) FinalizeBulkIndex(ctx context.Context) error {
	f.rec.record("finalize")
	if f.finalizeDelay > 0 {
		time.Sleep(f.finalizeDelay)
	}
	return f.finalizeErr
}

func (f *fakeBulkIndexer) VectorIndexExists(ctx context.Context) (bool, error) {
	return f.indexExists, f.existsErr
}

// fakeDeferSearch implements embeddings.EmbeddingSearch and
// embeddings.BulkIndexerProvider, recording calls. Store calls are tagged
// "store:main" or "store:catchup" based on the batch's CreateAt relative to
// mainCutoff, so ordering assertions can distinguish the two passes.
type fakeDeferSearch struct {
	rec        *callRecorder
	bulk       embeddings.BulkIndexer // nil = store without bulk support
	mainCutoff int64
	clearErr   error
	clearPanic bool
	storeErr   error
}

func (s *fakeDeferSearch) Store(ctx context.Context, docs []embeddings.PostDocument) error {
	tag := "store:main"
	if len(docs) > 0 && docs[0].CreateAt > s.mainCutoff {
		tag = "store:catchup"
	}
	s.rec.record(tag)
	return s.storeErr
}

func (s *fakeDeferSearch) Search(ctx context.Context, query string, opts embeddings.SearchOptions) ([]embeddings.SearchResult, error) {
	return nil, nil
}

func (s *fakeDeferSearch) Delete(ctx context.Context, postIDs []string) error {
	return nil
}

func (s *fakeDeferSearch) Clear(ctx context.Context) error {
	if s.clearPanic {
		panic("clear panicked")
	}
	s.rec.record("clear")
	return s.clearErr
}

func (s *fakeDeferSearch) DeleteOrphaned(ctx context.Context, nowTime, batchSize int64) (int64, error) {
	return 0, nil
}

func (s *fakeDeferSearch) BulkIndexer() embeddings.BulkIndexer {
	return s.bulk
}

// vectorStateTracker tracks vector index state KV writes/deletes through a
// mock client.
type vectorStateTracker struct {
	mu      sync.Mutex
	saved   []VectorIndexState
	deleted bool
}

func (tr *vectorStateTracker) savedStates() []VectorIndexState {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return slices.Clone(tr.saved)
}

func (tr *vectorStateTracker) wasDeleted() bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.deleted
}

// mockVectorStateOps wires VectorIndexStateKey KVSet/KVDelete expectations
// into the tracker. Call before adding catch-all KVSet/KVDelete mocks.
func mockVectorStateOps(mockClient *mocks.MockClient, tracker *vectorStateTracker) {
	mockClient.On("KVSet", VectorIndexStateKey, mock.AnythingOfType("indexer.VectorIndexState")).
		Run(func(args mock.Arguments) {
			tracker.mu.Lock()
			tracker.saved = append(tracker.saved, args.Get(1).(VectorIndexState))
			tracker.mu.Unlock()
		}).
		Return(nil).Maybe()
	mockClient.On("KVDelete", VectorIndexStateKey).
		Run(func(args mock.Arguments) {
			tracker.mu.Lock()
			tracker.deleted = true
			tracker.mu.Unlock()
		}).
		Return(nil).Maybe()
}

func TestRunReindexJobDeferLifecycle(t *testing.T) {
	newDeferJobStatus := func(jobID string, cutoff int64) *JobStatus {
		return &JobStatus{
			JobID:     jobID,
			Status:    JobStatusRunning,
			StartedAt: time.Now(),
			CutoffAt:  cutoff,
		}
	}

	t.Run("defer mode ordering: prepare, clear, main pass, finalize, catch-up, completed", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		mainCutoff := now - 5000
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		for i := 0; i < 5; i++ {
			_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ($1, $2, 0, $3, '', 'channel1')",
				fmt.Sprintf("main-post%d", i), now-10000+int64(i), fmt.Sprintf("Main %d", i))
			require.NoError(t, err)
		}
		// One post after the cutoff for the catch-up pass to sweep.
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ('catchup-post', $1, 0, 'Catch up', '', 'channel1')", now-1000)
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: mainCutoff}

		tracker := &vectorStateTracker{}
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus("job-defer-ordering", mainCutoff)
		idx.runReindexJob(jobStatus, true, true)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)

		calls := rec.snapshot()
		prepareIdx := rec.firstIndex("prepare")
		clearIdx := rec.firstIndex("clear")
		mainIdx := rec.firstIndex("store:main")
		finalizeIdx := rec.firstIndex("finalize")
		catchupIdx := rec.firstIndex("store:catchup")
		require.NotEqual(t, -1, prepareIdx, "PrepareBulkIndex not called; calls: %v", calls)
		require.NotEqual(t, -1, clearIdx, "Clear not called; calls: %v", calls)
		require.NotEqual(t, -1, mainIdx, "main pass Store not called; calls: %v", calls)
		require.NotEqual(t, -1, finalizeIdx, "FinalizeBulkIndex not called; calls: %v", calls)
		require.NotEqual(t, -1, catchupIdx, "catch-up Store not called; calls: %v", calls)
		assert.Less(t, prepareIdx, clearIdx, "prepare must precede clear; calls: %v", calls)
		assert.Less(t, clearIdx, mainIdx, "clear must precede the main pass; calls: %v", calls)
		assert.Less(t, mainIdx, finalizeIdx, "main pass must precede finalize; calls: %v", calls)
		assert.Less(t, finalizeIdx, catchupIdx, "finalize must precede the catch-up pass; calls: %v", calls)

		// The building phase was recorded and the state cleared on success.
		states := tracker.savedStates()
		require.NotEmpty(t, states)
		assert.Equal(t, VectorIndexState{JobID: "job-defer-ordering", Phase: VectorIndexPhaseBuilding}, states[len(states)-1])
		assert.True(t, tracker.wasDeleted(), "vector index state must be cleared after a successful build")
	})

	t.Run("finalize failure fails the job and reverts the phase to dropped", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ('post1', $1, 0, 'Message', '', 'channel1')", now-10000)
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec, finalizeErr: errors.New("build blew up")}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: now}

		tracker := &vectorStateTracker{}
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus("job-finalize-fail", now-5000)
		idx.runReindexJob(jobStatus, true, true)

		assert.Equal(t, JobStatusFailed, jobStatus.Status, "a failed index build must never complete the job")
		assert.Contains(t, jobStatus.Error, "Failed to rebuild vector index")
		assert.False(t, tracker.wasDeleted(), "phase state must stay in place when the build fails")
		// Nothing is building anymore: the phase reverts to dropped with
		// the owning job preserved so a resume can take ownership cleanly.
		states := tracker.savedStates()
		require.NotEmpty(t, states)
		assert.Equal(t, VectorIndexState{JobID: "job-finalize-fail", Phase: VectorIndexPhaseDropped}, states[len(states)-1])
	})

	t.Run("main pass failure attempts finalize before recording failed", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ('post1', $1, 0, 'Message', '', 'channel1')", now-10000)
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: now, storeErr: errors.New("store blew up")}

		tracker := &vectorStateTracker{}
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)
		idx.storeRetryAttempts = 1 // fail fast instead of exponential backoff

		jobStatus := newDeferJobStatus("job-mainpass-fail", now-5000)
		idx.runReindexJob(jobStatus, true, true)

		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Failed to index posts")
		finalizeIdx := rec.firstIndex("finalize")
		require.NotEqual(t, -1, finalizeIdx, "finalize must be attempted on main-pass failure; calls: %v", rec.snapshot())
		assert.Less(t, rec.firstIndex("store:main"), finalizeIdx)
		assert.True(t, tracker.wasDeleted(), "state must be cleared after a successful restore rebuild")
	})

	t.Run("main pass failure with failed rebuild surfaces both errors", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ('post1', $1, 0, 'Message', '', 'channel1')", now-10000)
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec, finalizeErr: errors.New("out of shared memory")}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: now, storeErr: errors.New("store blew up")}

		tracker := &vectorStateTracker{}
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)
		idx.storeRetryAttempts = 1 // fail fast instead of exponential backoff

		jobStatus := newDeferJobStatus("job-mainpass-rebuild-fail", now-5000)
		idx.runReindexJob(jobStatus, true, true)

		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Failed to index posts")
		assert.Contains(t, jobStatus.Error, "additionally failed to rebuild vector index")
		assert.Contains(t, jobStatus.Error, "out of shared memory")
		assert.False(t, tracker.wasDeleted(), "phase state must stay in place when the restore rebuild fails")
		states := tracker.savedStates()
		require.NotEmpty(t, states)
		assert.Equal(t, VectorIndexState{JobID: "job-mainpass-rebuild-fail", Phase: VectorIndexPhaseDropped}, states[len(states)-1])
	})

	t.Run("cancel during the main pass rebuilds the index before acknowledging", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ('post1', $1, 0, 'Message', '', 'channel1')", now-10000)
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: now}

		jobID := "job-defer-cancel"
		tracker := &vectorStateTracker{}
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		// Cancel is already requested when the pass polls the job row.
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.JobID = jobID
				status.Status = JobStatusCancelRequested
			}).
			Return(nil)
		mockClient.On("KVGet", IndexerCursorKey, mock.AnythingOfType("*indexer.Cursor")).
			Return(mmapi.ErrKVNotFound)
		mockClient.On("KVCompareAndSet", ReindexJobKey, mock.Anything, mock.Anything).Return(true, nil)
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogWarn", mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus(jobID, now-5000)
		idx.runReindexJob(jobStatus, true, true)

		assert.Equal(t, JobStatusCanceled, jobStatus.Status)
		require.NotEqual(t, -1, rec.firstIndex("finalize"),
			"the index must be rebuilt before the cancel terminalizes; calls: %v", rec.snapshot())
		assert.True(t, tracker.wasDeleted(), "state must be cleared after the cancel-path rebuild")
	})

	t.Run("cancel with a failed rebuild records the rebuild error on the canceled status", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ('post1', $1, 0, 'Message', '', 'channel1')", now-10000)
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec, finalizeErr: errors.New("out of shared memory")}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: now}

		jobID := "job-cancel-rebuild-fail"
		tracker := &vectorStateTracker{}
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		// Cancel is already requested when the pass polls the job row.
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.JobID = jobID
				status.Status = JobStatusCancelRequested
			}).
			Return(nil)
		mockClient.On("KVGet", IndexerCursorKey, mock.AnythingOfType("*indexer.Cursor")).
			Return(mmapi.ErrKVNotFound)
		var terminal JobStatus
		mockClient.On("KVCompareAndSet", ReindexJobKey, mock.Anything, mock.AnythingOfType("indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				terminal = args.Get(2).(JobStatus)
			}).
			Return(true, nil)
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogWarn", mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus(jobID, now-5000)
		idx.runReindexJob(jobStatus, true, true)

		assert.Equal(t, JobStatusCanceled, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Failed to rebuild vector index")
		assert.Contains(t, jobStatus.Error, "out of shared memory")
		// The persisted terminal row must carry the rebuild failure, not
		// just the server log.
		assert.Equal(t, JobStatusCanceled, terminal.Status)
		assert.Contains(t, terminal.Error, "Failed to rebuild vector index")
		assert.Contains(t, terminal.Error, "out of shared memory")
		// The failed rebuild reverts the phase to dropped with the owning
		// job preserved so a resume can take ownership cleanly.
		assert.False(t, tracker.wasDeleted(), "phase state must stay in place when the rebuild fails")
		states := tracker.savedStates()
		require.NotEmpty(t, states)
		assert.Equal(t, VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped}, states[len(states)-1])
	})

	t.Run("panic attempts finalize and fails the job", func(t *testing.T) {
		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, clearPanic: true}

		tracker := &vectorStateTracker{}
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, nil, nil)

		jobStatus := newDeferJobStatus("job-defer-panic", 0)
		idx.runReindexJob(jobStatus, true, true)

		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Job panicked")
		require.NotEqual(t, -1, rec.firstIndex("finalize"),
			"finalize must be attempted from the panic recovery path; calls: %v", rec.snapshot())
		assert.Less(t, rec.firstIndex("prepare"), rec.firstIndex("finalize"))
		assert.True(t, tracker.wasDeleted())
	})

	t.Run("maintain mode never calls prepare or finalize", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ('post1', $1, 0, 'Message', '', 'channel1')", now-10000)
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: now}

		mockClient := mocks.NewMockClient(t)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus("job-maintain", now-5000)
		idx.runReindexJob(jobStatus, true, false)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)
		assert.Equal(t, -1, rec.firstIndex("prepare"), "prepare must not run in maintain mode")
		assert.Equal(t, -1, rec.firstIndex("finalize"), "finalize must not run in maintain mode")
	})

	t.Run("defer without bulk support falls back to maintain mode and clears the claim", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ('post1', $1, 0, 'Message', '', 'channel1')", now-10000)
		require.NoError(t, err)

		rec := &callRecorder{}
		search := &fakeDeferSearch{rec: rec, bulk: nil, mainCutoff: now}

		tracker := &vectorStateTracker{}
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything).Return().Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus("job-no-bulk", now-5000)
		idx.runReindexJob(jobStatus, true, true)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)
		assert.Equal(t, -1, rec.firstIndex("prepare"))
		assert.Equal(t, -1, rec.firstIndex("finalize"))
		assert.True(t, tracker.wasDeleted(), "the claimed state must be cleared when falling back")
	})

	t.Run("resume of a deferred job skips clear but finalizes at the end", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId) VALUES ('post1', $1, 0, 'Message', '', 'channel1')", now-10000)
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: now}

		tracker := &vectorStateTracker{}
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		// Resume path: clearIndex=false, defer ownership already resolved.
		jobStatus := newDeferJobStatus("job-defer-resume", now-5000)
		idx.runReindexJob(jobStatus, false, true)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)
		assert.Equal(t, -1, rec.firstIndex("clear"), "a resume must not clear the index")
		prepareIdx := rec.firstIndex("prepare")
		finalizeIdx := rec.firstIndex("finalize")
		require.NotEqual(t, -1, prepareIdx)
		require.NotEqual(t, -1, finalizeIdx)
		assert.Less(t, prepareIdx, finalizeIdx)
		assert.True(t, tracker.wasDeleted())
	})
}

func TestResolveDeferredRebuild(t *testing.T) {
	deferCfg := func() embeddings.EmbeddingSearchConfig {
		return embeddings.EmbeddingSearchConfig{ReindexIndexStrategy: embeddings.ReindexIndexStrategyDefer}
	}
	maintainCfg := func() embeddings.EmbeddingSearchConfig {
		return embeddings.EmbeddingSearchConfig{}
	}
	bulkSearch := func(rec *callRecorder) embeddings.EmbeddingSearch {
		return &fakeDeferSearch{rec: rec, bulk: &fakeBulkIndexer{rec: rec}}
	}
	nonBulkSearch := func(rec *callRecorder) embeddings.EmbeddingSearch {
		return &fakeDeferSearch{rec: rec, bulk: nil}
	}

	tests := []struct {
		name          string
		clearIndex    bool
		configGetter  func() embeddings.EmbeddingSearchConfig
		search        func(rec *callRecorder) embeddings.EmbeddingSearch
		existingState *VectorIndexState
		saveErr       error
		want          bool
		wantSaved     *VectorIndexState
	}{
		{
			name:         "fresh reindex with maintain strategy",
			clearIndex:   true,
			configGetter: maintainCfg,
			search:       bulkSearch,
			want:         false,
		},
		{
			name:         "fresh reindex with defer strategy and bulk-capable store",
			clearIndex:   true,
			configGetter: deferCfg,
			search:       bulkSearch,
			want:         true,
			wantSaved:    &VectorIndexState{JobID: "new-job", Phase: VectorIndexPhaseDropped},
		},
		{
			name:         "fresh reindex with defer strategy but store without bulk support",
			clearIndex:   true,
			configGetter: deferCfg,
			search:       nonBulkSearch,
			want:         false,
		},
		{
			name:         "fresh reindex with defer strategy but state persistence failure",
			clearIndex:   true,
			configGetter: deferCfg,
			search:       bulkSearch,
			saveErr:      errors.New("kv down"),
			want:         false,
		},
		{
			name:         "resume without leftover state",
			clearIndex:   false,
			configGetter: maintainCfg,
			search:       bulkSearch,
			want:         false,
		},
		{
			name:          "resume takes ownership of leftover state regardless of config",
			clearIndex:    false,
			configGetter:  maintainCfg,
			search:        bulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseDropped},
			want:          true,
			wantSaved:     &VectorIndexState{JobID: "new-job", Phase: VectorIndexPhaseDropped},
		},
		{
			name:          "resume with leftover state but store without bulk support",
			clearIndex:    false,
			configGetter:  maintainCfg,
			search:        nonBulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseDropped},
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &callRecorder{}
			mockClient := mocks.NewMockClient(t)

			if tt.existingState != nil {
				mockClient.On("KVGet", VectorIndexStateKey, mock.AnythingOfType("*indexer.VectorIndexState")).
					Run(func(args mock.Arguments) {
						*args.Get(1).(*VectorIndexState) = *tt.existingState
					}).
					Return(nil).Maybe()
			} else {
				mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).
					Return(mmapi.ErrKVNotFound).Maybe()
			}

			var saved *VectorIndexState
			mockClient.On("KVSet", VectorIndexStateKey, mock.AnythingOfType("indexer.VectorIndexState")).
				Run(func(args mock.Arguments) {
					state := args.Get(1).(VectorIndexState)
					saved = &state
				}).
				Return(tt.saveErr).Maybe()
			mockClient.On("LogWarn", mock.Anything).Return().Maybe()
			mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
			mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

			search := tt.search(rec)
			idx := New(func() embeddings.EmbeddingSearch { return search }, tt.configGetter, mockClient, nil, nil, nil)

			got := idx.resolveDeferredRebuild(tt.clearIndex, "new-job")
			assert.Equal(t, tt.want, got)

			if tt.wantSaved != nil {
				require.NotNil(t, saved, "expected the state to be persisted")
				assert.Equal(t, *tt.wantSaved, *saved)
			}
		})
	}
}

func TestFinalizeDeferredIndexHeartbeat(t *testing.T) {
	rec := &callRecorder{}
	bulk := &fakeBulkIndexer{rec: rec, finalizeDelay: 100 * time.Millisecond}

	var mu sync.Mutex
	heartbeatSaves := 0
	mockClient := mocks.NewMockClient(t)
	mockClient.On("KVSet", VectorIndexStateKey, mock.Anything).Return(nil)
	mockClient.On("KVSet", ReindexJobKey, mock.Anything).
		Run(func(args mock.Arguments) {
			mu.Lock()
			heartbeatSaves++
			mu.Unlock()
		}).
		Return(nil).Maybe()
	mockClient.On("KVGet", ReindexJobKey, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
	mockClient.On("KVDelete", VectorIndexStateKey).Return(nil)
	mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

	idx := New(nil, nil, mockClient, nil, nil, nil)
	idx.heartbeatInterval = 5 * time.Millisecond

	// Empty JobID makes saveJobStatus an unconditional KVSet, so heartbeat
	// ticks are directly countable.
	jobStatus := &JobStatus{Status: JobStatusRunning, StartedAt: time.Now()}
	before := time.Now()

	err := idx.finalizeDeferredIndex(context.Background(), jobStatus, bulk)
	require.NoError(t, err)

	mu.Lock()
	saves := heartbeatSaves
	mu.Unlock()
	assert.GreaterOrEqual(t, saves, 2, "the heartbeat must keep ticking while the index build blocks")
	assert.True(t, jobStatus.LastUpdatedAt.After(before), "the heartbeat must advance LastUpdatedAt")
}

func TestIndexPostDeferGating(t *testing.T) {
	post := &model.Post{
		Id:        "post1",
		Message:   "Test message",
		Type:      model.PostTypeDefault,
		UserId:    "user1",
		ChannelId: "channel1",
		CreateAt:  1234567890,
	}
	channel := &model.Channel{
		Id:     "channel1",
		TeamId: "team1",
		Type:   model.ChannelTypeOpen,
	}

	tests := []struct {
		name      string
		state     *VectorIndexState
		wantStore bool
	}{
		{
			name:      "no state indexes normally",
			state:     nil,
			wantStore: true,
		},
		{
			name:      "dropped phase still indexes (index-free inserts are cheap)",
			state:     &VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseDropped},
			wantStore: true,
		},
		{
			name:      "building phase skips live indexing",
			state:     &VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseBuilding},
			wantStore: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockClient(t)
			mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

			if tt.state != nil {
				mockClient.On("KVGet", VectorIndexStateKey, mock.AnythingOfType("*indexer.VectorIndexState")).
					Run(func(args mock.Arguments) {
						*args.Get(1).(*VectorIndexState) = *tt.state
					}).
					Return(nil)
			} else {
				mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).
					Return(mmapi.ErrKVNotFound)
			}
			mockClient.On("LogDebug", mock.Anything, mock.Anything).Return().Maybe()

			if tt.wantStore {
				mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil).Once()
			}

			idx := New(func() embeddings.EmbeddingSearch { return mockSearch }, nil, mockClient, &bots.MMBots{}, nil, nil)
			err := idx.IndexPost(context.Background(), post, channel)
			require.NoError(t, err)
		})
	}
}

func TestDeferredIndexRebuildActive(t *testing.T) {
	t.Run("nil client reports inactive", func(t *testing.T) {
		assert.False(t, DeferredIndexRebuildActive(nil))
	})

	t.Run("absent state reports inactive", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).Return(mmapi.ErrKVNotFound)
		assert.False(t, DeferredIndexRebuildActive(mockClient))
	})

	t.Run("existing state reports active", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockClient.On("KVGet", VectorIndexStateKey, mock.AnythingOfType("*indexer.VectorIndexState")).
			Run(func(args mock.Arguments) {
				*args.Get(1).(*VectorIndexState) = VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseDropped}
			}).
			Return(nil)
		assert.True(t, DeferredIndexRebuildActive(mockClient))
	})
}

func TestReconcileVectorIndexState(t *testing.T) {
	stubState := VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseDropped}

	tests := []struct {
		name        string
		state       *VectorIndexState
		indexExists bool
		existsErr   error
		jobRow      *JobStatus
		wantErr     bool
		wantDeleted bool
	}{
		{
			name:  "no leftover state is a no-op",
			state: nil,
		},
		{
			name:        "stale state with a valid index in the catalog is cleared",
			state:       &stubState,
			indexExists: true,
			wantDeleted: true,
		},
		{
			name:  "missing index with a live owning job is left for the job to rebuild",
			state: &stubState,
			jobRow: &JobStatus{
				JobID:         "job1",
				Status:        JobStatusRunning,
				StartedAt:     time.Now(),
				LastUpdatedAt: time.Now(),
			},
		},
		{
			name:  "missing index without an owning job keeps the state and search stays gated",
			state: &stubState,
		},
		{
			name:      "catalog check failure is propagated",
			state:     &stubState,
			existsErr: errors.New("catalog unavailable"),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &callRecorder{}
			bulk := &fakeBulkIndexer{rec: rec, indexExists: tt.indexExists, existsErr: tt.existsErr}
			search := &fakeDeferSearch{rec: rec, bulk: bulk}

			mockClient := mocks.NewMockClient(t)
			if tt.state != nil {
				mockClient.On("KVGet", VectorIndexStateKey, mock.AnythingOfType("*indexer.VectorIndexState")).
					Run(func(args mock.Arguments) {
						*args.Get(1).(*VectorIndexState) = *tt.state
					}).
					Return(nil)
			} else {
				mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).
					Return(mmapi.ErrKVNotFound)
			}
			if tt.jobRow != nil {
				mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
					Run(func(args mock.Arguments) {
						*args.Get(1).(*JobStatus) = *tt.jobRow
					}).
					Return(nil).Maybe()
			} else {
				mockClient.On("KVGet", ReindexJobKey, mock.Anything).
					Return(mmapi.ErrKVNotFound).Maybe()
			}

			deleted := false
			mockClient.On("KVDelete", VectorIndexStateKey).
				Run(func(args mock.Arguments) { deleted = true }).
				Return(nil).Maybe()
			mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
			mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

			idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, nil, nil, nil)

			err := idx.ReconcileVectorIndexState(context.Background())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantDeleted, deleted)
		})
	}
}
