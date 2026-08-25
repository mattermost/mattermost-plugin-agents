// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"errors"
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

	tests := []struct {
		name      string
		existing  JobStatus
		wantErr   error
		overwrite bool
	}{
		{
			name: "failed full reindex",
			existing: JobStatus{
				JobID:         "failed-reindex",
				Status:        JobStatusFailed,
				Operation:     JobOperationReindex,
				Resumable:     false,
				ProcessedRows: 0,
			},
			wantErr: ErrRebuildIncompleteReindex,
		},
		{
			name: "failed full reindex with empty operation",
			existing: JobStatus{
				JobID:     "legacy-reindex",
				Status:    JobStatusFailed,
				Operation: "",
				Resumable: false,
			},
			wantErr: ErrRebuildIncompleteReindex,
		},
		{
			name: "canceled full reindex",
			existing: JobStatus{
				JobID:     "canceled-reindex",
				Status:    JobStatusCanceled,
				Operation: JobOperationReindex,
				Resumable: false,
			},
			wantErr: ErrRebuildIncompleteReindex,
		},
		{
			name: "failed rebuild leftover is allowed",
			existing: JobStatus{
				JobID:     "failed-rebuild",
				Status:    JobStatusFailed,
				Operation: JobOperationRebuildVectorIndex,
				Resumable: false,
			},
			overwrite: true,
		},
		{
			name: "failed catch-up leftover is allowed",
			existing: JobStatus{
				JobID:     "failed-catchup",
				Status:    JobStatusFailed,
				Operation: JobOperationReindex,
				Resumable: true,
			},
			overwrite: true,
		},
		{
			name: "completed reindex is allowed",
			existing: JobStatus{
				JobID:     "completed-reindex",
				Status:    JobStatusCompleted,
				Operation: JobOperationReindex,
				Resumable: false,
			},
			overwrite: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockClient(t)
			mockMutexAPI := &plugintest.API{}
			search := &fakeDeferSearch{rec: &callRecorder{}, bulk: &fakeBulkIndexer{rec: &callRecorder{}}}
			mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
				Run(func(args mock.Arguments) {
					*args.Get(1).(*JobStatus) = tt.existing
				}).
				Return(nil)
			mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
			mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

			if tt.overwrite {
				mockClient.On("KVCompareAndSet", ReindexJobKey, mock.Anything, mock.MatchedBy(func(v interface{}) bool {
					status, ok := v.(JobStatus)
					return ok && status.Operation == JobOperationRebuildVectorIndex
				})).Return(true, nil)
				mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).Return(errors.New("kv unreachable"))
				mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()
			}

			idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, nil, nil, mockMutexAPI)
			_, err := idx.StartRebuildVectorIndex(context.Background())
			require.Error(t, err)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				mockClient.AssertNotCalled(t, "KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything)
				return
			}
			assert.NotErrorIs(t, err, ErrRebuildIncompleteReindex)
			mockClient.AssertCalled(t, "KVCompareAndSet", ReindexJobKey, mock.Anything, mock.Anything)
		})
	}
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

func TestStartRebuildVectorIndexRejectsIncompatibleIdentity(t *testing.T) {
	tests := []struct {
		name   string
		stored ModelInfo
		cfg    embeddings.EmbeddingSearchConfig
	}{
		{
			name: "dimension mismatch",
			stored: ModelInfo{
				ProviderType: "openai",
				ModelName:    "text-embedding-3-small",
				Dimensions:   768,
			},
			cfg: modelCfg("openai", "text-embedding-3-small", 1536),
		},
		{
			name: "model name mismatch",
			stored: ModelInfo{
				ProviderType: "openai",
				ModelName:    "text-embedding-ada-002",
				Dimensions:   1536,
			},
			cfg: modelCfg("openai", "text-embedding-3-small", 1536),
		},
		{
			name: "provider mismatch",
			stored: ModelInfo{
				ProviderType: "openai",
				ModelName:    "text-embedding-3-small",
				Dimensions:   1536,
			},
			cfg: modelCfg("anthropic", "text-embedding-3-small", 1536),
		},
		{
			name: "vector element type mismatch",
			stored: ModelInfo{
				ProviderType:      "openai",
				ModelName:         "text-embedding-3-small",
				Dimensions:        1536,
				VectorElementType: embeddings.VectorElementTypeVector,
			},
			cfg: func() embeddings.EmbeddingSearchConfig {
				cfg := modelCfg("openai", "text-embedding-3-small", 1536)
				cfg.VectorElementType = embeddings.VectorElementTypeHalfvec
				return cfg
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockClient(t)
			mockMutexAPI := &plugintest.API{}
			search := &fakeDeferSearch{rec: &callRecorder{}, bulk: &fakeBulkIndexer{rec: &callRecorder{}}}
			mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
				Return(mmapi.ErrKVNotFound)
			mockClient.On("KVGet", IndexerModelKey, mock.AnythingOfType("*indexer.ModelInfo")).
				Run(func(args mock.Arguments) {
					*args.Get(1).(*ModelInfo) = tt.stored
				}).
				Return(nil)
			mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
			mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

			idx := New(
				func() embeddings.EmbeddingSearch { return search },
				func() embeddings.EmbeddingSearchConfig { return tt.cfg },
				mockClient, nil, nil, mockMutexAPI,
			)
			_, err := idx.StartRebuildVectorIndex(context.Background())
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrRebuildIncompatible)
		})
	}
}

func TestSaveHNSWMAfterRebuild(t *testing.T) {
	jobIdentity := &ModelInfo{
		ProviderType: "openai",
		ModelName:    "text-embedding-3-small",
		Dimensions:   1536,
		HNSWM:        embeddings.DefaultHNSWM,
	}

	tests := []struct {
		name         string
		stored       *ModelInfo
		job          *ModelInfo
		wantProvider string
		wantModel    string
		wantDims     int
		wantM        int
	}{
		{
			name:         "missing stored metadata writes only hnsw m",
			stored:       nil,
			job:          jobIdentity,
			wantProvider: "",
			wantModel:    "",
			wantDims:     0,
			wantM:        embeddings.DefaultHNSWM,
		},
		{
			name: "blank stored identity is preserved against a full job snapshot",
			stored: &ModelInfo{
				HNSWM: 16,
			},
			job:          jobIdentity,
			wantProvider: "",
			wantModel:    "",
			wantDims:     0,
			wantM:        embeddings.DefaultHNSWM,
		},
		{
			name: "matching stored identity keeps provider model and dimensions",
			stored: &ModelInfo{
				ProviderType: "openai",
				ModelName:    "text-embedding-3-small",
				Dimensions:   1536,
				HNSWM:        16,
			},
			job:          jobIdentity,
			wantProvider: "openai",
			wantModel:    "text-embedding-3-small",
			wantDims:     1536,
			wantM:        embeddings.DefaultHNSWM,
		},
		{
			name: "different stored identity is not overwritten by the job snapshot",
			stored: &ModelInfo{
				ProviderType: "openai",
				ModelName:    "text-embedding-ada-002",
				Dimensions:   768,
				HNSWM:        16,
			},
			job:          jobIdentity,
			wantProvider: "openai",
			wantModel:    "text-embedding-ada-002",
			wantDims:     768,
			wantM:        embeddings.DefaultHNSWM,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &jobKVStore{}
			if tt.stored != nil {
				copied := *tt.stored
				store.model = &copied
			}
			mockClient := mocks.NewMockClient(t)
			store.wire(mockClient)

			idx := New(nil, nil, mockClient, nil, nil, nil)
			idx.saveHNSWMAfterRebuild(&JobStatus{ModelInfo: tt.job})

			require.NotNil(t, store.model)
			assert.Equal(t, tt.wantProvider, store.model.ProviderType)
			assert.Equal(t, tt.wantModel, store.model.ModelName)
			assert.Equal(t, tt.wantDims, store.model.Dimensions)
			assert.Equal(t, tt.wantM, store.model.HNSWM)
		})
	}
}

func TestHandleJobErrorSkipsCursorForRebuild(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	cursorWrites := 0
	mockClient.On("KVSet", IndexerCursorKey, mock.Anything).
		Run(func(mock.Arguments) { cursorWrites++ }).
		Return(nil).Maybe()
	mockClient.On("KVSet", ReindexJobKey, mock.Anything).Return(nil)
	mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

	idx := New(nil, nil, mockClient, nil, nil, nil)
	idx.handleJobError(&JobStatus{
		Operation: JobOperationRebuildVectorIndex,
		Status:    JobStatusRunning,
	}, "catch-up failed", 99, "post-id")

	assert.Zero(t, cursorWrites, "rebuild failures must not write IndexerCursorKey")
}

func TestStartReindexJobRejectsResumeOfRebuild(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	mockMutexAPI := &plugintest.API{}
	search := &fakeDeferSearch{rec: &callRecorder{}, bulk: &fakeBulkIndexer{rec: &callRecorder{}}}
	failedRebuild := JobStatus{
		JobID:         "rebuild-job",
		Status:        JobStatusFailed,
		Operation:     JobOperationRebuildVectorIndex,
		ProcessedRows: 50,
		Resumable:     false,
	}
	mockClient.On("KVGet", ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
		Run(func(args mock.Arguments) {
			*args.Get(1).(*JobStatus) = failedRebuild
		}).
		Return(nil)
	mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

	idx := New(func() embeddings.EmbeddingSearch { return search }, nil, mockClient, nil, nil, mockMutexAPI)
	_, err := idx.StartReindexJob(false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCannotResumeRebuild)
}
