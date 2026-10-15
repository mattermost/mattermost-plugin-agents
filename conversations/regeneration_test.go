// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
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

// TestHandleRegenerateSingleShotConversations covers the analysis and meeting
// regenerate paths: they must reuse the post's existing conversation, rebuild
// the request from its stored system prompt and user turn, and scrub the old
// response anchor instead of creating a new conversation or re-reading sources.
func TestHandleRegenerateSingleShotConversations(t *testing.T) {
	const (
		userID      = "user-id"
		botID       = "bot-id"
		regenPostID = "regen-post-id"
		threadID    = "thread-root-id"
		sessionID   = "session-id"
		systemText  = "stored system prompt"
		userText    = "stored formatted source"
	)

	tests := []struct {
		name        string
		operation   string
		convOwner   string
		postProps   map[string]any
		setupMM     func(mm *mocks.MockClient)
		expectErr   string
		expectRegen bool
	}{
		{
			name:        "meeting summary reuses stored transcription turn",
			operation:   llm.OperationMeetingSummary,
			convOwner:   userID,
			postProps:   map[string]any{meetingTranscriptPostIDProp: "transcript-post"},
			expectRegen: true,
			setupMM: func(mm *mocks.MockClient) {
				mm.On("GetPost", "transcript-post").Return(transcriptPostWithCaptions("captions-file"), nil)
				mm.On("HasPermissionToFileAction", sessionID, "captions-file", model.AccessControlPolicyActionDownloadFileAttachment).Return(true)
				mm.On("GetFileInfo", "captions-file").Return(&model.FileInfo{Id: "captions-file", ChannelId: "meeting-channel"}, nil)
				mm.On("HasPermissionToChannel", userID, "meeting-channel", model.PermissionReadChannel).Return(true)
			},
		},
		{
			name:      "meeting summary from captions denied by file policy",
			operation: llm.OperationMeetingSummary,
			convOwner: userID,
			postProps: map[string]any{meetingTranscriptPostIDProp: "transcript-post"},
			expectErr: "meeting source file",
			setupMM: func(mm *mocks.MockClient) {
				mm.On("GetPost", "transcript-post").Return(transcriptPostWithCaptions("captions-file"), nil)
				mm.On("HasPermissionToFileAction", sessionID, "captions-file", model.AccessControlPolicyActionDownloadFileAttachment).Return(false)
			},
		},
		{
			name:      "meeting summary from recording denied by file policy",
			operation: llm.OperationMeetingSummary,
			convOwner: userID,
			postProps: map[string]any{meetingRecordingFileIDProp: "recording-file"},
			expectErr: "meeting source file",
			setupMM: func(mm *mocks.MockClient) {
				mm.On("HasPermissionToFileAction", sessionID, "recording-file", model.AccessControlPolicyActionDownloadFileAttachment).Return(false)
			},
		},
		{
			name:        "thread analysis reuses conversation after permission check",
			operation:   llm.OperationThreadAnalysis,
			convOwner:   userID,
			postProps:   map[string]any{ThreadIDProp: threadID, AnalysisTypeProp: "summarize_thread"},
			expectRegen: true,
			setupMM: func(mm *mocks.MockClient) {
				mm.On("GetPost", threadID).Return(&model.Post{Id: threadID, ChannelId: "thread-channel"}, nil)
				mm.On("HasPermissionToChannel", userID, "thread-channel", model.PermissionReadChannel).Return(true)
			},
		},
		{
			name:      "thread analysis without channel access fails",
			operation: llm.OperationThreadAnalysis,
			convOwner: userID,
			postProps: map[string]any{ThreadIDProp: threadID, AnalysisTypeProp: "summarize_thread"},
			expectErr: "permission",
			setupMM: func(mm *mocks.MockClient) {
				mm.On("GetPost", threadID).Return(&model.Post{Id: threadID, ChannelId: "thread-channel"}, nil)
				mm.On("HasPermissionToChannel", userID, "thread-channel", model.PermissionReadChannel).Return(false)
			},
		},
		{
			name:        "channel interval reuses conversation",
			operation:   llm.OperationChannelInterval,
			convOwner:   userID,
			expectRegen: true,
		},
		{
			name:      "non-owner cannot regenerate",
			operation: llm.OperationMeetingSummary,
			convOwner: "someone-else",
			expectErr: "only the original poster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convStore := newLoadedStateFlowStore()
			conv := &store.Conversation{
				ID:           "conv-id",
				UserID:       tt.convOwner,
				BotID:        botID,
				SystemPrompt: systemText,
				Operation:    tt.operation,
			}
			require.NoError(t, convStore.CreateConversation(conv))

			userContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: userText}})
			require.NoError(t, err)
			oldContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "old summary"}})
			require.NoError(t, err)
			postID := regenPostID
			require.NoError(t, convStore.CreateTurn(&store.Turn{ID: "user-turn", ConversationID: conv.ID, Role: "user", Content: userContent, Sequence: 1}))
			require.NoError(t, convStore.CreateTurn(&store.Turn{ID: "old-anchor", ConversationID: conv.ID, PostID: &postID, Role: "assistant", Content: oldContent, Sequence: 2}))

			mm := mocks.NewMockClient(t)
			mm.On("GetUser", userID).Return(&model.User{Id: userID, Locale: "en"}, nil).Maybe()
			locale := "en"
			mm.On("GetConfig").Return(&model.Config{LocalizationSettings: model.LocalizationSettings{DefaultServerLocale: &locale}}).Maybe()
			if tt.setupMM != nil {
				tt.setupMM(mm)
			}

			lm := &loadedStateLLM{}
			mockAPI := &plugintest.API{}
			pluginAPI := pluginapi.NewClient(mockAPI, nil)
			botsService := bots.New(mockAPI, pluginAPI, enterprise.NewLicenseChecker(pluginAPI), nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
			botsService.SetBotsForTesting([]*bots.Bot{loadedStateBot(lm)})

			streamSvc := &regenRecordingStreaming{}
			c := &Conversations{
				mmClient:         mm,
				bots:             botsService,
				contextBuilder:   loadedStateBuilder(t),
				convService:      conversation.NewService(convStore, nil, nil, nil),
				streamingService: streamSvc,
				licenseChecker:   enterprisetest.CheckerAt(enterprise.LevelEnterprise),
				configProvider:   &channelFollowUpTestConfig{},
			}

			post := &model.Post{Id: regenPostID, UserId: botID, Message: "old summary"}
			post.AddProp(streaming.ConversationIDProp, conv.ID)
			for k, v := range tt.postProps {
				post.AddProp(k, v)
			}
			channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: botID + "__" + userID}

			err = c.HandleRegenerate(auth.WithSessionID(t.Context(), sessionID), userID, post, channel)

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			if tt.expectErr != "" {
				require.ErrorContains(t, err, tt.expectErr)
				require.Empty(t, lm.requests)
				require.Len(t, turns, 2, "a rejected regenerate must leave the conversation untouched")
				return
			}
			require.NoError(t, err)

			require.Len(t, lm.requests, 1)
			req := lm.requests[0]
			require.Equal(t, tt.operation, req.Operation)
			require.Len(t, req.Posts, 2)
			require.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
			require.Equal(t, systemText, req.Posts[0].Message)
			require.Equal(t, llm.PostRoleUser, req.Posts[1].Role)
			require.Equal(t, userText, req.Posts[1].Message)

			require.Len(t, turns, 1, "the old response anchor must be scrubbed")
			require.Equal(t, "user-turn", turns[0].ID)

			convs := convStore.conversations
			require.Len(t, convs, 1, "regenerate must not create a new conversation")

			require.Len(t, streamSvc.streamed, 1)
			require.Equal(t, conv.ID, streamSvc.streamed[0].GetProp(streaming.ConversationIDProp))
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

func TestIsInitialResponse(t *testing.T) {
	strPtr := func(s string) *string { return &s }
	turns := []store.Turn{
		{ID: "u1", Role: "user", Sequence: 1},
		{ID: "a1", Role: "assistant", PostID: strPtr("summary-post"), Sequence: 2},
		{ID: "u2", Role: "user", Sequence: 3},
		{ID: "a2", Role: "assistant", PostID: strPtr("follow-up-post"), Sequence: 4},
	}

	tests := []struct {
		name   string
		postID string
		want   bool
	}{
		{name: "operation's first response", postID: "summary-post", want: true},
		{name: "follow-up reply in the same conversation", postID: "follow-up-post", want: false},
		{name: "follow-up without a persisted turn", postID: "unknown-post", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isInitialResponse(turns, tt.postID))
		})
	}

	t.Run("operation post without a persisted turn", func(t *testing.T) {
		require.True(t, isInitialResponse(turns[:1], "summary-post"))
	})
}

func transcriptPostWithCaptions(fileID string) *model.Post {
	post := &model.Post{Id: "transcript-post"}
	post.AddProp("captions", []any{map[string]any{"file_id": fileID}})
	return post
}
