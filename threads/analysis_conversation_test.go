// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package threads_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llm/mocks"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/threads"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var testConnStr string

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	cancel()
	if err != nil {
		fmt.Printf("Failed to start postgres container: %v\n", err)
		os.Exit(1)
	}

	testConnStr, err = container.ConnectionString(context.Background(), "sslmode=disable")
	if err != nil {
		fmt.Printf("Failed to get connection string: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := testcontainers.TerminateContainer(container); err != nil {
		fmt.Printf("Failed to terminate container: %v\n", err)
	}

	os.Exit(code)
}

// testBotLookup is a simple test double for BotLookup.
type testBotLookup struct {
	botUserIDs map[string]bool
}

func (t *testBotLookup) IsAnyBot(userID string) bool {
	return t.botUserIDs[userID]
}

func (t *testBotLookup) GetBotConfigByID(string) (bool, int64, bool) {
	return false, 0, false
}

// testSetup holds the objects needed for analysis conversation tests.
type testSetup struct {
	convService *conversation.Service
	store       *store.Store
	prompts     *llm.Prompts
}

// setupTest creates a real conversation.Service backed by a Postgres testcontainer.
// Each call gets an isolated schema.
func setupTest(t *testing.T) testSetup {
	t.Helper()

	db, err := sqlx.Connect("postgres", testConnStr)
	require.NoError(t, err)

	schemaName := fmt.Sprintf("test_%d", time.Now().UnixNano())
	_, err = db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schemaName))
	require.NoError(t, err)

	_, err = db.Exec(fmt.Sprintf("SET search_path TO %s", schemaName))
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schemaName))
		db.Close()
	})

	s := store.New(db)
	err = s.RunMigrations()
	require.NoError(t, err)

	p, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	bots := &testBotLookup{botUserIDs: map[string]bool{}}
	svc := conversation.NewService(s, p, nil, bots)
	return testSetup{convService: svc, store: s, prompts: p}
}

// setupMockThread sets up a mock client that returns thread data for a given post ID.
func setupMockThread(t *testing.T, postID string, threadPosts []*model.Post, users map[string]*model.User) *mmapimocks.MockClient {
	t.Helper()

	mockClient := mmapimocks.NewMockClient(t)

	postList := &model.PostList{
		Order: make([]string, 0, len(threadPosts)),
		Posts: make(map[string]*model.Post, len(threadPosts)),
	}
	for _, p := range threadPosts {
		postList.Order = append(postList.Order, p.Id)
		postList.Posts[p.Id] = p
	}
	mockClient.EXPECT().GetPostThread(postID).Return(postList, nil)

	for userID, user := range users {
		mockClient.EXPECT().GetUser(userID).Return(user, nil)
	}

	return mockClient
}

func TestAnalyzeCreatesConversation(t *testing.T) {
	threadPost := &model.Post{Id: "post123", Message: "Test message", UserId: "user123"}
	threadUsers := map[string]*model.User{
		"user123": {Id: "user123", Username: "testuser123"},
	}
	botID := model.NewId()
	userID := model.NewId()

	tests := []struct {
		name              string
		promptName        string
		expectedOperation string
		expectedSystemSub string // substring expected in the system prompt
		expectedUserSub   string // substring expected in the user turn
	}{
		{
			name:              "summarize creates conversation with thread_analysis operation",
			promptName:        prompts.PromptSummarizeThreadSystem,
			expectedOperation: llm.OperationThreadAnalysis,
			expectedSystemSub: "summary",
			expectedUserSub:   "testuser123",
		},
		{
			name:              "action items creates conversation with thread_analysis operation",
			promptName:        prompts.PromptFindActionItemsSystem,
			expectedOperation: llm.OperationThreadAnalysis,
			expectedSystemSub: "action items",
			expectedUserSub:   "testuser123",
		},
		{
			name:              "open questions creates conversation with thread_analysis operation",
			promptName:        prompts.PromptFindOpenQuestionsSystem,
			expectedOperation: llm.OperationThreadAnalysis,
			expectedSystemSub: "question",
			expectedUserSub:   "testuser123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := setupTest(t)
			mockClient := setupMockThread(t, "post123", []*model.Post{threadPost}, threadUsers)
			mockLLM := mocks.NewMockLanguageModel(t)

			// Capture the completion request to verify tools are disabled
			var capturedConfig llm.LanguageModelConfig
			mockLLM.EXPECT().ChatCompletion(mock.Anything, mock.Anything).
				Run(func(req llm.CompletionRequest, opts ...llm.LanguageModelOption) {
					for _, opt := range opts {
						opt(&capturedConfig)
					}
				}).
				Return(&llm.TextStreamResult{}, nil)

			ctx := llm.NewContext()
			ctx.RequestingUser = &model.User{Id: userID, Username: "requester", Locale: "en"}

			svc := threads.New(mockLLM, ts.prompts, mockClient, ts.convService)
			result, err := svc.Analyze("post123", ctx, tc.promptName, botID, userID)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotEmpty(t, result.ConversationID)

			// Verify tools were disabled
			assert.True(t, capturedConfig.ToolsDisabled, "tools should be disabled for thread analysis")

			// Verify conversation was created with correct operation
			conv, err := ts.store.GetConversation(result.ConversationID)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOperation, conv.Operation)
			assert.Equal(t, botID, conv.BotID)
			assert.Equal(t, userID, conv.UserID)

			// Verify the system prompt contains expected content
			assert.Contains(t, conv.SystemPrompt, tc.expectedSystemSub,
				"system prompt should contain expected substring")

			// Verify user turn was created with formatted thread data
			turns, err := ts.store.GetTurnsForConversation(result.ConversationID)
			require.NoError(t, err)
			require.Len(t, turns, 1)
			assert.Equal(t, "user", turns[0].Role)
			assert.Equal(t, 1, turns[0].Sequence)

			var blocks []conversation.ContentBlock
			err = json.Unmarshal(turns[0].Content, &blocks)
			require.NoError(t, err)
			require.Len(t, blocks, 1)
			assert.Equal(t, "text", blocks[0].Type)
			assert.Contains(t, blocks[0].Text, tc.expectedUserSub,
				"user turn should contain formatted thread data")
		})
	}
}

func TestAnalyzeThreadDataError(t *testing.T) {
	ts := setupTest(t)
	mockClient := mmapimocks.NewMockClient(t)
	mockLLM := mocks.NewMockLanguageModel(t)

	// GetPostThread returns an error
	mockClient.EXPECT().GetPostThread("badpost").Return(nil, errors.New("thread not found"))

	botID := model.NewId()
	userID := model.NewId()
	ctx := llm.NewContext()
	ctx.RequestingUser = &model.User{Id: userID, Username: "requester", Locale: "en"}

	svc := threads.New(mockLLM, ts.prompts, mockClient, ts.convService)
	result, err := svc.Analyze("badpost", ctx, prompts.PromptSummarizeThreadSystem, botID, userID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "thread")
	assert.Nil(t, result)

	// No conversation should have been created
	summaries, err := ts.store.GetConversationSummariesForUser(userID, 10, 0)
	require.NoError(t, err)
	assert.Empty(t, summaries)
}

func TestAnalyzeLLMError(t *testing.T) {
	threadPost := &model.Post{Id: "post456", Message: "some content", UserId: "user456"}
	threadUsers := map[string]*model.User{
		"user456": {Id: "user456", Username: "someuser"},
	}

	ts := setupTest(t)
	mockClient := setupMockThread(t, "post456", []*model.Post{threadPost}, threadUsers)
	mockLLM := mocks.NewMockLanguageModel(t)

	mockLLM.EXPECT().ChatCompletion(mock.Anything, mock.Anything).
		Return(nil, errors.New("llm unavailable"))

	botID := model.NewId()
	userID := model.NewId()
	ctx := llm.NewContext()
	ctx.RequestingUser = &model.User{Id: userID, Username: "requester", Locale: "en"}

	svc := threads.New(mockLLM, ts.prompts, mockClient, ts.convService)
	result, err := svc.Analyze("post456", ctx, prompts.PromptSummarizeThreadSystem, botID, userID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "llm unavailable")
	assert.Nil(t, result)

	// Conversation and user turn should still exist (created before LLM call)
	summaries, err := ts.store.GetConversationSummariesForUser(userID, 10, 0)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, llm.OperationThreadAnalysis, getConvOperation(t, ts.store, summaries[0].ID))

	turns, err := ts.store.GetTurnsForConversation(summaries[0].ID)
	require.NoError(t, err)
	assert.Len(t, turns, 1, "user turn should exist even though LLM call failed")
}

func TestFollowUpContinuesConversation(t *testing.T) {
	threadPost := &model.Post{Id: "post789", Message: "discussion about feature X", UserId: "user789"}
	threadUsers := map[string]*model.User{
		"user789": {Id: "user789", Username: "featureuser"},
	}

	ts := setupTest(t)
	mockClient := setupMockThread(t, "post789", []*model.Post{threadPost}, threadUsers)
	mockLLM := mocks.NewMockLanguageModel(t)

	mockLLM.EXPECT().ChatCompletion(mock.Anything, mock.Anything).
		Return(llm.NewStreamFromString("Here is the summary."), nil).Once()

	botID := model.NewId()
	userID := model.NewId()
	ctx := llm.NewContext()
	ctx.RequestingUser = &model.User{Id: userID, Username: "requester", Locale: "en"}

	svc := threads.New(mockLLM, ts.prompts, mockClient, ts.convService)

	// Step 1: Initial analysis
	result, err := svc.Analyze("post789", ctx, prompts.PromptSummarizeThreadSystem, botID, userID)
	require.NoError(t, err)
	conversationID := result.ConversationID

	// Drain the stream
	_, _ = result.Stream.ReadAll()

	// Simulate post creation by setting RootPostID
	analysisPostID := model.NewId()
	err = ts.store.UpdateConversationRootPostID(conversationID, analysisPostID)
	require.NoError(t, err)

	// Simulate assistant turn being written (normally done by streaming layer)
	assistantContent, _ := json.Marshal([]conversation.ContentBlock{
		{Type: "text", Text: "Here is the summary."},
	})
	maxSeq, _ := ts.store.GetMaxSequenceForConversation(conversationID)
	err = ts.store.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: conversationID,
		Role:           "assistant",
		Content:        assistantContent,
		Sequence:       maxSeq + 1,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	// Step 2: Follow-up -- look up existing conversation, append user turn
	getResult, err := ts.convService.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    "",
		RootPostID:   analysisPostID,
		Operation:    llm.OperationThreadAnalysis,
		SystemPrompt: "",
		UserMessage:  "Can you elaborate on the main points?",
	})
	require.NoError(t, err)
	assert.False(t, getResult.IsNew, "should find existing conversation, not create a new one")
	assert.Equal(t, conversationID, getResult.Conversation.ID)

	// Verify turns: original user, assistant, follow-up user
	turns, err := ts.store.GetTurnsForConversation(conversationID)
	require.NoError(t, err)
	require.Len(t, turns, 3)
	assert.Equal(t, "user", turns[0].Role)
	assert.Equal(t, "assistant", turns[1].Role)
	assert.Equal(t, "user", turns[2].Role)

	// Verify BuildCompletionRequest includes all turns plus system prompt
	conv, err := ts.store.GetConversation(conversationID)
	require.NoError(t, err)
	request, err := ts.convService.BuildCompletionRequest(conv, ctx)
	require.NoError(t, err)

	// System + 3 turns = 4 posts
	require.Len(t, request.Posts, 4)
	assert.Equal(t, llm.PostRoleSystem, request.Posts[0].Role)
	assert.Equal(t, llm.PostRoleUser, request.Posts[1].Role)
	assert.Equal(t, llm.PostRoleBot, request.Posts[2].Role)
	assert.Equal(t, llm.PostRoleUser, request.Posts[3].Role)
	assert.Contains(t, request.Posts[3].Message, "Can you elaborate")
	assert.Equal(t, llm.OperationThreadAnalysis, request.Operation)
}

func TestAnalyzeOperationSubType(t *testing.T) {
	threadPost := &model.Post{Id: "postSub", Message: "subtype test", UserId: "userSub"}
	threadUsers := map[string]*model.User{
		"userSub": {Id: "userSub", Username: "subtypeuser"},
	}

	tests := []struct {
		name                     string
		promptName               string
		expectedOperationSubType string
	}{
		{
			name:                     "summarize sets OperationSubType",
			promptName:               prompts.PromptSummarizeThreadSystem,
			expectedOperationSubType: prompts.PromptSummarizeThreadSystem,
		},
		{
			name:                     "action items sets OperationSubType",
			promptName:               prompts.PromptFindActionItemsSystem,
			expectedOperationSubType: prompts.PromptFindActionItemsSystem,
		},
		{
			name:                     "open questions sets OperationSubType",
			promptName:               prompts.PromptFindOpenQuestionsSystem,
			expectedOperationSubType: prompts.PromptFindOpenQuestionsSystem,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := setupTest(t)
			mockClient := setupMockThread(t, "postSub", []*model.Post{threadPost}, threadUsers)
			mockLLM := mocks.NewMockLanguageModel(t)

			var capturedRequest llm.CompletionRequest
			mockLLM.EXPECT().ChatCompletion(mock.Anything, mock.Anything).
				Run(func(req llm.CompletionRequest, opts ...llm.LanguageModelOption) {
					capturedRequest = req
				}).
				Return(&llm.TextStreamResult{}, nil)

			ctx := llm.NewContext()
			ctx.RequestingUser = &model.User{Id: model.NewId(), Username: "requester", Locale: "en"}

			svc := threads.New(mockLLM, ts.prompts, mockClient, ts.convService)
			_, err := svc.Analyze("postSub", ctx, tc.promptName, model.NewId(), model.NewId())
			require.NoError(t, err)

			assert.Equal(t, tc.expectedOperationSubType, capturedRequest.OperationSubType)
		})
	}
}

// getConvOperation is a test helper that fetches a conversation and returns its Operation field.
func getConvOperation(t *testing.T, s *store.Store, convID string) string {
	t.Helper()
	conv, err := s.GetConversation(convID)
	require.NoError(t, err)
	return conv.Operation
}
