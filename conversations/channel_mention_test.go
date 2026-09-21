// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
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
			assistantBlocks[i].Shared = new(true)
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
			resultBlocks[i].Shared = new(true)
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

// Text stored in the seeded user turns, keyed to the post each turn anchors to,
// and the messages the posts carry once the hooks have run.
const (
	rootTurnText    = "root turn text"
	replyTurnText   = "reply turn text"
	editedReplyText = "edited reply text"
	editedRootText  = "the root post as it now reads"
	storedTitle     = "Deploying the unreleased pricing change"
	titleSourceText = "Deploy the unreleased pricing change"
	unanchoredText  = "turn written without a post of its own"
)

// deletedPostFixture holds a channel conversation with two user turns: one
// anchored to the thread root post, one anchored to a mid-thread reply.
type deletedPostFixture struct {
	service        *conversations.Conversations
	store          *store.Store
	conversationID string
	rootPostID     string
	replyPostID    string
	channelID      string
	userID         string
	// seededPostID is the anchor of the conversation a case seeded for itself.
	seededPostID string
}

// setupDeletedPostFixture builds a Conversations backed by a real store in a
// fresh schema and seeds the two-turn conversation through conversation.Service.
func setupDeletedPostFixture(t *testing.T) *deletedPostFixture {
	t.Helper()

	setupDB, err := sqlx.Connect("postgres", channelMentionTestConnStr)
	require.NoError(t, err)

	schemaName := fmt.Sprintf("test_%d", time.Now().UnixNano())
	_, err = setupDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schemaName))
	require.NoError(t, err)
	setupDB.Close()

	// search_path goes in the connection string so every pooled connection
	// inherits it, including the ones opened for nested statements.
	db, err := sqlx.Connect("postgres", channelMentionTestConnStr+"&search_path="+schemaName)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schemaName))
		db.Close()
	})

	s := store.New(db)
	require.NoError(t, s.RunMigrations())

	botID := model.NewId()
	userID := model.NewId()
	channelID := model.NewId()
	rootPostID := model.NewId()
	replyPostID := model.NewId()

	mmClient := &fakeMMClient{
		posts: map[string]*model.Post{
			rootPostID: {
				Id:        rootPostID,
				UserId:    userID,
				ChannelId: channelID,
				Message:   rootTurnText,
			},
			replyPostID: {
				Id:        replyPostID,
				UserId:    userID,
				ChannelId: channelID,
				RootId:    rootPostID,
				Message:   replyTurnText,
			},
		},
	}

	convService := conversation.NewService(s, nil, mmClient, &channelMentionBotLookup{
		botIDs: map[string]bool{botID: true},
	})

	first, err := convService.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  rootTurnText,
		UserPostID:   &rootPostID,
	})
	require.NoError(t, err)
	require.True(t, first.IsNew)

	second, err := convService.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:      userID,
		BotID:       botID,
		ChannelID:   channelID,
		RootPostID:  rootPostID,
		Operation:   "conversation",
		UserMessage: replyTurnText,
		UserPostID:  &replyPostID,
	})
	require.NoError(t, err)
	require.False(t, second.IsNew)
	require.Equal(t, first.Conversation.ID, second.Conversation.ID)

	service := conversations.New(
		nil,
		mmClient,
		nil,
		nil,
		nil,
		mmapi.NewTestDBClient(db),
		nil,
		nil,
		nil,
		nil,
	)
	service.SetConversationService(convService)

	fixture := &deletedPostFixture{
		service:        service,
		store:          s,
		conversationID: first.Conversation.ID,
		rootPostID:     rootPostID,
		replyPostID:    replyPostID,
		channelID:      channelID,
		userID:         userID,
	}

	// Both turn texts are stored before the method under test runs.
	require.Contains(t, fixture.turnContents(t), rootTurnText)
	require.Contains(t, fixture.turnContents(t), replyTurnText)

	return fixture
}

// turnContents returns the concatenated raw JSON content of every turn still
// stored for the fixture conversation.
func (f *deletedPostFixture) turnContents(t *testing.T) string {
	t.Helper()

	return f.contentsOfConversation(t, f.conversationID)
}

// contentsOfConversation returns the concatenated raw JSON content of every
// turn stored for the given conversation.
func (f *deletedPostFixture) contentsOfConversation(t *testing.T, conversationID string) string {
	t.Helper()

	turns, err := f.store.GetTurnsForConversation(conversationID)
	require.NoError(t, err)

	var b strings.Builder
	for _, turn := range turns {
		b.Write(turn.Content)
	}
	return b.String()
}

// conversationIsRetrievable reports whether the fixture conversation row is
// still readable, which the store scopes to rows that are not soft-deleted.
func (f *deletedPostFixture) conversationIsRetrievable(t *testing.T) bool {
	t.Helper()

	conv, err := f.store.GetConversation(f.conversationID)
	if errors.Is(err, store.ErrConversationNotFound) {
		return false
	}
	require.NoError(t, err)
	require.NotNil(t, conv)
	return true
}

// seedTitledConversation stores a titled conversation in the thread of the
// fixture, holding a single opening user turn anchored to anchorPostID, or
// anchored to no post when that is nil.
func seedTitledConversation(t *testing.T, f *deletedPostFixture, anchorPostID *string, text string) string {
	t.Helper()

	content, err := json.Marshal([]conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: text},
	})
	require.NoError(t, err)

	now := model.GetMillis()
	channelID := f.channelID
	rootPostID := f.rootPostID
	conv := &store.Conversation{
		ID:         model.NewId(),
		UserID:     f.userID,
		BotID:      model.NewId(),
		ChannelID:  &channelID,
		RootPostID: &rootPostID,
		Title:      storedTitle,
		Operation:  "conversation",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	require.NoError(t, f.store.CreateConversation(conv))
	require.NoError(t, f.store.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: conv.ID,
		PostID:         anchorPostID,
		Role:           "user",
		Content:        content,
		Sequence:       1,
		CreatedAt:      now,
	}))

	return conv.ID
}

// rootPostWithMessage builds the fixture's thread root post carrying message.
func rootPostWithMessage(f *deletedPostFixture, message string) *model.Post {
	return &model.Post{
		Id:        f.rootPostID,
		UserId:    f.userID,
		ChannelId: f.channelID,
		Message:   message,
	}
}

// replyPostWithMessage builds the fixture's thread reply carrying message.
func replyPostWithMessage(f *deletedPostFixture, message string) *model.Post {
	return &model.Post{
		Id:        f.replyPostID,
		UserId:    f.userID,
		ChannelId: f.channelID,
		RootId:    f.rootPostID,
		Message:   message,
	}
}

// anchoredPostAction selects the post hook a case drives.
type anchoredPostAction int

const (
	anchoredPostDeleted anchoredPostAction = iota
	anchoredPostEdited
)

// TestStoredTurnTextFollowsAnchoredPost covers the stored text of a user turn
// and the title written from it: both describe the message of the post the
// turn is anchored to, so the post hooks keep them in step with it.
func TestStoredTurnTextFollowsAnchoredPost(t *testing.T) {
	tests := []struct {
		name   string
		action anchoredPostAction
		// prepare runs before the hook and returns the ID of the conversation
		// the case is about; a case without one is about the fixture
		// conversation.
		prepare func(t *testing.T, f *deletedPostFixture) string
		// post is the post handed to the hook.
		post func(f *deletedPostFixture) *model.Post
		// previous is the post as it read before the edit.
		previous func(f *deletedPostFixture) *model.Post
		// expectedTitle, when set, is the title that conversation carries
		// after the hook has run.
		expectedTitle *string
		validate      func(t *testing.T, f *deletedPostFixture, conversationID string)
	}{
		{
			name:   "deleted mid-thread reply post",
			action: anchoredPostDeleted,
			post: func(f *deletedPostFixture) *model.Post {
				return replyPostWithMessage(f, replyTurnText)
			},
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.NotContains(t, contents, replyTurnText,
					"turn content anchored to the deleted reply should not remain")
				assert.Contains(t, contents, rootTurnText,
					"turn content anchored to other posts should remain")
			},
		},
		{
			name:   "deleted thread root post",
			action: anchoredPostDeleted,
			post: func(f *deletedPostFixture) *model.Post {
				return rootPostWithMessage(f, rootTurnText)
			},
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				assert.NotContains(t, f.turnContents(t), rootTurnText,
					"turn content anchored to the deleted root should not remain")
				assert.False(t, f.conversationIsRetrievable(t),
					"conversation keyed to the deleted root post should be soft-deleted")
			},
		},
		{
			name:   "deleted post unrelated to the conversation",
			action: anchoredPostDeleted,
			post: func(f *deletedPostFixture) *model.Post {
				return &model.Post{
					Id:        model.NewId(),
					UserId:    f.userID,
					ChannelId: f.channelID,
					Message:   "unrelated message",
				}
			},
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, rootTurnText)
				assert.Contains(t, contents, replyTurnText)
				assert.True(t, f.conversationIsRetrievable(t))
			},
		},
		{
			name:   "deleted nil post",
			action: anchoredPostDeleted,
			post: func(f *deletedPostFixture) *model.Post {
				return nil
			},
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, rootTurnText)
				assert.Contains(t, contents, replyTurnText)
				assert.True(t, f.conversationIsRetrievable(t))
			},
		},
		{
			name:   "deleted post with empty id",
			action: anchoredPostDeleted,
			post: func(f *deletedPostFixture) *model.Post {
				return &model.Post{ChannelId: f.channelID}
			},
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, rootTurnText)
				assert.Contains(t, contents, replyTurnText)
				assert.True(t, f.conversationIsRetrievable(t))
			},
		},
		{
			name:   "edited reply post with a new message",
			action: anchoredPostEdited,
			post: func(f *deletedPostFixture) *model.Post {
				return replyPostWithMessage(f, editedReplyText)
			},
			previous: func(f *deletedPostFixture) *model.Post {
				return replyPostWithMessage(f, replyTurnText)
			},
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, editedReplyText,
					"turn content anchored to the post should carry its current message")
				assert.NotContains(t, contents, replyTurnText)
				assert.Contains(t, contents, rootTurnText,
					"turn content anchored to other posts should be left as stored")
			},
		},
		{
			name:   "edited reply post with an unchanged message",
			action: anchoredPostEdited,
			post: func(f *deletedPostFixture) *model.Post {
				return replyPostWithMessage(f, replyTurnText)
			},
			previous: func(f *deletedPostFixture) *model.Post {
				return replyPostWithMessage(f, replyTurnText)
			},
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				assert.Contains(t, f.turnContents(t), replyTurnText)
			},
		},
		{
			name:   "edited post unrelated to the conversation",
			action: anchoredPostEdited,
			post: func(f *deletedPostFixture) *model.Post {
				return &model.Post{
					Id:        model.NewId(),
					UserId:    f.userID,
					ChannelId: f.channelID,
					Message:   editedReplyText,
				}
			},
			previous: func(f *deletedPostFixture) *model.Post { return nil },
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, rootTurnText)
				assert.Contains(t, contents, replyTurnText)
				assert.NotContains(t, contents, editedReplyText)
			},
		},
		{
			name:     "edited nil post",
			action:   anchoredPostEdited,
			post:     func(f *deletedPostFixture) *model.Post { return nil },
			previous: func(f *deletedPostFixture) *model.Post { return nil },
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, rootTurnText)
				assert.Contains(t, contents, replyTurnText)
			},
		},
		{
			name:   "the post the title was written from is edited",
			action: anchoredPostEdited,
			post: func(f *deletedPostFixture) *model.Post {
				return rootPostWithMessage(f, editedRootText)
			},
			previous: func(f *deletedPostFixture) *model.Post {
				return rootPostWithMessage(f, rootTurnText)
			},
			expectedTitle: new(""),
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, editedRootText,
					"the turn holds the message its post now carries")
				assert.NotContains(t, contents, rootTurnText)
			},
		},
		{
			name:   "the post the title was written from is deleted",
			action: anchoredPostDeleted,
			prepare: func(t *testing.T, f *deletedPostFixture) string {
				f.seededPostID = model.NewId()
				return seedTitledConversation(t, f, &f.seededPostID, titleSourceText)
			},
			post: func(f *deletedPostFixture) *model.Post {
				return &model.Post{
					Id:        f.seededPostID,
					UserId:    f.userID,
					ChannelId: f.channelID,
					RootId:    f.rootPostID,
					Message:   titleSourceText,
				}
			},
			expectedTitle: new(""),
			validate: func(t *testing.T, f *deletedPostFixture, conversationID string) {
				assert.NotContains(t, f.contentsOfConversation(t, conversationID), titleSourceText,
					"the turn the title was written from holds no text either")
			},
		},
		{
			name:   "a post other than the one the title was written from is edited",
			action: anchoredPostEdited,
			post: func(f *deletedPostFixture) *model.Post {
				return replyPostWithMessage(f, editedReplyText)
			},
			previous: func(f *deletedPostFixture) *model.Post {
				return replyPostWithMessage(f, replyTurnText)
			},
			expectedTitle: new(storedTitle),
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, editedReplyText,
					"the edited post's own turn holds the message it now carries")
				assert.Contains(t, contents, rootTurnText,
					"the turn the title was written from is left as stored")
			},
		},
		{
			name:   "a post other than the one the title was written from is deleted",
			action: anchoredPostDeleted,
			post: func(f *deletedPostFixture) *model.Post {
				return replyPostWithMessage(f, replyTurnText)
			},
			expectedTitle: new(storedTitle),
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.NotContains(t, contents, replyTurnText)
				assert.Contains(t, contents, rootTurnText,
					"the turn the title was written from is left as stored")
			},
		},
		{
			name:   "a conversation whose opening turn has no anchored post",
			action: anchoredPostEdited,
			prepare: func(t *testing.T, f *deletedPostFixture) string {
				return seedTitledConversation(t, f, nil, unanchoredText)
			},
			post: func(f *deletedPostFixture) *model.Post {
				return rootPostWithMessage(f, editedRootText)
			},
			previous: func(f *deletedPostFixture) *model.Post {
				return rootPostWithMessage(f, rootTurnText)
			},
			expectedTitle: new(storedTitle),
			validate: func(t *testing.T, f *deletedPostFixture, conversationID string) {
				assert.Contains(t, f.contentsOfConversation(t, conversationID), unanchoredText,
					"a turn no post is anchored to keeps its text")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := setupDeletedPostFixture(t)
			require.NoError(t, f.store.UpdateConversationTitle(f.conversationID, storedTitle))

			conversationID := f.conversationID
			if tt.prepare != nil {
				conversationID = tt.prepare(t, f)
			}

			var previous *model.Post
			if tt.previous != nil {
				previous = tt.previous(f)
			}

			switch tt.action {
			case anchoredPostDeleted:
				require.NoError(t, f.service.DeleteConversationsForDeletedPost(tt.post(f)))
			case anchoredPostEdited:
				require.NoError(t, f.service.UpdateTurnForEditedPost(tt.post(f), previous))
			default:
				t.Fatalf("unknown action %v", tt.action)
			}

			if tt.expectedTitle != nil {
				conv, err := f.store.GetConversation(conversationID)
				require.NoError(t, err)
				assert.Equal(t, *tt.expectedTitle, conv.Title,
					"the stored title describes the text the opening user turn holds")
			}

			if tt.validate != nil {
				tt.validate(t, f, conversationID)
			}
		})
	}
}
