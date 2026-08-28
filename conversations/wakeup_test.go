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
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestResumeConversationWritesWakeTurnAndFollowsUp(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	userContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "please watch the job"}})
	require.NoError(t, err)
	require.NoError(t, convStore.CreateTurn(&store.Turn{
		ID:             "user-1",
		ConversationID: conv.ID,
		Role:           "user",
		Content:        userContent,
		Sequence:       1,
	}))

	lm := &loadedStateLLM{}
	bot := loadedStateBot(lm)
	streamingService := &loadedStateStreamingService{}

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{bot})

	user := &model.User{Id: "user-id", Username: "user"}
	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
	post := &model.Post{Id: "bot-post-id", UserId: "bot-id"}

	mmClient := mocks.NewMockClient(t)
	mmClient.On("GetUser", "user-id").Return(user, nil).Once()
	mmClient.On("GetChannel", "dm-channel").Return(channel, nil).Once()
	mmClient.On("GetPost", "bot-post-id").Return(post, nil).Once()
	mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("LogError", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("LogWarn", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetConfig").Return(&model.Config{}).Maybe()

	c := &Conversations{
		mmClient:         mmClient,
		contextBuilder:   loadedStateBuilder(t),
		bots:             botsService,
		convService:      conversation.NewService(convStore, nil, nil, nil),
		streamingService: streamingService,
	}

	require.NoError(t, c.ResumeConversation(context.Background(), mmtools.WakeRecord{
		ConversationID: conv.ID,
		BotID:          "bot-id",
		UserID:         "user-id",
		ChannelID:      "dm-channel",
		PostID:         "bot-post-id",
		IsDM:           true,
		Reason:         "cursor cloud agent",
	}))
	streamingService.waitForStreaming()

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	require.Equal(t, "user", turns[1].Role)
	require.Nil(t, turns[1].PostID)

	var blocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[1].Content, &blocks))
	require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
	require.Equal(t, mmtools.WakeUserMessage("cursor cloud agent"), blocks[0].Text)

	require.Len(t, lm.requests, 1)
	require.Contains(t, lm.requests[0].Posts[len(lm.requests[0].Posts)-1].Message, "cursor cloud agent")
}

func TestResumeConversationDropsWhenConversationMissing(t *testing.T) {
	convStore := newLoadedStateFlowStore()

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{loadedStateBot(&loadedStateLLM{})})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Return().Once()

	c := &Conversations{
		mmClient:    mmClient,
		bots:        botsService,
		convService: conversation.NewService(convStore, nil, nil, nil),
	}

	require.NoError(t, c.ResumeConversation(context.Background(), mmtools.WakeRecord{
		ConversationID: "missing",
		BotID:          "bot-id",
		UserID:         "user-id",
		ChannelID:      "dm-channel",
		PostID:         "bot-post-id",
	}))
}
