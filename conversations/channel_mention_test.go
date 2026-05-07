// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
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

var channelMentionTestConnStr string

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

	channelMentionTestConnStr, err = container.ConnectionString(context.Background(), "sslmode=disable")
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

// channelMentionBotLookup implements conversation.BotLookup for testing.
type channelMentionBotLookup struct {
	botIDs map[string]bool
}

func (b *channelMentionBotLookup) IsAnyBot(userID string) bool {
	return b.botIDs[userID]
}

func (b *channelMentionBotLookup) GetBotConfigByID(botID string) (bool, int64, bool) {
	return false, 0, false
}

func setupChannelMentionService(t *testing.T) (*conversation.Service, *store.Store) {
	t.Helper()

	db, err := sqlx.Connect("postgres", channelMentionTestConnStr)
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

	bots := &channelMentionBotLookup{botIDs: map[string]bool{}}
	svc := conversation.NewService(s, nil, nil, bots)
	return svc, s
}

func TestChannelMentionFirstMentionCreatesConversation(t *testing.T) {
	svc, s := setupChannelMentionService(t)

	botID := model.NewId()
	userID := model.NewId()
	channelID := model.NewId()
	rootPostID := model.NewId()
	userPostID := model.NewId()

	result, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "Hello @bot",
		UserPostID:   &userPostID,
	})
	require.NoError(t, err)
	require.True(t, result.IsNew)
	require.NotEmpty(t, result.Conversation.ID)

	conv, err := s.GetConversationByThreadBotUser(rootPostID, botID, userID)
	require.NoError(t, err)
	require.NotNil(t, conv)
	assert.Equal(t, result.Conversation.ID, conv.ID)
	assert.Equal(t, userID, conv.UserID)
	assert.Equal(t, botID, conv.BotID)

	// Verify user turn was written
	turns, err := s.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	assert.Equal(t, "user", turns[0].Role)
	assert.Equal(t, 1, turns[0].Sequence)
}

func TestChannelMentionSecondMentionContinuesConversation(t *testing.T) {
	svc, s := setupChannelMentionService(t)

	botID := model.NewId()
	userID := model.NewId()
	channelID := model.NewId()
	rootPostID := model.NewId()
	firstPostID := model.NewId()
	secondPostID := model.NewId()

	// First mention creates conversation
	first, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "First question",
		UserPostID:   &firstPostID,
	})
	require.NoError(t, err)
	require.True(t, first.IsNew)

	// Second mention continues existing conversation
	second, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "Follow-up question",
		UserPostID:   &secondPostID,
	})
	require.NoError(t, err)
	require.False(t, second.IsNew)
	assert.Equal(t, first.Conversation.ID, second.Conversation.ID)

	// Verify turns accumulated
	turns, err := s.GetTurnsForConversation(first.Conversation.ID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	assert.Equal(t, 1, turns[0].Sequence)
	assert.Equal(t, 2, turns[1].Sequence)
}

func TestChannelMentionPerUserThreadIsolation(t *testing.T) {
	svc, s := setupChannelMentionService(t)

	botID := model.NewId()
	aliceID := model.NewId()
	bobID := model.NewId()
	channelID := model.NewId()
	rootPostID := model.NewId()
	alicePostID := model.NewId()
	bobPostID := model.NewId()

	aliceResult, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       aliceID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "Alice question",
		UserPostID:   &alicePostID,
	})
	require.NoError(t, err)
	require.True(t, aliceResult.IsNew)

	bobResult, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       bobID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "Bob question",
		UserPostID:   &bobPostID,
	})
	require.NoError(t, err)
	require.True(t, bobResult.IsNew)
	assert.NotEqual(t, aliceResult.Conversation.ID, bobResult.Conversation.ID)
	assert.Equal(t, aliceID, aliceResult.Conversation.UserID)
	assert.Equal(t, bobID, bobResult.Conversation.UserID)

	alicePostID2 := model.NewId()
	aliceAgain, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       aliceID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "Alice follow-up",
		UserPostID:   &alicePostID2,
	})
	require.NoError(t, err)
	require.False(t, aliceAgain.IsNew)
	assert.Equal(t, aliceResult.Conversation.ID, aliceAgain.Conversation.ID)

	aliceLookup, err := s.GetConversationByThreadBotUser(rootPostID, botID, aliceID)
	require.NoError(t, err)
	assert.Equal(t, aliceResult.Conversation.ID, aliceLookup.ID)

	bobLookup, err := s.GetConversationByThreadBotUser(rootPostID, botID, bobID)
	require.NoError(t, err)
	assert.Equal(t, bobResult.Conversation.ID, bobLookup.ID)
}

func TestChannelMentionMultiBotThreadIsolation(t *testing.T) {
	svc, s := setupChannelMentionService(t)

	botAID := model.NewId()
	botBID := model.NewId()
	userID := model.NewId()
	channelID := model.NewId()
	rootPostID := model.NewId()
	postA := model.NewId()
	postB := model.NewId()

	// Bot A mentioned
	resultA, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botAID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "Bot A system prompt",
		UserMessage:  "Hello @bot-a",
		UserPostID:   &postA,
	})
	require.NoError(t, err)
	require.True(t, resultA.IsNew)

	// Bot B mentioned in the same thread
	resultB, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botBID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "Bot B system prompt",
		UserMessage:  "Hello @bot-b",
		UserPostID:   &postB,
	})
	require.NoError(t, err)
	require.True(t, resultB.IsNew)

	// Separate conversations
	assert.NotEqual(t, resultA.Conversation.ID, resultB.Conversation.ID)

	// Each conversation has only its own turns
	turnsA, err := s.GetTurnsForConversation(resultA.Conversation.ID)
	require.NoError(t, err)
	require.Len(t, turnsA, 1)

	turnsB, err := s.GetTurnsForConversation(resultB.Conversation.ID)
	require.NoError(t, err)
	require.Len(t, turnsB, 1)

	// Verify different system prompts
	convA, err := s.GetConversation(resultA.Conversation.ID)
	require.NoError(t, err)
	assert.Equal(t, "Bot A system prompt", convA.SystemPrompt)

	convB, err := s.GetConversation(resultB.Conversation.ID)
	require.NoError(t, err)
	assert.Equal(t, "Bot B system prompt", convB.SystemPrompt)
}

func TestChannelMentionContextMerge(t *testing.T) {
	_, s := setupChannelMentionService(t)

	botID := model.NewId()
	userID := model.NewId()
	otherUserID := model.NewId()
	channelID := model.NewId()
	rootPostID := model.NewId()
	userPostID := model.NewId()

	bots := &channelMentionBotLookup{botIDs: map[string]bool{botID: true}}
	svc := conversation.NewService(s, nil, nil, bots)

	// Create conversation with a user turn
	result, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "Hello @bot what is the weather?",
		UserPostID:   &userPostID,
	})
	require.NoError(t, err)

	// Simulate an assistant turn linked to a response post
	responsePostID := model.NewId()
	_, err = svc.CreatePlaceholderAssistantTurn(result.Conversation.ID, &responsePostID)
	require.NoError(t, err)

	// Build channel mention request with thread data that includes a post from another user
	otherUserPost := &model.Post{
		Id:        model.NewId(),
		UserId:    otherUserID,
		ChannelId: channelID,
		RootId:    rootPostID,
		Message:   "I think it's sunny",
		CreateAt:  1000,
	}
	userPost := &model.Post{
		Id:        userPostID,
		UserId:    userID,
		ChannelId: channelID,
		RootId:    rootPostID,
		Message:   "Hello @bot what is the weather?",
		CreateAt:  500,
	}
	botResponsePost := &model.Post{
		Id:        responsePostID,
		UserId:    botID,
		ChannelId: channelID,
		RootId:    rootPostID,
		Message:   "Let me check the weather for you.",
		CreateAt:  600,
	}

	threadData := &mmapi.ThreadData{
		Posts: []*model.Post{userPost, botResponsePost, otherUserPost},
		UsersByID: map[string]*model.User{
			userID:      {Id: userID, Username: "alice"},
			otherUserID: {Id: otherUserID, Username: "bob"},
			botID:       {Id: botID, Username: "bot"},
		},
	}

	ctx := &llm.Context{}
	req, err := svc.BuildChannelMentionRequest(result.Conversation, ctx, threadData)
	require.NoError(t, err)

	// Verify request structure:
	// [system prompt, user turn (from DB), assistant turn (from DB), other user as @bob: ...]
	require.GreaterOrEqual(t, len(req.Posts), 4)
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)

	// User post rendered from turn
	assert.Equal(t, llm.PostRoleUser, req.Posts[1].Role)

	// Assistant post rendered from turn
	assert.Equal(t, llm.PostRoleBot, req.Posts[2].Role)

	// Other user's post rendered as plain text with @username prefix
	assert.Equal(t, llm.PostRoleUser, req.Posts[3].Role)
	assert.Contains(t, req.Posts[3].Message, "@bob:")
	assert.Contains(t, req.Posts[3].Message, "I think it's sunny")
}

func TestChannelMentionToolPrivacy(t *testing.T) {
	svc, s := setupChannelMentionService(t)

	botID := model.NewId()
	userID := model.NewId()
	channelID := model.NewId()
	rootPostID := model.NewId()
	userPostID := model.NewId()

	// Create conversation
	result, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "Run a tool",
		UserPostID:   &userPostID,
	})
	require.NoError(t, err)

	// Write auto-run tool turns with shared=false (channel default)
	toolTurns := []toolrunner.ToolTurn{
		{
			AssistantMessage: "Let me run a tool",
			AssistantToolCalls: []llm.ToolCall{
				{
					ID:        "tc_01",
					Name:      "get_weather",
					Arguments: json.RawMessage(`{"city":"NYC"}`),
				},
			},
			ToolResults: []toolrunner.ToolResult{
				{
					ToolCallID: "tc_01",
					Name:       "get_weather",
					Result:     "72F, sunny",
					IsError:    false,
				},
			},
			TokensIn:  100,
			TokensOut: 50,
		},
	}

	err = svc.WriteToolTurns(result.Conversation.ID, toolTurns, false)
	require.NoError(t, err)

	// Verify turns were written
	turns, err := s.GetTurnsForConversation(result.Conversation.ID)
	require.NoError(t, err)
	// 1 user turn + 1 assistant (tool_use) + 1 tool_result = 3
	require.Len(t, turns, 3)

	// Check assistant turn has tool_use blocks with shared=false
	var assistantBlocks []conversation.ContentBlock
	err = json.Unmarshal(turns[1].Content, &assistantBlocks)
	require.NoError(t, err)

	foundToolUse := false
	for _, block := range assistantBlocks {
		if block.Type == conversation.BlockTypeToolUse {
			foundToolUse = true
			require.NotNil(t, block.Shared)
			assert.False(t, *block.Shared, "auto-run tool blocks in channel should have shared=false")
		}
	}
	require.True(t, foundToolUse)

	// Check tool_result turn has shared=false
	var resultBlocks []conversation.ContentBlock
	err = json.Unmarshal(turns[2].Content, &resultBlocks)
	require.NoError(t, err)

	for _, block := range resultBlocks {
		if block.Type == conversation.BlockTypeToolResult {
			require.NotNil(t, block.Shared)
			assert.False(t, *block.Shared, "tool result blocks in channel should have shared=false")
		}
	}

	// Apply privacy filter for non-requester
	filteredBlocks := conversation.FilterForNonRequester(assistantBlocks)
	for _, block := range filteredBlocks {
		if block.Type == conversation.BlockTypeToolUse {
			assert.Nil(t, block.Input, "non-requester should see redacted tool input")
		}
	}

	filteredResultBlocks := conversation.FilterForNonRequester(resultBlocks)
	for _, block := range filteredResultBlocks {
		if block.Type == conversation.BlockTypeToolResult {
			assert.Empty(t, block.Content, "non-requester should see redacted tool result content")
		}
	}
}

func TestChannelMentionToolSharingFlip(t *testing.T) {
	svc, s := setupChannelMentionService(t)

	botID := model.NewId()
	userID := model.NewId()
	channelID := model.NewId()
	rootPostID := model.NewId()
	userPostID := model.NewId()

	// Create conversation
	result, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "Run a tool",
		UserPostID:   &userPostID,
	})
	require.NoError(t, err)

	// Write tool turns with shared=false
	err = svc.WriteToolTurns(result.Conversation.ID, []toolrunner.ToolTurn{
		{
			AssistantMessage: "Running tool",
			AssistantToolCalls: []llm.ToolCall{
				{
					ID:        "tc_01",
					Name:      "search",
					Arguments: json.RawMessage(`{"query":"test"}`),
				},
			},
			ToolResults: []toolrunner.ToolResult{
				{
					ToolCallID: "tc_01",
					Name:       "search",
					Result:     "Found 3 results",
					IsError:    false,
				},
			},
		},
	}, false)
	require.NoError(t, err)

	// Verify shared=false initially
	turns, err := s.GetTurnsForConversation(result.Conversation.ID)
	require.NoError(t, err)
	require.Len(t, turns, 3)

	// Simulate sharing approval: flip shared=true on both tool_use and tool_result turns
	assistantTurn := turns[1]
	var assistantBlocks []conversation.ContentBlock
	err = json.Unmarshal(assistantTurn.Content, &assistantBlocks)
	require.NoError(t, err)

	for i := range assistantBlocks {
		if assistantBlocks[i].Type == conversation.BlockTypeToolUse {
			assistantBlocks[i].Shared = conversation.BoolPtr(true)
		}
	}
	updatedAssistant, err := json.Marshal(assistantBlocks)
	require.NoError(t, err)
	err = s.UpdateTurnContent(assistantTurn.ID, updatedAssistant)
	require.NoError(t, err)

	resultTurn := turns[2]
	var resultBlocks []conversation.ContentBlock
	err = json.Unmarshal(resultTurn.Content, &resultBlocks)
	require.NoError(t, err)

	for i := range resultBlocks {
		if resultBlocks[i].Type == conversation.BlockTypeToolResult {
			resultBlocks[i].Shared = conversation.BoolPtr(true)
		}
	}
	updatedResult, err := json.Marshal(resultBlocks)
	require.NoError(t, err)
	err = s.UpdateTurnContent(resultTurn.ID, updatedResult)
	require.NoError(t, err)

	// Verify non-requester now sees full content
	updatedTurns, err := s.GetTurnsForConversation(result.Conversation.ID)
	require.NoError(t, err)

	var verifyAssistantBlocks []conversation.ContentBlock
	err = json.Unmarshal(updatedTurns[1].Content, &verifyAssistantBlocks)
	require.NoError(t, err)
	filtered := conversation.FilterForNonRequester(verifyAssistantBlocks)
	for _, block := range filtered {
		if block.Type == conversation.BlockTypeToolUse {
			assert.NotNil(t, block.Input, "after sharing approval, non-requester should see full tool input")
		}
	}

	var verifyResultBlocks []conversation.ContentBlock
	err = json.Unmarshal(updatedTurns[2].Content, &verifyResultBlocks)
	require.NoError(t, err)
	filteredResults := conversation.FilterForNonRequester(verifyResultBlocks)
	for _, block := range filteredResults {
		if block.Type == conversation.BlockTypeToolResult {
			assert.Equal(t, "Found 3 results", block.Content, "after sharing approval, non-requester should see full tool result")
		}
	}
}

func TestChannelMentionTurnLookupByPostID(t *testing.T) {
	svc, s := setupChannelMentionService(t)

	botID := model.NewId()
	userID := model.NewId()
	channelID := model.NewId()
	rootPostID := model.NewId()
	userPostID := model.NewId()

	result, err := svc.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  "Hello",
		UserPostID:   &userPostID,
	})
	require.NoError(t, err)

	// Create placeholder assistant turn linked to a response post
	responsePostID := model.NewId()
	turnID, err := svc.CreatePlaceholderAssistantTurn(result.Conversation.ID, &responsePostID)
	require.NoError(t, err)

	// Look up the turn by PostID (simulates handleToolCall looking up the turn)
	turn, err := s.GetTurnByPostID(responsePostID)
	require.NoError(t, err)
	require.NotNil(t, turn)
	assert.Equal(t, turnID, turn.ID)
	assert.Equal(t, result.Conversation.ID, turn.ConversationID)

	// Verify ownership via conversation
	conv, err := s.GetConversation(turn.ConversationID)
	require.NoError(t, err)
	assert.Equal(t, userID, conv.UserID)
}
