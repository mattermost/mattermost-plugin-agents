// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/subtitles"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// summaryLLM records streamed requests and answers chunk summaries.
type summaryLLM struct {
	mu         sync.Mutex
	inputLimit int
	streamed   []llm.CompletionRequest
	chunkCalls int
}

func (l *summaryLLM) ChatCompletion(_ context.Context, request llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.streamed = append(l.streamed, request)
	return llm.NewStreamFromString("summary"), nil
}

func (l *summaryLLM) ChatCompletionNoStream(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.chunkCalls++
	return fmt.Sprintf("chunk summary %d", l.chunkCalls), nil
}

func (l *summaryLLM) CountTokens(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}
func (l *summaryLLM) InputTokenLimit() int  { return l.inputLimit }
func (l *summaryLLM) OutputTokenLimit() int { return 8192 }

type summaryBotLookup struct{}

func (summaryBotLookup) IsAnyBot(string) bool                        { return false }
func (summaryBotLookup) GetBotConfigByID(string) (bool, int64, bool) { return false, 0, false }

// newSummaryStore starts a Postgres container and returns a migrated store.
func newSummaryStore(t *testing.T) *store.Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sqlx.Connect("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	s := store.New(db)
	require.NoError(t, s.RunMigrations())
	return s
}

func TestSummarizeTranscriptionCreatesConversation(t *testing.T) {
	const (
		userID = "user-id"
		botID  = "bot-id"
	)

	convStore := newSummaryStore(t)
	p, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	vtt := "WEBVTT\n\n1\n00:00:00.000 --> 00:00:02.000\n<v Alice>We agreed to ship on Friday.\n\n2\n00:00:02.000 --> 00:00:04.000\n<v Bob>I will write the release notes.\n"

	var long strings.Builder
	long.WriteString("WEBVTT\n")
	for i := range 300 {
		fmt.Fprintf(&long, "\n%d\n00:00:%02d.000 --> 00:00:%02d.500\n<v Alice>We agreed to ship on Friday and review the plan again.\n", i+1, i%60, i%60)
	}

	tests := []struct {
		name          string
		vtt           string
		inputLimit    int
		expectChunked bool
		// threadTaken simulates the user replying in the summary thread
		// before transcription finished, which claims the thread first.
		threadTaken bool
	}{
		{name: "short transcription is stored verbatim", vtt: vtt, inputLimit: 0},
		// A tiny limit forces the chunked path; the stored user turn holds
		// the joined chunk summaries rather than the raw transcription.
		{name: "long transcription stores chunk summaries", vtt: long.String(), inputLimit: 1, expectChunked: true},
		{name: "thread already has a conversation", vtt: vtt, inputLimit: 0, threadTaken: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootPostID := model.NewId()
			transcription, err := subtitles.NewSubtitlesFromVTT(strings.NewReader(tt.vtt))
			require.NoError(t, err)

			if tt.threadTaken {
				require.NoError(t, convStore.CreateConversation(&store.Conversation{
					ID: model.NewId(), UserID: userID, BotID: botID, RootPostID: &rootPostID,
				}))
			}

			mockAPI := &plugintest.API{}
			mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
			mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Maybe()
			mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

			lm := &summaryLLM{inputLimit: tt.inputLimit}
			bot := bots.NewBot(llm.BotConfig{Name: "matty"}, llm.ServiceConfig{}, &model.Bot{UserId: botID, Username: "matty"}, lm)

			s := &Service{
				pluginAPI:      pluginapi.NewClient(mockAPI, nil),
				prompts:        p,
				licenseChecker: enterprisetest.CheckerAt(enterprise.LevelEnterprise),
				convService:    conversation.NewService(convStore, p, nil, summaryBotLookup{}),
			}

			result, err := s.SummarizeTranscription(t.Context(), bot, transcription, &llm.Context{RequestingUser: &model.User{Id: userID, Locale: "en"}}, userID, rootPostID)
			require.NoError(t, err)
			require.NotEmpty(t, result.ConversationID)
			_, err = result.Stream.ReadAll()
			require.NoError(t, err)

			conv, err := convStore.GetConversation(result.ConversationID)
			require.NoError(t, err)
			require.Equal(t, userID, conv.UserID)
			require.Equal(t, botID, conv.BotID)
			require.Equal(t, llm.OperationMeetingSummary, conv.Operation)
			require.Equal(t, TitleMeetingSummary, conv.Title)
			if tt.threadTaken {
				require.Nil(t, conv.RootPostID, "summary must not collide with the thread's existing conversation")
			} else {
				require.NotNil(t, conv.RootPostID)
				require.Equal(t, rootPostID, *conv.RootPostID)
			}

			turns, err := convStore.GetTurnsForConversation(result.ConversationID)
			require.NoError(t, err)
			require.Len(t, turns, 1)
			require.Equal(t, "user", turns[0].Role)
			var blocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[0].Content, &blocks))
			require.Len(t, blocks, 1)

			// The streamed request is built from the stored conversation.
			require.Len(t, lm.streamed, 1)
			req := lm.streamed[0]
			require.Equal(t, llm.OperationMeetingSummary, req.Operation)
			require.Len(t, req.Posts, 2)
			require.Equal(t, conv.SystemPrompt, req.Posts[0].Message)
			require.Equal(t, blocks[0].Text, req.Posts[1].Message)

			if tt.expectChunked {
				require.Positive(t, lm.chunkCalls)
				require.Equal(t, llm.SubTypeChunkedTrue, req.OperationSubType)
				require.Contains(t, blocks[0].Text, "chunk summary 1")
			} else {
				require.Zero(t, lm.chunkCalls)
				require.Equal(t, llm.SubTypeChunkedFalse, req.OperationSubType)
				require.Contains(t, blocks[0].Text, "We agreed to ship on Friday.")
			}
		})
	}
}
