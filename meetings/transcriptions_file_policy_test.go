// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"io"
	"strings"
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
// WithFilePolicy. The goroutine must consult HasPermissionToFileAction and
// must not call admin GetFileInfo/GetFile after a deny.
func TestHandleSummarizeTranscriptionDeniedByFilePolicy(t *testing.T) {
	const (
		userID    = "user-id-1234567890123456789012"
		botID     = "bot-id-12345678901234567890123"
		callsID   = "calls-id-123456789012345678901"
		sessionID = "session-id-12345678901234567890"
		dmID      = "dmdmdmdmdmdmdmdmdmdmdmdmdm"
	)
	fileID := model.NewId()

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
	mockAPI.On("UpdatePost", mock.Anything).Return(&model.Post{}, nil).Maybe()
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()
	mockAPI.On("LogError", mock.Anything).Maybe()
	defer mockAPI.AssertExpectations(t)

	mmClient := mocks.NewMockClient(t)
	checked := make(chan struct{})
	mmClient.On(
		"HasPermissionToFileAction",
		sessionID,
		fileID,
		model.AccessControlPolicyActionDownloadFileAttachment,
	).Return(false).Run(func(mock.Arguments) { close(checked) }).Once()

	adminReadCalled := false
	mmClient.EXPECT().GetFileInfo(fileID).
		Run(func(string) { adminReadCalled = true }).
		Return(&model.FileInfo{Id: fileID, PostId: "post-id"}, nil).
		Maybe()
	mmClient.EXPECT().GetFile(fileID).
		Run(func(string) { adminReadCalled = true }).
		Return(io.NopCloser(strings.NewReader("sensitive transcript")), nil).
		Maybe()

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
	case <-checked:
	case <-time.After(2 * time.Second):
		t.Fatal("HasPermissionToFileAction was not consulted on the async transcription read")
	}
	require.False(t, adminReadCalled, "admin GetFileInfo/GetFile must not run after the caller's file policy denies access")
}
