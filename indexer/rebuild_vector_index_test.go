// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestStartRebuildVectorIndexRejects(t *testing.T) {
	t.Run("search not configured", func(t *testing.T) {
		idx := New(nil, nil, nil, nil, nil, nil)
		_, err := idx.StartRebuildVectorIndex(context.Background())
		require.Error(t, err)
		assert.Equal(t, "search functionality is not configured", err.Error())
	})

	t.Run("job already running", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		search := &fakeDeferSearch{rec: &callRecorder{}, bulk: &fakeBulkIndexer{rec: &callRecorder{}}}
		running := JobStatus{
			JobID:         "running-job",
			Status:        JobStatusRunning,
			StartedAt:     time.Now().Add(-time.Minute),
			LastUpdatedAt: time.Now(),
		}
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				*args.Get(1).(*JobStatus) = running
			}).
			Return(nil)

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, nil, nil, nil)
		status, err := idx.StartRebuildVectorIndex(context.Background())
		require.Error(t, err)
		assert.Equal(t, "job already running", err.Error())
		assert.Equal(t, JobStatusRunning, status.Status)
	})

	t.Run("vector store without bulk support", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockMutexAPI := &plugintest.API{}
		search := &fakeDeferSearch{rec: &callRecorder{}} // bulk is nil
		mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(mmapi.ErrKVNotFound)
		mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
		mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

		idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, nil, nil, mockMutexAPI)
		_, err := idx.StartRebuildVectorIndex(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, errVectorStoreNoBulkIndex)
	})
}

func TestRunRebuildVectorIndexJobPrepareFinalizeNoClear(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	rec := &callRecorder{}
	bulk := &fakeBulkIndexer{rec: rec}
	search := &fakeDeferSearch{rec: rec, bulk: bulk}

	tracker := &vectorStateTracker{}
	state := VectorIndexState{JobID: "rebuild-job", Phase: VectorIndexPhaseDropped}
	tracker.seed(state)
	deferRun := &deferredRun{state: state, adopted: false}

	store := &jobKVStore{}
	mockClient := mocks.NewMockClient(t)
	mockVectorStateOps(mockClient, tracker)
	store.wire(mockClient)
	mockClient.On("KVGet", mock.Anything, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
	mockClient.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockClient.On("KVDelete", mock.Anything).Return(nil).Maybe()
	mockClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
	mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
	mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

	cfg := modelCfg("openai", "text-embedding-3-small", 1536)
	cfg.HNSWM = embeddings.DefaultHNSWM
	idx := New(
		func() embeddings.EmbeddingSearch { return search },
		func() embeddings.EmbeddingSearchConfig { return cfg },
		mockClient, &bots.MMBots{}, db, nil,
	)

	jobStatus := &JobStatus{
		JobID:     "rebuild-job",
		Status:    JobStatusRunning,
		StartedAt: time.Now(),
		CutoffAt:  time.Now().UnixMilli(),
		ModelInfo: &ModelInfo{
			ProviderType: "openai",
			ModelName:    "text-embedding-3-small",
			Dimensions:   1536,
			HNSWM:        embeddings.DefaultHNSWM,
		},
	}
	idx.runRebuildVectorIndexJob(context.Background(), jobStatus, deferRun)

	require.Equal(t, JobStatusCompleted, jobStatus.Status, "job error: %s", jobStatus.Error)
	assert.NotContains(t, rec.snapshot(), "clear", "rebuild must not Clear the embeddings table")
	assert.Equal(t, 0, rec.firstIndex("prepare"), "PrepareBulkIndex should run first")
	require.NotEqual(t, -1, rec.firstIndex("finalize"), "FinalizeBulkIndex must run")
	assert.True(t, rec.firstIndex("prepare") < rec.firstIndex("finalize"))
	assert.True(t, tracker.wasDeleted(), "vector index state must be cleared after a successful rebuild")
}
