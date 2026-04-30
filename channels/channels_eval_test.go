// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channels_test

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/channels"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/evals"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fixedStart = int64(23974)
)

func TestChannelSummarization(t *testing.T) {
	evalConfigs := []struct {
		name            string
		filename        string
		expectedRubrics []string
	}{
		{
			name:     "developers webapp channel",
			filename: "developers_webapp.json",
			expectedRubrics: []string{
				"is a summary",
				"includes a mention that @daniel.espino-garcia mentioned react scan",
				"mentions positive feedback to react scan",
				"mentions @claudio.costa is working on adding code coverage tracking to the monorepo",
				"mentions claudio and harrison discussing exactly what should be tracked for code coverage",
				"mentions harrison queueing a item for a June 2nd webguild meeting about showing off PRs around accessibility",
				"does not mention the summarization process",
				"does not mention people joining or leaving the channel",
			},
		},
	}

	for _, config := range evalConfigs {
		testName := "channel summarization " + config.name
		evals.Run(t, testName, func(t *evals.EvalT) {
			// Load thread data from the JSON file
			path := filepath.Join(".", config.filename)
			threadData := evals.LoadThreadFromJSON(t, path)

			// Setup mocks
			mmClient := mocks.NewMockClient(t)
			promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
			require.NoError(t, err, "Failed to load prompts")

			// Setup mock expectations
			setupChannelMocksFromThreadData(mmClient, threadData)

			// Create a conversation service with in-memory store for eval tests
			evalStore := newEvalInMemoryStore()
			evalBots := &evalBotLookup{botUserIDs: map[string]bool{}}
			convSvc := conversation.NewService(evalStore, nil, nil, evalBots)

			// Create channel service
			channelService := channels.New(
				t.LLM,
				promptsObj,
				mmClient,
				nil, // dbClient not needed for this test
				convSvc,
			)

			// Create context
			ctx := llm.NewContext()
			ctx.RequestingUser = threadData.RequestingUser()
			ctx.Channel = threadData.Channel
			ctx.Team = threadData.Team

			// Perform summarization based on type
			userID := "eval-user"
			if threadData.RequestingUser() != nil {
				userID = threadData.RequestingUser().Id
			}
			result, err := channelService.Interval(ctx, threadData.Channel.Id, userID, "eval-bot", fixedStart, 0, prompts.PromptSummarizeChannelRangeSystem)
			require.NoError(t, err, "Failed to summarize channel")
			require.NotNil(t, result, "Expected a non-nil result")
			textStream := result.Stream
			require.NotNil(t, textStream, "Expected a non-nil text stream")

			// Read the response
			summary, err := textStream.ReadAll()
			require.NoError(t, err, "Failed to read summary from text stream")
			assert.NotEmpty(t, summary, "Expected a non-empty channel summary")

			// Evaluate the summary against rubrics
			for _, rubric := range config.expectedRubrics {
				evals.LLMRubricT(t, rubric, summary)
			}
		})
	}
}

// evalInMemoryStore implements conversation.Store for eval tests.
type evalInMemoryStore struct {
	conversations map[string]*evalConv
	turns         map[string]*evalTurn
	turnsByConv   map[string][]string
}

type evalConv struct {
	store.Conversation
}

type evalTurn struct {
	store.Turn
}

func newEvalInMemoryStore() *evalInMemoryStore {
	return &evalInMemoryStore{
		conversations: make(map[string]*evalConv),
		turns:         make(map[string]*evalTurn),
		turnsByConv:   make(map[string][]string),
	}
}

func (s *evalInMemoryStore) CreateConversation(conv *store.Conversation) error {
	s.conversations[conv.ID] = &evalConv{*conv}
	return nil
}

func (s *evalInMemoryStore) GetConversation(id string) (*store.Conversation, error) {
	c, ok := s.conversations[id]
	if !ok {
		return nil, store.ErrConversationNotFound
	}
	return &c.Conversation, nil
}

func (s *evalInMemoryStore) GetConversationByThreadBotUser(_, _, _ string) (*store.Conversation, error) {
	return nil, store.ErrConversationNotFound
}

func (s *evalInMemoryStore) UpdateConversationTitle(id, title string) error {
	if c, ok := s.conversations[id]; ok {
		c.Title = title
	}
	return nil
}

func (s *evalInMemoryStore) UpdateConversationRootPostID(id string, rootPostID string) error {
	if c, ok := s.conversations[id]; ok {
		c.RootPostID = &rootPostID
	}
	return nil
}

func (s *evalInMemoryStore) CreateTurn(turn *store.Turn) error {
	s.turns[turn.ID] = &evalTurn{*turn}
	s.turnsByConv[turn.ConversationID] = append(s.turnsByConv[turn.ConversationID], turn.ID)
	return nil
}

func (s *evalInMemoryStore) CreateTurnAutoSequence(turn *store.Turn) error {
	maxSeq, _ := s.GetMaxSequenceForConversation(turn.ConversationID)
	turn.Sequence = maxSeq + 1
	return s.CreateTurn(turn)
}

func (s *evalInMemoryStore) GetTurnsForConversation(conversationID string) ([]store.Turn, error) {
	ids := s.turnsByConv[conversationID]
	turns := make([]store.Turn, 0, len(ids))
	for _, id := range ids {
		if t, ok := s.turns[id]; ok {
			turns = append(turns, t.Turn)
		}
	}
	return turns, nil
}

func (s *evalInMemoryStore) UpdateTurnContent(id string, content json.RawMessage) error {
	if t, ok := s.turns[id]; ok {
		t.Content = content
	}
	return nil
}

func (s *evalInMemoryStore) UpdateTurnTokens(id string, tokensIn, tokensOut int64) error {
	if t, ok := s.turns[id]; ok {
		t.TokensIn = tokensIn
		t.TokensOut = tokensOut
	}
	return nil
}

func (s *evalInMemoryStore) GetMaxSequenceForConversation(conversationID string) (int, error) {
	maxSeq := 0
	for _, id := range s.turnsByConv[conversationID] {
		if t, ok := s.turns[id]; ok && t.Sequence > maxSeq {
			maxSeq = t.Sequence
		}
	}
	return maxSeq, nil
}

// evalBotLookup implements conversation.BotLookup for eval tests.
type evalBotLookup struct {
	botUserIDs map[string]bool
}

func (b *evalBotLookup) IsAnyBot(userID string) bool {
	return b.botUserIDs[userID]
}

func setupChannelMocksFromThreadData(mmClient *mocks.MockClient, threadData *evals.ThreadExport) {
	// Mock posts retrieval - return the thread data as channel posts
	mmClient.On("GetPostsSince", threadData.Channel.Id, fixedStart).Return(threadData.PostList, nil)

	// Mock users
	for userID, user := range threadData.Users {
		mmClient.On("GetUser", userID).Return(user, nil)
	}

	// Mock file info if needed
	for _, fileInfo := range threadData.FileInfos {
		mmClient.On("GetFileInfo", fileInfo.Id).Return(fileInfo, nil).Maybe()
	}

	// Mock file content if needed
	for id, file := range threadData.Files {
		mmClient.On("GetFile", id).Return(io.NopCloser(bytes.NewReader(file)), nil).Maybe()
	}
}
