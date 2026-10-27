// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/meetings"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// regenRecordingStreaming records the posts streamed into.
type regenRecordingStreaming struct {
	loadedStateStreamingService
	streamed []*model.Post
}

func (s *regenRecordingStreaming) StreamToPost(_ context.Context, result *llm.TextStreamResult, post *model.Post, _ string, _ string) {
	_, _ = result.ReadAll()
	s.streamed = append(s.streamed, post)
}

func (s *regenRecordingStreaming) GetStreamingContext(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}

// fakeMeetingRegenerator records meeting summary regenerations.
type fakeMeetingRegenerator struct {
	postIDs []string
	err     error
}

func (f *fakeMeetingRegenerator) RegenerateSummary(_ context.Context, _ *bots.Bot, _ *llm.Context, _ *store.Conversation, postID string) (*llm.TextStreamResult, error) {
	f.postIDs = append(f.postIDs, postID)
	if f.err != nil {
		return nil, f.err
	}
	return llm.NewStreamFromString("regenerated summary"), nil
}

// regenChannelTool is an embedded channel tool that records the channel_id it
// resolves with.
func regenChannelTool(name string, captured *string) llm.Tool {
	return llm.Tool{
		Name:         name,
		Description:  "channel tool",
		ServerOrigin: mcp.EmbeddedClientKey,
		Schema:       map[string]any{"type": "object"},
		Resolver: func(_ context.Context, _ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
			var parsed struct {
				ChannelID string `json:"channel_id"`
			}
			if err := args(&parsed); err != nil {
				return "", err
			}
			*captured = parsed.ChannelID
			return "ok", nil
		},
	}
}

type regenPath string

const (
	pathSingleShot   regenPath = "single shot"
	pathConversation regenPath = "conversation"
	pathChannel      regenPath = "channel summary"
	pathMeeting      regenPath = "meeting summary"
)

// TestHandleRegenerate drives HandleRegenerate and checks which path it takes
// for each kind of post, and that it fails closed before any LLM call when a
// check does not pass.
func TestHandleRegenerate(t *testing.T) {
	const (
		userID          = testRequesterID
		botID           = "bot-id"
		otherBotID      = "other-bot-id"
		regenPostID     = "regen-post-id"
		threadID        = "thread-root-id"
		threadChannel   = "thread-channel"
		analyzedChannel = "analyzed-channel"
		sessionID       = "session-id"
		systemText      = "stored system prompt"
		userText        = "stored user turn"
		keptFileID      = "transcript-file"
	)

	allowThread := func(mm *mocks.MockClient) {
		mm.On("GetPost", threadID).Return(&model.Post{Id: threadID, ChannelId: threadChannel}, nil)
		mm.On("HasPermissionToChannel", userID, threadChannel, model.PermissionReadChannel).Return(true)
	}
	analyzed := func(readable bool) func(mm *mocks.MockClient) {
		return func(mm *mocks.MockClient) {
			mm.On("HasPermissionToChannel", userID, analyzedChannel, model.PermissionReadChannel).Return(readable)
			mm.On("GetChannel", analyzedChannel).Return(&model.Channel{Id: analyzedChannel, Type: model.ChannelTypeOpen, TeamId: "team-id"}, nil).Maybe()
		}
	}
	threadProps := map[string]any{ThreadIDProp: threadID, AnalysisTypeProp: "summarize_thread"}
	followUpProps := map[string]any{streaming.RespondingToProp: "user-post-id"}

	tests := []struct {
		name         string
		operation    string
		rootIsPost   bool
		postAuthor   string
		postProps    map[string]any
		licenseLevel enterprise.Level
		botConfig    func(*llm.BotConfig)
		meetingErr   error
		setupMM      func(mm *mocks.MockClient)
		wantPath     regenPath
		wantErr      string
		wantErrIs    error
		wantLicense  enterprise.Capability
	}{
		{
			name:       "thread analysis post regenerates from its stored turn",
			operation:  llm.OperationThreadAnalysis,
			rootIsPost: true,
			postProps:  threadProps,
			setupMM:    allowThread,
			wantPath:   pathSingleShot,
		},
		{
			name:      "thread analysis follow-up regenerates as a conversation",
			operation: llm.OperationThreadAnalysis,
			postProps: followUpProps,
			wantPath:  pathConversation,
		},
		{
			name:       "thread analysis post without its thread fails",
			operation:  llm.OperationThreadAnalysis,
			rootIsPost: true,
			postProps:  map[string]any{AnalysisTypeProp: "summarize_thread"},
			wantErrIs:  ErrAnalysisSourceUnavailable,
		},
		{
			name:       "thread analysis without thread channel access fails",
			operation:  llm.OperationThreadAnalysis,
			rootIsPost: true,
			postProps:  threadProps,
			setupMM: func(mm *mocks.MockClient) {
				mm.On("GetPost", threadID).Return(&model.Post{Id: threadID, ChannelId: threadChannel}, nil)
				mm.On("HasPermissionToChannel", userID, threadChannel, model.PermissionReadChannel).Return(false)
			},
			wantErrIs: ErrAnalysisSourceUnavailable,
		},
		{
			name:       "channel summary rebinds tools to the analyzed channel",
			operation:  llm.OperationChannelSummary,
			rootIsPost: true,
			postProps:  map[string]any{AnalyzedChannelIDProp: analyzedChannel},
			setupMM:    analyzed(true),
			wantPath:   pathChannel,
		},
		{
			name:       "channel summary fails when the bot is blocked in the analyzed channel",
			operation:  llm.OperationChannelSummary,
			rootIsPost: true,
			postProps:  map[string]any{AnalyzedChannelIDProp: analyzedChannel},
			botConfig: func(cfg *llm.BotConfig) {
				cfg.ChannelAccessLevel = llm.ChannelAccessLevelBlock
				cfg.ChannelIDs = []string{analyzedChannel}
			},
			setupMM:   analyzed(true),
			wantErrIs: bots.ErrUsageRestriction,
		},
		{
			name:       "channel summary fails without analyzed channel access",
			operation:  llm.OperationChannelSummary,
			rootIsPost: true,
			postProps:  map[string]any{AnalyzedChannelIDProp: analyzedChannel},
			setupMM:    analyzed(false),
			wantErrIs:  ErrAnalysisSourceUnavailable,
		},
		{
			name:      "channel summary follow-up regenerates as a conversation",
			operation: llm.OperationChannelSummary,
			postProps: followUpProps,
			wantPath:  pathConversation,
		},
		{
			name:      "meeting summary from captions regenerates through meetings",
			operation: llm.OperationMeetingSummary,
			postProps: map[string]any{meetings.ReferencedTranscriptPostID: "transcript-post"},
			wantPath:  pathMeeting,
		},
		{
			name:      "meeting summary from a recording regenerates through meetings",
			operation: llm.OperationMeetingSummary,
			postProps: map[string]any{meetings.ReferencedRecordingFileID: "recording-file"},
			wantPath:  pathMeeting,
		},
		{
			name:       "meeting summary denied by the transcript file policy",
			operation:  llm.OperationMeetingSummary,
			postProps:  map[string]any{meetings.ReferencedRecordingFileID: "recording-file"},
			meetingErr: mmapi.ErrFileActionForbidden,
			wantErrIs:  mmapi.ErrFileActionForbidden,
		},
		{
			name:      "meeting follow-up regenerates as a conversation",
			operation: llm.OperationMeetingSummary,
			postProps: followUpProps,
			wantPath:  pathConversation,
		},
		{
			name:      "plain conversation post",
			operation: llm.OperationConversation,
			postProps: followUpProps,
			wantPath:  pathConversation,
		},
		{
			name:       "post by another bot than the conversation's is rejected",
			operation:  llm.OperationThreadAnalysis,
			rootIsPost: true,
			postAuthor: otherBotID,
			postProps:  threadProps,
			wantErr:    "not authored by the conversation's bot",
		},
		{
			name:      "interval summary follow-up regenerates as a conversation",
			operation: llm.OperationChannelInterval,
			postProps: followUpProps,
			wantPath:  pathConversation,
		},
		{
			name:       "interval summary post tagged no-regen is rejected",
			operation:  llm.OperationChannelInterval,
			rootIsPost: true,
			postProps:  map[string]any{streaming.NoRegen: "true"},
			wantErrIs:  ErrNotRegenerable,
		},
		{
			name:       "interval summary post without its no-regen tag still fails closed",
			operation:  llm.OperationChannelInterval,
			rootIsPost: true,
			wantErr:    "cannot be regenerated",
		},
		{
			name:      "unknown operation fails closed",
			operation: "something_new",
			postProps: followUpProps,
			wantErr:   "not supported",
		},
		{
			name:         "thread analysis below Professional",
			operation:    llm.OperationThreadAnalysis,
			rootIsPost:   true,
			postProps:    threadProps,
			licenseLevel: enterprise.LevelUnlicensed,
			wantLicense:  enterprise.CapThreadSummarization,
		},
		{
			name:         "channel summary below Professional",
			operation:    llm.OperationChannelSummary,
			rootIsPost:   true,
			postProps:    map[string]any{AnalyzedChannelIDProp: analyzedChannel},
			licenseLevel: enterprise.LevelUnlicensed,
			wantLicense:  enterprise.CapChannelSummarization,
		},
		{
			name:         "meeting summary below Enterprise",
			operation:    llm.OperationMeetingSummary,
			postProps:    map[string]any{meetings.ReferencedTranscriptPostID: "transcript-post"},
			licenseLevel: enterprise.LevelProfessional,
			wantLicense:  enterprise.CapMeetings,
		},
		{
			name:         "meeting follow-up below Enterprise",
			operation:    llm.OperationMeetingSummary,
			postProps:    followUpProps,
			licenseLevel: enterprise.LevelProfessional,
			wantLicense:  enterprise.CapMeetings,
		},
		{
			name:         "interval summary follow-up below Professional",
			operation:    llm.OperationChannelInterval,
			postProps:    followUpProps,
			licenseLevel: enterprise.LevelUnlicensed,
			wantLicense:  enterprise.CapChannelSummarization,
		},
		{
			name:         "thread analysis follow-up below Professional",
			operation:    llm.OperationThreadAnalysis,
			postProps:    followUpProps,
			licenseLevel: enterprise.LevelUnlicensed,
			wantLicense:  enterprise.CapThreadSummarization,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convStore := newLoadedStateFlowStore()
			conv := &store.Conversation{
				ID:           "conv-id",
				UserID:       userID,
				BotID:        botID,
				SystemPrompt: systemText,
				Operation:    tt.operation,
			}
			rootPostID := "analysis-root-post"
			if tt.rootIsPost {
				rootPostID = regenPostID
			}
			conv.RootPostID = &rootPostID
			require.NoError(t, convStore.CreateConversation(conv))

			userContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: userText}})
			require.NoError(t, err)
			oldContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "old response"}})
			require.NoError(t, err)
			postID := regenPostID
			require.NoError(t, convStore.CreateTurn(&store.Turn{ID: "user-turn", ConversationID: conv.ID, Role: "user", Content: userContent, Sequence: 1}))
			require.NoError(t, convStore.CreateTurn(&store.Turn{ID: "old-anchor", ConversationID: conv.ID, PostID: &postID, Role: "assistant", Content: oldContent, Sequence: 2}))

			mm := mocks.NewMockClient(t)
			mm.On("GetUser", userID).Return(&model.User{Id: userID, Locale: "en"}, nil).Maybe()
			locale := "en"
			mm.On("GetConfig").Return(&model.Config{LocalizationSettings: model.LocalizationSettings{DefaultServerLocale: &locale}}).Maybe()
			mm.On("KVGet", mock.Anything, mock.Anything).Return(nil).Maybe()
			mm.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()
			mm.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Maybe()
			if tt.setupMM != nil {
				tt.setupMM(mm)
			}
			analysisRoot := &model.Post{Id: "analysis-root-post", UserId: botID}
			switch tt.operation {
			case llm.OperationThreadAnalysis:
				analysisRoot.AddProp(ThreadIDProp, threadID)
			case llm.OperationChannelSummary, llm.OperationChannelInterval:
				analysisRoot.AddProp(AnalyzedChannelIDProp, analyzedChannel)
			}
			mm.On("GetPost", analysisRoot.Id).Return(analysisRoot, nil).Maybe()
			mm.On("GetPost", threadID).Return(&model.Post{Id: threadID, ChannelId: threadChannel}, nil).Maybe()
			mm.On("HasPermissionToChannel", userID, mock.Anything, model.PermissionReadChannel).Return(true).Maybe()

			lm := &loadedStateLLM{}
			botCfg := loadedStateBot(lm).GetConfig()
			if tt.botConfig != nil {
				tt.botConfig(&botCfg)
			}
			bot := bots.NewBot(botCfg, llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI}, &model.Bot{UserId: botID, Username: "matty"}, lm)
			otherBot := bots.NewBot(llm.BotConfig{ID: otherBotID, Name: "other"}, llm.ServiceConfig{}, &model.Bot{UserId: otherBotID, Username: "other"}, lm)

			mockAPI := &plugintest.API{}
			pluginAPI := pluginapi.NewClient(mockAPI, nil)
			botsService := bots.New(mockAPI, pluginAPI, enterprise.NewLicenseChecker(pluginAPI), nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
			botsService.SetBotsForTesting([]*bots.Bot{bot, otherBot})

			var boundChannelID string
			builder := loadedStateBuilder(t,
				regenChannelTool("read_channel", &boundChannelID),
				regenChannelTool("get_channel_info", new(string)),
			)

			licenseLevel := enterprise.LevelEnterprise
			if tt.wantLicense != "" {
				licenseLevel = tt.licenseLevel
			}
			meetingRegen := &fakeMeetingRegenerator{err: tt.meetingErr}
			streamSvc := &regenRecordingStreaming{}
			c := &Conversations{
				mmClient:         mm,
				bots:             botsService,
				contextBuilder:   builder,
				convService:      conversation.NewService(convStore, nil, nil, nil),
				streamingService: streamSvc,
				licenseChecker:   enterprisetest.CheckerAt(licenseLevel),
				configProvider:   &channelFollowUpTestConfig{},
				meetingSummaries: meetingRegen,
			}

			author := botID
			if tt.postAuthor != "" {
				author = tt.postAuthor
			}
			post := &model.Post{Id: regenPostID, UserId: author, Message: "old response", FileIds: []string{keptFileID}}
			post.AddProp(streaming.ConversationIDProp, conv.ID)
			for k, v := range tt.postProps {
				post.AddProp(k, v)
			}
			channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: botID + "__" + userID}

			err = c.HandleRegenerate(auth.WithSessionID(t.Context(), sessionID), userID, post, channel)

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			if tt.wantPath == "" {
				require.Error(t, err)
				switch {
				case tt.wantLicense != "":
					var licErr *enterprise.LicenseError
					require.True(t, errors.As(err, &licErr), "want license error, got %v", err)
					require.Equal(t, tt.wantLicense, licErr.Capability)
				case tt.wantErrIs != nil:
					require.ErrorIs(t, err, tt.wantErrIs)
				default:
					require.ErrorContains(t, err, tt.wantErr)
				}
				require.Empty(t, lm.requests, "no LLM request may be sent")
				require.Empty(t, streamSvc.streamed)
				require.Len(t, turns, 2, "a rejected regenerate must leave the conversation untouched")
				return
			}
			require.NoError(t, err)

			require.Len(t, streamSvc.streamed, 1)
			require.Same(t, post, streamSvc.streamed[0])
			require.Empty(t, post.Message, "the old response is cleared before streaming")
			require.Equal(t, conv.ID, post.GetProp(streaming.ConversationIDProp))

			if tt.wantPath == pathMeeting {
				require.Equal(t, []string{regenPostID}, meetingRegen.postIDs)
				require.Empty(t, lm.requests)
				require.Equal(t, model.StringArray{keptFileID}, post.FileIds, "meeting regenerate must keep the transcript attached")
				return
			}
			require.Empty(t, meetingRegen.postIDs)
			require.Len(t, turns, 1, "the old response anchor must be scrubbed")
			require.Equal(t, "user-turn", turns[0].ID)
			require.Len(t, lm.requests, 1)
			req := lm.requests[0]
			require.Equal(t, llm.SubTypeRegenerate, req.OperationSubType)
			require.Equal(t, systemText, req.Posts[0].Message)
			require.Equal(t, userText, req.Posts[1].Message)

			switch tt.wantPath {
			case pathSingleShot:
				require.Len(t, req.Posts, 2)
				require.Empty(t, req.Context.Tools.GetTools(), "single-shot regenerate runs without tools")
				require.Equal(t, model.StringArray{keptFileID}, post.FileIds)
			case pathChannel:
				readChannel := req.Context.Tools.GetTool("read_channel")
				require.NotNil(t, readChannel)
				_, err := readChannel.Resolver(t.Context(), req.Context, func(args any) error {
					return json.Unmarshal([]byte(`{"channel_id":"attacker-channel"}`), args)
				})
				require.NoError(t, err)
				require.Equal(t, analyzedChannel, boundChannelID, "channel tools must stay bound to the analyzed channel")
			case pathConversation:
				require.NotNil(t, post.FileIds)
				require.Empty(t, post.FileIds, "conversation regenerate detaches the previous generation's files")
			}
		})
	}
}

func TestHandleRegenerateRequiresConversation(t *testing.T) {
	const (
		userID = "user-id"
		botID  = "bot-id"
	)
	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	botsService := bots.New(mockAPI, pluginAPI, enterprise.NewLicenseChecker(pluginAPI), nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{loadedStateBot(&loadedStateLLM{})})

	c := &Conversations{
		bots:        botsService,
		convService: conversation.NewService(newLoadedStateFlowStore(), nil, nil, nil),
	}

	// Posts created before meeting flows were migrated carry no
	// conversation_id; regenerating them must fail cleanly.
	post := &model.Post{Id: "legacy-post", UserId: botID}
	post.AddProp("llm_requester_user_id", userID)

	err := c.HandleRegenerate(t.Context(), userID, post, &model.Channel{Id: "dm-channel"})
	require.ErrorContains(t, err, "missing conversation_id")
}
