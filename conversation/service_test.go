// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const attachmentTestSessionID = "attachment-test-session"

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

	// configByID is consulted by GetBotConfigByID. When nil/missing, the
	// lookup returns ok=false so callers fall back to safe defaults.
	configByID map[string]testBotConfig
}

type testBotConfig struct {
	enableVision bool
	maxFileSize  int64
}

func (t *testBotLookup) IsAnyBot(userID string) bool {
	return t.botUserIDs[userID]
}

func (t *testBotLookup) GetBotConfigByID(botID string) (bool, int64, bool) {
	cfg, ok := t.configByID[botID]
	if !ok {
		return false, 0, false
	}
	return cfg.enableVision, cfg.maxFileSize, true
}

// testLLM is a simple test double for llm.LanguageModel that returns a
// fixed response for ChatCompletionNoStream.
type testLLM struct {
	noStreamResponse string
	noStreamErr      error
}

func (t *testLLM) ChatCompletion(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	return nil, fmt.Errorf("not implemented in test")
}

func (t *testLLM) ChatCompletionNoStream(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (string, error) {
	return t.noStreamResponse, t.noStreamErr
}

func (t *testLLM) CountTokens(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}
func (t *testLLM) InputTokenLimit() int  { return 100000 }
func (t *testLLM) OutputTokenLimit() int { return 8192 }

func setupTestService(t *testing.T) (*Service, *store.Store) {
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

	bots := &testBotLookup{botUserIDs: map[string]bool{}}
	svc := NewService(s, nil, nil, bots)
	return svc, s
}

func TestCreateConversation(t *testing.T) {
	tests := []struct {
		name     string
		params   CreateConversationParams
		validate func(t *testing.T, svc *Service, s *store.Store, result *CreateConversationResult, err error)
	}{
		{
			name: "creates conversation and user turn",
			params: CreateConversationParams{
				UserID:       model.NewId(),
				BotID:        model.NewId(),
				ChannelID:    new("chan1"),
				RootPostID:   new("root1"),
				Operation:    "conversation",
				SystemPrompt: "You are a helpful assistant",
				UserMessage:  "Hello!",
				UserPostID:   new("post1"),
			},
			validate: func(t *testing.T, svc *Service, s *store.Store, result *CreateConversationResult, err error) {
				require.NoError(t, err)
				require.NotEmpty(t, result.ConversationID)
				require.NotEmpty(t, result.UserTurnID)

				conv, err := s.GetConversation(result.ConversationID)
				require.NoError(t, err)
				assert.Equal(t, "You are a helpful assistant", conv.SystemPrompt)
				assert.Equal(t, "conversation", conv.Operation)
				assert.NotNil(t, conv.ChannelID)
				assert.Equal(t, "chan1", *conv.ChannelID)
				assert.NotNil(t, conv.RootPostID)
				assert.Equal(t, "root1", *conv.RootPostID)

				turns, err := s.GetTurnsForConversation(result.ConversationID)
				require.NoError(t, err)
				require.Len(t, turns, 1)
				assert.Equal(t, "user", turns[0].Role)
				assert.Equal(t, 1, turns[0].Sequence)
				assert.NotNil(t, turns[0].PostID)
				assert.Equal(t, "post1", *turns[0].PostID)

				var blocks []ContentBlock
				err = json.Unmarshal(turns[0].Content, &blocks)
				require.NoError(t, err)
				require.Len(t, blocks, 1)
				assert.Equal(t, BlockTypeText, blocks[0].Type)
				assert.Equal(t, "Hello!", blocks[0].Text)
			},
		},
		{
			name: "creates conversation with nil optional fields",
			params: CreateConversationParams{
				UserID:       model.NewId(),
				BotID:        model.NewId(),
				Operation:    "search",
				SystemPrompt: "Search system prompt",
				UserMessage:  "Find something",
			},
			validate: func(t *testing.T, svc *Service, s *store.Store, result *CreateConversationResult, err error) {
				require.NoError(t, err)

				conv, err := s.GetConversation(result.ConversationID)
				require.NoError(t, err)
				assert.Nil(t, conv.ChannelID)
				assert.Nil(t, conv.RootPostID)

				turns, err := s.GetTurnsForConversation(result.ConversationID)
				require.NoError(t, err)
				require.Len(t, turns, 1)
				assert.Nil(t, turns[0].PostID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, s := setupTestService(t)
			result, err := svc.CreateConversation(tt.params)
			tt.validate(t, svc, s, result, err)
		})
	}
}

func TestCreateConversation_DuplicateThreadBotUser(t *testing.T) {
	svc, _ := setupTestService(t)

	botID := model.NewId()
	userID := model.NewId()
	rootPostID := "dup_root"

	_, err := svc.CreateConversation(CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "first",
	})
	require.NoError(t, err)

	_, err = svc.CreateConversation(CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "second",
	})
	assert.ErrorIs(t, err, store.ErrConversationConflict)

	_, err = svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        botID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "third",
	})
	assert.NoError(t, err, "different users in the same thread must get separate conversations")
}

// TestGetOrCreateConversation_MultipleUsersSameThread exercises the common
// channel-mention scenario where two users @mention the bot in the same
// thread. Each user must receive a distinct conversation so approval and
// tool execution stay scoped to the original requester. The previous schema
// had a unique index on (RootPostID, BotID) only, which caused the second
// user to hit "conversation vanished after conflict" — the conflict fired
// but the re-lookup filter (which includes UserID) found nothing.
func TestGetOrCreateConversation_MultipleUsersSameThread(t *testing.T) {
	svc, _ := setupTestService(t)

	botID := model.NewId()
	userA := model.NewId()
	userB := model.NewId()
	rootPostID := "shared_thread_root"

	resultA, err := svc.GetOrCreateConversation(GetOrCreateParams{
		UserID:       userA,
		BotID:        botID,
		ChannelID:    "chan1",
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "hi from A",
		UserPostID:   new("post_A"),
	})
	require.NoError(t, err, "userA should create a conversation without error")
	require.True(t, resultA.IsNew)

	resultB, err := svc.GetOrCreateConversation(GetOrCreateParams{
		UserID:       userB,
		BotID:        botID,
		ChannelID:    "chan1",
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "hi from B",
		UserPostID:   new("post_B"),
	})
	require.NoError(t, err, "userB in the same thread must not hit 'conversation vanished after conflict'")
	require.True(t, resultB.IsNew)

	assert.NotEqual(t, resultA.Conversation.ID, resultB.Conversation.ID,
		"each user must get a distinct conversation so approval scoping remains per-requester")
}

func TestGetOrCreateConversation_New(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.GetOrCreateConversation(GetOrCreateParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		ChannelID:    "chan1",
		RootPostID:   "root1",
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "Hello",
		UserPostID:   new("post1"),
	})

	require.NoError(t, err)
	assert.True(t, result.IsNew)
	assert.NotNil(t, result.Conversation)
	assert.NotEmpty(t, result.UserTurnID)

	turns, err := s.GetTurnsForConversation(result.Conversation.ID)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	assert.Equal(t, 1, turns[0].Sequence)
}

func TestGetOrCreateConversation_Existing(t *testing.T) {
	svc, s := setupTestService(t)

	botID := model.NewId()
	userID := model.NewId()
	rootPostID := "existing_root"

	// Create the initial conversation.
	first, err := svc.CreateConversation(CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "first message",
		UserPostID:   new("post1"),
	})
	require.NoError(t, err)

	// GetOrCreate with the SAME user should find it and add a new user turn.
	result, err := svc.GetOrCreateConversation(GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    "chan1",
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "prompt (ignored for existing)",
		UserMessage:  "second message",
		UserPostID:   new("post2"),
	})

	require.NoError(t, err)
	assert.False(t, result.IsNew)
	assert.Equal(t, first.ConversationID, result.Conversation.ID)

	turns, err := s.GetTurnsForConversation(result.Conversation.ID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	assert.Equal(t, 1, turns[0].Sequence)
	assert.Equal(t, 2, turns[1].Sequence)
}

func TestGetOrCreateConversation_AppendsUserTurn(t *testing.T) {
	svc, s := setupTestService(t)

	botID := model.NewId()
	userID := model.NewId()
	rootPostID := "multi_turn_root"
	convID := ""

	// Create initial conversation with 1 user turn.
	createResult, err := svc.CreateConversation(CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "msg1",
	})
	require.NoError(t, err)
	convID = createResult.ConversationID

	// Add 2 more turns directly (assistant seq=2, user seq=3).
	assistantContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "response"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "assistant",
		Content:        assistantContent,
		Sequence:       2,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	userContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "msg2"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "user",
		Content:        userContent,
		Sequence:       3,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	// GetOrCreate should add a turn at seq=4.
	result, err := svc.GetOrCreateConversation(GetOrCreateParams{
		UserID:      userID,
		BotID:       botID,
		ChannelID:   "chan1",
		RootPostID:  rootPostID,
		Operation:   "conversation",
		UserMessage: "msg3",
	})
	require.NoError(t, err)
	assert.False(t, result.IsNew)

	turns, err := s.GetTurnsForConversation(convID)
	require.NoError(t, err)
	require.Len(t, turns, 4)
	assert.Equal(t, 4, turns[3].Sequence)
}

func TestBuildCompletionRequest_NewConversation(t *testing.T) {
	svc, _ := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "What is 2+2?",
	})
	require.NoError(t, err)

	conv, err := svc.GetConversation(result.ConversationID)
	require.NoError(t, err)

	req, err := svc.BuildCompletionRequest(conv, &llm.Context{})
	require.NoError(t, err)

	require.Len(t, req.Posts, 2)
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	assert.Equal(t, "You are helpful", req.Posts[0].Message)
	assert.Equal(t, llm.PostRoleUser, req.Posts[1].Role)
	assert.Equal(t, "What is 2+2?", req.Posts[1].Message)
}

func TestBuildCompletionRequest_MultiTurn(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "user1",
	})
	require.NoError(t, err)
	convID := result.ConversationID

	// Add assistant turn (seq=2).
	assistantContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "assistant1"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "assistant",
		Content:        assistantContent,
		Sequence:       2,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	// Add second user turn (seq=3).
	userContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "user2"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "user",
		Content:        userContent,
		Sequence:       3,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(convID)
	require.NoError(t, err)

	req, err := svc.BuildCompletionRequest(conv, &llm.Context{})
	require.NoError(t, err)

	// system + 3 turns
	require.Len(t, req.Posts, 4)
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	assert.Equal(t, llm.PostRoleUser, req.Posts[1].Role)
	assert.Equal(t, "user1", req.Posts[1].Message)
	assert.Equal(t, llm.PostRoleBot, req.Posts[2].Role)
	assert.Equal(t, "assistant1", req.Posts[2].Message)
	assert.Equal(t, llm.PostRoleUser, req.Posts[3].Role)
	assert.Equal(t, "user2", req.Posts[3].Message)
}

func TestBuildCompletionRequest_WithToolTurns(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "use tool",
	})
	require.NoError(t, err)
	convID := result.ConversationID

	// Assistant turn with tool_use (seq=2).
	assistantBlocks := []ContentBlock{
		{Type: BlockTypeText, Text: "Let me call a tool"},
		{
			Type:   BlockTypeToolUse,
			ID:     "tc1",
			Name:   "get_weather",
			Input:  json.RawMessage(`{"city":"NYC"}`),
			Status: StatusSuccess,
			Shared: new(true),
		},
	}
	assistantContent, _ := json.Marshal(assistantBlocks)
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "assistant",
		Content:        assistantContent,
		Sequence:       2,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	// Tool result turn (seq=3).
	resultBlocks := []ContentBlock{
		{
			Type:      BlockTypeToolResult,
			ToolUseID: "tc1",
			Content:   "72F, sunny",
			Status:    StatusSuccess,
			Shared:    new(true),
		},
	}
	resultContent, _ := json.Marshal(resultBlocks)
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "tool_result",
		Content:        resultContent,
		Sequence:       3,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	// Final assistant turn (seq=4).
	finalBlocks := []ContentBlock{{Type: BlockTypeText, Text: "The weather is 72F and sunny."}}
	finalContent, _ := json.Marshal(finalBlocks)
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "assistant",
		Content:        finalContent,
		Sequence:       4,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(convID)
	require.NoError(t, err)

	req, err := svc.BuildCompletionRequest(conv, &llm.Context{})
	require.NoError(t, err)

	// tool_result turn is merged into the preceding assistant turn, so:
	// system + user + assistant(tool_use+result) + final_assistant = 4
	require.Len(t, req.Posts, 4)
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	assert.Equal(t, llm.PostRoleUser, req.Posts[1].Role)
	assert.Equal(t, llm.PostRoleBot, req.Posts[2].Role)
	require.Len(t, req.Posts[2].ToolUse, 1)
	assert.Equal(t, "get_weather", req.Posts[2].ToolUse[0].Name)
	assert.Equal(t, "72F, sunny", req.Posts[2].ToolUse[0].Result)
	assert.Equal(t, llm.PostRoleBot, req.Posts[3].Role)
	assert.Equal(t, "The weather is 72F and sunny.", req.Posts[3].Message)
}

func TestBuildCompletionRequest_StripsPersistedAssistantReasoning(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "use tool with thinking",
	})
	require.NoError(t, err)
	convID := result.ConversationID

	assistantBlocks := []ContentBlock{
		{Type: BlockTypeThinking, Text: "I should call the weather tool", Signature: "sig_abc123"},
		{Type: BlockTypeText, Text: "Let me call a tool"},
		{
			Type:   BlockTypeToolUse,
			ID:     "tc1",
			Name:   "get_weather",
			Input:  json.RawMessage(`{"city":"NYC"}`),
			Status: StatusSuccess,
			Shared: new(true),
		},
	}
	assistantContent, err := json.Marshal(assistantBlocks)
	require.NoError(t, err)
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "assistant",
		Content:        assistantContent,
		Sequence:       2,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	resultBlocks := []ContentBlock{
		{
			Type:      BlockTypeToolResult,
			ToolUseID: "tc1",
			Content:   "72F, sunny",
			Status:    StatusSuccess,
			Shared:    new(true),
		},
	}
	resultContent, err := json.Marshal(resultBlocks)
	require.NoError(t, err)
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "tool_result",
		Content:        resultContent,
		Sequence:       3,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(convID)
	require.NoError(t, err)

	req, err := svc.BuildCompletionRequest(conv, &llm.Context{})
	require.NoError(t, err)

	require.Len(t, req.Posts, 3)
	toolRound := req.Posts[2]
	assert.Equal(t, llm.PostRoleBot, toolRound.Role)
	assert.Equal(t, "Let me call a tool", toolRound.Message)
	assert.Empty(t, toolRound.Reasoning)
	assert.Empty(t, toolRound.ReasoningSignature)
	require.Len(t, toolRound.ToolUse, 1)
	assert.Equal(t, "72F, sunny", toolRound.ToolUse[0].Result)
}

// TestBuildCompletionRequest_MultipleToolRoundsMerged verifies that multiple
// tool rounds (each stored as a separate assistant + tool_result turn pair)
// each get merged into a single assistant llm.Post with Result populated on
// every ToolUse entry, which is what bifrost requires.
func TestBuildCompletionRequest_MultipleToolRoundsMerged(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "multi-round",
	})
	require.NoError(t, err)
	convID := result.ConversationID

	addTurn := func(role string, seq int, blocks []ContentBlock) {
		content, _ := json.Marshal(blocks)
		addErr := s.CreateTurn(&store.Turn{
			ID:             model.NewId(),
			ConversationID: convID,
			Role:           role,
			Content:        content,
			Sequence:       seq,
			CreatedAt:      model.GetMillis(),
		})
		require.NoError(t, addErr)
	}

	addTurn("assistant", 2, []ContentBlock{
		{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: new(true)},
	})
	addTurn("tool_result", 3, []ContentBlock{
		{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "first result", Status: StatusSuccess, Shared: new(true)},
	})
	addTurn("assistant", 4, []ContentBlock{
		{Type: BlockTypeToolUse, ID: "tc2", Name: "search", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: new(true)},
	})
	addTurn("tool_result", 5, []ContentBlock{
		{Type: BlockTypeToolResult, ToolUseID: "tc2", Content: "second result", Status: StatusSuccess, Shared: new(true)},
	})

	conv, err := s.GetConversation(convID)
	require.NoError(t, err)

	req, err := svc.BuildCompletionRequest(conv, &llm.Context{})
	require.NoError(t, err)

	// system + user + round-1 assistant + round-2 assistant = 4
	require.Len(t, req.Posts, 4)

	round1 := req.Posts[2]
	assert.Equal(t, llm.PostRoleBot, round1.Role)
	require.Len(t, round1.ToolUse, 1)
	assert.Equal(t, "tc1", round1.ToolUse[0].ID)
	assert.Equal(t, "first result", round1.ToolUse[0].Result)

	round2 := req.Posts[3]
	assert.Equal(t, llm.PostRoleBot, round2.Role)
	require.Len(t, round2.ToolUse, 1)
	assert.Equal(t, "tc2", round2.ToolUse[0].ID)
	assert.Equal(t, "second result", round2.ToolUse[0].Result)
}

// TestBuildCompletionRequest_RedactsUnsharedToolContentByDefault pins the
// fail-safe contract: callers that do not explicitly opt in to full content
// must never see kept-private tool_result bytes in the LLM prompt. The
// redaction path is the only thing stopping kept-private data from being
// paraphrased into channel-visible LLM replies. Inverting this default would
// re-open a Medium-severity channel data leak, so this test double-covers:
// (a) no options → redacted, (b) AllowUnsharedToolContent=true → full content.
func TestBuildCompletionRequest_RedactsUnsharedToolContentByDefault(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "question",
	})
	require.NoError(t, err)
	convID := result.ConversationID

	// One shared call, one the user kept private, and one awaiting the
	// share decision.
	assistantBlocks := []ContentBlock{
		{Type: BlockTypeToolUse, ID: "tc-shared", Name: "search", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: new(true)},
		{Type: BlockTypeToolUse, ID: "tc-private", Name: "read_dm", Input: json.RawMessage(`{"channel":"secret-dm"}`), Status: StatusSuccess, Shared: new(false)},
		{Type: BlockTypeToolUse, ID: "tc-pending", Name: "read_dm", Input: json.RawMessage(`{"channel":"other-dm"}`), Status: StatusSuccess, Shared: new(false)},
	}
	assistantContent, err := json.Marshal(assistantBlocks)
	require.NoError(t, err)
	err = s.CreateTurn(&store.Turn{
		ID: model.NewId(), ConversationID: convID, Role: "assistant",
		Content: assistantContent, Sequence: 2, CreatedAt: model.GetMillis(),
	})
	require.NoError(t, err)

	resultBlocks := []ContentBlock{
		{Type: BlockTypeToolResult, ToolUseID: "tc-shared", Content: "PUBLIC DATA", Status: StatusSuccess, Shared: new(true)},
		{Type: BlockTypeToolResult, ToolUseID: "tc-private", Content: "PRIVATE SECRET", Status: StatusSuccess, Shared: new(false), DecidedAt: new(model.GetMillis())},
		{Type: BlockTypeToolResult, ToolUseID: "tc-pending", Content: "PENDING SECRET", Status: StatusSuccess, Shared: new(false)},
	}
	resultContent, err := json.Marshal(resultBlocks)
	require.NoError(t, err)
	err = s.CreateTurn(&store.Turn{
		ID: model.NewId(), ConversationID: convID, Role: "tool_result",
		Content: resultContent, Sequence: 3, CreatedAt: model.GetMillis(),
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(convID)
	require.NoError(t, err)

	resultsByID := func(req *llm.CompletionRequest) map[string]string {
		out := map[string]string{}
		for _, p := range req.Posts {
			for _, tc := range p.ToolUse {
				if tc.Result != "" {
					out[tc.ID] = tc.Result
				}
			}
		}
		return out
	}

	t.Run("default redacts unshared content (fail-safe)", func(t *testing.T) {
		req, err := svc.BuildCompletionRequest(conv, &llm.Context{})
		require.NoError(t, err)
		results := resultsByID(req)
		assert.Equal(t, "PUBLIC DATA", results["tc-shared"])
		assert.Equal(t, "[arguments and result not shared by user]", results["tc-private"],
			"kept-private tool_result content must never leak into the default LLM prompt; "+
				"a channel-visible response could paraphrase it")
		assert.Equal(t, "[result pending the user's decision to share it]", results["tc-pending"])
		for _, p := range req.Posts {
			for _, tc := range p.ToolUse {
				if tc.ID != "tc-shared" {
					assert.JSONEq(t, `{}`, string(tc.Arguments))
				}
			}
		}
	})

	t.Run("empty BuildOptions still redacts", func(t *testing.T) {
		req, err := svc.BuildCompletionRequest(conv, &llm.Context{}, BuildOptions{})
		require.NoError(t, err)
		results := resultsByID(req)
		assert.Equal(t, UnsharedToolResultRedaction, results["tc-private"],
			"a zero-value BuildOptions must behave like no options at all")
	})

	t.Run("AllowUnsharedToolContent=true sends full content (DM-only opt-in)", func(t *testing.T) {
		req, err := svc.BuildCompletionRequest(conv, &llm.Context{}, BuildOptions{AllowUnsharedToolContent: true})
		require.NoError(t, err)
		results := resultsByID(req)
		assert.Equal(t, "PUBLIC DATA", results["tc-shared"])
		assert.Equal(t, "PRIVATE SECRET", results["tc-private"])
		assert.Equal(t, "PENDING SECRET", results["tc-pending"])
	})
}

// TestBuildChannelMentionRequest_RedactsUnsharedToolContentByDefault mirrors
// the BuildCompletionRequest guard for the channel-mention path. A subsequent
// @mention in the same thread must never see kept-private tool output from
// an earlier mention.
func TestBuildChannelMentionRequest_RedactsUnsharedToolContentByDefault(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "first mention",
	})
	require.NoError(t, err)
	convID := result.ConversationID

	// Prior mention executed a tool; user kept the result private.
	assistantBlocks := []ContentBlock{
		{Type: BlockTypeToolUse, ID: "tc-private", Name: "read_dm", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: new(false)},
	}
	assistantContent, err := json.Marshal(assistantBlocks)
	require.NoError(t, err)
	err = s.CreateTurn(&store.Turn{
		ID: model.NewId(), ConversationID: convID, Role: "assistant",
		Content: assistantContent, Sequence: 2, CreatedAt: model.GetMillis(),
	})
	require.NoError(t, err)

	resultBlocks := []ContentBlock{
		{Type: BlockTypeToolResult, ToolUseID: "tc-private", Content: "PRIVATE SECRET", Status: StatusSuccess, Shared: new(false)},
	}
	resultContent, err := json.Marshal(resultBlocks)
	require.NoError(t, err)
	err = s.CreateTurn(&store.Turn{
		ID: model.NewId(), ConversationID: convID, Role: "tool_result",
		Content: resultContent, Sequence: 3, CreatedAt: model.GetMillis(),
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(convID)
	require.NoError(t, err)

	// threadData == nil is fine — it falls back to BuildCompletionRequest,
	// which is the path the regression shows up on in practice (the
	// webapp doesn't always have thread data on a fresh mention).
	req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, nil)
	require.NoError(t, err)

	for _, p := range req.Posts {
		for _, tc := range p.ToolUse {
			assert.NotContains(t, tc.Result, "PRIVATE SECRET",
				"channel-mention prompts must redact by default — "+
					"a later @mention in the thread would otherwise leak kept-private tool output")
		}
	}
}

func TestBuildCompletionRequest_SystemPromptIsFirst(t *testing.T) {
	svc, _ := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "I am system",
		UserMessage:  "Hello",
	})
	require.NoError(t, err)

	conv, err := svc.GetConversation(result.ConversationID)
	require.NoError(t, err)

	req, err := svc.BuildCompletionRequest(conv, &llm.Context{})
	require.NoError(t, err)

	require.True(t, len(req.Posts) >= 2)
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	assert.Equal(t, "I am system", req.Posts[0].Message)
}

func TestBuildCompletionRequest_ExcludeAfterPostID(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "user1",
		UserPostID:   new("user_post1"),
	})
	require.NoError(t, err)
	convID := result.ConversationID

	// Assistant turn (seq=2) with postID "resp1".
	assistantContent1, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "assistant1"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		PostID:         new("resp1"),
		Role:           "assistant",
		Content:        assistantContent1,
		Sequence:       2,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	// Second user turn (seq=3).
	userContent2, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "user2"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		PostID:         new("user_post2"),
		Role:           "user",
		Content:        userContent2,
		Sequence:       3,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	// Second assistant turn (seq=4) with postID "resp2".
	assistantContent2, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "assistant2"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		PostID:         new("resp2"),
		Role:           "assistant",
		Content:        assistantContent2,
		Sequence:       4,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(convID)
	require.NoError(t, err)

	// Exclude from "resp2" onward: should return system + user1 + assistant1 + user2.
	req, err := svc.BuildCompletionRequest(conv, &llm.Context{}, BuildOptions{ExcludeAfterPostID: "resp2"})
	require.NoError(t, err)

	require.Len(t, req.Posts, 4)
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	assert.Equal(t, "system", req.Posts[0].Message)
	assert.Equal(t, llm.PostRoleUser, req.Posts[1].Role)
	assert.Equal(t, "user1", req.Posts[1].Message)
	assert.Equal(t, llm.PostRoleBot, req.Posts[2].Role)
	assert.Equal(t, "assistant1", req.Posts[2].Message)
	assert.Equal(t, llm.PostRoleUser, req.Posts[3].Role)
	assert.Equal(t, "user2", req.Posts[3].Message)
}

// Regen of a post that went through a tool-approval continuation must drop
// the demoted prior anchor and its tool_result, so the request ends on the
// user turn (bifrost rejects an assistant-ended prefill).
func TestBuildCompletionRequest_ExcludeAfterPostID_ToolApprovalContinuationLeavesUserTail(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "user1",
		UserPostID:   new("user_post1"),
	})
	require.NoError(t, err)
	convID := result.ConversationID

	// Demoted prior anchor (left behind by a continuation finalize).
	demotedContent, _ := json.Marshal([]ContentBlock{
		{Type: BlockTypeText, Text: "Let me search."},
		{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Status: StatusSuccess, Shared: new(true)},
	})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		PostID:         nil,
		Role:           "assistant",
		Content:        demotedContent,
		Sequence:       2,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	resultContent, _ := json.Marshal([]ContentBlock{
		{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "5 channels found", Shared: new(true)},
	})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		PostID:         nil,
		Role:           "tool_result",
		Content:        resultContent,
		Sequence:       3,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	anchorContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "Found 5 channels."}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		PostID:         new("resp_post"),
		Role:           "assistant",
		Content:        anchorContent,
		Sequence:       4,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(convID)
	require.NoError(t, err)

	req, err := svc.BuildCompletionRequest(conv, &llm.Context{}, BuildOptions{ExcludeAfterPostID: "resp_post"})
	require.NoError(t, err)

	require.NotEmpty(t, req.Posts)
	last := req.Posts[len(req.Posts)-1]
	require.Equal(t, llm.PostRoleUser, last.Role)
	require.Equal(t, "user1", last.Message)
	require.Len(t, req.Posts, 2)
	require.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
}

func TestCreatePlaceholderAssistantTurn(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "msg",
	})
	require.NoError(t, err)

	turnID, err := svc.CreatePlaceholderAssistantTurn(result.ConversationID, new("response_post"))
	require.NoError(t, err)
	require.NotEmpty(t, turnID)

	turns, err := s.GetTurnsForConversation(result.ConversationID)
	require.NoError(t, err)
	require.Len(t, turns, 2) // user + placeholder

	placeholder := turns[1]
	assert.Equal(t, "assistant", placeholder.Role)
	assert.Equal(t, 2, placeholder.Sequence)
	assert.JSONEq(t, "[]", string(placeholder.Content))
	require.NotNil(t, placeholder.PostID)
	assert.Equal(t, "response_post", *placeholder.PostID)
}

func TestFinalizeAssistantTurn(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "msg",
	})
	require.NoError(t, err)

	turnID, err := svc.CreatePlaceholderAssistantTurn(result.ConversationID, nil)
	require.NoError(t, err)

	content := []ContentBlock{
		{Type: BlockTypeThinking, Text: "thinking about it", Signature: "sig123"},
		{Type: BlockTypeText, Text: "Here is my answer"},
	}
	err = svc.FinalizeAssistantTurn(turnID, content, 1500, 200)
	require.NoError(t, err)

	turns, err := s.GetTurnsForConversation(result.ConversationID)
	require.NoError(t, err)
	require.Len(t, turns, 2) // user + finalized assistant

	finalized := turns[1]
	assert.Equal(t, int64(1500), finalized.TokensIn)
	assert.Equal(t, int64(200), finalized.TokensOut)

	var blocks []ContentBlock
	err = json.Unmarshal(finalized.Content, &blocks)
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	assert.Equal(t, BlockTypeThinking, blocks[0].Type)
	assert.Equal(t, "thinking about it", blocks[0].Text)
	assert.Equal(t, "sig123", blocks[0].Signature)
	assert.Equal(t, BlockTypeText, blocks[1].Type)
	assert.Equal(t, "Here is my answer", blocks[1].Text)
}

func TestWriteToolTurns_SingleRound(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "use tools",
	})
	require.NoError(t, err)

	toolTurns := []toolrunner.ToolTurn{
		{
			AssistantMessage: "I'll call two tools",
			AssistantToolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "tool_a", Arguments: json.RawMessage(`{"x":1}`), Status: llm.ToolCallStatusAutoApproved},
				{ID: "tc2", Name: "tool_b", Arguments: json.RawMessage(`{"y":2}`), Status: llm.ToolCallStatusAutoApproved},
			},
			ToolResults: []toolrunner.ToolResult{
				{ToolCallID: "tc1", Name: "tool_a", Result: "result_a", IsError: false},
				{ToolCallID: "tc2", Name: "tool_b", Result: "result_b", IsError: false},
			},
			TokensIn:  100,
			TokensOut: 50,
		},
	}

	err = svc.WriteToolTurns(result.ConversationID, toolTurns, true)
	require.NoError(t, err)

	turns, err := s.GetTurnsForConversation(result.ConversationID)
	require.NoError(t, err)
	// user(1) + assistant(2) + tool_result(3) = 3 total
	require.Len(t, turns, 3)

	// Assistant turn
	assert.Equal(t, "assistant", turns[1].Role)
	assert.Equal(t, 2, turns[1].Sequence)
	assert.Equal(t, int64(100), turns[1].TokensIn)
	assert.Equal(t, int64(50), turns[1].TokensOut)

	var assistantBlocks []ContentBlock
	err = json.Unmarshal(turns[1].Content, &assistantBlocks)
	require.NoError(t, err)
	// text + 2 tool_use
	require.Len(t, assistantBlocks, 3)
	assert.Equal(t, BlockTypeText, assistantBlocks[0].Type)
	assert.Equal(t, BlockTypeToolUse, assistantBlocks[1].Type)
	assert.Equal(t, "tc1", assistantBlocks[1].ID)
	// WriteToolTurns is invoked only after the toolrunner auto-executes a
	// round, so successful tool_use blocks are tagged auto_approved.
	assert.Equal(t, StatusAutoApproved, assistantBlocks[1].Status)
	require.NotNil(t, assistantBlocks[1].Shared)
	assert.True(t, *assistantBlocks[1].Shared)
	assert.Equal(t, BlockTypeToolUse, assistantBlocks[2].Type)
	assert.Equal(t, "tc2", assistantBlocks[2].ID)

	// Tool result turn
	assert.Equal(t, "tool_result", turns[2].Role)
	assert.Equal(t, 3, turns[2].Sequence)

	var resultBlocks []ContentBlock
	err = json.Unmarshal(turns[2].Content, &resultBlocks)
	require.NoError(t, err)
	require.Len(t, resultBlocks, 2)
	assert.Equal(t, BlockTypeToolResult, resultBlocks[0].Type)
	assert.Equal(t, "tc1", resultBlocks[0].ToolUseID)
	assert.Equal(t, "result_a", resultBlocks[0].Content)
	assert.Equal(t, StatusSuccess, resultBlocks[0].Status)
	assert.Equal(t, BlockTypeToolResult, resultBlocks[1].Type)
	assert.Equal(t, "tc2", resultBlocks[1].ToolUseID)
	assert.Equal(t, "result_b", resultBlocks[1].Content)
}

func TestWriteToolTurns_MultipleRounds(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "multi round",
	})
	require.NoError(t, err)

	toolTurns := []toolrunner.ToolTurn{
		{
			AssistantMessage:   "round 1",
			AssistantToolCalls: []llm.ToolCall{{ID: "tc1", Name: "tool_a", Arguments: json.RawMessage(`{}`)}},
			ToolResults:        []toolrunner.ToolResult{{ToolCallID: "tc1", Name: "tool_a", Result: "r1"}},
			TokensIn:           10,
			TokensOut:          5,
		},
		{
			AssistantMessage:   "round 2",
			AssistantToolCalls: []llm.ToolCall{{ID: "tc2", Name: "tool_b", Arguments: json.RawMessage(`{}`)}},
			ToolResults:        []toolrunner.ToolResult{{ToolCallID: "tc2", Name: "tool_b", Result: "r2"}},
			TokensIn:           20,
			TokensOut:          10,
		},
	}

	err = svc.WriteToolTurns(result.ConversationID, toolTurns, true)
	require.NoError(t, err)

	turns, err := s.GetTurnsForConversation(result.ConversationID)
	require.NoError(t, err)
	// user(1) + assistant(2) + result(3) + assistant(4) + result(5) = 5
	require.Len(t, turns, 5)
	assert.Equal(t, 1, turns[0].Sequence)
	assert.Equal(t, 2, turns[1].Sequence)
	assert.Equal(t, 3, turns[2].Sequence)
	assert.Equal(t, 4, turns[3].Sequence)
	assert.Equal(t, 5, turns[4].Sequence)
}

func TestWriteToolTurns_SharedFlag(t *testing.T) {
	tests := []struct {
		name     string
		shared   bool
		expected bool
	}{
		{name: "shared true", shared: true, expected: true},
		{name: "shared false", shared: false, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, s := setupTestService(t)

			result, err := svc.CreateConversation(CreateConversationParams{
				UserID:       model.NewId(),
				BotID:        model.NewId(),
				Operation:    "conversation",
				SystemPrompt: "prompt",
				UserMessage:  "msg",
			})
			require.NoError(t, err)

			toolTurns := []toolrunner.ToolTurn{
				{
					AssistantToolCalls: []llm.ToolCall{{ID: "tc1", Name: "tool", Arguments: json.RawMessage(`{}`)}},
					ToolResults:        []toolrunner.ToolResult{{ToolCallID: "tc1", Name: "tool", Result: "r"}},
				},
			}

			err = svc.WriteToolTurns(result.ConversationID, toolTurns, tt.shared)
			require.NoError(t, err)

			turns, err := s.GetTurnsForConversation(result.ConversationID)
			require.NoError(t, err)
			require.Len(t, turns, 3)

			// Check assistant turn's tool_use block
			var assistantBlocks []ContentBlock
			err = json.Unmarshal(turns[1].Content, &assistantBlocks)
			require.NoError(t, err)
			for _, b := range assistantBlocks {
				if b.Type == BlockTypeToolUse {
					require.NotNil(t, b.Shared)
					assert.Equal(t, tt.expected, *b.Shared)
				}
			}

			// Check tool_result turn's blocks
			var resultBlocks []ContentBlock
			err = json.Unmarshal(turns[2].Content, &resultBlocks)
			require.NoError(t, err)
			for _, b := range resultBlocks {
				if b.Type == BlockTypeToolResult {
					require.NotNil(t, b.Shared)
					assert.Equal(t, tt.expected, *b.Shared)
				}
			}
		})
	}
}

func TestWriteToolTurns_ErroredTool(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "msg",
	})
	require.NoError(t, err)

	toolTurns := []toolrunner.ToolTurn{
		{
			AssistantToolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "failing_tool", Arguments: json.RawMessage(`{}`), Status: llm.ToolCallStatusError},
			},
			ToolResults: []toolrunner.ToolResult{
				{ToolCallID: "tc1", Name: "failing_tool", Result: "something went wrong", IsError: true},
			},
		},
	}

	err = svc.WriteToolTurns(result.ConversationID, toolTurns, true)
	require.NoError(t, err)

	turns, err := s.GetTurnsForConversation(result.ConversationID)
	require.NoError(t, err)
	require.Len(t, turns, 3)

	// Check tool_use block has error status.
	var assistantBlocks []ContentBlock
	err = json.Unmarshal(turns[1].Content, &assistantBlocks)
	require.NoError(t, err)
	for _, b := range assistantBlocks {
		if b.Type == BlockTypeToolUse {
			assert.Equal(t, StatusError, b.Status)
		}
	}

	// Check tool_result block has error status and error message content.
	var resultBlocks []ContentBlock
	err = json.Unmarshal(turns[2].Content, &resultBlocks)
	require.NoError(t, err)
	require.Len(t, resultBlocks, 1)
	assert.Equal(t, BlockTypeToolResult, resultBlocks[0].Type)
	assert.Equal(t, StatusError, resultBlocks[0].Status)
	assert.Equal(t, "something went wrong", resultBlocks[0].Content)
}

// renamingLLM renames the conversation while the title is being generated,
// as a user can from the product view right after sending a message.
type renamingLLM struct {
	testLLM
	rename func()
	calls  int
}

func (r *renamingLLM) ChatCompletionNoStream(ctx context.Context, req llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	r.calls++
	if r.rename != nil {
		r.rename()
	}
	return r.testLLM.ChatCompletionNoStream(ctx, req, opts...)
}

func TestGenerateTitle(t *testing.T) {
	type wsEvent struct {
		name      string
		payload   map[string]any
		broadcast *model.WebsocketBroadcast
	}

	tests := []struct {
		name string
		// renameBefore and renameDuring rename the conversation through the
		// store, before generation starts or while the LLM call runs.
		renameBefore  bool
		renameDuring  bool
		wantTitle     string
		wantLLMCalled bool
		wantEvent     bool
	}{
		{
			name:          "an untitled conversation gets the generated title and the owner is told",
			wantTitle:     "Go Programming Language",
			wantLLMCalled: true,
			wantEvent:     true,
		},
		{
			name:         "a rename before generation is kept and no title is generated",
			renameBefore: true,
			wantTitle:    "Named by me",
		},
		{
			name:          "a rename while the title is generated is kept",
			renameDuring:  true,
			wantTitle:     "Named by me",
			wantLLMCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var events []wsEvent
			mmClient := mmapimocks.NewMockClient(t)
			mmClient.On("PublishWebSocketEvent", mock.AnythingOfType("string"), mock.AnythingOfType("map[string]interface {}"), mock.AnythingOfType("*model.WebsocketBroadcast")).
				Run(func(args mock.Arguments) {
					payload, _ := args.Get(1).(map[string]any)
					broadcast, _ := args.Get(2).(*model.WebsocketBroadcast)
					events = append(events, wsEvent{name: args.String(0), payload: payload, broadcast: broadcast})
				}).Return().Maybe()
			svc, s := setupTestServiceWithClient(t, mmClient, nil)

			userID := model.NewId()
			result, err := svc.CreateConversation(CreateConversationParams{
				UserID:       userID,
				BotID:        model.NewId(),
				Operation:    "conversation",
				SystemPrompt: "prompt",
				UserMessage:  "Tell me about Go",
			})
			require.NoError(t, err)

			rename := func() {
				renamed, renameErr := s.RenameConversation(result.ConversationID, "Named by me")
				assert.NoError(t, renameErr)
				assert.True(t, renamed)
			}
			mockLLM := &renamingLLM{noStreamResponse: `"Go Programming Language"`}
			if tt.renameBefore {
				rename()
			}
			if tt.renameDuring {
				mockLLM.rename = rename
			}

			err = svc.GenerateTitle(context.Background(), result.ConversationID, mockLLM, "Tell me about Go", &llm.Context{})
			require.NoError(t, err)

			conv, err := s.GetConversation(result.ConversationID)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTitle, conv.Title)
			assert.Equal(t, tt.wantLLMCalled, mockLLM.calls > 0)

			if !tt.wantEvent {
				assert.Empty(t, events, "no event when the generated title was not saved")
				return
			}
			require.Len(t, events, 1)
			assert.Equal(t, WebsocketEventConversationUpdated, events[0].name)
			assert.Equal(t, map[string]any{"conversation_id": result.ConversationID, "title": tt.wantTitle}, events[0].payload)
			assert.Equal(t, &model.WebsocketBroadcast{UserId: userID}, events[0].broadcast)
		})
	}
}

func TestBuildChannelMentionRequest_BotTurnsOnly(t *testing.T) {
	svc, s := setupTestService(t)

	botID := model.NewId()
	rootPostID := "chan_root"

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        botID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "system prompt",
		UserMessage:  "hello bot",
		UserPostID:   new("post1"),
	})
	require.NoError(t, err)

	// Add assistant turn linked to a post.
	assistantContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "hello user"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: result.ConversationID,
		PostID:         new("post2"),
		Role:           "assistant",
		Content:        assistantContent,
		Sequence:       2,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(result.ConversationID)
	require.NoError(t, err)

	// ThreadData contains only the bot's posts.
	threadData := &mmapi.ThreadData{
		Posts: []*model.Post{
			{Id: "post1", UserId: "someuser", CreateAt: 1000, Message: "hello bot"},
			{Id: "post2", UserId: botID, CreateAt: 2000, Message: "hello user"},
		},
		UsersByID: map[string]*model.User{
			"someuser": {Id: "someuser", Username: "alice"},
			botID:      {Id: botID, Username: "aibot"},
		},
	}

	svc.bots = &testBotLookup{botUserIDs: map[string]bool{botID: true}}

	req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData)
	require.NoError(t, err)

	// system + user turn + assistant turn = 3 (both posts are from the bot's turns)
	require.Len(t, req.Posts, 3)
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	assert.Equal(t, "conversation", req.Operation)
}

func TestBuildChannelMentionRequest_MixedThread(t *testing.T) {
	svc, s := setupTestService(t)

	botID := model.NewId()
	userA := model.NewId()
	userB := model.NewId()
	rootPostID := "mixed_root"

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       userA,
		BotID:        botID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "question from A",
		UserPostID:   new("postA1"),
	})
	require.NoError(t, err)

	// Bot's response.
	assistantContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "answer to A"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: result.ConversationID,
		PostID:         new("postBot1"),
		Role:           "assistant",
		Content:        assistantContent,
		Sequence:       2,
		CreatedAt:      2000,
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(result.ConversationID)
	require.NoError(t, err)

	// Thread has: userA post, bot response, userB post, bot's second response.
	// userB's post is NOT in the conversation (different user, not a turn).
	threadData := &mmapi.ThreadData{
		Posts: []*model.Post{
			{Id: "postA1", UserId: userA, CreateAt: 1000, Message: "question from A"},
			{Id: "postBot1", UserId: botID, CreateAt: 2000, Message: "answer to A"},
			{Id: "postB1", UserId: userB, CreateAt: 3000, Message: "comment from B"},
		},
		UsersByID: map[string]*model.User{
			userA: {Id: userA, Username: "alice"},
			userB: {Id: userB, Username: "bob"},
			botID: {Id: botID, Username: "aibot"},
		},
	}

	svc.bots = &testBotLookup{botUserIDs: map[string]bool{botID: true}}

	req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData)
	require.NoError(t, err)

	// system + user_turn(postA1) + assistant_turn(postBot1) + plain_text(postB1) = 4
	require.Len(t, req.Posts, 4)
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	// The order should be chronological.
	assert.Equal(t, llm.PostRoleUser, req.Posts[1].Role)
	assert.Equal(t, llm.PostRoleBot, req.Posts[2].Role)
	assert.Equal(t, llm.PostRoleUser, req.Posts[3].Role)
	assert.Contains(t, req.Posts[3].Message, "@bob")
	assert.Contains(t, req.Posts[3].Message, "comment from B")
}

func TestBuildChannelMentionRequest_StopsAtCurrentUserTurn(t *testing.T) {
	svc, s := setupTestService(t)

	botID := model.NewId()
	userID := model.NewId()
	rootPostID := "trim_root"
	currentPostID := "current_mention"

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "@aibot look at the earlier post",
		UserPostID:   new(currentPostID),
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(result.ConversationID)
	require.NoError(t, err)

	threadData := &mmapi.ThreadData{
		Posts: []*model.Post{
			{Id: rootPostID, UserId: userID, CreateAt: 1000, Message: "earlier context"},
			{Id: currentPostID, UserId: userID, CreateAt: 2000, Message: "@aibot look at the earlier post"},
			{Id: "placeholder", UserId: botID, CreateAt: 3000, Message: "Thinking..."},
			{Id: "later_reply", UserId: userID, CreateAt: 4000, Message: "this happened after the mention"},
		},
		UsersByID: map[string]*model.User{
			userID: {Id: userID, Username: "alice"},
			botID:  {Id: botID, Username: "aibot"},
		},
	}

	svc.bots = &testBotLookup{botUserIDs: map[string]bool{botID: true}}

	req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData)
	require.NoError(t, err)

	require.Len(t, req.Posts, 3, "system + earlier context + current mention")
	assert.Contains(t, req.Posts[1].Message, "earlier context")
	assert.Contains(t, req.Posts[2].Message, "@aibot look at the earlier post")
	for _, post := range req.Posts {
		assert.NotContains(t, post.Message, "Thinking...")
		assert.NotContains(t, post.Message, "this happened after the mention")
	}
}

func TestBuildChannelMentionRequest_PostLessUserTurnsFollowThreadChronology(t *testing.T) {
	type turnSpec struct {
		role      string
		text      string
		postID    string
		createdAt int64
	}
	type postSpec struct {
		id       string
		bot      bool
		createAt int64
		message  string
	}

	// Every case shares a first exchange: the requester's mention and the
	// agent's reply.
	baseTurns := []turnSpec{
		{role: "user", text: "MENTION", postID: "mention", createdAt: 1000},
		{role: "assistant", text: "REPLY", postID: "reply", createdAt: 1100},
	}
	basePosts := []postSpec{
		{id: "mention", createAt: 1000, message: "MENTION"},
		{id: "reply", bot: true, createAt: 1050, message: "REPLY"},
	}

	tests := []struct {
		name        string
		turns       []turnSpec
		posts       []postSpec
		responseID  string
		wantOrder   []string
		wantMissing []string
	}{
		{
			name:  "wake turn follows posts made while the agent slept and skips the wake placeholder",
			turns: []turnSpec{{role: "user", text: "WAKE", createdAt: 5000}},
			posts: []postSpec{
				{id: "peer1", createAt: 2000, message: "PEER_ONE"},
				{id: "peer2", createAt: 3000, message: "PEER_TWO"},
				{id: "wake_placeholder", bot: true, createAt: 4999, message: "PLACEHOLDER_TEXT"},
			},
			responseID:  "wake_placeholder",
			wantOrder:   []string{"REPLY", "PEER_ONE", "PEER_TWO", "WAKE"},
			wantMissing: []string{"PLACEHOLDER_TEXT"},
		},
		{
			name: "earlier wake stays before the post that answered it",
			turns: []turnSpec{
				{role: "user", text: "WAKE_ONE", createdAt: 5000},
				{role: "assistant", text: "WAKE_ONE_TOOL_ROUND", createdAt: 5010},
				{role: "assistant", text: "WAKE_ONE_REPLY", postID: "wake_reply", createdAt: 5020},
				{role: "user", text: "WAKE_TWO", createdAt: 9000},
			},
			posts: []postSpec{
				{id: "peer_a", createAt: 2000, message: "PEER_A"},
				// The answer's post predates its wake turn, as when the
				// placeholder is created first.
				{id: "wake_reply", bot: true, createAt: 4999, message: "WAKE_ONE_REPLY"},
				{id: "peer_b", createAt: 6000, message: "PEER_B"},
				{id: "wake2_placeholder", bot: true, createAt: 8999},
			},
			responseID: "wake2_placeholder",
			wantOrder:  []string{"REPLY", "PEER_A", "WAKE_ONE", "WAKE_ONE_TOOL_ROUND", "WAKE_ONE_REPLY", "PEER_B", "WAKE_TWO"},
		},
		{
			name: "steering note follows the new message in a live turn",
			turns: []turnSpec{
				{role: "user", text: "SECOND_MESSAGE", postID: "second", createdAt: 3000},
				{role: "user", text: "STEERED_NOTE", createdAt: 3001},
			},
			posts: []postSpec{
				{id: "peer1", createAt: 2000, message: "PEER_ONE"},
				{id: "second", createAt: 2999, message: "SECOND_MESSAGE"},
				{id: "live_placeholder", bot: true, createAt: 3000, message: "PLACEHOLDER_TEXT"},
			},
			wantOrder:   []string{"REPLY", "PEER_ONE", "SECOND_MESSAGE", "STEERED_NOTE"},
			wantMissing: []string{"PLACEHOLDER_TEXT"},
		},
		{
			name: "steering note stays before the reply it preceded on later turns",
			turns: []turnSpec{
				{role: "user", text: "SECOND_MESSAGE", postID: "second", createdAt: 3000},
				{role: "user", text: "STEERED_NOTE", createdAt: 3001},
				{role: "assistant", text: "SECOND_REPLY", postID: "second_reply", createdAt: 3100},
			},
			posts: []postSpec{
				{id: "second", createAt: 2999, message: "SECOND_MESSAGE"},
				{id: "second_reply", bot: true, createAt: 3000, message: "SECOND_REPLY"},
			},
			wantOrder: []string{"REPLY", "SECOND_MESSAGE", "STEERED_NOTE", "SECOND_REPLY"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, s := setupTestService(t)
			botID := model.NewId()
			userID := model.NewId()
			peerID := model.NewId()
			rootPostID := "mention"

			conv := &store.Conversation{
				ID: model.NewId(), UserID: userID, BotID: botID, RootPostID: &rootPostID,
				SystemPrompt: "system", Operation: "conversation",
			}
			require.NoError(t, s.CreateConversation(conv))
			for i, spec := range append(slices.Clone(baseTurns), tt.turns...) {
				content, err := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: spec.text}})
				require.NoError(t, err)
				turn := &store.Turn{
					ID: model.NewId(), ConversationID: conv.ID, Role: spec.role,
					Content: content, Sequence: i + 1, CreatedAt: spec.createdAt,
				}
				if spec.postID != "" {
					turn.PostID = new(spec.postID)
				}
				require.NoError(t, s.CreateTurn(turn))
			}

			var posts []*model.Post
			for _, spec := range append(slices.Clone(basePosts), tt.posts...) {
				author := peerID
				switch {
				case spec.bot:
					author = botID
				case strings.Contains(spec.message, "MENTION") || strings.Contains(spec.message, "SECOND_MESSAGE"):
					author = userID
				}
				posts = append(posts, &model.Post{Id: spec.id, UserId: author, CreateAt: spec.createAt, Message: spec.message})
			}
			slices.SortStableFunc(posts, func(a, b *model.Post) int { return int(a.CreateAt - b.CreateAt) })
			threadData := &mmapi.ThreadData{
				Posts: posts,
				UsersByID: map[string]*model.User{
					userID: {Id: userID, Username: "alice"},
					peerID: {Id: peerID, Username: "mallory"},
					botID:  {Id: botID, Username: "aibot"},
				},
			}
			svc.bots = &testBotLookup{botUserIDs: map[string]bool{botID: true}}

			req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData, BuildOptions{ResponsePostID: tt.responseID})
			require.NoError(t, err)

			var rendered []string
			for _, post := range req.Posts[1:] {
				rendered = append(rendered, post.Message)
			}
			last := -1
			for _, marker := range tt.wantOrder {
				idx := slices.IndexFunc(rendered, func(msg string) bool { return strings.HasSuffix(msg, marker) })
				require.GreaterOrEqual(t, idx, 0, "%q missing from %q", marker, rendered)
				require.Greater(t, idx, last, "%q out of order in %q", marker, rendered)
				last = idx
			}
			for _, marker := range tt.wantMissing {
				require.NotContains(t, strings.Join(rendered, "\n"), marker)
			}
		})
	}
}

func TestBuildChannelMentionRequest_SkipThreadPostsAfterLatestUserTurn(t *testing.T) {
	type postSpec struct {
		id       string
		createAt int64
		updateAt int64
	}
	const (
		anchorID = "mention"
		anchorAt = int64(1000)
	)

	tests := []struct {
		name        string
		skip        bool
		anchorGone  bool
		peers       []postSpec
		wantPeers   []string
		wantMissing []string
	}{
		{
			name:        "post created after the latest user turn is dropped",
			skip:        true,
			peers:       []postSpec{{id: "PEER_AFTER", createAt: 3000, updateAt: 3000}},
			wantMissing: []string{"PEER_AFTER"},
		},
		{
			name:      "post created before the latest user turn is kept",
			skip:      true,
			peers:     []postSpec{{id: "PEER_BEFORE", createAt: 500, updateAt: 500}},
			wantPeers: []string{"PEER_BEFORE"},
		},
		{
			name:        "post created at the latest user turn's time is kept, later edits drop it",
			skip:        true,
			peers:       []postSpec{{id: "PEER_SAME", createAt: anchorAt, updateAt: anchorAt}, {id: "PEER_EDITED", createAt: 500, updateAt: 3000}},
			wantPeers:   []string{"PEER_SAME"},
			wantMissing: []string{"PEER_EDITED"},
		},
		{
			name:        "every non-turn post is dropped when the latest user turn's post is gone",
			skip:        true,
			anchorGone:  true,
			peers:       []postSpec{{id: "PEER_BEFORE", createAt: 500, updateAt: 500}, {id: "PEER_AFTER", createAt: 3000, updateAt: 3000}},
			wantMissing: []string{"PEER_BEFORE", "PEER_AFTER"},
		},
		{
			name:      "later posts are included when the option is off",
			skip:      false,
			peers:     []postSpec{{id: "PEER_BEFORE", createAt: 500, updateAt: 500}, {id: "PEER_AFTER", createAt: 3000, updateAt: 3000}, {id: "PEER_EDITED", createAt: 600, updateAt: 3500}},
			wantPeers: []string{"PEER_BEFORE", "PEER_AFTER", "PEER_EDITED"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, s := setupTestService(t)
			botID := model.NewId()
			userID := model.NewId()
			peerID := model.NewId()
			rootPostID := anchorID

			conv := &store.Conversation{
				ID: model.NewId(), UserID: userID, BotID: botID, RootPostID: &rootPostID,
				SystemPrompt: "system", Operation: "conversation",
			}
			require.NoError(t, s.CreateConversation(conv))
			for i, spec := range []struct{ role, text, postID string }{
				{"user", "MENTION", anchorID},
				{"assistant", "REPLY", "reply"},
				{"user", "WAKE", ""},
			} {
				content, err := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: spec.text}})
				require.NoError(t, err)
				turn := &store.Turn{ID: model.NewId(), ConversationID: conv.ID, Role: spec.role, Content: content, Sequence: i + 1, CreatedAt: anchorAt + int64(i)*2000}
				if spec.postID != "" {
					turn.PostID = new(spec.postID)
				}
				require.NoError(t, s.CreateTurn(turn))
			}

			posts := []*model.Post{
				{Id: anchorID, UserId: userID, CreateAt: anchorAt, UpdateAt: anchorAt, Message: "MENTION"},
				{Id: "reply", UserId: botID, CreateAt: 2000, UpdateAt: 2000, Message: "REPLY"},
			}
			if tt.anchorGone {
				posts = posts[1:]
			}
			for _, peer := range tt.peers {
				posts = append(posts, &model.Post{Id: peer.id, UserId: peerID, CreateAt: peer.createAt, UpdateAt: peer.updateAt, Message: peer.id})
			}
			slices.SortStableFunc(posts, func(a, b *model.Post) int { return int(a.CreateAt - b.CreateAt) })
			threadData := &mmapi.ThreadData{
				Posts: posts,
				UsersByID: map[string]*model.User{
					userID: {Id: userID, Username: "alice"},
					peerID: {Id: peerID, Username: "mallory"},
					botID:  {Id: botID, Username: "bot"},
				},
			}
			svc.bots = &testBotLookup{botUserIDs: map[string]bool{botID: true}}

			req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData,
				BuildOptions{SkipThreadPostsAfterLatestUserTurn: tt.skip})
			require.NoError(t, err)

			var rendered strings.Builder
			for _, post := range req.Posts {
				rendered.WriteString(post.Message)
				rendered.WriteString("\n")
			}
			// Post-linked turns and the post-less wake turn always render.
			for _, always := range []string{"REPLY", "WAKE"} {
				assert.Contains(t, rendered.String(), always)
			}
			if !tt.anchorGone {
				assert.Contains(t, rendered.String(), "MENTION")
			}
			for _, peer := range tt.wantPeers {
				assert.Contains(t, rendered.String(), peer)
			}
			for _, peer := range tt.wantMissing {
				assert.NotContains(t, rendered.String(), peer)
			}
		})
	}
}

func TestBuildChannelMentionRequest_MultiBotThread(t *testing.T) {
	svc, s := setupTestService(t)

	botA := model.NewId()
	botB := model.NewId()
	userID := model.NewId()
	rootPostID := "multibot_root"

	// Conversation belongs to botA.
	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       userID,
		BotID:        botA,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "hello bots",
		UserPostID:   new("postU1"),
	})
	require.NoError(t, err)

	// BotA's response.
	aContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "botA says hi"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: result.ConversationID,
		PostID:         new("postBotA1"),
		Role:           "assistant",
		Content:        aContent,
		Sequence:       2,
		CreatedAt:      2000,
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(result.ConversationID)
	require.NoError(t, err)

	// Thread includes botB's post which is not in botA's conversation.
	threadData := &mmapi.ThreadData{
		Posts: []*model.Post{
			{Id: "postU1", UserId: userID, CreateAt: 1000, Message: "hello bots"},
			{Id: "postBotA1", UserId: botA, CreateAt: 2000, Message: "botA says hi"},
			{Id: "postBotB1", UserId: botB, CreateAt: 3000, Message: "botB says hello"},
		},
		UsersByID: map[string]*model.User{
			userID: {Id: userID, Username: "user1"},
			botA:   {Id: botA, Username: "botA"},
			botB:   {Id: botB, Username: "botB"},
		},
	}

	svc.bots = &testBotLookup{botUserIDs: map[string]bool{botA: true, botB: true}}

	req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData)
	require.NoError(t, err)

	// system + user_turn(postU1) + assistant_turn(postBotA1) + plain(postBotB1) = 4
	require.Len(t, req.Posts, 4)

	// BotB's post should appear as plain text with @botB prefix.
	botBPost := req.Posts[3]
	assert.Equal(t, llm.PostRoleUser, botBPost.Role)
	assert.Contains(t, botBPost.Message, "@botB")
}

func TestBuildChannelMentionRequest_NoThreadPosts(t *testing.T) {
	svc, _ := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "msg",
	})
	require.NoError(t, err)

	conv, err := svc.GetConversation(result.ConversationID)
	require.NoError(t, err)

	// Nil threadData should fall back to BuildCompletionRequest behavior.
	req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, nil)
	require.NoError(t, err)

	require.Len(t, req.Posts, 2) // system + user
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	assert.Equal(t, llm.PostRoleUser, req.Posts[1].Role)
}

// TestBuildChannelMentionRequest_ToolRoundsMerged verifies that tool rounds
// persisted as non-post assistant + tool_result turn pairs get merged into a
// single assistant llm.Post — the same behavior BuildCompletionRequest
// guarantees. Without this merge, the tool_result turn is emitted as a
// separate user-role post and the assistant's tool_use goes out with an
// empty Result field, which bifrost/Anthropic rejects with
// "text content blocks must be non-empty".
func TestBuildChannelMentionRequest_ToolRoundsMerged(t *testing.T) {
	svc, s := setupTestService(t)

	botID := model.NewId()
	userID := model.NewId()
	rootPostID := "root_tool_merge"

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "what's the weather?",
		UserPostID:   new("postU1"),
	})
	require.NoError(t, err)
	convID := result.ConversationID

	// Tool-round assistant turn (no PostID — WriteToolTurns doesn't attach one).
	toolUseBlocks := []ContentBlock{
		{
			Type:   BlockTypeToolUse,
			ID:     "tc1",
			Name:   "get_weather",
			Input:  json.RawMessage(`{"city":"NYC"}`),
			Status: StatusSuccess,
			Shared: new(true),
		},
	}
	toolUseContent, _ := json.Marshal(toolUseBlocks)
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "assistant",
		Content:        toolUseContent,
		Sequence:       2,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	// Tool-result turn (no PostID).
	toolResultBlocks := []ContentBlock{
		{
			Type:      BlockTypeToolResult,
			ToolUseID: "tc1",
			Content:   "72F, sunny",
			Status:    StatusSuccess,
			Shared:    new(true),
		},
	}
	toolResultContent, _ := json.Marshal(toolResultBlocks)
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "tool_result",
		Content:        toolResultContent,
		Sequence:       3,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	// Final assistant turn linked to the visible response post.
	finalBlocks := []ContentBlock{{Type: BlockTypeText, Text: "Weather in NYC is 72F and sunny."}}
	finalContent, _ := json.Marshal(finalBlocks)
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		PostID:         new("postBot1"),
		Role:           "assistant",
		Content:        finalContent,
		Sequence:       4,
		CreatedAt:      model.GetMillis(),
	})
	require.NoError(t, err)

	conv, err := s.GetConversation(convID)
	require.NoError(t, err)

	threadData := &mmapi.ThreadData{
		Posts: []*model.Post{
			{Id: "postU1", UserId: userID, CreateAt: 1000, Message: "what's the weather?"},
			{Id: "postBot1", UserId: botID, CreateAt: 2000, Message: "Weather in NYC is 72F and sunny."},
		},
		UsersByID: map[string]*model.User{
			userID: {Id: userID, Username: "alice"},
			botID:  {Id: botID, Username: "aibot"},
		},
	}
	svc.bots = &testBotLookup{botUserIDs: map[string]bool{botID: true}}

	req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData)
	require.NoError(t, err)

	// Expected structure: system + user(postU1) + tool-round assistant (tool_use+result merged) + final assistant(postBot1) = 4.
	require.Len(t, req.Posts, 4, "tool_result must merge into the preceding assistant turn instead of becoming its own llm.Post")

	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	assert.Equal(t, llm.PostRoleUser, req.Posts[1].Role)

	toolRound := req.Posts[2]
	assert.Equal(t, llm.PostRoleBot, toolRound.Role, "the tool round must surface as assistant/bot, not as two separate posts")
	require.Len(t, toolRound.ToolUse, 1, "tool_use and its tool_result must pair into a single ToolCall")
	assert.Equal(t, "tc1", toolRound.ToolUse[0].ID)
	assert.Equal(t, "get_weather", toolRound.ToolUse[0].Name)
	assert.Equal(t, "72F, sunny", toolRound.ToolUse[0].Result,
		"tool_result content must merge onto the tool_use's Result field; otherwise providers see an orphan tool_use and reject the request")

	final := req.Posts[3]
	assert.Equal(t, llm.PostRoleBot, final.Role)
	assert.Equal(t, "Weather in NYC is 72F and sunny.", final.Message)
}

// A tool call left pending or accepted (the user replied instead of approving,
// or stopped the response while it ran) must not replay as a call that ran.
func TestBuildRequests_UnexecutedToolCallsReplayAsNotExecuted(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		result     *string
		wantResult string
		wantStatus llm.ToolCallStatus
	}{
		{
			name:       "pending without result",
			status:     StatusPending,
			wantResult: NotExecutedToolResult,
			wantStatus: llm.ToolCallStatusRejected,
		},
		{
			name:       "accepted without result",
			status:     StatusAccepted,
			wantResult: NotExecutedToolResult,
			wantStatus: llm.ToolCallStatusRejected,
		},
		{
			name:       "executed call keeps its empty result",
			status:     StatusSuccess,
			result:     new(""),
			wantResult: "",
			wantStatus: llm.ToolCallStatusSuccess,
		},
		{
			name:       "pending call with a stored result keeps it",
			status:     StatusPending,
			result:     new("72F"),
			wantResult: "72F",
			wantStatus: llm.ToolCallStatusPending,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, s := setupTestService(t)
			botID := model.NewId()
			userID := model.NewId()
			rootPostID := "root_" + model.NewId()

			result, err := svc.CreateConversation(CreateConversationParams{
				UserID:       userID,
				BotID:        botID,
				RootPostID:   &rootPostID,
				Operation:    "conversation",
				SystemPrompt: "system",
				UserMessage:  "what's the weather?",
				UserPostID:   new("postU1"),
			})
			require.NoError(t, err)
			convID := result.ConversationID

			blocks := []ContentBlock{
				{Type: BlockTypeText, Text: "Let me check."},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "get_weather", Input: json.RawMessage(`{}`), Status: tc.status, Shared: new(true)},
			}
			if tc.result != nil {
				blocks = append(blocks, ContentBlock{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: *tc.result, Status: StatusSuccess, Shared: new(true)})
			}
			content, _ := json.Marshal(blocks)
			require.NoError(t, s.CreateTurn(&store.Turn{
				ID: model.NewId(), ConversationID: convID, PostID: new("postBot1"),
				Role: "assistant", Content: content, Sequence: 2, CreatedAt: model.GetMillis(),
			}))
			followUp, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "never mind"}})
			require.NoError(t, s.CreateTurn(&store.Turn{
				ID: model.NewId(), ConversationID: convID, PostID: new("postU2"),
				Role: "user", Content: followUp, Sequence: 3, CreatedAt: model.GetMillis(),
			}))

			conv, err := s.GetConversation(convID)
			require.NoError(t, err)
			svc.bots = &testBotLookup{botUserIDs: map[string]bool{botID: true}}

			dmReq, err := svc.BuildCompletionRequest(conv, &llm.Context{})
			require.NoError(t, err)
			channelReq, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, &mmapi.ThreadData{
				Posts: []*model.Post{
					{Id: "postU1", UserId: userID, CreateAt: 1000, Message: "what's the weather?"},
					{Id: "postBot1", UserId: botID, CreateAt: 2000, Message: "Let me check."},
					{Id: "postU2", UserId: userID, CreateAt: 3000, Message: "never mind"},
				},
				UsersByID: map[string]*model.User{userID: {Id: userID, Username: "alice"}},
			})
			require.NoError(t, err)

			for name, req := range map[string]*llm.CompletionRequest{"dm": dmReq, "channel": channelReq} {
				var calls []llm.ToolCall
				for _, post := range req.Posts {
					calls = append(calls, post.ToolUse...)
				}
				require.Len(t, calls, 1, name)
				assert.Equal(t, tc.wantResult, calls[0].Result, name)
				assert.Equal(t, tc.wantStatus, calls[0].Status, name)
			}

			turns, err := s.GetTurnsForConversation(convID)
			require.NoError(t, err)
			stored, err := UnmarshalBlocks(turns[1].Content)
			require.NoError(t, err)
			assert.Equal(t, tc.status, stored[1].Status, "stored data must not change")
		})
	}
}

func TestSequenceNumbering_Concurrent(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "first",
	})
	require.NoError(t, err)
	convID := result.ConversationID

	// Rapidly create several turns and verify sequences remain consistent.
	for range 10 {
		_, placeholderErr := svc.CreatePlaceholderAssistantTurn(convID, nil)
		require.NoError(t, placeholderErr)
	}

	turns, err := s.GetTurnsForConversation(convID)
	require.NoError(t, err)
	require.Len(t, turns, 11) // 1 user + 10 placeholders

	// Verify all sequences are unique and contiguous.
	for i, turn := range turns {
		assert.Equal(t, i+1, turn.Sequence, "turn %d should have sequence %d", i, i+1)
	}
}

func TestGetOrCreateConversation_RaceConflict(t *testing.T) {
	svc, s := setupTestService(t)

	botID := model.NewId()
	userID := model.NewId()
	rootPostID := "race_root"

	// Pre-create a conversation directly via the store to simulate a race.
	now := model.GetMillis()
	convID := model.NewId()
	err := s.CreateConversation(&store.Conversation{
		ID:           convID,
		UserID:       userID,
		BotID:        botID,
		RootPostID:   &rootPostID,
		Title:        "",
		SystemPrompt: "system",
		Operation:    "conversation",
		CreatedAt:    now,
		UpdatedAt:    now,
		DeleteAt:     0,
	})
	require.NoError(t, err)

	// Create an initial turn so the conversation is not empty.
	content, err := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "first"}})
	require.NoError(t, err)
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		Role:           "user",
		Content:        content,
		Sequence:       1,
		CreatedAt:      now,
	})
	require.NoError(t, err)

	// GetOrCreateConversation with the same (RootPostID, BotID, UserID) should
	// hit the conflict path, retry the lookup, and append the user turn.
	result, err := svc.GetOrCreateConversation(GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    "chan1",
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "prompt (ignored)",
		UserMessage:  "second message",
		UserPostID:   new("post2"),
	})
	require.NoError(t, err)
	assert.False(t, result.IsNew)
	assert.Equal(t, convID, result.Conversation.ID)

	// Verify the user turn was appended.
	turns, err := s.GetTurnsForConversation(convID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	assert.Equal(t, 2, turns[1].Sequence)

	var blocks []ContentBlock
	err = json.Unmarshal(turns[1].Content, &blocks)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "second message", blocks[0].Text)
}

// setupTestServiceWithClient is like setupTestService but also wires in an
// mmapi.Client mock and a populated testBotLookup so attachment-handling
// tests can drive lazy file/image resolution end-to-end.
func setupTestServiceWithClient(
	t *testing.T,
	mmClient mmapi.Client,
	bots *testBotLookup,
) (*Service, *store.Store) {
	t.Helper()

	if mockClient, ok := mmClient.(*mmapimocks.MockClient); ok {
		mockClient.On(
			"HasPermissionToFileAction",
			attachmentTestSessionID,
			mock.Anything,
			model.AccessControlPolicyActionDownloadFileAttachment,
		).Return(true).Maybe()
	}

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

	if bots == nil {
		bots = &testBotLookup{botUserIDs: map[string]bool{}}
	}
	svc := NewService(s, nil, mmClient, bots)
	return svc, s
}

// TestCreateConversation_FileIDsPersistsAsContentBlocks pins the user-turn
// schema for attachments. CreateConversation must store FileIDs as
// BlockTypeImage / BlockTypeFile entries (lazy-load: NO inlined Content)
// alongside the leading text block.
func TestCreateConversation_FileIDsPersistsAsContentBlocks(t *testing.T) {
	mmClient := mmapimocks.NewMockClient(t)
	// No .Maybe(): GetFileInfo MUST be called for every FileID so the
	// MIME type is read from the server (not inferred from the filename
	// extension). Mockery fails the test if either expectation is unmet.
	mmClient.On("GetFileInfo", "img1").Return(&model.FileInfo{
		Id: "img1", Name: "shot.png", MimeType: "image/png",
	}, nil)
	mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
		Id: "doc1", Name: "foo.txt", MimeType: "text/plain",
	}, nil)

	svc, s := setupTestServiceWithClient(t, mmClient, nil)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		SessionID:    attachmentTestSessionID,
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "look at these",
		FileIDs:      []string{"img1", "doc1"},
	})
	require.NoError(t, err)

	turns, err := s.GetTurnsForConversation(result.ConversationID)
	require.NoError(t, err)
	require.Len(t, turns, 1)

	var blocks []ContentBlock
	err = json.Unmarshal(turns[0].Content, &blocks)
	require.NoError(t, err)

	require.Len(t, blocks, 3, "expect [text, image, file] in this order")

	assert.Equal(t, BlockTypeText, blocks[0].Type)
	assert.Equal(t, "look at these", blocks[0].Text)

	assert.Equal(t, BlockTypeImage, blocks[1].Type, "image attachment must persist as a BlockTypeImage")
	assert.Equal(t, "img1", blocks[1].FileID)
	assert.Equal(t, "image/png", blocks[1].MimeType)
	assert.Empty(t, blocks[1].Content,
		"image block must NOT inline content at user-turn write time; image bytes lazy-load via mmClient at request build time")

	assert.Equal(t, BlockTypeFile, blocks[2].Type, "non-image attachment must persist as a BlockTypeFile")
	assert.Equal(t, "doc1", blocks[2].FileID)
	assert.Equal(t, "text/plain", blocks[2].MimeType)
	assert.Empty(t, blocks[2].Content,
		"file content must NOT be inlined at user-turn write time; it lazy-loads via mmClient at request build time")
}

// TestGetOrCreateConversation_AppendsFileIDs verifies the same shape applies
// when appending a new user turn to an existing conversation.
func TestGetOrCreateConversation_AppendsFileIDs(t *testing.T) {
	mmClient := mmapimocks.NewMockClient(t)
	// No .Maybe(): GetFileInfo for the appended attachment MUST run so
	// MIME comes from the server, not the filename extension.
	mmClient.On("GetFileInfo", "doc2").Return(&model.FileInfo{
		Id: "doc2", Name: "notes.txt", MimeType: "text/plain",
	}, nil)

	svc, s := setupTestServiceWithClient(t, mmClient, nil)

	botID := model.NewId()
	userID := model.NewId()
	rootPostID := "thread1"

	// Seed an initial conversation.
	_, err := svc.CreateConversation(CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "first",
	})
	require.NoError(t, err)

	res, err := svc.GetOrCreateConversation(GetOrCreateParams{
		UserID:       userID,
		SessionID:    attachmentTestSessionID,
		BotID:        botID,
		ChannelID:    "chan1",
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "ignored",
		UserMessage:  "follow-up with attachment",
		FileIDs:      []string{"doc2"},
	})
	require.NoError(t, err)
	require.False(t, res.IsNew)

	turns, err := s.GetTurnsForConversation(res.Conversation.ID)
	require.NoError(t, err)
	require.Len(t, turns, 2)

	var blocks []ContentBlock
	err = json.Unmarshal(turns[1].Content, &blocks)
	require.NoError(t, err)

	require.Len(t, blocks, 2)
	assert.Equal(t, BlockTypeText, blocks[0].Type)
	assert.Equal(t, "follow-up with attachment", blocks[0].Text)

	assert.Equal(t, BlockTypeFile, blocks[1].Type)
	assert.Equal(t, "doc2", blocks[1].FileID)
	assert.Equal(t, "text/plain", blocks[1].MimeType)
	assert.Empty(t, blocks[1].Content,
		"appended file blocks must also leave Content empty (lazy-load model)")
}

// TestCreateConversation_EmptyFileIDsAndMessage keeps the existing behavior
// for a message with no attachments and no body — empty content array.
func TestCreateConversation_EmptyFileIDsAndMessage(t *testing.T) {
	svc, s := setupTestServiceWithClient(t, nil, nil)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "",
	})
	require.NoError(t, err)

	turns, err := s.GetTurnsForConversation(result.ConversationID)
	require.NoError(t, err)
	require.Len(t, turns, 1)

	var blocks []ContentBlock
	err = json.Unmarshal(turns[0].Content, &blocks)
	require.NoError(t, err)
	assert.Empty(t, blocks)
}

// TestCreateConversation_FileIDInfoErrorKeepsBareBlock pins the resilience
// contract. If GetFileInfo errors for one fileID, the user turn must still
// be created with the rest, and the bad attachment must keep a bare block so
// request building can tell the model it could not be loaded.
func TestCreateConversation_FileIDInfoErrorKeepsBareBlock(t *testing.T) {
	mmClient := mmapimocks.NewMockClient(t)
	// Both GetFileInfo calls MUST happen — the bad one returns an error,
	// but production must still attempt the fetch (otherwise an
	// implementation that filtered FileIDs by some local rule would
	// silently match this test). LogError must also actually fire so
	// the error is observable to operators.
	mmClient.On("GetFileInfo", "broken").Return(nil, errors.New("file gone"))
	mmClient.On("GetFileInfo", "ok1").Return(&model.FileInfo{
		Id: "ok1", Name: "good.txt", MimeType: "text/plain",
	}, nil)
	mmClient.On("LogError", mock.Anything, mock.Anything).Return()

	svc, s := setupTestServiceWithClient(t, mmClient, nil)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		SessionID:    attachmentTestSessionID,
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "with mixed",
		FileIDs:      []string{"broken", "ok1"},
	})
	require.NoError(t, err, "a single bad file must not break conversation creation")

	turns, err := s.GetTurnsForConversation(result.ConversationID)
	require.NoError(t, err)
	require.Len(t, turns, 1)

	var blocks []ContentBlock
	err = json.Unmarshal(turns[0].Content, &blocks)
	require.NoError(t, err)

	require.Len(t, blocks, 3)
	assert.Equal(t, BlockTypeText, blocks[0].Type)
	assert.Equal(t, ContentBlock{Type: BlockTypeFile, FileID: "broken"}, blocks[1])
	assert.Equal(t, BlockTypeFile, blocks[2].Type)
	assert.Equal(t, "ok1", blocks[2].FileID)
}

// TestBuildCompletionRequest_AttachmentsResolveLazily wires the full path:
// CreateConversation persists references → BuildCompletionRequest →
// BlocksToPost re-fetches the file/image bytes via mmClient.
func TestBuildCompletionRequest_AttachmentsResolveLazily(t *testing.T) {
	t.Run("EnableVision=true populates Files and message gets file content", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)

		// Used twice: once for user-turn creation (read MIME) and once at
		// request build time (lazy resolve).
		mmClient.On("GetFileInfo", "img1").Return(&model.FileInfo{
			Id: "img1", Name: "pic.png", MimeType: "image/png", Size: 100,
		}, nil)
		mmClient.On("GetFile", "img1").Return(io.NopCloser(strings.NewReader("PNGDATA")), nil)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id: "doc1", Name: "notes.txt", MimeType: "text/plain", Size: 5,
		}, nil)
		mmClient.On("GetFile", "doc1").Return(io.NopCloser(strings.NewReader("hello")), nil)

		botID := model.NewId()
		bots := &testBotLookup{
			botUserIDs: map[string]bool{},
			configByID: map[string]testBotConfig{
				botID: {enableVision: true, maxFileSize: 0},
			},
		}

		svc, _ := setupTestServiceWithClient(t, mmClient, bots)

		result, err := svc.CreateConversation(CreateConversationParams{
			UserID:       model.NewId(),
			SessionID:    attachmentTestSessionID,
			BotID:        botID,
			Operation:    "conversation",
			SystemPrompt: "system",
			UserMessage:  "the body",
			FileIDs:      []string{"img1", "doc1"},
		})
		require.NoError(t, err)

		conv, err := svc.GetConversation(result.ConversationID)
		require.NoError(t, err)

		req, err := svc.BuildCompletionRequest(conv, &llm.Context{}, BuildOptions{SessionID: attachmentTestSessionID})
		require.NoError(t, err)

		require.Len(t, req.Posts, 2, "system + user")
		userPost := req.Posts[1]
		assert.Equal(t, llm.PostRoleUser, userPost.Role)

		require.Len(t, userPost.Files, 1, "image attachment must round-trip back into Post.Files")
		assert.Equal(t, "image/png", userPost.Files[0].MimeType)

		assert.Contains(t, userPost.Message, "the body")
		assert.Contains(t, userPost.Message, "Attached File Contents:")
		assert.Contains(t, userPost.Message, "File Name: notes.txt")
		assert.Contains(t, userPost.Message, "Content: hello")
	})

	t.Run("EnableVision=false replaces image with a placeholder and keeps file text content", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)

		mmClient.On("GetFileInfo", "img1").Return(&model.FileInfo{
			Id: "img1", Name: "pic.png", MimeType: "image/png", Size: 100,
		}, nil)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id: "doc1", Name: "notes.txt", MimeType: "text/plain", Size: 5,
		}, nil)
		mmClient.On("GetFile", "doc1").Return(io.NopCloser(strings.NewReader("hello")), nil)
		// NOTE: no GetFile(img1) expectation — vision is off so it must NOT be called.

		botID := model.NewId()
		bots := &testBotLookup{
			botUserIDs: map[string]bool{},
			configByID: map[string]testBotConfig{
				botID: {enableVision: false, maxFileSize: 0},
			},
		}

		svc, _ := setupTestServiceWithClient(t, mmClient, bots)

		result, err := svc.CreateConversation(CreateConversationParams{
			UserID:       model.NewId(),
			SessionID:    attachmentTestSessionID,
			BotID:        botID,
			Operation:    "conversation",
			SystemPrompt: "system",
			UserMessage:  "no vision plz",
			FileIDs:      []string{"img1", "doc1"},
		})
		require.NoError(t, err)

		conv, err := svc.GetConversation(result.ConversationID)
		require.NoError(t, err)

		req, err := svc.BuildCompletionRequest(conv, &llm.Context{}, BuildOptions{SessionID: attachmentTestSessionID})
		require.NoError(t, err)

		require.Len(t, req.Posts, 2)
		userPost := req.Posts[1]
		// Explicit AssertNotCalled so the no-vision contract is
		// readable in the test, not just implicit in mockery's strict
		// mode. Production silently fetching the image bytes anyway
		// would be a bug we'd want to surface here.
		mmClient.AssertNotCalled(t, "GetFile", "img1")
		assert.Empty(t, userPost.Files, "image bytes must not be sent when vision is disabled")
		assert.Contains(t, userPost.Message,
			"[Image attached: pic.png. Image input is disabled for this agent, so you cannot see it.]")
		assert.Contains(t, userPost.Message, "Content: hello",
			"text-file content must still flow through to the LLM even when vision is off")
	})
}

// TestBuildCompletionRequest_RevokedAttachmentIsNotNamed covers an attachment
// the user could read when the turn was written but not when the request is
// rebuilt: the model learns an attachment is unavailable, never its name or
// content.
func TestBuildCompletionRequest_RevokedAttachmentIsNotNamed(t *testing.T) {
	const revokedSessionID = "revoked-session"

	tests := []struct {
		name     string
		fileInfo *model.FileInfo
	}{
		{
			name:     "image",
			fileInfo: &model.FileInfo{Id: "img-secret", Name: "secret-plan.png", MimeType: "image/png", Size: 100},
		},
		{
			name:     "file",
			fileInfo: &model.FileInfo{Id: "doc-secret", Name: "secret-plan.txt", MimeType: "text/plain", Size: 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mmClient := mmapimocks.NewMockClient(t)
			mmClient.On(
				"HasPermissionToFileAction",
				revokedSessionID,
				tt.fileInfo.Id,
				model.AccessControlPolicyActionDownloadFileAttachment,
			).Return(false)
			mmClient.On("GetFileInfo", tt.fileInfo.Id).Return(tt.fileInfo, nil).Once()

			botID := model.NewId()
			bots := &testBotLookup{
				botUserIDs: map[string]bool{},
				configByID: map[string]testBotConfig{botID: {enableVision: true}},
			}
			svc, _ := setupTestServiceWithClient(t, mmClient, bots)

			result, err := svc.CreateConversation(CreateConversationParams{
				UserID:       model.NewId(),
				SessionID:    attachmentTestSessionID,
				BotID:        botID,
				Operation:    "conversation",
				SystemPrompt: "system",
				UserMessage:  "see attached",
				FileIDs:      []string{tt.fileInfo.Id},
			})
			require.NoError(t, err)

			conv, err := svc.GetConversation(result.ConversationID)
			require.NoError(t, err)

			req, err := svc.BuildCompletionRequest(conv, &llm.Context{}, BuildOptions{SessionID: revokedSessionID})
			require.NoError(t, err)

			require.Len(t, req.Posts, 2)
			userPost := req.Posts[1]
			assert.Empty(t, userPost.Files)
			assert.Equal(t, "see attached\n[An attachment here is not available to the requesting user.]", userPost.Message)
			mmClient.AssertNotCalled(t, "GetFile", tt.fileInfo.Id)
		})
	}
}

// TestBuildChannelMentionRequest_AttachmentsResolveLazily mirrors the
// BuildCompletionRequest integration test for the channel-mention path.
// Both builders run user turns through BlocksToPost, so attachments must
// flow identically — a regression in only one path would leak otherwise.
//
// This test passes a non-nil threadData containing the user-turn's PostID so
// BuildChannelMentionRequest takes its post-iteration branch (the long body
// at service.go ~line 586+) and resolves the user turn's attachment blocks
// via turnsToLLMPosts → BlocksToPost using the bot's vision/maxFileSize
// config. With nil threadData the function short-circuits to
// BuildCompletionRequest, which would not exercise this path.
func TestBuildChannelMentionRequest_AttachmentsResolveLazily(t *testing.T) {
	t.Run("EnableVision=true populates Files and message gets file content", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)

		mmClient.On("GetFileInfo", "img1").Return(&model.FileInfo{
			Id: "img1", Name: "pic.png", MimeType: "image/png", Size: 100,
		}, nil)
		mmClient.On("GetFile", "img1").Return(io.NopCloser(strings.NewReader("PNGDATA")), nil)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id: "doc1", Name: "notes.txt", MimeType: "text/plain", Size: 5,
		}, nil)
		mmClient.On("GetFile", "doc1").Return(io.NopCloser(strings.NewReader("hello")), nil)

		botID := model.NewId()
		userID := model.NewId()
		userPostID := "post_user_1"
		bots := &testBotLookup{
			botUserIDs: map[string]bool{botID: true},
			configByID: map[string]testBotConfig{
				botID: {enableVision: true, maxFileSize: 0},
			},
		}

		svc, _ := setupTestServiceWithClient(t, mmClient, bots)

		result, err := svc.CreateConversation(CreateConversationParams{
			UserID:       userID,
			SessionID:    attachmentTestSessionID,
			BotID:        botID,
			Operation:    "conversation",
			SystemPrompt: "system",
			UserMessage:  "channel mention body",
			UserPostID:   new(userPostID),
			FileIDs:      []string{"img1", "doc1"},
		})
		require.NoError(t, err)

		conv, err := svc.GetConversation(result.ConversationID)
		require.NoError(t, err)

		// Non-nil threadData with a post that matches the user turn's
		// PostID — this drives the post-iteration branch so the user
		// turn's attachment blocks flow through turnsToLLMPosts with the
		// bot's vision/maxFileSize config.
		threadData := &mmapi.ThreadData{
			Posts: []*model.Post{
				{Id: userPostID, UserId: userID, CreateAt: 1000, Message: "channel mention body"},
			},
			UsersByID: map[string]*model.User{
				userID: {Id: userID, Username: "alice"},
				botID:  {Id: botID, Username: "aibot"},
			},
		}

		req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData, BuildOptions{SessionID: attachmentTestSessionID})
		require.NoError(t, err)

		require.Len(t, req.Posts, 2, "system + user (resolved from the user turn)")
		userPost := req.Posts[1]
		require.Equal(t, llm.PostRoleUser, userPost.Role)
		require.Len(t, userPost.Files, 1,
			"BuildChannelMentionRequest must lazy-resolve image attachments just like BuildCompletionRequest")
		assert.Equal(t, "image/png", userPost.Files[0].MimeType)
		assert.Contains(t, userPost.Message, "channel mention body")
		assert.Contains(t, userPost.Message, "Attached File Contents:")
		assert.Contains(t, userPost.Message, "File Name: notes.txt")
		assert.Contains(t, userPost.Message, "Content: hello")
	})

	t.Run("EnableVision=false replaces image with a placeholder and keeps file text content", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)

		mmClient.On("GetFileInfo", "img1").Return(&model.FileInfo{
			Id: "img1", Name: "pic.png", MimeType: "image/png", Size: 100,
		}, nil)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id: "doc1", Name: "notes.txt", MimeType: "text/plain", Size: 5,
		}, nil)
		mmClient.On("GetFile", "doc1").Return(io.NopCloser(strings.NewReader("hello")), nil)
		// NOTE: no GetFile(img1) expectation — vision is off so the
		// channel-mention path must NOT fetch the image bytes either.

		botID := model.NewId()
		userID := model.NewId()
		userPostID := "post_user_2"
		bots := &testBotLookup{
			botUserIDs: map[string]bool{botID: true},
			configByID: map[string]testBotConfig{
				botID: {enableVision: false, maxFileSize: 0},
			},
		}

		svc, _ := setupTestServiceWithClient(t, mmClient, bots)

		result, err := svc.CreateConversation(CreateConversationParams{
			UserID:       userID,
			SessionID:    attachmentTestSessionID,
			BotID:        botID,
			Operation:    "conversation",
			SystemPrompt: "system",
			UserMessage:  "no vision channel mention",
			UserPostID:   new(userPostID),
			FileIDs:      []string{"img1", "doc1"},
		})
		require.NoError(t, err)

		conv, err := svc.GetConversation(result.ConversationID)
		require.NoError(t, err)

		threadData := &mmapi.ThreadData{
			Posts: []*model.Post{
				{Id: userPostID, UserId: userID, CreateAt: 1000, Message: "no vision channel mention"},
			},
			UsersByID: map[string]*model.User{
				userID: {Id: userID, Username: "alice"},
				botID:  {Id: botID, Username: "aibot"},
			},
		}

		req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData, BuildOptions{SessionID: attachmentTestSessionID})
		require.NoError(t, err)

		require.Len(t, req.Posts, 2)
		userPost := req.Posts[1]
		mmClient.AssertNotCalled(t, "GetFile", "img1")
		assert.Empty(t, userPost.Files,
			"image bytes must not be sent on the channel-mention path when vision is disabled")
		assert.Contains(t, userPost.Message,
			"[Image attached: pic.png. Image input is disabled for this agent, so you cannot see it.]")
		assert.Contains(t, userPost.Message, "Content: hello",
			"text-file content must still flow through to the LLM via the channel-mention path")
	})

	t.Run("image on root post is visible to later mention in same thread", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)

		mmClient.On("GetFileInfo", "img-root").Return(&model.FileInfo{
			Id: "img-root", Name: "screenshot.png", MimeType: "image/png", Size: 100,
		}, nil)
		mmClient.On("GetFile", "img-root").Return(io.NopCloser(strings.NewReader("PNGDATA")), nil)

		botID := model.NewId()
		userID := model.NewId()
		rootPostID := "root_post_with_image"
		mentionPostID := "later_mention_post"
		bots := &testBotLookup{
			botUserIDs: map[string]bool{botID: true},
			configByID: map[string]testBotConfig{
				botID: {enableVision: true, maxFileSize: 0},
			},
		}

		svc, _ := setupTestServiceWithClient(t, mmClient, bots)

		result, err := svc.CreateConversation(CreateConversationParams{
			UserID:       userID,
			BotID:        botID,
			RootPostID:   new(rootPostID),
			Operation:    "conversation",
			SystemPrompt: "system",
			UserMessage:  "@aibot what do you think?",
			UserPostID:   new(mentionPostID),
		})
		require.NoError(t, err)

		conv, err := svc.GetConversation(result.ConversationID)
		require.NoError(t, err)

		threadData := &mmapi.ThreadData{
			Posts: []*model.Post{
				{
					Id:       rootPostID,
					UserId:   userID,
					CreateAt: 1000,
					Message:  "I noticed this",
					FileIds:  []string{"img-root"},
				},
				{
					Id:       mentionPostID,
					UserId:   userID,
					CreateAt: 2000,
					Message:  "@aibot what do you think?",
				},
			},
			UsersByID: map[string]*model.User{
				userID: {Id: userID, Username: "alice"},
				botID:  {Id: botID, Username: "aibot"},
			},
		}

		req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData, BuildOptions{SessionID: attachmentTestSessionID})
		require.NoError(t, err)

		require.Len(t, req.Posts, 3, "system + root thread post + later mention turn")
		rootPost := req.Posts[1]
		require.Equal(t, llm.PostRoleUser, rootPost.Role)
		require.Len(t, rootPost.Files, 1,
			"image attachments on earlier thread posts must be visible when a later post mentions the agent")
		assert.Equal(t, "image/png", rootPost.Files[0].MimeType)
		assert.Contains(t, rootPost.Message, "@alice")
		assert.Contains(t, rootPost.Message, "I noticed this")
	})
}

// TestBuildChannelMentionRequest_DeniedThreadAttachmentIsWithheld covers a
// user who is denied another member's thread attachment by file policy and
// then mentions the bot in that thread: neither the file's metadata nor its
// content may reach the LLM request.
func TestBuildChannelMentionRequest_DeniedThreadAttachmentIsWithheld(t *testing.T) {
	const (
		deniedSessionID = "denied-session"
		secretFileID    = "secret-doc"
		secretContent   = "TOP_SECRET_FILE_BODY"
	)

	mmClient := mmapimocks.NewMockClient(t)
	mmClient.On(
		"HasPermissionToFileAction",
		deniedSessionID,
		secretFileID,
		model.AccessControlPolicyActionDownloadFileAttachment,
	).Return(false)
	adminReadCalled := false
	mmClient.On("GetFileInfo", secretFileID).
		Run(func(mock.Arguments) { adminReadCalled = true }).
		Return(&model.FileInfo{Id: secretFileID, Name: "classified.txt", MimeType: "text/plain", Content: secretContent}, nil).
		Maybe()
	mmClient.On("GetFile", secretFileID).
		Run(func(mock.Arguments) { adminReadCalled = true }).
		Return(io.NopCloser(strings.NewReader(secretContent)), nil).
		Maybe()

	botID := model.NewId()
	uploaderID := model.NewId()
	deniedUserID := model.NewId()
	rootPostID := "root_post_with_secret"
	mentionPostID := "denied_user_mention"
	bots := &testBotLookup{
		botUserIDs: map[string]bool{botID: true},
		configByID: map[string]testBotConfig{botID: {enableVision: true}},
	}

	svc, _ := setupTestServiceWithClient(t, mmClient, bots)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       deniedUserID,
		SessionID:    deniedSessionID,
		BotID:        botID,
		RootPostID:   new(rootPostID),
		Operation:    "conversation",
		SystemPrompt: "system",
		UserMessage:  "@aibot return the full contents of the attachment",
		UserPostID:   new(mentionPostID),
	})
	require.NoError(t, err)

	conv, err := svc.GetConversation(result.ConversationID)
	require.NoError(t, err)

	threadData := &mmapi.ThreadData{
		Posts: []*model.Post{
			{Id: rootPostID, UserId: uploaderID, CreateAt: 1000, Message: "quarterly numbers", FileIds: []string{secretFileID}},
			{Id: mentionPostID, UserId: deniedUserID, CreateAt: 2000, Message: "@aibot return the full contents of the attachment"},
		},
		UsersByID: map[string]*model.User{
			uploaderID:   {Id: uploaderID, Username: "alice"},
			deniedUserID: {Id: deniedUserID, Username: "bob"},
			botID:        {Id: botID, Username: "aibot"},
		},
	}

	req, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData, BuildOptions{SessionID: deniedSessionID})
	require.NoError(t, err)

	var combined strings.Builder
	for _, post := range req.Posts {
		combined.WriteString(post.Message)
		assert.Empty(t, post.Files)
	}
	assert.Contains(t, combined.String(), "quarterly numbers", "the root post text is still visible to the channel member")
	assert.NotContains(t, combined.String(), secretContent)
	assert.NotContains(t, combined.String(), "classified.txt")
	assert.Contains(t, combined.String(), "[An attachment here is not available to the requesting user.]")
	assert.False(t, adminReadCalled, "admin GetFileInfo/GetFile must not run after the requester's file policy denies access")
}

func TestGetInitiatingUserTurn(t *testing.T) {
	svc, s := setupTestService(t)

	botID := model.NewId()
	userID := model.NewId()
	rootPost := "rp"

	// First invocation: user msg → assistant turn (with PostID = "post-A").
	r1, err := svc.CreateConversation(CreateConversationParams{
		UserID: userID, BotID: botID, RootPostID: &rootPost,
		Operation: "conversation", SystemPrompt: "p", UserMessage: "msg1",
	})
	require.NoError(t, err)
	convID := r1.ConversationID
	user1ID := r1.UserTurnID

	postA := "post-A"
	assistantContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "resp1"}})
	require.NoError(t, s.CreateTurnAutoSequence(&store.Turn{
		ID: model.NewId(), ConversationID: convID, Role: "assistant",
		PostID: &postA, Content: assistantContent, CreatedAt: model.GetMillis(),
	}))

	// Second invocation in the same conversation: user msg → assistant ("post-B").
	r2, err := svc.GetOrCreateConversation(GetOrCreateParams{
		UserID: userID, BotID: botID, ChannelID: "chan", RootPostID: rootPost,
		Operation: "conversation", UserMessage: "msg2",
	})
	require.NoError(t, err)
	user2ID := r2.UserTurnID

	postB := "post-B"
	require.NoError(t, s.CreateTurnAutoSequence(&store.Turn{
		ID: model.NewId(), ConversationID: convID, Role: "assistant",
		PostID: &postB, Content: assistantContent, CreatedAt: model.GetMillis(),
	}))

	t.Run("first invocation: post-A maps to user1", func(t *testing.T) {
		got, err := svc.GetInitiatingUserTurn(convID, postA)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, user1ID, got.ID)
	})

	t.Run("second invocation: post-B maps to user2", func(t *testing.T) {
		got, err := svc.GetInitiatingUserTurn(convID, postB)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, user2ID, got.ID)
	})

	t.Run("unknown post ID returns nil", func(t *testing.T) {
		got, err := svc.GetInitiatingUserTurn(convID, "no-such-post")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestGetPreviousUserTurn(t *testing.T) {
	svc, _ := setupTestService(t)

	botID := model.NewId()
	userID := model.NewId()
	rootPost := "rp"

	r1, err := svc.CreateConversation(CreateConversationParams{
		UserID: userID, BotID: botID, RootPostID: &rootPost,
		Operation: "conversation", SystemPrompt: "p", UserMessage: "msg1",
	})
	require.NoError(t, err)
	convID := r1.ConversationID

	r2, err := svc.GetOrCreateConversation(GetOrCreateParams{
		UserID: userID, BotID: botID, ChannelID: "chan", RootPostID: rootPost,
		Operation: "conversation", UserMessage: "msg2",
	})
	require.NoError(t, err)

	t.Run("first user turn has no predecessor", func(t *testing.T) {
		got, err := svc.GetPreviousUserTurn(convID, r1.UserTurnID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("second user turn returns first", func(t *testing.T) {
		got, err := svc.GetPreviousUserTurn(convID, r2.UserTurnID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, r1.UserTurnID, got.ID)
	})

	t.Run("unknown turn ID returns nil", func(t *testing.T) {
		got, err := svc.GetPreviousUserTurn(convID, "unknown")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}
