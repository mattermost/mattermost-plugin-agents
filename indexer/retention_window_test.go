// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestShouldIndexPostWithFloor(t *testing.T) {
	mockBots := &bots.MMBots{}
	idx := New(nil, nil, nil, mockBots, nil, nil)
	channel := &model.Channel{Id: "channel1", Type: model.ChannelTypeOpen}

	tests := []struct {
		name     string
		createAt int64
		floor    int64
		want     bool
	}{
		{name: "N=0 indexes any create time", createAt: 1, floor: 0, want: true},
		{name: "post above floor is indexed", createAt: 200, floor: 100, want: true},
		{name: "post at floor is indexed", createAt: 100, floor: 100, want: true},
		{name: "post below floor is skipped", createAt: 99, floor: 100, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			post := &model.Post{
				Id:       "post1",
				Message:  "hello",
				Type:     model.PostTypeDefault,
				UserId:   "user1",
				CreateAt: tt.createAt,
			}
			assert.Equal(t, tt.want, idx.shouldIndexPostWithFloor(post, channel, tt.floor))
		})
	}
}

func TestBumpCursorToFloor(t *testing.T) {
	tests := []struct {
		name   string
		cursor Cursor
		floor  int64
		want   Cursor
	}{
		{name: "no floor leaves cursor", cursor: Cursor{LastCreateAt: 10, LastID: "a"}, floor: 0, want: Cursor{LastCreateAt: 10, LastID: "a"}},
		{name: "cursor older than floor is bumped", cursor: Cursor{LastCreateAt: 10, LastID: "a"}, floor: 50, want: Cursor{LastCreateAt: 50, LastID: ""}},
		{name: "cursor at floor is kept", cursor: Cursor{LastCreateAt: 50, LastID: "a"}, floor: 50, want: Cursor{LastCreateAt: 50, LastID: "a"}},
		{name: "cursor newer than floor is kept", cursor: Cursor{LastCreateAt: 80, LastID: "a"}, floor: 50, want: Cursor{LastCreateAt: 80, LastID: "a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, bumpCursorToFloor(tt.cursor, tt.floor))
		})
	}
}

func TestCheckModelCompatibilityRetention(t *testing.T) {
	baseStored := ModelInfo{
		ProviderType: "openai",
		Dimensions:   1536,
		ModelName:    "text-embedding-3-small",
		HNSWM:        embeddings.DefaultHNSWM,
	}
	currentOf := func(days int) ModelInfo {
		c := baseStored
		c.IndexRetentionDays = retentionDaysPtr(days)
		return c
	}

	tests := []struct {
		name           string
		stored         ModelInfo
		current        ModelInfo
		wantCompat     bool
		wantReindex    bool
		wantCatchUp    bool
		wantReason     string
		wantStoredDays *int
	}{
		{
			name:        "missing stored days is compatible and does not nag",
			stored:      baseStored,
			current:     currentOf(730),
			wantCompat:  true,
			wantReindex: false,
			wantCatchUp: false,
		},
		{
			name:           "365 to 730 needs catch-up and search stays compatible",
			stored:         func() ModelInfo { s := baseStored; s.IndexRetentionDays = retentionDaysPtr(365); return s }(),
			current:        currentOf(730),
			wantCompat:     true,
			wantReindex:    false,
			wantCatchUp:    true,
			wantReason:     "index retention increased: stored=365, current=730",
			wantStoredDays: retentionDaysPtr(365),
		},
		{
			name:           "365 to all posts needs catch-up",
			stored:         func() ModelInfo { s := baseStored; s.IndexRetentionDays = retentionDaysPtr(365); return s }(),
			current:        currentOf(0),
			wantCompat:     true,
			wantCatchUp:    true,
			wantReason:     "index retention increased: stored=365, current=0",
			wantStoredDays: retentionDaysPtr(365),
		},
		{
			name:           "730 to 365 stays compatible with no catch-up",
			stored:         func() ModelInfo { s := baseStored; s.IndexRetentionDays = retentionDaysPtr(730); return s }(),
			current:        currentOf(365),
			wantCompat:     true,
			wantCatchUp:    false,
			wantReason:     "Lowering this does not remove already-indexed posts. Full Reindex if you want the index smaller.",
			wantStoredDays: retentionDaysPtr(730),
		},
		{
			name:           "all posts to 365 stays compatible with no catch-up",
			stored:         func() ModelInfo { s := baseStored; s.IndexRetentionDays = retentionDaysPtr(0); return s }(),
			current:        currentOf(365),
			wantCompat:     true,
			wantCatchUp:    false,
			wantReason:     "Lowering this does not remove already-indexed posts. Full Reindex if you want the index smaller.",
			wantStoredDays: retentionDaysPtr(0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockClient(t)
			mockClient.On("KVGet", IndexerModelKey, mock.AnythingOfType("*indexer.ModelInfo")).
				Run(func(args mock.Arguments) {
					*args.Get(1).(*ModelInfo) = tt.stored
				}).
				Return(nil)

			idx := New(nil, nil, mockClient, nil, nil, nil)
			result := idx.CheckModelCompatibility(tt.current)

			assert.Equal(t, tt.wantCompat, result.Compatible)
			assert.Equal(t, tt.wantReindex, result.NeedsReindex)
			assert.Equal(t, tt.wantCatchUp, result.NeedsCatchUp)
			assert.Equal(t, tt.wantReason, result.Reason)
			assert.Equal(t, tt.wantStoredDays, result.StoredIndexRetentionDays)
		})
	}
}

func TestIndexablePostsFetchRespectsFloor(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	_, err := db.Exec("INSERT INTO Channels (Id, Type, Name, TeamId) VALUES ('channel1', 'O', 'town-square', 'team1')")
	require.NoError(t, err)

	now := model.GetMillis()
	floor := now - (365 * embeddings.MillisPerDay)
	seeds := []struct {
		id       string
		createAt int64
	}{
		{"old", floor - 1000},
		{"edge", floor},
		{"in-window", floor + 1000},
	}
	for _, p := range seeds {
		_, err = db.Exec(
			"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ($1, $2, 0, $3, '', 'channel1', 'user1')",
			p.id, p.createAt, "msg "+p.id)
		require.NoError(t, err)
	}

	idx := New(nil, nil, nil, &bots.MMBots{}, db, nil)
	fetch := idx.postFetcher(indexablePostsFetchSQL(floor, false), now, floor)
	posts, err := fetch(context.Background(), Cursor{}, 100)
	require.NoError(t, err)

	got := make([]string, 0, len(posts))
	for _, p := range posts {
		got = append(got, p.ID)
	}
	assert.ElementsMatch(t, []string{"edge", "in-window"}, got)
}

func TestCatchUpAfterWideningIndexesOnlyTheGap(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	_, err := db.Exec("INSERT INTO Channels (Id, Type, Name, TeamId) VALUES ('channel1', 'O', 'town-square', 'team1')")
	require.NoError(t, err)

	now := model.GetMillis()
	floor365 := now - (365 * embeddings.MillisPerDay)
	floor730 := now - (730 * embeddings.MillisPerDay)
	lastIndexed := now - 5000

	type seed struct {
		id       string
		createAt int64
		indexed  bool
	}
	seeds := []seed{
		{"too-old", floor730 - embeddings.MillisPerDay, false},
		{"gap", floor365 - (100 * embeddings.MillisPerDay), false},
		{"already", floor365 + embeddings.MillisPerDay, true},
		{"recent", lastIndexed + 100, false},
	}
	for _, p := range seeds {
		_, err = db.Exec(
			"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ($1, $2, 0, $3, '', 'channel1', 'user1')",
			p.id, p.createAt, "msg "+p.id)
		require.NoError(t, err)
		if p.indexed {
			_, err = db.Exec(
				"INSERT INTO llm_posts_embeddings (id, post_id, content, embedding, created_at) VALUES ($1, $1, $2, '[0.1, 0.2, 0.3]', $3)",
				p.id, "msg "+p.id, p.createAt)
			require.NoError(t, err)
		}
	}

	store := &jobKVStore{}
	ts := lastIndexed
	store.lastIdx = &ts
	store.model = &ModelInfo{
		ProviderType:       "openai",
		ModelName:          "text-embedding-3-small",
		Dimensions:         1536,
		HNSWM:              embeddings.DefaultHNSWM,
		IndexRetentionDays: retentionDaysPtr(365),
	}

	mockClient := mocks.NewMockClient(t)
	mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
	mockMutexAPI := &plugintest.API{}
	mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()
	store.wire(mockClient)
	mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
	mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

	var stored []string
	var storedMu sync.Mutex
	mockSearch.On("Store", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			docs := args.Get(1).([]embeddings.PostDocument)
			storedMu.Lock()
			for _, doc := range docs {
				stored = append(stored, doc.PostID)
			}
			storedMu.Unlock()
		}).
		Return(nil).Maybe()

	cfg := embeddings.EmbeddingSearchConfig{
		IndexRetentionDays: 730,
		Dimensions:         1536,
		EmbeddingProvider: embeddings.UpstreamConfig{
			Type:       "openai",
			Parameters: []byte(`{"embeddingModel":"text-embedding-3-small"}`),
		},
	}
	idx := New(func() embeddings.EmbeddingSearch { return mockSearch }, func() embeddings.EmbeddingSearchConfig { return cfg }, mockClient, &bots.MMBots{}, db, mockMutexAPI)

	status, err := idx.StartCatchUpJob()
	require.NoError(t, err)
	assert.Equal(t, JobOperationCatchUp, status.Operation)
	assert.Equal(t, 730, status.IndexRetentionDays)
	assert.InDelta(t, float64(floor730), float64(status.RetentionFloor), 5000)
	assert.Less(t, seeds[1].createAt, lastIndexed, "gap is historically before the last job wall clock")
	waitForJobStatus(t, store, JobStatusCompleted, 5*time.Second)

	storedMu.Lock()
	defer storedMu.Unlock()
	assert.ElementsMatch(t, []string{"gap", "recent"}, stored)
	assert.NotContains(t, stored, "too-old")
	assert.NotContains(t, stored, "already")

	store.mu.Lock()
	require.NotNil(t, store.model.IndexRetentionDays)
	assert.Equal(t, 730, *store.model.IndexRetentionDays)
	assert.Equal(t, "text-embedding-3-small", store.model.ModelName)
	store.mu.Unlock()
}

func TestCheckIndexHealthUsesInWindowCounts(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	now := model.GetMillis()
	inWindow := now - (30 * embeddings.MillisPerDay)
	outOfWindow := now - (400 * embeddings.MillisPerDay)

	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("in-%d", i)
		_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, $3, '')", id, inWindow+int64(i), "in")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding, created_at) VALUES ($1, $1, $2, '[0.1, 0.2, 0.3]', $3)", id, "in", inWindow+int64(i))
		require.NoError(t, err)
	}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("old-%d", i)
		createAt := outOfWindow - int64(i)*1000
		_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, $3, '')", id, createAt, "old")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding, created_at) VALUES ($1, $1, $2, '[0.1, 0.2, 0.3]', $3)", id, "old", createAt)
		require.NoError(t, err)
	}

	mockClient := mocks.NewMockClient(t)
	mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
	mockClient.On("KVGet", VectorIndexStateKey, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()

	idx := New(func() embeddings.EmbeddingSearch { return mockSearch }, func() embeddings.EmbeddingSearchConfig {
		return embeddings.EmbeddingSearchConfig{IndexRetentionDays: 365}
	}, mockClient, nil, db, nil)

	result, err := idx.CheckIndexHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(5), result.DBPostCount)
	assert.Equal(t, int64(5), result.IndexedPostCount)
	assert.Equal(t, int64(0), result.MissingPosts)
	assert.Equal(t, "healthy", result.Status)
}

func TestEditedPostsFetchExcludesOutOfWindow(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	_, err := db.Exec("INSERT INTO Channels (Id, Type, Name, TeamId) VALUES ('channel1', 'O', 'town-square', 'team1')")
	require.NoError(t, err)

	now := model.GetMillis()
	floor := now - (365 * embeddings.MillisPerDay)
	_, err = db.Exec(
		"INSERT INTO Posts (Id, CreateAt, UpdateAt, DeleteAt, Message, Type, ChannelId, UserId, Props) VALUES ('old-edit', $1, $2, 0, 'edited', '', 'channel1', 'user1', '{}')",
		floor-1000, now-10)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO Posts (Id, CreateAt, UpdateAt, DeleteAt, Message, Type, ChannelId, UserId, Props) VALUES ('new-edit', $1, $2, 0, 'edited', '', 'channel1', 'user1', '{}')",
		floor+1000, now-10)
	require.NoError(t, err)

	var posts []PostRecord
	err = db.SelectContext(context.Background(), &posts, editedPostsFetchSQL(floor), int64(0), "", now, 100, floor)
	require.NoError(t, err)
	require.Len(t, posts, 1)
	assert.Equal(t, "new-edit", posts[0].ID)
}

func TestFilterAndCreateDocsWithFloor(t *testing.T) {
	idx := New(nil, nil, nil, &bots.MMBots{}, nil, nil)
	posts := []PostRecord{
		{ID: "old", Message: "old", UserID: "user1", CreateAt: 50, ChannelType: "O"},
		{ID: "in", Message: "in", UserID: "user1", CreateAt: 150, ChannelType: "O"},
	}
	docs := idx.filterAndCreateDocsWithFloor(posts, 100)
	require.Len(t, docs, 1)
	assert.Equal(t, "in", docs[0].PostID)
}

func TestCheckIndexHealthWidenedEmptyGapThenCatchUp(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	now := model.GetMillis()
	inWindow := now - (30 * embeddings.MillisPerDay)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("in-%d", i)
		_, err := db.Exec("INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type) VALUES ($1, $2, 0, $3, '')", id, inWindow+int64(i), "in")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO llm_posts_embeddings (id, post_id, content, embedding, created_at) VALUES ($1, $1, $2, '[0.1, 0.2, 0.3]', $3)", id, "in", inWindow+int64(i))
		require.NoError(t, err)
	}

	store := &jobKVStore{}
	ts := now - 1000
	store.lastIdx = &ts
	store.model = &ModelInfo{
		ProviderType:       "openai",
		ModelName:          "text-embedding-3-small",
		Dimensions:         1536,
		HNSWM:              embeddings.DefaultHNSWM,
		IndexRetentionDays: retentionDaysPtr(365),
	}

	mockClient := mocks.NewMockClient(t)
	mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
	mockMutexAPI := &plugintest.API{}
	mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()
	store.wire(mockClient)
	mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
	mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()
	mockSearch.On("Store", mock.Anything, mock.Anything).Return(nil).Maybe()

	cfg := embeddings.EmbeddingSearchConfig{
		IndexRetentionDays: 730,
		Dimensions:         1536,
		EmbeddingProvider: embeddings.UpstreamConfig{
			Type:       "openai",
			Parameters: []byte(`{"embeddingModel":"text-embedding-3-small"}`),
		},
	}
	idx := New(func() embeddings.EmbeddingSearch { return mockSearch }, func() embeddings.EmbeddingSearchConfig { return cfg }, mockClient, nil, db, mockMutexAPI)

	health, err := idx.CheckIndexHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), health.DBPostCount)
	assert.Equal(t, int64(3), health.IndexedPostCount)
	assert.Equal(t, int64(0), health.MissingPosts)
	assert.Equal(t, "healthy", health.Status)

	compat := idx.CheckModelCompatibility(ModelInfo{
		ProviderType:       "openai",
		ModelName:          "text-embedding-3-small",
		Dimensions:         1536,
		HNSWM:              embeddings.DefaultHNSWM,
		IndexRetentionDays: retentionDaysPtr(730),
	})
	assert.True(t, compat.Compatible)
	assert.False(t, compat.NeedsReindex)
	assert.True(t, compat.NeedsCatchUp)

	_, err = idx.StartCatchUpJob()
	require.NoError(t, err)
	waitForJobStatus(t, store, JobStatusCompleted, 5*time.Second)

	health, err = idx.CheckIndexHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "healthy", health.Status)
	assert.Equal(t, int64(0), health.MissingPosts)

	compat = idx.CheckModelCompatibility(ModelInfo{
		ProviderType:       "openai",
		ModelName:          "text-embedding-3-small",
		Dimensions:         1536,
		HNSWM:              embeddings.DefaultHNSWM,
		IndexRetentionDays: retentionDaysPtr(730),
	})
	assert.True(t, compat.Compatible)
	assert.False(t, compat.NeedsCatchUp)
	store.mu.Lock()
	require.NotNil(t, store.model.IndexRetentionDays)
	assert.Equal(t, 730, *store.model.IndexRetentionDays)
	assert.Equal(t, "text-embedding-3-small", store.model.ModelName)
	store.mu.Unlock()
}

func TestCatchUpResumeKeepsSkipExistingAndSnapshottedWindow(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	_, err := db.Exec("INSERT INTO Channels (Id, Type, Name, TeamId) VALUES ('channel1', 'O', 'town-square', 'team1')")
	require.NoError(t, err)

	now := model.GetMillis()
	floor730 := now - (730 * embeddings.MillisPerDay)
	floor365 := now - (365 * embeddings.MillisPerDay)
	seeds := []struct {
		id       string
		createAt int64
		indexed  bool
	}{
		{"too-old", floor730 - embeddings.MillisPerDay, false},
		{"gap", floor365 - (100 * embeddings.MillisPerDay), false},
		{"already", floor365 + embeddings.MillisPerDay, true},
	}
	for _, p := range seeds {
		_, err = db.Exec(
			"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ($1, $2, 0, $3, '', 'channel1', 'user1')",
			p.id, p.createAt, "msg "+p.id)
		require.NoError(t, err)
		if p.indexed {
			_, err = db.Exec(
				"INSERT INTO llm_posts_embeddings (id, post_id, content, embedding, created_at) VALUES ($1, $1, $2, '[0.1, 0.2, 0.3]', $3)",
				p.id, "msg "+p.id, p.createAt)
			require.NoError(t, err)
		}
	}

	store := &jobKVStore{}
	raw, err := json.Marshal(JobStatus{
		JobID:              "catch-1",
		Status:             JobStatusFailed,
		Operation:          JobOperationCatchUp,
		Resumable:          true,
		ProcessedRows:      1,
		TotalRows:          2,
		RetentionFloor:     floor730,
		IndexRetentionDays: 730,
		CutoffAt:           now,
		StartedAt:          time.Now().Add(-time.Minute),
	})
	require.NoError(t, err)
	store.jobJSON = raw
	store.cursor = &Cursor{LastCreateAt: floor730, LastID: ""}
	store.model = &ModelInfo{
		ProviderType:       "openai",
		ModelName:          "text-embedding-3-small",
		Dimensions:         1536,
		HNSWM:              embeddings.DefaultHNSWM,
		IndexRetentionDays: retentionDaysPtr(365),
	}

	mockClient := mocks.NewMockClient(t)
	mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
	mockMutexAPI := &plugintest.API{}
	mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()
	store.wire(mockClient)
	mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
	mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

	var stored []string
	var storedMu sync.Mutex
	mockSearch.On("Store", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			docs := args.Get(1).([]embeddings.PostDocument)
			storedMu.Lock()
			for _, doc := range docs {
				stored = append(stored, doc.PostID)
			}
			storedMu.Unlock()
		}).
		Return(nil).Maybe()

	cfg := embeddings.EmbeddingSearchConfig{
		IndexRetentionDays: 3650,
		Dimensions:         1536,
		EmbeddingProvider: embeddings.UpstreamConfig{
			Type:       "openai",
			Parameters: []byte(`{"embeddingModel":"text-embedding-3-small"}`),
		},
	}
	idx := New(func() embeddings.EmbeddingSearch { return mockSearch }, func() embeddings.EmbeddingSearchConfig { return cfg }, mockClient, &bots.MMBots{}, db, mockMutexAPI)

	status, err := idx.StartReindexJob(false)
	require.NoError(t, err)
	assert.Equal(t, JobOperationCatchUp, status.Operation)
	assert.Equal(t, 730, status.IndexRetentionDays)
	assert.Equal(t, floor730, status.RetentionFloor)
	waitForJobStatus(t, store, JobStatusCompleted, 5*time.Second)

	storedMu.Lock()
	defer storedMu.Unlock()
	assert.Equal(t, []string{"gap"}, stored)
	assert.NotContains(t, stored, "already")
	assert.NotContains(t, stored, "too-old")

	store.mu.Lock()
	require.NotNil(t, store.model.IndexRetentionDays)
	assert.Equal(t, 730, *store.model.IndexRetentionDays)
	assert.Equal(t, "text-embedding-3-small", store.model.ModelName)
	assert.Equal(t, 1536, store.model.Dimensions)
	store.mu.Unlock()
}

func TestCatchUpIgnoresMidJobRetentionWiden(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	_, err := db.Exec("INSERT INTO Channels (Id, Type, Name, TeamId) VALUES ('channel1', 'O', 'town-square', 'team1')")
	require.NoError(t, err)

	now := model.GetMillis()
	floor730 := now - (730 * embeddings.MillisPerDay)
	_, err = db.Exec(
		"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ('too-old', $1, 0, 'old', '', 'channel1', 'user1')",
		floor730-embeddings.MillisPerDay)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ('gap', $1, 0, 'gap', '', 'channel1', 'user1')",
		now-(400*embeddings.MillisPerDay))
	require.NoError(t, err)

	var days atomic.Int64
	days.Store(730)

	store := &jobKVStore{}
	ts := now - 1000
	store.lastIdx = &ts
	store.model = &ModelInfo{
		ProviderType:       "openai",
		ModelName:          "old-model",
		Dimensions:         768,
		HNSWM:              embeddings.DefaultHNSWM,
		IndexRetentionDays: retentionDaysPtr(365),
	}

	mockClient := mocks.NewMockClient(t)
	mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
	mockMutexAPI := &plugintest.API{}
	mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()
	store.wire(mockClient)
	mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
	mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

	var stored []string
	var storedMu sync.Mutex
	mockSearch.On("Store", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			docs := args.Get(1).([]embeddings.PostDocument)
			storedMu.Lock()
			for _, doc := range docs {
				stored = append(stored, doc.PostID)
			}
			storedMu.Unlock()
		}).
		Return(nil).Maybe()

	reachedPersist := make(chan struct{})
	releasePersist := make(chan struct{})

	idx := New(func() embeddings.EmbeddingSearch { return mockSearch }, func() embeddings.EmbeddingSearchConfig {
		return embeddings.EmbeddingSearchConfig{
			IndexRetentionDays: int(days.Load()),
			Dimensions:         1536,
			EmbeddingProvider: embeddings.UpstreamConfig{
				Type:       "openai",
				Parameters: []byte(`{"embeddingModel":"text-embedding-3-small"}`),
			},
		}
	}, mockClient, &bots.MMBots{}, db, mockMutexAPI)
	idx.beforePersistModelInfo = func() {
		close(reachedPersist)
		<-releasePersist
	}

	status, err := idx.StartCatchUpJob()
	require.NoError(t, err)
	assert.Equal(t, 730, status.IndexRetentionDays)

	select {
	case <-reachedPersist:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach model-info persist")
	}
	days.Store(0)
	close(releasePersist)

	waitForStoredRetentionDays(t, store, 730, 5*time.Second)

	storedMu.Lock()
	defer storedMu.Unlock()
	assert.Equal(t, []string{"gap"}, stored)
	assert.NotContains(t, stored, "too-old")

	store.mu.Lock()
	assert.Equal(t, "old-model", store.model.ModelName)
	assert.Equal(t, 768, store.model.Dimensions)
	store.mu.Unlock()
}

func TestJobStartUsesSingleConfigSnapshot(t *testing.T) {
	tests := []struct {
		name      string
		startJob  func(idx *Indexer) (JobStatus, error)
		wantStore []string
	}{
		{
			name: "catch-up",
			startJob: func(idx *Indexer) (JobStatus, error) {
				return idx.StartCatchUpJob()
			},
			wantStore: []string{"in-window"},
		},
		{
			name: "full reindex",
			startJob: func(idx *Indexer) (JobStatus, error) {
				return idx.StartReindexJob(true)
			},
			wantStore: []string{"in-window"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			defer cleanupDB(t, db)

			_, err := db.Exec("INSERT INTO Channels (Id, Type, Name, TeamId) VALUES ('channel1', 'O', 'town-square', 'team1')")
			require.NoError(t, err)

			now := model.GetMillis()
			floor365 := now - (365 * embeddings.MillisPerDay)
			_, err = db.Exec(
				"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ('in-window', $1, 0, 'in', '', 'channel1', 'user1')",
				floor365+embeddings.MillisPerDay)
			require.NoError(t, err)
			_, err = db.Exec(
				"INSERT INTO Posts (Id, CreateAt, DeleteAt, Message, Type, ChannelId, UserId) VALUES ('older-year', $1, 0, 'old', '', 'channel1', 'user1')",
				floor365-(100*embeddings.MillisPerDay))
			require.NoError(t, err)

			var reads atomic.Int32
			store := &jobKVStore{}
			ts := now - 1000
			store.lastIdx = &ts
			store.model = &ModelInfo{
				ProviderType:       "openai",
				ModelName:          "text-embedding-3-small",
				Dimensions:         1536,
				HNSWM:              embeddings.DefaultHNSWM,
				IndexRetentionDays: retentionDaysPtr(180),
			}

			mockClient := mocks.NewMockClient(t)
			mockSearch := embeddingsmocks.NewMockEmbeddingSearch(t)
			mockMutexAPI := &plugintest.API{}
			mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
			mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()
			store.wire(mockClient)
			mockClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
			mockClient.On("LogError", mock.Anything, mock.Anything).Return().Maybe()
			mockSearch.On("Clear", mock.Anything).Return(nil).Maybe()

			var stored []string
			var storedMu sync.Mutex
			mockSearch.On("Store", mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					docs := args.Get(1).([]embeddings.PostDocument)
					storedMu.Lock()
					for _, doc := range docs {
						stored = append(stored, doc.PostID)
					}
					storedMu.Unlock()
				}).
				Return(nil).Maybe()

			idx := New(func() embeddings.EmbeddingSearch { return mockSearch }, func() embeddings.EmbeddingSearchConfig {
				days := 730
				if reads.Add(1) == 1 {
					days = 365
				}
				return embeddings.EmbeddingSearchConfig{
					IndexRetentionDays: days,
					Dimensions:         1536,
					EmbeddingProvider: embeddings.UpstreamConfig{
						Type:       "openai",
						Parameters: []byte(`{"embeddingModel":"text-embedding-3-small"}`),
					},
				}
			}, mockClient, &bots.MMBots{}, db, mockMutexAPI)

			status, err := tt.startJob(idx)
			require.NoError(t, err)
			assert.Equal(t, 365, status.IndexRetentionDays)
			assert.InDelta(t, float64(floor365), float64(status.RetentionFloor), 5000)

			waitForStoredRetentionDays(t, store, 365, 5*time.Second)

			storedMu.Lock()
			defer storedMu.Unlock()
			assert.ElementsMatch(t, tt.wantStore, stored)
			assert.NotContains(t, stored, "older-year")
		})
	}
}
