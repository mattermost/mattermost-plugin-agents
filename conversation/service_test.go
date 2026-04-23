// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
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

// testLLM is a simple test double for llm.LanguageModel that returns a
// fixed response for ChatCompletionNoStream.
type testLLM struct {
	noStreamResponse string
	noStreamErr      error
}

func (t *testLLM) ChatCompletion(_ llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	return nil, fmt.Errorf("not implemented in test")
}

func (t *testLLM) ChatCompletionNoStream(_ llm.CompletionRequest, _ ...llm.LanguageModelOption) (string, error) {
	return t.noStreamResponse, t.noStreamErr
}

func (t *testLLM) CountTokens(_ string) int { return 0 }
func (t *testLLM) InputTokenLimit() int     { return 100000 }

func stringPtr(s string) *string { return &s }

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
				ChannelID:    stringPtr("chan1"),
				RootPostID:   stringPtr("root1"),
				Operation:    "conversation",
				SystemPrompt: "You are a helpful assistant",
				UserMessage:  "Hello!",
				UserPostID:   stringPtr("post1"),
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
		UserPostID:   stringPtr("post_A"),
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
		UserPostID:   stringPtr("post_B"),
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
		UserPostID:   stringPtr("post1"),
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
		UserPostID:   stringPtr("post1"),
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
		UserPostID:   stringPtr("post2"),
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

	conv, err := svc.store.GetConversation(result.ConversationID)
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
			Shared: BoolPtr(true),
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
			Shared:    BoolPtr(true),
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
		{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(true)},
	})
	addTurn("tool_result", 3, []ContentBlock{
		{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "first result", Status: StatusSuccess, Shared: BoolPtr(true)},
	})
	addTurn("assistant", 4, []ContentBlock{
		{Type: BlockTypeToolUse, ID: "tc2", Name: "search", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(true)},
	})
	addTurn("tool_result", 5, []ContentBlock{
		{Type: BlockTypeToolResult, ToolUseID: "tc2", Content: "second result", Status: StatusSuccess, Shared: BoolPtr(true)},
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

	// Two tool calls, one shared, one unshared.
	assistantBlocks := []ContentBlock{
		{Type: BlockTypeToolUse, ID: "tc-shared", Name: "search", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(true)},
		{Type: BlockTypeToolUse, ID: "tc-private", Name: "read_dm", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(false)},
	}
	assistantContent, err := json.Marshal(assistantBlocks)
	require.NoError(t, err)
	err = s.CreateTurn(&store.Turn{
		ID: model.NewId(), ConversationID: convID, Role: "assistant",
		Content: assistantContent, Sequence: 2, CreatedAt: model.GetMillis(),
	})
	require.NoError(t, err)

	resultBlocks := []ContentBlock{
		{Type: BlockTypeToolResult, ToolUseID: "tc-shared", Content: "PUBLIC DATA", Status: StatusSuccess, Shared: BoolPtr(true)},
		{Type: BlockTypeToolResult, ToolUseID: "tc-private", Content: "PRIVATE SECRET", Status: StatusSuccess, Shared: BoolPtr(false)},
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
		assert.Equal(t, UnsharedToolResultRedaction, results["tc-private"],
			"kept-private tool_result content must never leak into the default LLM prompt; "+
				"a channel-visible response could paraphrase it")
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
		{Type: BlockTypeToolUse, ID: "tc-private", Name: "read_dm", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(false)},
	}
	assistantContent, err := json.Marshal(assistantBlocks)
	require.NoError(t, err)
	err = s.CreateTurn(&store.Turn{
		ID: model.NewId(), ConversationID: convID, Role: "assistant",
		Content: assistantContent, Sequence: 2, CreatedAt: model.GetMillis(),
	})
	require.NoError(t, err)

	resultBlocks := []ContentBlock{
		{Type: BlockTypeToolResult, ToolUseID: "tc-private", Content: "PRIVATE SECRET", Status: StatusSuccess, Shared: BoolPtr(false)},
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

	conv, err := svc.store.GetConversation(result.ConversationID)
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
		UserPostID:   stringPtr("user_post1"),
	})
	require.NoError(t, err)
	convID := result.ConversationID

	// Assistant turn (seq=2) with postID "resp1".
	assistantContent1, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "assistant1"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		PostID:         stringPtr("resp1"),
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
		PostID:         stringPtr("user_post2"),
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
		PostID:         stringPtr("resp2"),
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

	turnID, err := svc.CreatePlaceholderAssistantTurn(result.ConversationID, stringPtr("response_post"))
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

func TestGenerateTitle(t *testing.T) {
	svc, s := setupTestService(t)

	result, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "prompt",
		UserMessage:  "Tell me about Go",
	})
	require.NoError(t, err)

	mockLLM := &testLLM{noStreamResponse: `"Go Programming Language"`}

	err = svc.GenerateTitle(result.ConversationID, mockLLM, "Tell me about Go", &llm.Context{})
	require.NoError(t, err)

	conv, err := s.GetConversation(result.ConversationID)
	require.NoError(t, err)
	assert.Equal(t, "Go Programming Language", conv.Title)
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
		UserPostID:   stringPtr("post1"),
	})
	require.NoError(t, err)

	// Add assistant turn linked to a post.
	assistantContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "hello user"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: result.ConversationID,
		PostID:         stringPtr("post2"),
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
		UserPostID:   stringPtr("postA1"),
	})
	require.NoError(t, err)

	// Bot's response.
	assistantContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "answer to A"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: result.ConversationID,
		PostID:         stringPtr("postBot1"),
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
		UserPostID:   stringPtr("postU1"),
	})
	require.NoError(t, err)

	// BotA's response.
	aContent, _ := json.Marshal([]ContentBlock{{Type: BlockTypeText, Text: "botA says hi"}})
	err = s.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: result.ConversationID,
		PostID:         stringPtr("postBotA1"),
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

	conv, err := svc.store.GetConversation(result.ConversationID)
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
		UserPostID:   stringPtr("postU1"),
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
			Shared: BoolPtr(true),
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
			Shared:    BoolPtr(true),
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
		PostID:         stringPtr("postBot1"),
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
	for i := 0; i < 10; i++ {
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
		UserPostID:   stringPtr("post2"),
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
