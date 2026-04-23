// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/embeddings"
	embeddingmocks "github.com/mattermost/mattermost-plugin-agents/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	llmmocks "github.com/mattermost/mattermost-plugin-agents/llm/mocks"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeConversationStore is an in-memory implementation of conversation.Store for testing.
type fakeConversationStore struct {
	conversations []*store.Conversation
	turns         []*store.Turn
}

func newFakeConversationStore() *fakeConversationStore {
	return &fakeConversationStore{}
}

func (s *fakeConversationStore) CreateConversation(conv *store.Conversation) error {
	s.conversations = append(s.conversations, conv)
	return nil
}

func (s *fakeConversationStore) GetConversation(id string) (*store.Conversation, error) {
	for _, c := range s.conversations {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, store.ErrConversationNotFound
}

func (s *fakeConversationStore) GetConversationByThreadBotUser(rootPostID, botID, userID string) (*store.Conversation, error) {
	for _, c := range s.conversations {
		if c.RootPostID != nil && *c.RootPostID == rootPostID && c.BotID == botID && c.UserID == userID {
			return c, nil
		}
	}
	return nil, store.ErrConversationNotFound
}

func (s *fakeConversationStore) UpdateConversationTitle(id, title string) error {
	for _, c := range s.conversations {
		if c.ID == id {
			c.Title = title
			return nil
		}
	}
	return store.ErrConversationNotFound
}

func (s *fakeConversationStore) CreateTurn(turn *store.Turn) error {
	s.turns = append(s.turns, turn)
	return nil
}

func (s *fakeConversationStore) CreateTurnAutoSequence(turn *store.Turn) error {
	maxSeq, _ := s.GetMaxSequenceForConversation(turn.ConversationID)
	turn.Sequence = maxSeq + 1
	return s.CreateTurn(turn)
}

func (s *fakeConversationStore) GetTurnsForConversation(conversationID string) ([]store.Turn, error) {
	var result []store.Turn
	for _, t := range s.turns {
		if t.ConversationID == conversationID {
			result = append(result, *t)
		}
	}
	if result == nil {
		result = []store.Turn{}
	}
	return result, nil
}

func (s *fakeConversationStore) UpdateTurnContent(id string, content json.RawMessage) error {
	for _, t := range s.turns {
		if t.ID == id {
			t.Content = content
			return nil
		}
	}
	return errors.New("turn not found")
}

func (s *fakeConversationStore) UpdateTurnTokens(id string, tokensIn, tokensOut int64) error {
	for _, t := range s.turns {
		if t.ID == id {
			t.TokensIn = tokensIn
			t.TokensOut = tokensOut
			return nil
		}
	}
	return errors.New("turn not found")
}

func (s *fakeConversationStore) GetMaxSequenceForConversation(conversationID string) (int, error) {
	maxSeq := 0
	for _, t := range s.turns {
		if t.ConversationID == conversationID && t.Sequence > maxSeq {
			maxSeq = t.Sequence
		}
	}
	return maxSeq, nil
}

func (s *fakeConversationStore) UpdateConversationRootPostID(id string, rootPostID string) error {
	for _, c := range s.conversations {
		if c.ID == id {
			c.RootPostID = &rootPostID
			return nil
		}
	}
	return store.ErrConversationNotFound
}

// fakeBotLookup is a no-op implementation of conversation.BotLookup.
type fakeBotLookup struct{}

func (f *fakeBotLookup) IsAnyBot(string) bool { return false }

// makeSearchResult creates a single embeddings.SearchResult for testing.
func makeSearchResult(postID, channelID, userID, content string, score float32) embeddings.SearchResult {
	return embeddings.SearchResult{
		Document: embeddings.PostDocument{
			PostID:    postID,
			ChannelID: channelID,
			UserID:    userID,
			Content:   content,
		},
		Score: score,
	}
}

// setupMockClientForSearch sets up the standard mock expectations for a search test
// (GetChannel, GetUser, GetConfig) so that buildPrompt can succeed.
func setupMockClientForSearch(mc *mmapimocks.MockClient) {
	mc.On("GetChannel", mock.Anything).Return(&model.Channel{
		Id:          "channel1",
		DisplayName: "General",
		Type:        model.ChannelTypeOpen,
	}, nil).Maybe()
	mc.On("GetUser", mock.Anything).Return(&model.User{
		Id:       "user1",
		Username: "testuser",
	}, nil).Maybe()
	siteURL := "http://localhost:8065"
	mc.On("GetConfig").Return(&model.Config{
		ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
	}).Maybe()
}

func TestSearchQueryCreatesConversation(t *testing.T) {
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	tests := []struct {
		name          string
		query         string
		searchResults []embeddings.SearchResult
		llmAnswer     string
		llmErr        error
		expectError   string
		validate      func(t *testing.T, resp Response, fakeStore *fakeConversationStore)
	}{
		{
			name:  "creates conversation with operation=search and persists assistant turn",
			query: "how do I deploy",
			searchResults: []embeddings.SearchResult{
				makeSearchResult("post1", "channel1", "user1", "deploy instructions", 0.9),
			},
			llmAnswer: "Here is how you deploy.",
			validate: func(t *testing.T, resp Response, fakeStore *fakeConversationStore) {
				// Conversation was created
				require.Len(t, fakeStore.conversations, 1)
				conv := fakeStore.conversations[0]
				require.Equal(t, "search", conv.Operation)
				require.Equal(t, "requester1", conv.UserID)
				require.Equal(t, "bot1", conv.BotID)
				require.Nil(t, conv.ChannelID, "search conversations have no channel")
				require.Nil(t, conv.RootPostID, "search conversations have no thread")

				// System prompt contains RAG results
				require.Contains(t, conv.SystemPrompt, "deploy instructions")

				// Two turns: user + assistant
				require.Len(t, fakeStore.turns, 2)
				require.Equal(t, "user", fakeStore.turns[0].Role)
				require.Equal(t, "assistant", fakeStore.turns[1].Role)

				// User turn content is the query
				var userBlocks []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(fakeStore.turns[0].Content, &userBlocks))
				require.Len(t, userBlocks, 1)
				require.Equal(t, "how do I deploy", userBlocks[0].Text)

				// Assistant turn has the answer
				var assistantBlocks []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(fakeStore.turns[1].Content, &assistantBlocks))
				require.Len(t, assistantBlocks, 1)
				require.Equal(t, "Here is how you deploy.", assistantBlocks[0].Text)

				// API response unchanged
				require.Equal(t, "Here is how you deploy.", resp.Answer)
				require.Len(t, resp.Results, 1)
			},
		},
		{
			name:          "no results skips conversation creation",
			query:         "obscure query nobody wrote about",
			searchResults: []embeddings.SearchResult{},
			validate: func(t *testing.T, resp Response, fakeStore *fakeConversationStore) {
				require.Empty(t, fakeStore.conversations)
				require.Empty(t, fakeStore.turns)
				require.Contains(t, resp.Answer, "couldn't find any relevant messages")
			},
		},
		{
			name:  "LLM error propagates after conversation creation",
			query: "test query",
			searchResults: []embeddings.SearchResult{
				makeSearchResult("post1", "channel1", "user1", "some content", 0.9),
			},
			llmErr:      errors.New("LLM unavailable"),
			expectError: "failed to generate answer",
			validate: func(t *testing.T, _ Response, fakeStore *fakeConversationStore) {
				// Conversation was created (before LLM call)
				require.Len(t, fakeStore.conversations, 1)
				// Only user turn (assistant turn not written on error)
				require.Len(t, fakeStore.turns, 1)
				require.Equal(t, "user", fakeStore.turns[0].Role)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockEmbedding := embeddingmocks.NewMockEmbeddingSearch(t)
			mockClient := mmapimocks.NewMockClient(t)
			mockLLM := llmmocks.NewMockLanguageModel(t)
			fakeStore := newFakeConversationStore()

			setupMockClientForSearch(mockClient)

			mockEmbedding.On("Search", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
				Return(tc.searchResults, nil)

			if len(tc.searchResults) > 0 {
				answer := tc.llmAnswer
				mockLLM.On("ChatCompletionNoStream", mock.Anything, mock.Anything).
					Return(answer, tc.llmErr)
			}

			convService := conversation.NewService(fakeStore, promptsObj, mockClient, &fakeBotLookup{})

			s := New(
				func() embeddings.EmbeddingSearch { return mockEmbedding },
				mockClient,
				promptsObj,
				nil,
				nil,
				convService,
			)

			bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, mockLLM)

			resp, err := s.SearchQuery(context.Background(), "requester1", bot, tc.query, "", "", 5)

			if tc.expectError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectError)
			} else {
				require.NoError(t, err)
			}

			if tc.validate != nil {
				tc.validate(t, resp, fakeStore)
			}
		})
	}
}

func TestSearchQueryToolsAlwaysDisabled(t *testing.T) {
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	mockEmbedding := embeddingmocks.NewMockEmbeddingSearch(t)
	mockClient := mmapimocks.NewMockClient(t)
	mockLLM := llmmocks.NewMockLanguageModel(t)
	fakeStore := newFakeConversationStore()

	setupMockClientForSearch(mockClient)

	mockEmbedding.On("Search", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Return([]embeddings.SearchResult{
			makeSearchResult("post1", "channel1", "user1", "content", 0.9),
		}, nil)

	// Capture the options passed to ChatCompletionNoStream
	var capturedOpts []llm.LanguageModelOption
	mockLLM.On("ChatCompletionNoStream", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			if len(args) > 1 {
				capturedOpts = args[1].([]llm.LanguageModelOption)
			}
		}).
		Return("answer", nil)

	convService := conversation.NewService(fakeStore, promptsObj, mockClient, &fakeBotLookup{})

	s := New(
		func() embeddings.EmbeddingSearch { return mockEmbedding },
		mockClient,
		promptsObj,
		nil,
		nil,
		convService,
	)

	bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, mockLLM)

	_, err = s.SearchQuery(context.Background(), "user1", bot, "test", "", "", 5)
	require.NoError(t, err)

	// Verify WithToolsDisabled was applied by checking the config it produces
	cfg := &llm.LanguageModelConfig{}
	for _, opt := range capturedOpts {
		opt(cfg)
	}
	require.True(t, cfg.ToolsDisabled, "ChatCompletionNoStream must be called with tools disabled")
}

func TestSearchQueryUsesConversationCompletionRequest(t *testing.T) {
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	mockEmbedding := embeddingmocks.NewMockEmbeddingSearch(t)
	mockClient := mmapimocks.NewMockClient(t)
	mockLLM := llmmocks.NewMockLanguageModel(t)
	fakeStore := newFakeConversationStore()

	setupMockClientForSearch(mockClient)

	mockEmbedding.On("Search", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Return([]embeddings.SearchResult{
			makeSearchResult("post1", "channel1", "user1", "content", 0.9),
		}, nil)

	// Capture the CompletionRequest passed to the LLM
	var capturedReq llm.CompletionRequest
	mockLLM.On("ChatCompletionNoStream", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			capturedReq = args[0].(llm.CompletionRequest)
		}).
		Return("answer", nil)

	convService := conversation.NewService(fakeStore, promptsObj, mockClient, &fakeBotLookup{})

	s := New(
		func() embeddings.EmbeddingSearch { return mockEmbedding },
		mockClient,
		promptsObj,
		nil,
		nil,
		convService,
	)

	bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, mockLLM)

	_, err = s.SearchQuery(context.Background(), "user1", bot, "what is X", "", "", 5)
	require.NoError(t, err)

	// The request should be built from conversation turns (system + user)
	require.Equal(t, llm.OperationSearch, capturedReq.Operation)
	require.Equal(t, llm.SubTypeNoStream, capturedReq.OperationSubType)
	require.Len(t, capturedReq.Posts, 2, "system + user turn from conversation store")
	require.Equal(t, llm.PostRoleSystem, capturedReq.Posts[0].Role)
	require.Equal(t, llm.PostRoleUser, capturedReq.Posts[1].Role)
	require.Equal(t, "what is X", capturedReq.Posts[1].Message)
}

func TestSearchQueryNilConversationServiceFallsBack(t *testing.T) {
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	mockEmbedding := embeddingmocks.NewMockEmbeddingSearch(t)
	mockClient := mmapimocks.NewMockClient(t)
	mockLLM := llmmocks.NewMockLanguageModel(t)

	setupMockClientForSearch(mockClient)

	mockEmbedding.On("Search", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Return([]embeddings.SearchResult{
			makeSearchResult("post1", "channel1", "user1", "content", 0.9),
		}, nil)

	mockLLM.On("ChatCompletionNoStream", mock.Anything).
		Return("fallback answer", nil)

	// No conversation service (nil)
	s := New(
		func() embeddings.EmbeddingSearch { return mockEmbedding },
		mockClient,
		promptsObj,
		nil,
		nil,
		nil,
	)

	bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, mockLLM)

	resp, err := s.SearchQuery(context.Background(), "user1", bot, "test", "", "", 5)
	require.NoError(t, err)
	require.Equal(t, "fallback answer", resp.Answer)
}
