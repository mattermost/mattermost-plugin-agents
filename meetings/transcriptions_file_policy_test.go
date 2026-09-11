// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestHandleSummarizeTranscriptionDeniedByFilePolicy proves the async
// transcription read is session-gated. HandleSummarizeTranscription returns
// after posting "Sure…" — a return-error assertion would not catch a missing
// WithFilePolicy. The worker consults HasPermissionToFileAction, then
// UpdatePost on the denied-path error; waiting for that callback means the
// goroutine has finished, so an unexpected admin GetFile/GetFileInfo fails
// the mock instead of racing the test.
func TestHandleSummarizeTranscriptionDeniedByFilePolicy(t *testing.T) {
	const (
		userID    = "user-id-1234567890123456789012"
		botID     = "bot-id-12345678901234567890123"
		callsID   = "calls-id-123456789012345678901"
		sessionID = "session-id-12345678901234567890"
		dmID      = "dmdmdmdmdmdmdmdmdmdmdmdmdm"
	)
	fileID := model.NewId()

	workerDone := make(chan struct{})
	mockAPI := &plugintest.API{}
	siteURL := "http://localhost"
	cfg := model.Config{}
	cfg.SetDefaults()
	cfg.ServiceSettings.SiteURL = &siteURL
	mockAPI.On("GetConfig").Return(&cfg)
	mockAPI.On("GetUser", userID).Return(&model.User{Id: userID, Locale: "en"}, nil)
	mockAPI.On("GetUser", callsID).Return(&model.User{Id: callsID, Username: "calls", IsBot: true}, nil)
	mockAPI.On("GetDirectChannel", botID, userID).Return(&model.Channel{Id: dmID}, nil)
	mockAPI.On("CreatePost", mock.Anything).Return(&model.Post{Id: model.NewId(), ChannelId: dmID}, nil)
	mockAPI.On("UpdatePost", mock.Anything).Return(&model.Post{}, nil).Run(func(mock.Arguments) {
		select {
		case <-workerDone:
		default:
			close(workerDone)
		}
	}).Once()
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()
	mockAPI.On("LogError", mock.Anything).Maybe()
	defer mockAPI.AssertExpectations(t)

	mmClient := mocks.NewMockClient(t)
	mmClient.On(
		"HasPermissionToFileAction",
		sessionID,
		fileID,
		model.AccessControlPolicyActionDownloadFileAttachment,
	).Return(false).Once()

	s := &Service{
		pluginAPI:     pluginapi.NewClient(mockAPI, nil),
		mmClient:      mmClient,
		i18n:          i18n.Init(),
		conversations: &conversations.Conversations{},
	}

	post := &model.Post{
		Id:      "transcript-post-id",
		UserId:  callsID,
		FileIds: []string{fileID},
	}
	post.AddProp("captions", []any{map[string]any{"file_id": fileID}})

	bot := bots.NewBot(
		llm.BotConfig{Name: "matty", DisplayName: "Matty"},
		llm.ServiceConfig{},
		&model.Bot{UserId: botID, Username: "matty"},
		nil,
	)

	result, err := s.HandleSummarizeTranscription(userID, bot, post, &model.Channel{Id: "channel-id"}, sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, result["postid"])

	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("denied-path UpdatePost was not called; the transcription worker never finished")
	}
}
