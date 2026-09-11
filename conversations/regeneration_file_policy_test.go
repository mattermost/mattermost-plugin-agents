// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/subtitles"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

// regenNoopStreaming satisfies streaming.Service without WaitGroup bookkeeping.
type regenNoopStreaming struct{}

func (regenNoopStreaming) StreamToNewPost(context.Context, string, string, *llm.TextStreamResult, *model.Post, string) error {
	return nil
}
func (regenNoopStreaming) StreamToNewDM(context.Context, string, *llm.TextStreamResult, string, *model.Post, string) error {
	return nil
}
func (regenNoopStreaming) StreamToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}
func (regenNoopStreaming) StreamContinuationToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}
func (regenNoopStreaming) StopStreaming(string) {}
func (regenNoopStreaming) GetStreamingContext(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}
func (regenNoopStreaming) FinishStreaming(string) {}

type regenMeetingsStub struct {
	captionsFileID string
	captionsErr    error
}

func (s regenMeetingsStub) GetCaptionsFileIDFromProps(*model.Post) (string, error) {
	return s.captionsFileID, s.captionsErr
}

func (s regenMeetingsStub) SummarizeTranscription(context.Context, *bots.Bot, *subtitles.Subtitles, *llm.Context) (*llm.TextStreamResult, error) {
	return nil, errors.New("summarize not used")
}

// TestHandleRegenerateDeniedByFilePolicy proves a session-scoped file policy
// denial fails closed before admin GetFile/GetFileInfo on both meeting-regen
// branches. Removing WithFilePolicy would let those admin reads run.
func TestHandleRegenerateDeniedByFilePolicy(t *testing.T) {
	const (
		userID    = "user-id-1234567890123456789012"
		botID     = "bot-id-12345678901234567890123"
		sessionID = "session-id-12345678901234567890"
	)

	tests := []struct {
		name      string
		setupPost func(post *model.Post, fileID string)
		setup     func(t *testing.T, mm *mocks.MockClient, c *Conversations, fileID string)
		wantErr   string
	}{
		{
			name: "recording file policy denial does not read file metadata",
			setupPost: func(post *model.Post, fileID string) {
				post.AddProp(ReferencedRecordingFileID, fileID)
			},
			wantErr: "not permitted to read recording file on regen",
		},
		{
			name: "transcription file policy denial does not read file contents",
			setupPost: func(post *model.Post, _ string) {
				post.AddProp(ReferencedTranscriptPostID, "transcript-post-id")
			},
			setup: func(t *testing.T, mm *mocks.MockClient, c *Conversations, fileID string) {
				t.Helper()
				mm.On("GetPost", "transcript-post-id").Return(&model.Post{Id: "transcript-post-id"}, nil)
				c.meetingsService = regenMeetingsStub{captionsFileID: fileID}
			},
			wantErr: "not permitted to read transcription file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fileID := model.NewId()
			mmClient := mocks.NewMockClient(t)
			mmClient.On("GetUser", userID).Return(&model.User{Id: userID}, nil)
			mmClient.On(
				"HasPermissionToFileAction",
				sessionID,
				fileID,
				model.AccessControlPolicyActionDownloadFileAttachment,
			).Return(false)
			adminReadCalled := false
			mmClient.EXPECT().GetFileInfo(fileID).
				Run(func(string) { adminReadCalled = true }).
				Return(&model.FileInfo{Id: fileID}, nil).
				Maybe()
			mmClient.EXPECT().GetFile(fileID).
				Run(func(string) { adminReadCalled = true }).
				Return(io.NopCloser(strings.NewReader("sensitive meeting contents")), nil).
				Maybe()

			mockAPI := &plugintest.API{}
			pluginAPI := pluginapi.NewClient(mockAPI, nil)
			licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
			botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, accesscontrol.New(accesscontrol.PassthroughClient{}, nil, accesscontrol.NoMCPServerIDs, nil), &http.Client{}, nil)
			botsService.SetBotsForTesting([]*bots.Bot{
				bots.NewBot(
					llm.BotConfig{Name: "matty", DisplayName: "Matty", UserAccessLevel: llm.UserAccessLevelAll},
					llm.ServiceConfig{},
					&model.Bot{UserId: botID, Username: "matty", DisplayName: "Matty"},
					nil,
				),
			})

			c := &Conversations{
				mmClient:         mmClient,
				bots:             botsService,
				convService:      &conversation.Service{},
				streamingService: regenNoopStreaming{},
			}
			if tt.setup != nil {
				tt.setup(t, mmClient, c, fileID)
			}

			post := &model.Post{Id: "regen-post-id", UserId: botID}
			post.AddProp(streaming.LLMRequesterUserIDProp, userID)
			tt.setupPost(post, fileID)

			err := c.HandleRegenerate(auth.WithSessionID(t.Context(), sessionID), userID, post, &model.Channel{Id: "channel-id"})
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
			require.False(t, adminReadCalled, "admin GetFileInfo/GetFile must not run after the caller's file policy denies access")
		})
	}
}
