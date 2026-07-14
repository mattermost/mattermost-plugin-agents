// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
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
// failing or blocking on demand. onFinalize (when set) runs at the start of
// FinalizeBulkIndex, e.g. to simulate a post edit during the build window.
type fakeBulkIndexer struct {
	rec           *callRecorder
	prepareErr    error
	finalizeErr   error
	finalizeDelay time.Duration
	onFinalize    func()
	indexExists   bool
	existsErr     error
}

func (f *fakeBulkIndexer) PrepareBulkIndex(ctx context.Context) error {
	f.rec.record("prepare")
	return f.prepareErr
}

func (f *fakeBulkIndexer) FinalizeBulkIndex(ctx context.Context) error {
	f.rec.record("finalize")
	if f.onFinalize != nil {
		f.onFinalize()
	}
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
// mainCutoff, so ordering assertions can distinguish the two passes. Stored
// document contents are captured for content assertions.
type fakeDeferSearch struct {
	rec        *callRecorder
	bulk       embeddings.BulkIndexer // nil = store without bulk support
	mainCutoff int64
	clearErr   error
	clearPanic bool
	storeErr   error

	mu             sync.Mutex
	storedContents []string
}

func (s *fakeDeferSearch) Store(ctx context.Context, docs []embeddings.PostDocument) error {
	tag := "store:main"
	if len(docs) > 0 && docs[0].CreateAt > s.mainCutoff {
		tag = "store:catchup"
	}
	s.rec.record(tag)
	s.mu.Lock()
	for _, doc := range docs {
		s.storedContents = append(s.storedContents, doc.Content)
	}
	s.mu.Unlock()
	return s.storeErr
}

func (s *fakeDeferSearch) contents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.storedContents)
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

// vectorStateTracker simulates the durable phase-state KV row with real
// compare-and-set semantics, recording every applied write and delete.
type vectorStateTracker struct {
	mu      sync.Mutex
	current *VectorIndexState
	saved   []VectorIndexState
	deleted bool
}

func (tr *vectorStateTracker) seed(state VectorIndexState) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.current = &state
}

func (tr *vectorStateTracker) currentState() *VectorIndexState {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.current == nil {
		return nil
	}
	state := *tr.current
	return &state
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

// mockVectorStateOps wires VectorIndexStateKey reads and compare-and-set
// mutations to the tracker. Call before adding catch-all KVGet /
// KVCompareAndSet mocks so the key-specific expectations match first.
func mockVectorStateOps(mockClient *mocks.MockClient, tracker *vectorStateTracker) {
	mockClient.On("KVGet", VectorIndexStateKey, mock.AnythingOfType("*indexer.VectorIndexState")).
		Return(func(key string, value interface{}) error {
			tracker.mu.Lock()
			defer tracker.mu.Unlock()
			if tracker.current == nil {
				return mmapi.ErrKVNotFound
			}
			*value.(*VectorIndexState) = *tracker.current
			return nil
		}).Maybe()
	mockClient.On("KVCompareAndSet", VectorIndexStateKey, mock.Anything, mock.Anything).
		Return(func(key string, oldValue, newValue interface{}) (bool, error) {
			tracker.mu.Lock()
			defer tracker.mu.Unlock()
			if oldValue == nil {
				if tracker.current != nil {
					return false, nil
				}
			} else {
				old, ok := oldValue.(VectorIndexState)
				if !ok || tracker.current == nil || *tracker.current != old {
					return false, nil
				}
			}
			if newValue == nil {
				tracker.current = nil
				tracker.deleted = true
				return true, nil
			}
			state := newValue.(VectorIndexState)
			tracker.current = &state
			tracker.saved = append(tracker.saved, state)
			return true, nil
		}).Maybe()
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
	// runReindexJob is called with the claim already made (StartReindexJob
	// resolves it under the cluster mutex), so defer tests seed the state
	// and hand the same in-memory value to the run, mirroring the CAS-proof
	// contract of resolveDeferredRebuild.
	seedClaim := func(tracker *vectorStateTracker, jobID string) *deferredRun {
		state := VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseDropped}
		tracker.seed(state)
		return &deferredRun{state: state, adopted: false}
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
		deferRun := seedClaim(tracker, "job-defer-ordering")
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		var cursorWrites int
		mockClient.On("KVSet", IndexerCursorKey, mock.Anything).
			Run(func(args mock.Arguments) { cursorWrites++ }).
			Return(nil).Maybe()
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus("job-defer-ordering", mainCutoff)
		idx.runReindexJob(jobStatus, true, deferRun)

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

		// The full phase sequence was recorded durably (the pre-drop
		// freshness fence re-asserts the dropped claim, then building, then
		// repairing after the successful build) and the state was cleared
		// only after the repair pass.
		states := tracker.savedStates()
		require.Len(t, states, 3, "expected freshness fence, building, repairing transitions; got %v", states)
		assert.Equal(t, VectorIndexPhaseDropped, states[0].Phase)
		assert.Equal(t, VectorIndexPhaseBuilding, states[1].Phase)
		assert.Positive(t, states[1].BuildStartedAt)
		assert.Equal(t, "job-defer-ordering", states[2].JobID)
		assert.Equal(t, VectorIndexPhaseRepairing, states[2].Phase)
		assert.Equal(t, states[1].BuildStartedAt, states[2].BuildStartedAt)
		assert.True(t, tracker.wasDeleted(), "vector index state must be cleared after the repair pass")
		// Neither the repair pass nor the small main/catch-up passes may
		// checkpoint into the shared cursor key here; the repair pass in
		// particular must never write it (cursor poisoning).
		assert.Zero(t, cursorWrites, "IndexerCursorKey must not be written")
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
		deferRun := seedClaim(tracker, "job-finalize-fail")
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus("job-finalize-fail", now-5000)
		idx.runReindexJob(jobStatus, true, deferRun)

		assert.Equal(t, JobStatusFailed, jobStatus.Status, "a failed index build must never complete the job")
		assert.Contains(t, jobStatus.Error, "Failed to rebuild vector index")
		assert.False(t, tracker.wasDeleted(), "phase state must stay in place when the build fails")
		// Nothing is building anymore: the phase reverts to dropped with
		// the owning job and build timestamp preserved so a resume can
		// take ownership cleanly and repair the whole gated window.
		current := tracker.currentState()
		require.NotNil(t, current)
		assert.Equal(t, "job-finalize-fail", current.JobID)
		assert.Equal(t, VectorIndexPhaseDropped, current.Phase)
		assert.Positive(t, current.BuildStartedAt)
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
		deferRun := seedClaim(tracker, "job-mainpass-fail")
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
		idx.runReindexJob(jobStatus, true, deferRun)

		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Failed to index posts")
		finalizeIdx := rec.firstIndex("finalize")
		require.NotEqual(t, -1, finalizeIdx, "finalize must be attempted on main-pass failure; calls: %v", rec.snapshot())
		assert.Less(t, rec.firstIndex("store:main"), finalizeIdx)
		// The rebuild succeeded but the gated-window edits are still
		// unrepaired: the repairing marker must survive the failed job and
		// the terminal error must say so.
		assert.False(t, tracker.wasDeleted(), "the repairing marker must survive a failed job")
		current := tracker.currentState()
		require.NotNil(t, current)
		assert.Equal(t, VectorIndexPhaseRepairing, current.Phase)
		assert.Contains(t, jobStatus.Error, "pending repair")
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
		deferRun := seedClaim(tracker, "job-mainpass-rebuild-fail")
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
		idx.runReindexJob(jobStatus, true, deferRun)

		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Failed to index posts")
		assert.Contains(t, jobStatus.Error, "additionally failed to rebuild vector index")
		assert.Contains(t, jobStatus.Error, "out of shared memory")
		assert.False(t, tracker.wasDeleted(), "phase state must stay in place when the restore rebuild fails")
		current := tracker.currentState()
		require.NotNil(t, current)
		assert.Equal(t, "job-mainpass-rebuild-fail", current.JobID)
		assert.Equal(t, VectorIndexPhaseDropped, current.Phase)
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
		deferRun := seedClaim(tracker, jobID)
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
		idx.runReindexJob(jobStatus, true, deferRun)

		assert.Equal(t, JobStatusCanceled, jobStatus.Status)
		require.NotEqual(t, -1, rec.firstIndex("finalize"),
			"the index must be rebuilt before the cancel terminalizes; calls: %v", rec.snapshot())
		// The canceled run never repaired the gated-window edits: the
		// repairing marker must survive and the status must say so.
		assert.False(t, tracker.wasDeleted(), "the repairing marker must survive a canceled job")
		current := tracker.currentState()
		require.NotNil(t, current)
		assert.Equal(t, VectorIndexPhaseRepairing, current.Phase)
		assert.Contains(t, jobStatus.Error, "pending repair")
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
		deferRun := seedClaim(tracker, jobID)
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
		idx.runReindexJob(jobStatus, true, deferRun)

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
		current := tracker.currentState()
		require.NotNil(t, current)
		assert.Equal(t, jobID, current.JobID)
		assert.Equal(t, VectorIndexPhaseDropped, current.Phase)
	})

	t.Run("panic attempts finalize and fails the job", func(t *testing.T) {
		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, clearPanic: true}

		tracker := &vectorStateTracker{}
		deferRun := seedClaim(tracker, "job-defer-panic")
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, nil, nil)

		jobStatus := newDeferJobStatus("job-defer-panic", 0)
		idx.runReindexJob(jobStatus, true, deferRun)

		assert.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Job panicked")
		require.NotEqual(t, -1, rec.firstIndex("finalize"),
			"finalize must be attempted from the panic recovery path; calls: %v", rec.snapshot())
		assert.Less(t, rec.firstIndex("prepare"), rec.firstIndex("finalize"))
		// The rebuild succeeded but the repair never ran: the marker stays.
		assert.False(t, tracker.wasDeleted(), "the repairing marker must survive a panicked job")
		current := tracker.currentState()
		require.NotNil(t, current)
		assert.Equal(t, VectorIndexPhaseRepairing, current.Phase)
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
		idx.runReindexJob(jobStatus, true, nil)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)
		assert.Equal(t, -1, rec.firstIndex("prepare"), "prepare must not run in maintain mode")
		assert.Equal(t, -1, rec.firstIndex("finalize"), "finalize must not run in maintain mode")
	})

	t.Run("defer without bulk support falls back to maintain mode and clears a fresh claim", func(t *testing.T) {
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
		deferRun := seedClaim(tracker, "job-no-bulk")
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
		idx.runReindexJob(jobStatus, true, deferRun)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)
		assert.Equal(t, -1, rec.firstIndex("prepare"))
		assert.Equal(t, -1, rec.firstIndex("finalize"))
		assert.True(t, tracker.wasDeleted(), "a fresh claim must be cleared when falling back")
	})

	t.Run("defer without bulk support keeps an adopted claim so search stays gated", func(t *testing.T) {
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
		deferRun := seedClaim(tracker, "job-adopted-no-bulk")
		deferRun.adopted = true // the index may genuinely be dropped already
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

		jobStatus := newDeferJobStatus("job-adopted-no-bulk", now-5000)
		idx.runReindexJob(jobStatus, true, deferRun)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)
		assert.False(t, tracker.wasDeleted(), "an adopted claim must stay so search remains gated over the missing index")
		require.NotNil(t, tracker.currentState())
	})

	t.Run("stale claim detected before prepare aborts without any DDL or clear", func(t *testing.T) {
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
		// The worker paused after claiming; a successor stale-reclaimed the
		// job, adopted the state, completed, and now owns (or already
		// cleared) the key. The paused worker's in-memory claim no longer
		// matches KV, so the pre-prepare freshness CAS must fail and abort
		// the run before it can DROP the successor's index or Clear() its
		// data.
		tracker.seed(VectorIndexState{JobID: "successor-job", Phase: VectorIndexPhaseDropped})
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus("job-superseded", now-5000)
		staleRun := &deferredRun{state: VectorIndexState{JobID: "job-superseded", Phase: VectorIndexPhaseDropped}}
		idx.runReindexJob(jobStatus, true, staleRun)

		require.Equal(t, JobStatusFailed, jobStatus.Status)
		assert.Contains(t, jobStatus.Error, "Deferred index claim lost before bulk load began")
		assert.Equal(t, -1, rec.firstIndex("prepare"), "a stale claimant must not drop the index")
		assert.Equal(t, -1, rec.firstIndex("clear"), "a stale claimant must not truncate the data")
		assert.Equal(t, -1, rec.firstIndex("finalize"), "a stale claimant must not run the build DDL")
		current := tracker.currentState()
		require.NotNil(t, current, "the successor's claim must be preserved")
		assert.Equal(t, "successor-job", current.JobID)
		assert.Equal(t, VectorIndexPhaseDropped, current.Phase)
	})

	t.Run("post edited during the build is re-indexed with the new content", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		mainCutoff := now - 5000
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, UpdateAt, DeleteAt, Message, Type, ChannelId) VALUES ('edited-post', $1, $1, 0, 'Original message', '', 'channel1')", now-10000)
		require.NoError(t, err)
		// The stale pre-edit embedding row that catch-up's NOT EXISTS would
		// never touch.
		_, err = db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ('edited-post', 'edited-post', 'Original message', '[0.1, 0.2, 0.3]')")
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: mainCutoff}
		// Simulate the edit landing while the index build runs (live
		// indexing is gated in the building phase).
		bulk.onFinalize = func() {
			_, execErr := db.Exec("UPDATE Posts SET Message = 'Edited message', UpdateAt = $1 WHERE Id = 'edited-post'", model.GetMillis())
			require.NoError(t, execErr)
		}

		tracker := &vectorStateTracker{}
		deferRun := seedClaim(tracker, "job-edited-post")
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		var cursorWrites int
		mockClient.On("KVSet", IndexerCursorKey, mock.Anything).
			Run(func(args mock.Arguments) { cursorWrites++ }).
			Return(nil).Maybe()
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus("job-edited-post", mainCutoff)
		idx.runReindexJob(jobStatus, true, deferRun)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)

		// The repair is an in-place overwrite through Store: no global
		// pre-delete may run, so the existing row survives untouched (the
		// fake search never writes to the DB — a deletion could only come
		// from the removed pre-delete).
		var rows int
		require.NoError(t, db.Get(&rows, "SELECT COUNT(*) FROM llm_posts_embeddings WHERE post_id = 'edited-post'"))
		assert.Equal(t, 1, rows, "the embedding row must never be globally pre-deleted")

		contents := search.contents()
		assert.Contains(t, contents, "Edited message",
			"the post must be re-embedded with its post-edit content; stored contents: %v", contents)

		// The repair pass must not poison the shared main-pass cursor.
		assert.Zero(t, cursorWrites, "IndexerCursorKey must not be written by the repair pass")
		assert.True(t, tracker.wasDeleted(), "the state must clear once the repair completes")
	})

	t.Run("post edited into a non-indexable form during the build loses its stale rows", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		mainCutoff := now - 5000
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, UpdateAt, DeleteAt, Message, Type, ChannelId) VALUES ('emptied-post', $1, $1, 0, 'Original message', '', 'channel1')", now-10000)
		require.NoError(t, err)
		// The pre-edit embedding row that must not survive the repair.
		_, err = db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ('emptied-post', 'emptied-post', 'Original message', '[0.1, 0.2, 0.3]')")
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: mainCutoff}
		// A props-stripping integration edit empties the message while live
		// indexing is gated: the post no longer matches the indexable
		// predicate, so the re-embed loop will never overwrite its rows.
		bulk.onFinalize = func() {
			_, execErr := db.Exec("UPDATE Posts SET Message = '', UpdateAt = $1 WHERE Id = 'emptied-post'", model.GetMillis())
			require.NoError(t, execErr)
		}

		tracker := &vectorStateTracker{}
		deferRun := seedClaim(tracker, "job-emptied-post")
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus("job-emptied-post", mainCutoff)
		idx.runReindexJob(jobStatus, true, deferRun)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)

		var rows int
		require.NoError(t, db.Get(&rows, "SELECT COUNT(*) FROM llm_posts_embeddings WHERE post_id = 'emptied-post'"))
		assert.Zero(t, rows, "stale rows of a post edited into a non-indexable form must be removed by the repair")
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
		deferRun := seedClaim(tracker, "job-defer-resume")
		deferRun.adopted = true
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
		idx.runReindexJob(jobStatus, false, deferRun)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)
		assert.Equal(t, -1, rec.firstIndex("clear"), "a resume must not clear the index")
		prepareIdx := rec.firstIndex("prepare")
		finalizeIdx := rec.firstIndex("finalize")
		require.NotEqual(t, -1, prepareIdx)
		require.NotEqual(t, -1, finalizeIdx)
		assert.Less(t, prepareIdx, finalizeIdx)
		assert.True(t, tracker.wasDeleted())
	})

	t.Run("cancel during the build leaves the repairing state with a pending-repair note", func(t *testing.T) {
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

		jobID := "job-cancel-during-build"
		tracker := &vectorStateTracker{}
		deferRun := seedClaim(tracker, jobID)

		// The cancel request lands while the (uninterruptible) build DDL is
		// running: the job row reports running until FinalizeBulkIndex
		// starts, then cancel_requested.
		var buildStarted atomic.Bool
		bulk.onFinalize = func() { buildStarted.Store(true) }

		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*JobStatus)
				status.JobID = jobID
				if buildStarted.Load() {
					status.Status = JobStatusCancelRequested
				} else {
					status.Status = JobStatusRunning
				}
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
		idx.runReindexJob(jobStatus, true, deferRun)

		assert.Equal(t, JobStatusCanceled, jobStatus.Status)
		require.NotEqual(t, -1, rec.firstIndex("finalize"), "the build must have run; calls: %v", rec.snapshot())
		// The cancel is acknowledged AFTER the build, and the repairing
		// marker plus a pending-repair note must survive it.
		assert.Contains(t, jobStatus.Error, "pending repair")
		assert.Contains(t, terminal.Error, "pending repair")
		assert.False(t, tracker.wasDeleted(), "the repairing state must be left in place on cancel")
		current := tracker.currentState()
		require.NotNil(t, current)
		assert.Equal(t, jobID, current.JobID)
		assert.Equal(t, VectorIndexPhaseRepairing, current.Phase)
	})

	t.Run("resume adopting a repairing state skips prepare and completes the repair", func(t *testing.T) {
		db := testDB(t)
		defer cleanupDB(t, db)

		now := model.GetMillis()
		buildStartedAt := now - 8000
		_, err := db.Exec("INSERT INTO Channels (Id, Type, Name) VALUES ('channel1', 'O', 'town-square')")
		require.NoError(t, err)
		// A post edited inside the gated build window of the previous run.
		_, err = db.Exec("INSERT INTO Posts (Id, CreateAt, UpdateAt, DeleteAt, Message, Type, ChannelId) VALUES ('edited-post', $1, $2, 0, 'Edited during build', '', 'channel1')", now-20000, now-7000)
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding) VALUES ('edited-post', 'edited-post', 'Original message', '[0.1, 0.2, 0.3]')")
		require.NoError(t, err)

		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec, indexExists: true}
		search := &fakeDeferSearch{rec: rec, bulk: bulk, mainCutoff: now}

		jobID := "job-resume-repairing"
		tracker := &vectorStateTracker{}
		adoptedState := VectorIndexState{JobID: jobID, Phase: VectorIndexPhaseRepairing, BuildStartedAt: buildStartedAt}
		tracker.seed(adoptedState)
		deferRun := &deferredRun{state: adoptedState, adopted: true}

		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
		mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
		mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, &bots.MMBots{}, db, nil)

		jobStatus := newDeferJobStatus(jobID, now-5000)
		idx.runReindexJob(jobStatus, false, deferRun)

		require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)
		// The index is intact in the repairing phase: no DDL may run.
		assert.Equal(t, -1, rec.firstIndex("prepare"), "prepare must not run when the index is intact")
		assert.Equal(t, -1, rec.firstIndex("finalize"), "finalize must not run when the index is intact")
		// The pending repair was completed and the marker cleared.
		contents := search.contents()
		assert.Contains(t, contents, "Edited during build",
			"the gated-window edit must be re-embedded; stored contents: %v", contents)
		assert.True(t, tracker.wasDeleted(), "the repairing marker must clear after the repair completes")
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
		casErr        error
		casConflict   bool
		readErr       error
		want          bool
		wantAdopted   bool
		wantErr       bool
		wantState     *VectorIndexState
		wantCleared   bool
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
			wantState:    &VectorIndexState{JobID: "new-job", Phase: VectorIndexPhaseDropped},
		},
		{
			name:         "fresh reindex with defer strategy but store without bulk support",
			clearIndex:   true,
			configGetter: deferCfg,
			search:       nonBulkSearch,
			want:         false,
		},
		{
			name:         "fresh claim persistence failure fails the job start",
			clearIndex:   true,
			configGetter: deferCfg,
			search:       bulkSearch,
			casErr:       errors.New("kv down"),
			wantErr:      true,
		},
		{
			name:         "fresh claim CAS conflict fails the job start instead of maintain fallback",
			clearIndex:   true,
			configGetter: deferCfg,
			search:       bulkSearch,
			casConflict:  true,
			wantErr:      true,
		},
		{
			// A full reindex re-embeds everything up to its own cutoff, so
			// the previous gated-edits window is moot: BuildStartedAt resets.
			name:          "fresh reindex ADOPTS leftover dropped state even with maintain strategy and resets BuildStartedAt",
			clearIndex:    true,
			configGetter:  maintainCfg,
			search:        bulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseDropped, BuildStartedAt: 12345},
			want:          true,
			wantAdopted:   true,
			wantState:     &VectorIndexState{JobID: "new-job", Phase: VectorIndexPhaseDropped},
		},
		{
			// Maintain-mode fresh run: the full re-embed supersedes the
			// pending repair, so the marker is deleted in one CAS.
			name:          "fresh reindex clears a leftover repairing state and starts clean",
			clearIndex:    true,
			configGetter:  maintainCfg,
			search:        bulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseRepairing, BuildStartedAt: 12345},
			want:          false,
			wantCleared:   true,
		},
		{
			// Defer-mode fresh run: the leftover marker is replaced by the
			// new dropped claim in a SINGLE CAS — no delete-then-create gap
			// where neither marker nor claim exists.
			name:          "fresh reindex with defer strategy atomically converts leftover repairing to a fresh claim",
			clearIndex:    true,
			configGetter:  deferCfg,
			search:        bulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseRepairing, BuildStartedAt: 12345},
			want:          true,
			wantAdopted:   true,
			wantCleared:   false,
			wantState:     &VectorIndexState{JobID: "new-job", Phase: VectorIndexPhaseDropped},
		},
		{
			name:          "fresh reindex failing to resolve a leftover repairing marker fails the job start",
			clearIndex:    true,
			configGetter:  maintainCfg,
			search:        bulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseRepairing, BuildStartedAt: 12345},
			casErr:        errors.New("kv down"),
			wantErr:       true,
		},
		{
			name:          "fresh reindex hitting a CAS conflict on a leftover repairing marker fails the job start",
			clearIndex:    true,
			configGetter:  deferCfg,
			search:        bulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseRepairing, BuildStartedAt: 12345},
			casConflict:   true,
			wantErr:       true,
		},
		{
			name:          "fresh reindex adopting leftover state fails when the ownership write fails",
			clearIndex:    true,
			configGetter:  maintainCfg,
			search:        bulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseDropped},
			casErr:        errors.New("kv down"),
			wantErr:       true,
		},
		{
			name:         "resume without leftover state",
			clearIndex:   false,
			configGetter: maintainCfg,
			search:       bulkSearch,
			want:         false,
		},
		{
			name:          "resume takes ownership of leftover state regardless of config and keeps BuildStartedAt",
			clearIndex:    false,
			configGetter:  maintainCfg,
			search:        bulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseDropped, BuildStartedAt: 12345},
			want:          true,
			wantAdopted:   true,
			wantState:     &VectorIndexState{JobID: "new-job", Phase: VectorIndexPhaseDropped, BuildStartedAt: 12345},
		},
		{
			// The index is intact in the repairing phase, so no bulk
			// support is needed to finish the pending repair.
			name:          "resume adopts a leftover repairing state even without bulk support",
			clearIndex:    false,
			configGetter:  maintainCfg,
			search:        nonBulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseRepairing, BuildStartedAt: 12345},
			want:          true,
			wantAdopted:   true,
			wantState:     &VectorIndexState{JobID: "new-job", Phase: VectorIndexPhaseRepairing, BuildStartedAt: 12345},
		},
		{
			name:          "leftover dropped state with store without bulk support stays gated in maintain mode",
			clearIndex:    false,
			configGetter:  maintainCfg,
			search:        nonBulkSearch,
			existingState: &VectorIndexState{JobID: "old-job", Phase: VectorIndexPhaseDropped},
			want:          false,
		},
		{
			name:         "state read error fails the resolution instead of silently maintaining",
			clearIndex:   false,
			configGetter: maintainCfg,
			search:       bulkSearch,
			readErr:      errors.New("kv unreachable"),
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &callRecorder{}
			mockClient := mocks.NewMockClient(t)

			switch {
			case tt.readErr != nil:
				mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).
					Return(tt.readErr)
			case tt.existingState != nil:
				mockClient.On("KVGet", VectorIndexStateKey, mock.AnythingOfType("*indexer.VectorIndexState")).
					Run(func(args mock.Arguments) {
						*args.Get(1).(*VectorIndexState) = *tt.existingState
					}).
					Return(nil).Maybe()
			default:
				mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).
					Return(mmapi.ErrKVNotFound).Maybe()
			}

			var saved *VectorIndexState
			cleared := false
			mockClient.On("KVCompareAndSet", VectorIndexStateKey, mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					if tt.casErr != nil || tt.casConflict {
						return
					}
					if args.Get(2) == nil {
						cleared = true
						return
					}
					state := args.Get(2).(VectorIndexState)
					saved = &state
				}).
				Return(tt.casErr == nil && !tt.casConflict, tt.casErr).Maybe()
			mockClient.On("LogWarn", mock.Anything).Return().Maybe()
			mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
			mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

			search := tt.search(rec)
			idx := New(func() embeddings.EmbeddingSearch { return search }, tt.configGetter, mockClient, nil, nil, nil)

			got, err := idx.resolveDeferredRebuild(tt.clearIndex, "new-job")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCleared, cleared, "leftover-state clearing mismatch")
			if !tt.want {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got, "expected a deferred run")
			assert.Equal(t, tt.wantAdopted, got.adopted)

			if tt.wantState != nil {
				require.NotNil(t, saved, "expected the state to be persisted")
				assert.Equal(t, *tt.wantState, *saved, "persisted state mismatch")
				assert.Equal(t, *tt.wantState, got.state, "in-memory state must match the CAS-written value")
			}
		})
	}
}

func TestFinalizeDeferredIndexHeartbeat(t *testing.T) {
	rec := &callRecorder{}
	bulk := &fakeBulkIndexer{rec: rec, finalizeDelay: 100 * time.Millisecond}

	tracker := &vectorStateTracker{}
	tracker.seed(VectorIndexState{JobID: "", Phase: VectorIndexPhaseDropped})

	var mu sync.Mutex
	heartbeatSaves := 0
	mockClient := mocks.NewMockClient(t)
	mockVectorStateOps(mockClient, tracker)
	mockClient.On("KVSet", ReindexJobKey, mock.Anything).
		Run(func(args mock.Arguments) {
			mu.Lock()
			heartbeatSaves++
			mu.Unlock()
		}).
		Return(nil).Maybe()
	mockClient.On("KVGet", ReindexJobKey, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
	mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
	mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

	idx := New(nil, nil, mockClient, nil, nil, nil)
	idx.heartbeatInterval = 5 * time.Millisecond

	// Empty JobID makes saveJobStatus an unconditional KVSet, so heartbeat
	// ticks are directly countable.
	jobStatus := &JobStatus{Status: JobStatusRunning, StartedAt: time.Now()}
	before := time.Now()

	newState, err := idx.finalizeDeferredIndex(context.Background(), jobStatus, bulk, VectorIndexState{JobID: "", Phase: VectorIndexPhaseDropped})
	require.NoError(t, err)
	assert.Positive(t, newState.BuildStartedAt)
	assert.Equal(t, VectorIndexPhaseRepairing, newState.Phase, "a successful build must hand back the repairing state")

	mu.Lock()
	saves := heartbeatSaves
	mu.Unlock()
	assert.GreaterOrEqual(t, saves, 2, "the heartbeat must keep ticking while the index build blocks")
	assert.True(t, jobStatus.LastUpdatedAt.After(before), "the heartbeat must advance LastUpdatedAt")
	// The state is NOT cleared here: the repairing marker persists until
	// the caller finishes the edited-posts repair.
	assert.False(t, tracker.wasDeleted(), "finalize must leave the repairing marker in place")
	current := tracker.currentState()
	require.NotNil(t, current)
	assert.Equal(t, VectorIndexPhaseRepairing, current.Phase)
}

func TestFinalizeDeferredIndexStateFencing(t *testing.T) {
	t.Run("stale in-memory ownership skips the DDL entirely", func(t *testing.T) {
		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}

		// The KV master holds a successor's claim; this run's in-memory
		// state no longer matches, so the dropped->building CAS must fail
		// and the DDL must not run.
		tracker := &vectorStateTracker{}
		tracker.seed(VectorIndexState{JobID: "other-job", Phase: VectorIndexPhaseDropped})
		mockClient := mocks.NewMockClient(t)
		mockVectorStateOps(mockClient, tracker)
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(nil, nil, mockClient, nil, nil, nil)

		jobStatus := &JobStatus{JobID: "my-job", Status: JobStatusRunning, StartedAt: time.Now()}
		_, err := idx.finalizeDeferredIndex(context.Background(), jobStatus, bulk, VectorIndexState{JobID: "my-job", Phase: VectorIndexPhaseDropped})

		require.Error(t, err)
		assert.Equal(t, -1, rec.firstIndex("finalize"), "the DDL must not run when ownership is lost")
		current := tracker.currentState()
		require.NotNil(t, current, "the other run's state must be untouched")
		assert.Equal(t, "other-job", current.JobID)
	})

	t.Run("failed building-phase write aborts before the DDL", func(t *testing.T) {
		rec := &callRecorder{}
		bulk := &fakeBulkIndexer{rec: rec}

		mockClient := mocks.NewMockClient(t)
		// The IndexPost gate only engages once the building phase is
		// durable; if this write fails the build must not start.
		mockClient.On("KVCompareAndSet", VectorIndexStateKey, mock.Anything, mock.Anything).
			Return(false, errors.New("kv down"))
		mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
		mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

		idx := New(nil, nil, mockClient, nil, nil, nil)

		jobStatus := &JobStatus{JobID: "my-job", Status: JobStatusRunning, StartedAt: time.Now()}
		_, err := idx.finalizeDeferredIndex(context.Background(), jobStatus, bulk, VectorIndexState{JobID: "my-job", Phase: VectorIndexPhaseDropped})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "building phase")
		assert.Equal(t, -1, rec.firstIndex("finalize"), "the DDL must not run when the gate could not engage")
	})
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
		readErr   error
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
		{
			name:      "repairing phase indexes normally (the index is valid again)",
			state:     &VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseRepairing},
			wantStore: true,
		},
		{
			// Fail closed: a skipped post is repairable (catch-up, health
			// check); a goroutine pile-up on a blocked write is not.
			name:      "unexpected KV error skips the write",
			readErr:   errors.New("kv unreachable"),
			wantStore: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockClient(t)
			mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)

			switch {
			case tt.readErr != nil:
				mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).
					Return(tt.readErr)
			case tt.state != nil:
				mockClient.On("KVGet", VectorIndexStateKey, mock.AnythingOfType("*indexer.VectorIndexState")).
					Run(func(args mock.Arguments) {
						*args.Get(1).(*VectorIndexState) = *tt.state
					}).
					Return(nil)
			default:
				mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).
					Return(mmapi.ErrKVNotFound)
			}
			mockClient.On("LogDebug", mock.Anything, mock.Anything).Return().Maybe()
			mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

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

	t.Run("dropped state reports active", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockClient.On("KVGet", VectorIndexStateKey, mock.AnythingOfType("*indexer.VectorIndexState")).
			Run(func(args mock.Arguments) {
				*args.Get(1).(*VectorIndexState) = VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseDropped}
			}).
			Return(nil)
		assert.True(t, DeferredIndexRebuildActive(mockClient))
	})

	t.Run("repairing state reports inactive (the index is valid, search works)", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockClient.On("KVGet", VectorIndexStateKey, mock.AnythingOfType("*indexer.VectorIndexState")).
			Run(func(args mock.Arguments) {
				*args.Get(1).(*VectorIndexState) = VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseRepairing}
			}).
			Return(nil)
		assert.False(t, DeferredIndexRebuildActive(mockClient))
	})

	t.Run("unexpected KV error fails closed and reports active", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).Return(errors.New("kv unreachable"))
		mockClient.On("LogError", mock.Anything, mock.Anything).Return()
		assert.True(t, DeferredIndexRebuildActive(mockClient),
			"a transient KV error must not trigger a synchronous constructor index build")
	})
}

func TestReconcileVectorIndexState(t *testing.T) {
	stubState := VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseDropped}

	tests := []struct {
		name          string
		state         *VectorIndexState
		indexExists   bool
		existsErr     error
		jobRow        *JobStatus
		wantErr       bool
		wantDeleted   bool
		wantRepairing bool
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
			// The window between the state claim and the DROP: the catalog
			// still shows a valid index, but the live owner's claim must
			// not be cleared out from under it.
			name:        "live owning job with a still-valid index keeps the claim",
			state:       &stubState,
			indexExists: true,
			jobRow: &JobStatus{
				JobID:         "job1",
				Status:        JobStatusRunning,
				StartedAt:     time.Now(),
				LastUpdatedAt: time.Now(),
			},
			wantDeleted: false,
		},
		{
			name:  "missing index without an owning job keeps the state and search stays gated",
			state: &stubState,
		},
		{
			name:  "building leftover with a missing index and no owner keeps the state",
			state: &VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseBuilding, BuildStartedAt: 12345},
		},
		{
			// The build committed but the building->repairing transition
			// never did: the gated-window edits still need repair, so the
			// marker converts instead of being deleted.
			name:          "building leftover with a valid index converts to repairing",
			state:         &VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseBuilding, BuildStartedAt: 12345},
			indexExists:   true,
			wantRepairing: true,
		},
		{
			// The repairing marker means the index SHOULD exist: catalog
			// evidence must never clear it, only a completed repair does.
			name:        "leftover repairing state is kept as a pending-repair marker",
			state:       &VectorIndexState{JobID: "job1", Phase: VectorIndexPhaseRepairing, BuildStartedAt: 12345},
			indexExists: true,
			wantDeleted: false,
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
			mockClient.On("KVCompareAndSet", VectorIndexStateKey, mock.Anything, nil).
				Run(func(args mock.Arguments) { deleted = true }).
				Return(true, nil).Maybe()
			var converted *VectorIndexState
			mockClient.On("KVCompareAndSet", VectorIndexStateKey, mock.Anything, mock.AnythingOfType("indexer.VectorIndexState")).
				Run(func(args mock.Arguments) {
					state := args.Get(2).(VectorIndexState)
					converted = &state
				}).
				Return(true, nil).Maybe()
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
			if tt.wantRepairing {
				require.NotNil(t, converted, "expected the state to convert to repairing")
				assert.Equal(t, VectorIndexPhaseRepairing, converted.Phase)
				assert.Equal(t, tt.state.JobID, converted.JobID, "JobID must be preserved")
				assert.Equal(t, tt.state.BuildStartedAt, converted.BuildStartedAt, "BuildStartedAt must be preserved")
			} else {
				assert.Nil(t, converted, "no repairing conversion expected")
			}
		})
	}
}
