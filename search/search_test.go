// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/chunking"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/indexer"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// allowVectorIndexStateRead: availability check sees no deferred reindex.
func allowVectorIndexStateRead(m *mmapimocks.MockClient) {
	m.On("KVGet", indexer.VectorIndexStateKey, mock.Anything).Return(mmapi.ErrKVNotFound).Maybe()
}

func licensedChecker() *enterprise.LicenseChecker {
	return enterprisetest.CheckerAt(enterprise.LevelEnterprise)
}

func TestEnrichResults(t *testing.T) {
	tests := []struct {
		name          string
		searchResults []embeddings.SearchResult
		setupMock     func(*mmapimocks.MockClient)
		expectedLen   int
		validate      func(t *testing.T, results []RAGResult)
	}{
		{
			name:          "empty input returns empty slice",
			searchResults: []embeddings.SearchResult{},
			setupMock:     nil,
			expectedLen:   0,
		},
		{
			name: "single result with public channel",
			searchResults: []embeddings.SearchResult{
				{
					Document: embeddings.PostDocument{
						PostID:    "post1",
						ChannelID: "channel1",
						UserID:    "user1",
						Content:   "test content",
					},
					Score: 0.95,
				},
			},
			setupMock: func(m *mmapimocks.MockClient) {
				m.On("GetChannel", "channel1").Return(&model.Channel{
					Id:          "channel1",
					DisplayName: "General",
					Type:        model.ChannelTypeOpen,
				}, nil)
				m.On("GetUser", "user1").Return(&model.User{
					Id:       "user1",
					Username: "testuser",
				}, nil)
			},
			expectedLen: 1,
			validate: func(t *testing.T, results []RAGResult) {
				require.Equal(t, "post1", results[0].PostID)
				require.Equal(t, "channel1", results[0].ChannelID)
				require.Equal(t, "General", results[0].ChannelName)
				require.Equal(t, "user1", results[0].UserID)
				require.Equal(t, "testuser", results[0].Username)
				require.Equal(t, "test content", results[0].Content)
				require.Equal(t, float32(0.95), results[0].Score)
			},
		},
		{
			name: "single result with DM channel",
			searchResults: []embeddings.SearchResult{
				{
					Document: embeddings.PostDocument{
						PostID:    "post1",
						ChannelID: "dm1",
						UserID:    "user1",
						Content:   "dm content",
					},
					Score: 0.9,
				},
			},
			setupMock: func(m *mmapimocks.MockClient) {
				m.On("GetChannel", "dm1").Return(&model.Channel{
					Id:   "dm1",
					Type: model.ChannelTypeDirect,
				}, nil)
				m.On("GetUser", "user1").Return(&model.User{
					Id:       "user1",
					Username: "testuser",
				}, nil)
			},
			expectedLen: 1,
			validate: func(t *testing.T, results []RAGResult) {
				require.Equal(t, "Direct Message", results[0].ChannelName)
			},
		},
		{
			name: "single result with group channel",
			searchResults: []embeddings.SearchResult{
				{
					Document: embeddings.PostDocument{
						PostID:    "post1",
						ChannelID: "group1",
						UserID:    "user1",
						Content:   "group content",
					},
					Score: 0.85,
				},
			},
			setupMock: func(m *mmapimocks.MockClient) {
				m.On("GetChannel", "group1").Return(&model.Channel{
					Id:   "group1",
					Type: model.ChannelTypeGroup,
				}, nil)
				m.On("GetUser", "user1").Return(&model.User{
					Id:       "user1",
					Username: "testuser",
				}, nil)
			},
			expectedLen: 1,
			validate: func(t *testing.T, results []RAGResult) {
				require.Equal(t, "Group Message", results[0].ChannelName)
			},
		},
		{
			name: "chunked result appends chunk info",
			searchResults: []embeddings.SearchResult{
				{
					Document: embeddings.PostDocument{
						PostID:    "post1",
						ChannelID: "channel1",
						UserID:    "user1",
						Content:   "chunk content",
						ChunkInfo: chunking.ChunkInfo{
							IsChunk:     true,
							ChunkIndex:  2,
							TotalChunks: 5,
						},
					},
					Score: 0.8,
				},
			},
			setupMock: func(m *mmapimocks.MockClient) {
				m.On("GetChannel", "channel1").Return(&model.Channel{
					Id:          "channel1",
					DisplayName: "General",
					Type:        model.ChannelTypeOpen,
				}, nil)
				m.On("GetUser", "user1").Return(&model.User{
					Id:       "user1",
					Username: "testuser",
				}, nil)
			},
			expectedLen: 1,
			validate: func(t *testing.T, results []RAGResult) {
				require.Equal(t, "General (Chunk 3 of 5)", results[0].ChannelName)
			},
		},
		{
			name: "channel fetch error falls back to Unknown Channel",
			searchResults: []embeddings.SearchResult{
				{
					Document: embeddings.PostDocument{
						PostID:    "post1",
						ChannelID: "channel1",
						UserID:    "user1",
						Content:   "test content",
					},
					Score: 0.9,
				},
			},
			setupMock: func(m *mmapimocks.MockClient) {
				m.On("GetChannel", "channel1").Return(nil, errors.New("channel not found"))
				m.On("LogWarn", mock.Anything, mock.Anything).Return()
				m.On("GetUser", "user1").Return(&model.User{
					Id:       "user1",
					Username: "testuser",
				}, nil)
			},
			expectedLen: 1,
			validate: func(t *testing.T, results []RAGResult) {
				require.Equal(t, "Unknown Channel", results[0].ChannelName)
				require.Equal(t, "testuser", results[0].Username)
			},
		},
		{
			name: "user fetch error falls back to Unknown User",
			searchResults: []embeddings.SearchResult{
				{
					Document: embeddings.PostDocument{
						PostID:    "post1",
						ChannelID: "channel1",
						UserID:    "user1",
						Content:   "test content",
					},
					Score: 0.9,
				},
			},
			setupMock: func(m *mmapimocks.MockClient) {
				m.On("GetChannel", "channel1").Return(&model.Channel{
					Id:          "channel1",
					DisplayName: "General",
					Type:        model.ChannelTypeOpen,
				}, nil)
				m.On("GetUser", "user1").Return(nil, errors.New("user not found"))
				m.On("LogWarn", mock.Anything, mock.Anything).Return()
			},
			expectedLen: 1,
			validate: func(t *testing.T, results []RAGResult) {
				require.Equal(t, "General", results[0].ChannelName)
				require.Equal(t, "Unknown User", results[0].Username)
			},
		},
		{
			name: "multiple results processes all",
			searchResults: []embeddings.SearchResult{
				{
					Document: embeddings.PostDocument{
						PostID:    "post1",
						CreateAt:  1700000000000,
						ChannelID: "channel1",
						UserID:    "user1",
						Content:   "content 1",
					},
					Score: 0.95,
				},
				{
					Document: embeddings.PostDocument{
						PostID:    "post2",
						CreateAt:  1700000060000,
						ChannelID: "channel2",
						UserID:    "user2",
						Content:   "content 2",
					},
					Score: 0.85,
				},
			},
			setupMock: func(m *mmapimocks.MockClient) {
				m.On("GetChannel", "channel1").Return(&model.Channel{
					Id:          "channel1",
					DisplayName: "Channel One",
					Type:        model.ChannelTypeOpen,
				}, nil)
				m.On("GetChannel", "channel2").Return(&model.Channel{
					Id:          "channel2",
					DisplayName: "Channel Two",
					Type:        model.ChannelTypeOpen,
				}, nil)
				m.On("GetUser", "user1").Return(&model.User{
					Id:       "user1",
					Username: "user_one",
				}, nil)
				m.On("GetUser", "user2").Return(&model.User{
					Id:       "user2",
					Username: "user_two",
				}, nil)
			},
			expectedLen: 2,
			validate: func(t *testing.T, results []RAGResult) {
				require.Equal(t, "post1", results[0].PostID)
				require.Equal(t, "Channel One", results[0].ChannelName)
				require.Equal(t, "user_one", results[0].Username)
				require.Equal(t, int64(1700000000000), results[0].CreateAt)
				require.Equal(t, "post2", results[1].PostID)
				require.Equal(t, "Channel Two", results[1].ChannelName)
				require.Equal(t, "user_two", results[1].Username)
				require.Equal(t, int64(1700000060000), results[1].CreateAt)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := mmapimocks.NewMockClient(t)
			allowVectorIndexStateRead(mockClient)
			if tc.setupMock != nil {
				tc.setupMock(mockClient)
			}

			s := New(nil, mockClient, nil)
			results := s.enrichResults(tc.searchResults)

			require.Len(t, results, tc.expectedLen)
			if tc.validate != nil {
				tc.validate(t, results)
			}
		})
	}
}

func TestExecuteSearch(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		opts        Options
		setupMocks  func(*mocks.MockEmbeddingSearch, *mmapimocks.MockClient)
		expectError string
		validate    func(t *testing.T, results []RAGResult)
	}{
		{
			name:        "empty query returns error",
			query:       "",
			opts:        Options{},
			setupMocks:  nil,
			expectError: "query cannot be empty",
		},
		{
			name:  "search error is propagated",
			query: "test query",
			opts:  Options{Limit: 5},
			setupMocks: func(me *mocks.MockEmbeddingSearch, mc *mmapimocks.MockClient) {
				me.On("Search", mock.Anything, "test query", mock.Anything).
					Return(nil, errors.New("search service unavailable"))
			},
			expectError: "search failed: search service unavailable",
		},
		{
			name:  "no results returns empty slice",
			query: "obscure query",
			opts:  Options{Limit: 5},
			setupMocks: func(me *mocks.MockEmbeddingSearch, mc *mmapimocks.MockClient) {
				me.On("Search", mock.Anything, "obscure query", mock.Anything).
					Return([]embeddings.SearchResult{}, nil)
			},
			expectError: "",
			validate: func(t *testing.T, results []RAGResult) {
				require.Empty(t, results)
			},
		},
		{
			name:  "with results returns enriched RAGResults",
			query: "test query",
			opts: Options{
				Limit:     5,
				TeamID:    "team1",
				ChannelID: "channel1",
				UserID:    "user1",
			},
			setupMocks: func(me *mocks.MockEmbeddingSearch, mc *mmapimocks.MockClient) {
				me.On("Search", mock.Anything, "test query", embeddings.SearchOptions{
					Limit:     5,
					TeamID:    "team1",
					ChannelID: "channel1",
					UserID:    "user1",
				}).Return([]embeddings.SearchResult{
					{
						Document: embeddings.PostDocument{
							PostID:    "post1",
							ChannelID: "channel1",
							UserID:    "user1",
							Content:   "test content",
						},
						Score: 0.9,
					},
				}, nil)
				mc.On("GetChannel", "channel1").Return(&model.Channel{
					Id:          "channel1",
					DisplayName: "General",
					Type:        model.ChannelTypeOpen,
				}, nil)
				mc.On("GetUser", "user1").Return(&model.User{
					Id:       "user1",
					Username: "testuser",
				}, nil)
			},
			expectError: "",
			validate: func(t *testing.T, results []RAGResult) {
				require.Len(t, results, 1)
				require.Equal(t, "post1", results[0].PostID)
				require.Equal(t, "General", results[0].ChannelName)
				require.Equal(t, "testuser", results[0].Username)
				require.Equal(t, "test content", results[0].Content)
			},
		},
		{
			name:  "default limit is 5 when 0 is passed",
			query: "test query",
			opts:  Options{Limit: 0}, // Should default to 5
			setupMocks: func(me *mocks.MockEmbeddingSearch, mc *mmapimocks.MockClient) {
				me.On("Search", mock.Anything, "test query", embeddings.SearchOptions{
					Limit: 5, // Should be 5, not 0
				}).Return([]embeddings.SearchResult{}, nil)
			},
			expectError: "",
			validate: func(t *testing.T, results []RAGResult) {
				require.Empty(t, results)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockEmbedding := mocks.NewMockEmbeddingSearch(t)
			mockClient := mmapimocks.NewMockClient(t)
			allowVectorIndexStateRead(mockClient)

			if tc.setupMocks != nil {
				tc.setupMocks(mockEmbedding, mockClient)
			}

			s := New(func() embeddings.EmbeddingSearch { return mockEmbedding }, mockClient, licensedChecker())
			results, err := s.Search(context.Background(), tc.query, tc.opts)

			if tc.expectError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectError)
				require.Nil(t, results)
			} else {
				require.NoError(t, err)
				if tc.validate != nil {
					tc.validate(t, results)
				}
			}
		})
	}
}

type fixedDocsSearch struct {
	docs []embeddings.PostDocument
}

func (s *fixedDocsSearch) Store(context.Context, []embeddings.PostDocument) error { return nil }
func (s *fixedDocsSearch) Delete(context.Context, []string) error                 { return nil }
func (s *fixedDocsSearch) Clear(context.Context) error                            { return nil }
func (s *fixedDocsSearch) DeleteOrphaned(context.Context, int64, int64) (int64, error) {
	return 0, nil
}
func (s *fixedDocsSearch) Search(context.Context, string, embeddings.SearchOptions) ([]embeddings.SearchResult, error) {
	out := make([]embeddings.SearchResult, 0, len(s.docs))
	for _, d := range s.docs {
		out = append(out, embeddings.SearchResult{Document: d, Score: 1})
	}
	return out, nil
}

func TestExecuteSearchReturnsIndexedRowsOutsideWriteWindow(t *testing.T) {
	now := time.Now().UnixMilli()
	cfg := embeddings.EmbeddingSearchConfig{IndexRetentionDays: 365}
	floor := cfg.IndexRetentionFloor(now)
	stale := embeddings.PostDocument{
		PostID:    "stale",
		CreateAt:  floor - embeddings.MillisPerDay,
		ChannelID: "channel1",
		UserID:    "user1",
		Content:   "old",
	}
	fresh := embeddings.PostDocument{
		PostID:    "fresh",
		CreateAt:  floor + embeddings.MillisPerDay,
		ChannelID: "channel1",
		UserID:    "user1",
		Content:   "new",
	}

	mockClient := mmapimocks.NewMockClient(t)
	allowVectorIndexStateRead(mockClient)
	mockClient.On("GetChannel", "channel1").Return(&model.Channel{
		Id:          "channel1",
		DisplayName: "General",
		Type:        model.ChannelTypeOpen,
	}, nil).Maybe()
	mockClient.On("GetUser", "user1").Return(&model.User{
		Id:       "user1",
		Username: "testuser",
	}, nil).Maybe()

	store := &fixedDocsSearch{docs: []embeddings.PostDocument{stale, fresh}}
	s := New(func() embeddings.EmbeddingSearch { return store }, mockClient, licensedChecker())

	results, err := s.Search(context.Background(), "test query", Options{Limit: 5})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "stale", results[0].PostID)
	require.Equal(t, "fresh", results[1].PostID)
}

// mockDeferredReindexActive gates search via deferred reindex state.
func mockDeferredReindexActive(m *mmapimocks.MockClient) {
	m.On("KVGet", indexer.VectorIndexStateKey, mock.AnythingOfType("*indexer.VectorIndexState")).
		Run(func(args mock.Arguments) {
			state := args.Get(1).(*indexer.VectorIndexState)
			state.JobID = "job1"
			state.Phase = indexer.VectorIndexPhaseDropped
		}).
		Return(nil)
}

func TestSearchUnavailableDuringDeferredReindex(t *testing.T) {
	t.Run("Search returns ErrSearchUnavailable without querying the store", func(t *testing.T) {
		// Strict mock: any Search call on the store fails the test.
		mockEmbedding := mocks.NewMockEmbeddingSearch(t)
		mockClient := mmapimocks.NewMockClient(t)
		mockDeferredReindexActive(mockClient)

		s := New(func() embeddings.EmbeddingSearch { return mockEmbedding }, mockClient, licensedChecker())
		results, err := s.Search(context.Background(), "test query", Options{Limit: 5})

		require.ErrorIs(t, err, ErrSearchUnavailable)
		require.Nil(t, results)
	})
}

func TestEnrichResultsSameChannelMultipleTimes(t *testing.T) {
	// Test that enrichResults correctly populates channel/user info
	// when the same channel appears in multiple results
	mockClient := mmapimocks.NewMockClient(t)
	allowVectorIndexStateRead(mockClient)

	mockClient.On("GetChannel", "channel1").Return(&model.Channel{
		Id:          "channel1",
		DisplayName: "General",
		Type:        model.ChannelTypeOpen,
	}, nil)

	mockClient.On("GetUser", "user1").Return(&model.User{
		Id:       "user1",
		Username: "testuser",
	}, nil)

	searchResults := []embeddings.SearchResult{
		{
			Document: embeddings.PostDocument{
				PostID:    "post1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "content 1",
			},
			Score: 0.9,
		},
		{
			Document: embeddings.PostDocument{
				PostID:    "post2",
				ChannelID: "channel1", // Same channel
				UserID:    "user1",    // Same user
				Content:   "content 2",
			},
			Score: 0.85,
		},
	}

	s := New(nil, mockClient, nil)
	results := s.enrichResults(searchResults)

	require.Len(t, results, 2)
	require.Equal(t, "General", results[0].ChannelName)
	require.Equal(t, "General", results[1].ChannelName)
	require.Equal(t, "testuser", results[0].Username)
	require.Equal(t, "testuser", results[1].Username)
}

func TestEnrichResultsSameUserMultipleTimes(t *testing.T) {
	// Test that enrichResults correctly populates user info
	// when the same user appears in results across different channels
	mockClient := mmapimocks.NewMockClient(t)
	allowVectorIndexStateRead(mockClient)

	mockClient.On("GetChannel", "channel1").Return(&model.Channel{
		Id:          "channel1",
		DisplayName: "Channel One",
		Type:        model.ChannelTypeOpen,
	}, nil)
	mockClient.On("GetChannel", "channel2").Return(&model.Channel{
		Id:          "channel2",
		DisplayName: "Channel Two",
		Type:        model.ChannelTypeOpen,
	}, nil)

	mockClient.On("GetUser", "user1").Return(&model.User{
		Id:       "user1",
		Username: "testuser",
	}, nil)

	searchResults := []embeddings.SearchResult{
		{
			Document: embeddings.PostDocument{
				PostID:    "post1",
				ChannelID: "channel1",
				UserID:    "user1",
				Content:   "content 1",
			},
			Score: 0.9,
		},
		{
			Document: embeddings.PostDocument{
				PostID:    "post2",
				ChannelID: "channel2",
				UserID:    "user1", // Same user
				Content:   "content 2",
			},
			Score: 0.85,
		},
	}

	s := New(nil, mockClient, nil)
	results := s.enrichResults(searchResults)

	require.Len(t, results, 2)
	require.Equal(t, "testuser", results[0].Username)
	require.Equal(t, "testuser", results[1].Username)
}

func TestExecuteSearchNotConfigured(t *testing.T) {
	// Test Search when getSearch() returns nil
	s := New(func() embeddings.EmbeddingSearch { return nil }, nil, licensedChecker())

	results, err := s.Search(context.Background(), "test query", Options{})

	require.Error(t, err)
	require.Contains(t, err.Error(), "embedding search not configured")
	require.Nil(t, results)
}
