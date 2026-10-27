// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"context"
	"fmt"
	"io"
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
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
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

const (
	summaryUserID    = "user-id"
	summaryBotID     = "bot-id"
	summaryChannelID = "meeting-channel"
	summaryFilePost  = "transcript-file-post"
	initialSession   = "initial-session"
	regenSession     = "regen-session"
	transcriptPhrase = "We agreed to ship on Friday"
)

// longVTT is a transcript well above conversation.InlineFileMaxBytes.
func longVTT() string {
	var long strings.Builder
	long.WriteString("WEBVTT\n")
	for i := range 300 {
		fmt.Fprintf(&long, "\n%d\n00:00:%02d.000 --> 00:00:%02d.500\n<v Alice>%s and review the plan again.\n", i+1, i%60, i%60, transcriptPhrase)
	}
	return long.String()
}

// transcriptFile serves fileID's content and metadata through a mock client,
// letting each session download it only when allowed[session] is true.
type transcriptFile struct {
	id       string
	content  string
	postType string
	allowed  map[string]bool
	reads    int
	// channelRevoked denies the owner read access to the meeting channel.
	channelRevoked bool
}

func (f *transcriptFile) register(mm *mocks.MockClient) {
	mm.EXPECT().HasPermissionToFileAction(mock.Anything, f.id, model.AccessControlPolicyActionDownloadFileAttachment).
		RunAndReturn(func(sessionID, _, _ string) bool { return f.allowed[sessionID] }).Maybe()
	mm.EXPECT().GetFileInfo(f.id).Return(&model.FileInfo{
		Id:        f.id,
		Name:      "transcript.vtt",
		MimeType:  "text/vtt",
		Extension: "vtt",
		Size:      int64(len(f.content)),
		PostId:    summaryFilePost,
		ChannelId: summaryChannelID,
	}, nil).Maybe()
	mm.EXPECT().GetFile(f.id).RunAndReturn(func(string) (io.ReadCloser, error) {
		f.reads++
		return io.NopCloser(strings.NewReader(f.content)), nil
	}).Maybe()
	mm.EXPECT().GetPost(summaryFilePost).Return(&model.Post{Id: summaryFilePost, ChannelId: summaryChannelID, Type: f.postType}, nil).Maybe()
	mm.EXPECT().HasPermissionToChannel(summaryUserID, summaryChannelID, model.PermissionReadChannel).
		RunAndReturn(func(string, string, *model.Permission) bool { return !f.channelRevoked }).Maybe()
	mm.EXPECT().LogError(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
}

func newSummaryService(t *testing.T, convStore *store.Store, mm mmapi.Client) *Service {
	t.Helper()
	p, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	mockAPI := &plugintest.API{}
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	return &Service{
		pluginAPI:      pluginapi.NewClient(mockAPI, nil),
		mmClient:       mm,
		prompts:        p,
		licenseChecker: enterprisetest.CheckerAt(enterprise.LevelEnterprise),
		convService:    conversation.NewService(convStore, p, mm, summaryBotLookup{}),
	}
}

func summaryBot(lm llm.LanguageModel) *bots.Bot {
	return bots.NewBot(llm.BotConfig{Name: "matty"}, llm.ServiceConfig{}, &model.Bot{UserId: summaryBotID, Username: "matty"}, lm)
}

func summaryContext() *llm.Context {
	return &llm.Context{RequestingUser: &model.User{Id: summaryUserID, Locale: "en"}}
}

func TestSummarizeTranscriptionCreatesConversation(t *testing.T) {
	convStore := newSummaryStore(t)

	vtt := "WEBVTT\n\n1\n00:00:00.000 --> 00:00:02.000\n<v Alice>" + transcriptPhrase + ".\n\n2\n00:00:02.000 --> 00:00:04.000\n<v Bob>I will write the release notes.\n"

	tests := []struct {
		name          string
		vtt           string
		inputLimit    int
		expectChunked bool
		// threadTaken simulates the user replying in the summary thread
		// before transcription finished, which claims the thread first.
		threadTaken bool
		fileDenied  bool
	}{
		{name: "short transcription", vtt: vtt},
		// A tiny limit forces the chunked path; the request carries the
		// joined chunk summaries rather than the raw transcription.
		{name: "long transcription is summarized in chunks", vtt: longVTT(), inputLimit: 1, expectChunked: true},
		{name: "thread already has a conversation", vtt: vtt, threadTaken: true},
		{name: "transcript file denied by file policy", vtt: vtt, fileDenied: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootPostID := model.NewId()
			file := &transcriptFile{id: model.NewId(), content: tt.vtt, allowed: map[string]bool{initialSession: !tt.fileDenied}}
			mm := mocks.NewMockClient(t)
			file.register(mm)

			transcription, err := subtitles.NewSubtitlesFromVTT(strings.NewReader(tt.vtt))
			require.NoError(t, err)

			if tt.threadTaken {
				require.NoError(t, convStore.CreateConversation(&store.Conversation{
					ID: model.NewId(), UserID: summaryUserID, BotID: summaryBotID, RootPostID: &rootPostID,
				}))
			}

			lm := &summaryLLM{inputLimit: tt.inputLimit}
			s := newSummaryService(t, convStore, mm)

			result, err := s.SummarizeTranscription(t.Context(), summaryBot(lm), transcription, summaryContext(), SummaryParams{
				UserID:           summaryUserID,
				RootPostID:       rootPostID,
				TranscriptFileID: file.id,
				SessionID:        initialSession,
			})
			if tt.fileDenied {
				require.ErrorIs(t, err, mmapi.ErrFileActionForbidden)
				require.Empty(t, lm.streamed)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, result.ConversationID)
			_, err = result.Stream.ReadAll()
			require.NoError(t, err)

			conv, err := convStore.GetConversation(result.ConversationID)
			require.NoError(t, err)
			require.Equal(t, summaryUserID, conv.UserID)
			require.Equal(t, summaryBotID, conv.BotID)
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
			require.NotContains(t, string(turns[0].Content), transcriptPhrase, "the stored turn must reference the transcript, not inline it")
			blocks, err := conversation.UnmarshalBlocks(turns[0].Content)
			require.NoError(t, err)
			require.Len(t, blocks, 2)
			require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
			require.Equal(t, conversation.BlockTypeFile, blocks[1].Type)
			require.Equal(t, file.id, blocks[1].FileID)

			require.Len(t, lm.streamed, 1)
			req := lm.streamed[0]
			require.Equal(t, llm.OperationMeetingSummary, req.Operation)
			require.Len(t, req.Posts, 2)
			require.Equal(t, conv.SystemPrompt, req.Posts[0].Message)
			require.Equal(t, llm.PostRoleUser, req.Posts[1].Role)

			if tt.expectChunked {
				require.Positive(t, lm.chunkCalls)
				require.Equal(t, llm.SubTypeChunkedTrue, req.OperationSubType)
				require.Contains(t, req.Posts[1].Message, "chunk summary 1")
			} else {
				require.Zero(t, lm.chunkCalls)
				require.Equal(t, llm.SubTypeChunkedFalse, req.OperationSubType)
				require.Contains(t, req.Posts[1].Message, transcriptPhrase)
			}
		})
	}
}

// startSummary runs the initial summary for file and anchors its response to
// a new post, as the streaming layer does, returning the conversation and post.
func startSummary(t *testing.T, s *Service, convStore *store.Store, lm *summaryLLM, file *transcriptFile) (*store.Conversation, string) {
	t.Helper()
	parse := subtitles.NewSubtitlesFromVTT
	if file.postType == "custom_zoom_chat" {
		parse = subtitles.NewSubtitlesFromZoomChat
	}
	transcription, err := parse(strings.NewReader(file.content))
	require.NoError(t, err)

	result, err := s.SummarizeTranscription(t.Context(), summaryBot(lm), transcription, summaryContext(), SummaryParams{
		UserID:           summaryUserID,
		RootPostID:       model.NewId(),
		TranscriptFileID: file.id,
		SessionID:        initialSession,
	})
	require.NoError(t, err)
	_, err = result.Stream.ReadAll()
	require.NoError(t, err)

	postID := model.NewId()
	require.NoError(t, convStore.CreateTurnAutoSequence(&store.Turn{
		ID:             model.NewId(),
		ConversationID: result.ConversationID,
		PostID:         &postID,
		Role:           "assistant",
		Content:        []byte(`[{"type":"text","text":"old summary"}]`),
		CreatedAt:      model.GetMillis(),
	}))
	conv, err := convStore.GetConversation(result.ConversationID)
	require.NoError(t, err)
	return conv, postID
}

func TestRegenerateSummary(t *testing.T) {
	convStore := newSummaryStore(t)

	tests := []struct {
		name         string
		content      string
		postType     string
		regenAllowed bool
		wantInRegen  string
	}{
		{name: "re-reads the VTT transcript", content: longVTT(), regenAllowed: true, wantInRegen: transcriptPhrase},
		{name: "re-reads a Zoom chat export", content: "00:00:01 Carol: the zoom chat decision\n", postType: "custom_zoom_chat", regenAllowed: true, wantInRegen: "the zoom chat decision"},
		{name: "file policy now denies the transcript", content: longVTT()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := &transcriptFile{
				id:       model.NewId(),
				content:  tt.content,
				postType: tt.postType,
				allowed:  map[string]bool{initialSession: true, regenSession: tt.regenAllowed},
			}
			mm := mocks.NewMockClient(t)
			file.register(mm)
			lm := &summaryLLM{}
			s := newSummaryService(t, convStore, mm)
			conv, postID := startSummary(t, s, convStore, lm, file)
			readsBefore := file.reads

			stream, err := s.RegenerateSummary(auth.WithSessionID(t.Context(), regenSession), summaryBot(lm), summaryContext(), conv, postID)

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			if !tt.regenAllowed {
				require.ErrorIs(t, err, mmapi.ErrFileActionForbidden)
				require.Len(t, lm.streamed, 1, "no regenerate request may be sent")
				require.Len(t, turns, 2, "a denied regenerate must leave the conversation untouched")
				return
			}
			require.NoError(t, err)
			_, err = stream.ReadAll()
			require.NoError(t, err)

			require.Greater(t, file.reads, readsBefore, "regenerate must re-read the transcript file")
			require.Len(t, lm.streamed, 2)
			req := lm.streamed[1]
			require.Equal(t, llm.OperationMeetingSummary, req.Operation)
			require.Equal(t, llm.SubTypeRegenerate, req.OperationSubType)
			require.Len(t, req.Posts, 2)
			require.Contains(t, req.Posts[1].Message, tt.wantInRegen)

			require.Len(t, turns, 1, "the old response anchor must be scrubbed")
			require.Equal(t, "user", turns[0].Role)
		})
	}
}

func TestMeetingSummaryFollowUpReferencesTranscriptFile(t *testing.T) {
	convStore := newSummaryStore(t)
	shortVTT := "WEBVTT\n\n1\n00:00:00.000 --> 00:00:02.000\n<v Alice>" + transcriptPhrase + ".\n"

	tests := []struct {
		name           string
		content        string
		channelRevoked bool
		wantInlined    bool
		wantDescriptor bool
	}{
		{name: "long transcript is referenced for read_file", content: longVTT(), wantDescriptor: true},
		{name: "short transcript is inlined", content: shortVTT, wantInlined: true},
		{name: "short transcript is withheld once the meeting channel is unreadable", content: shortVTT, channelRevoked: true},
		{name: "long transcript is not described once the meeting channel is unreadable", content: longVTT(), channelRevoked: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := &transcriptFile{id: model.NewId(), content: tt.content, allowed: map[string]bool{initialSession: true, regenSession: true}}
			mm := mocks.NewMockClient(t)
			file.register(mm)
			lm := &summaryLLM{}
			s := newSummaryService(t, convStore, mm)
			conv, _ := startSummary(t, s, convStore, lm, file)
			file.channelRevoked = tt.channelRevoked

			followUpPostID := model.NewId()
			_, err := s.convService.GetOrCreateConversation(conversation.GetOrCreateParams{
				UserID:      summaryUserID,
				SessionID:   regenSession,
				BotID:       summaryBotID,
				RootPostID:  *conv.RootPostID,
				UserMessage: "Who writes the release notes?",
				UserPostID:  &followUpPostID,
			})
			require.NoError(t, err)

			req, err := s.convService.BuildCompletionRequest(conv, summaryContext(), conversation.BuildOptions{SessionID: regenSession})
			require.NoError(t, err)

			require.Len(t, req.Posts, 4)
			require.Equal(t, "old summary", req.Posts[2].Message)
			require.Equal(t, "Who writes the release notes?", req.Posts[3].Message)
			if tt.wantInlined {
				require.Contains(t, req.Posts[1].Message, transcriptPhrase)
			} else {
				require.NotContains(t, req.Posts[1].Message, transcriptPhrase)
			}
			if tt.wantDescriptor {
				require.Contains(t, req.Posts[1].Message, file.id)
				require.Contains(t, req.Posts[1].Message, "read_file")
			} else {
				require.NotContains(t, req.Posts[1].Message, "read_file")
			}
		})
	}
}
