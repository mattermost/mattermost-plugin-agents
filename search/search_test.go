// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/chunking"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	llmmocks "github.com/mattermost/mattermost-plugin-ai/llm/mocks"
	mmapimocks "github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
						ChannelID: "channel1",
						UserID:    "user1",
						Content:   "content 1",
					},
					Score: 0.95,
				},
				{
					Document: embeddings.PostDocument{
						PostID:    "post2",
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
				require.Equal(t, "post2", results[1].PostID)
				require.Equal(t, "Channel Two", results[1].ChannelName)
				require.Equal(t, "user_two", results[1].Username)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := mmapimocks.NewMockClient(t)
			if tc.setupMock != nil {
				tc.setupMock(mockClient)
			}

			s := New(nil, mockClient, nil, nil, nil)
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

			if tc.setupMocks != nil {
				tc.setupMocks(mockEmbedding, mockClient)
			}

			s := New(func() embeddings.EmbeddingSearch { return mockEmbedding }, mockClient, nil, nil, nil)
			results, err := s.executeSearch(context.Background(), tc.query, tc.opts)

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

func TestBuildPrompt(t *testing.T) {
	// Load real prompts for tests
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err, "Failed to load prompts")

	tests := []struct {
		name        string
		query       string
		results     []RAGResult
		expectError bool
		validate    func(t *testing.T, req llm.CompletionRequest)
	}{
		{
			name:  "builds correct structure with system and user roles",
			query: "test query",
			results: []RAGResult{
				{PostID: "post1", Content: "content", ChannelName: "General", Username: "testuser", Score: 0.9},
			},
			expectError: false,
			validate: func(t *testing.T, req llm.CompletionRequest) {
				require.Len(t, req.Posts, 2)
				require.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
				require.NotEmpty(t, req.Posts[0].Message)
				require.Equal(t, llm.PostRoleUser, req.Posts[1].Role)
				require.Equal(t, "test query", req.Posts[1].Message)
			},
		},
		{
			name:  "context contains query and results",
			query: "another query",
			results: []RAGResult{
				{PostID: "post1", Content: "test content", ChannelName: "General", Username: "user1", Score: 0.8},
			},
			expectError: false,
			validate: func(t *testing.T, req llm.CompletionRequest) {
				require.NotNil(t, req.Context)
				require.Equal(t, "another query", req.Context.Parameters["Query"])
				results := req.Context.Parameters["Results"].([]RAGResult)
				require.Len(t, results, 1)
			},
		},
		{
			name:  "system message contains search results content",
			query: "search query",
			results: []RAGResult{
				{PostID: "post1", Content: "important information", ChannelName: "General", Username: "testuser", Score: 0.95},
			},
			expectError: false,
			validate: func(t *testing.T, req llm.CompletionRequest) {
				require.Contains(t, req.Posts[0].Message, "important information")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New(nil, nil, promptsObj, nil, nil)
			req, err := s.buildPrompt("", nil, tc.query, "", "", tc.results, "")

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if tc.validate != nil {
					tc.validate(t, req)
				}
			}
		})
	}
}

func TestSearchQuery(t *testing.T) {
	// Load real prompts for tests
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err, "Failed to load prompts")

	tests := []struct {
		name        string
		setupMocks  func(*mocks.MockEmbeddingSearch, *mmapimocks.MockClient, *llmmocks.MockLanguageModel)
		query       string
		expectError string
		validate    func(t *testing.T, resp Response)
	}{
		{
			name: "zero results returns empty response with message",
			setupMocks: func(me *mocks.MockEmbeddingSearch, mc *mmapimocks.MockClient, ml *llmmocks.MockLanguageModel) {
				me.On("Search", mock.Anything, "no results query", mock.Anything).
					Return([]embeddings.SearchResult{}, nil)
			},
			query:       "no results query",
			expectError: "",
			validate: func(t *testing.T, resp Response) {
				require.Contains(t, resp.Answer, "couldn't find any relevant messages")
				require.Empty(t, resp.Results)
			},
		},
		{
			name: "LLM failure returns error",
			setupMocks: func(me *mocks.MockEmbeddingSearch, mc *mmapimocks.MockClient, ml *llmmocks.MockLanguageModel) {
				me.On("Search", mock.Anything, "test query", mock.Anything).
					Return([]embeddings.SearchResult{
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
				siteURL := "http://localhost:8065"
				mc.On("GetConfig").Return(&model.Config{
					ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
				})
				ml.On("ChatCompletionNoStream", mock.Anything).
					Return("", errors.New("LLM service unavailable"))
			},
			query:       "test query",
			expectError: "failed to generate answer",
		},
		{
			name: "successful search with LLM response",
			setupMocks: func(me *mocks.MockEmbeddingSearch, mc *mmapimocks.MockClient, ml *llmmocks.MockLanguageModel) {
				me.On("Search", mock.Anything, "test query", mock.Anything).
					Return([]embeddings.SearchResult{
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
				siteURL := "http://localhost:8065"
				mc.On("GetConfig").Return(&model.Config{
					ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
				})
				ml.On("ChatCompletionNoStream", mock.Anything).
					Return("Based on the search results, here is the answer.", nil)
			},
			query:       "test query",
			expectError: "",
			validate: func(t *testing.T, resp Response) {
				require.Equal(t, "Based on the search results, here is the answer.", resp.Answer)
				require.Len(t, resp.Results, 1)
				require.Equal(t, "post1", resp.Results[0].PostID)
			},
		},
		{
			name: "search failure propagates error",
			setupMocks: func(me *mocks.MockEmbeddingSearch, mc *mmapimocks.MockClient, ml *llmmocks.MockLanguageModel) {
				me.On("Search", mock.Anything, "test query", mock.Anything).
					Return(nil, errors.New("search service unavailable"))
			},
			query:       "test query",
			expectError: "search failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockEmbedding := mocks.NewMockEmbeddingSearch(t)
			mockClient := mmapimocks.NewMockClient(t)
			mockLLM := llmmocks.NewMockLanguageModel(t)

			if tc.setupMocks != nil {
				tc.setupMocks(mockEmbedding, mockClient, mockLLM)
			}

			s := New(
				func() embeddings.EmbeddingSearch { return mockEmbedding },
				mockClient,
				promptsObj,
				nil,
				nil,
			)

			// Create a bot with the mock LLM
			bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, mockLLM)

			resp, err := s.SearchQuery(context.Background(), "user1", bot, tc.query, "", "", 5)

			if tc.expectError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectError)
			} else {
				require.NoError(t, err)
				if tc.validate != nil {
					tc.validate(t, resp)
				}
			}
		})
	}
}

func TestRunSearch(t *testing.T) {
	t.Run("search not enabled returns error", func(t *testing.T) {
		mockClient := mmapimocks.NewMockClient(t)
		s := New(func() embeddings.EmbeddingSearch { return nil }, mockClient, nil, nil, nil)
		bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, nil)

		_, err := s.RunSearch(context.Background(), "user1", bot, "test query", "", "", 5)

		require.Error(t, err)
		require.Contains(t, err.Error(), "search functionality is not configured")
	})

	t.Run("empty query returns error", func(t *testing.T) {
		mockEmbedding := mocks.NewMockEmbeddingSearch(t)
		mockClient := mmapimocks.NewMockClient(t)
		s := New(func() embeddings.EmbeddingSearch { return mockEmbedding }, mockClient, nil, nil, nil)
		bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, nil)

		_, err := s.RunSearch(context.Background(), "user1", bot, "", "", "", 5)

		require.Error(t, err)
		require.Contains(t, err.Error(), "query cannot be empty")
	})

	t.Run("DM creation failure returns error", func(t *testing.T) {
		mockEmbedding := mocks.NewMockEmbeddingSearch(t)
		mockClient := mmapimocks.NewMockClient(t)
		mockClient.On("DM", "user1", "bot1", mock.Anything).
			Return(errors.New("failed to create DM"))

		s := New(func() embeddings.EmbeddingSearch { return mockEmbedding }, mockClient, nil, nil, nil)
		bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, nil)

		_, err := s.RunSearch(context.Background(), "user1", bot, "test query", "", "", 5)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to create question post")
	})

	t.Run("successful RunSearch returns post info", func(t *testing.T) {
		mockEmbedding := mocks.NewMockEmbeddingSearch(t)
		mockClient := mmapimocks.NewMockClient(t)

		// First DM is for question post (synchronous)
		mockClient.On("DM", "user1", "bot1", mock.Anything).
			Run(func(args mock.Arguments) {
				post := args.Get(2).(*model.Post)
				post.Id = "question_post_id"
				post.ChannelId = "dm_channel_id"
			}).
			Return(nil).Once()

		// Second DM is for response post (async in goroutine) - use Maybe since test may finish before goroutine
		mockClient.On("DM", "bot1", "user1", mock.Anything).Return(nil).Maybe()

		// The goroutine may call LogError if the search fails - use Maybe to handle both cases
		mockClient.On("LogError", mock.Anything, mock.Anything).Maybe()

		// The goroutine may call Search - set up to return empty results to avoid further processing
		mockEmbedding.On("Search", mock.Anything, mock.Anything, mock.Anything).
			Return([]embeddings.SearchResult{}, nil).Maybe()

		// If zero results, UpdatePost is called
		mockClient.On("UpdatePost", mock.Anything).Return(nil).Maybe()

		s := New(func() embeddings.EmbeddingSearch { return mockEmbedding }, mockClient, nil, nil, nil)
		bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, nil)

		result, err := s.RunSearch(context.Background(), "user1", bot, "test query", "", "", 5)

		require.NoError(t, err)
		require.Equal(t, "question_post_id", result["postid"])
		require.Equal(t, "dm_channel_id", result["channelid"])
	})
}

func TestEnrichResultsSameChannelMultipleTimes(t *testing.T) {
	// Test that enrichResults correctly populates channel/user info
	// when the same channel appears in multiple results
	mockClient := mmapimocks.NewMockClient(t)

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

	s := New(nil, mockClient, nil, nil, nil)
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

	s := New(nil, mockClient, nil, nil, nil)
	results := s.enrichResults(searchResults)

	require.Len(t, results, 2)
	require.Equal(t, "testuser", results[0].Username)
	require.Equal(t, "testuser", results[1].Username)
}

func TestBuildPromptWithNilPrompts(t *testing.T) {
	// Test that buildPrompt fails gracefully when prompts are nil
	s := New(nil, nil, nil, nil, nil)
	_, err := s.buildPrompt("", nil, "test query", "", "", []RAGResult{}, "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to format prompt")
}

func TestBuildPromptWithLargeResults(t *testing.T) {
	// Load real prompts for tests
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err, "Failed to load prompts")

	// Create a large result set
	var largeResults []RAGResult
	for i := 0; i < 100; i++ {
		largeResults = append(largeResults, RAGResult{
			PostID:      fmt.Sprintf("post%d", i),
			ChannelID:   fmt.Sprintf("channel%d", i),
			ChannelName: fmt.Sprintf("Channel %d", i),
			UserID:      fmt.Sprintf("user%d", i),
			Username:    fmt.Sprintf("username%d", i),
			Content:     fmt.Sprintf("This is some content for result %d that adds some length to the prompt", i),
			Score:       float32(0.9 - float32(i)*0.001),
		})
	}

	s := New(nil, nil, promptsObj, nil, nil)
	req, err := s.buildPrompt("", nil, "test query with many results", "", "", largeResults, "")

	// Should succeed - prompt size is handled by the template
	require.NoError(t, err)
	require.NotEmpty(t, req.Posts[0].Message)
	require.Contains(t, req.Posts[0].Message, "username0")  // First result should be included
	require.Contains(t, req.Posts[0].Message, "username99") // Last result should be included
	require.Contains(t, req.Posts[0].Message, "post0")      // PostID should be included for citations
	require.Contains(t, req.Posts[0].Message, "Channel 0")
	require.Contains(t, req.Posts[0].Message, "Channel 99")
}

func TestExecuteSearchNotConfigured(t *testing.T) {
	// Test executeSearch when getSearch() returns nil
	s := New(func() embeddings.EmbeddingSearch { return nil }, nil, nil, nil, nil)

	results, err := s.executeSearch(context.Background(), "test query", Options{})

	require.Error(t, err)
	require.Contains(t, err.Error(), "embedding search not configured")
	require.Nil(t, results)
}

func TestSearchQueryWithEmptyQuery(t *testing.T) {
	// Load real prompts for tests
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err, "Failed to load prompts")

	mockEmbedding := mocks.NewMockEmbeddingSearch(t)
	mockClient := mmapimocks.NewMockClient(t)

	s := New(
		func() embeddings.EmbeddingSearch { return mockEmbedding },
		mockClient,
		promptsObj,
		nil,
		nil,
	)

	bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, nil)

	_, err = s.SearchQuery(context.Background(), "user1", bot, "", "", "", 5)

	require.Error(t, err)
	require.Contains(t, err.Error(), "query cannot be empty")
}
