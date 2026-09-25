// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// plantedWakeReason is distinctive wake-job content: if it ever shows up
// in a marshaled audit record, scheduler message content leaked into the trail.
const plantedWakeReason = "SENTINEL_WAKE_REASON_MUST_NOT_APPEAR_IN_AUDIT"

func TestWakeJobFromProps(t *testing.T) {
	tests := []struct {
		name    string
		props   any
		want    WakeJob
		wantErr bool
	}{
		{
			name:  "typed struct from same-process schedule",
			props: WakeJob{PostID: "post-id", Reason: "cursor agent"},
			want:  WakeJob{PostID: "post-id", Reason: "cursor agent"},
		},
		{
			name:  "generic JSON map after restart",
			props: map[string]any{"post_id": "post-id", "reason": "cursor agent"},
			want:  WakeJob{PostID: "post-id", Reason: "cursor agent"},
		},
		{
			name:    "missing post id",
			props:   map[string]any{"reason": "cursor agent"},
			wantErr: true,
		},
		{
			name:    "non-object props",
			props:   "bogus",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := wakeJobFromProps(tt.props)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, job)
		})
	}
}

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
	post := &model.Post{Id: "bot-post-id", UserId: "bot-id", ChannelId: "dm-channel", RootId: "root-post-id"}
	post.AddProp(streaming.ConversationIDProp, conv.ID)

	var wakePost *model.Post
	mmClient := mocks.NewMockClient(t)
	mmClient.On("GetPost", "bot-post-id").Return(post, nil).Once()
	mmClient.On("GetUser", "user-id").Return(user, nil).Once()
	mmClient.On("GetChannel", "dm-channel").Return(channel, nil).Once()
	mmClient.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) {
		wakePost = args.Get(0).(*model.Post)
		wakePost.Id = "wake-post-id"
	}).Return(nil).Once()
	mmClient.On("UpdatePost", mock.Anything).Return(nil).Once()
	mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("LogError", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("LogWarn", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetConfig").Return(&model.Config{}).Maybe()
	loggedAudit := captureLogAuditRec(mockAPI)

	c := &Conversations{
		mmClient:         mmClient,
		contextBuilder:   loadedStateBuilder(t),
		bots:             botsService,
		convService:      conversation.NewService(convStore, nil, nil, nil),
		streamingService: streamingService,
	}

	rec := plugin.MakeAuditRecord("waitForAsyncWork", model.AuditStatusFail)
	ctx := audit.WithRecord(context.Background(), rec)
	require.NoError(t, c.ResumeConversation(ctx, WakeJob{
		PostID: "bot-post-id",
		Reason: plantedWakeReason,
	}))
	streamingService.waitForStreaming()

	// The wake streams onto a new bot post in the same thread; the original
	// post's finished response is left untouched.
	require.NotNil(t, wakePost)
	require.Equal(t, "root-post-id", wakePost.RootId)
	require.Equal(t, "dm-channel", wakePost.ChannelId)
	require.Equal(t, "bot-id", wakePost.UserId)
	require.Equal(t, conv.ID, wakePost.GetProp(streaming.ConversationIDProp))

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	require.Equal(t, "user", turns[1].Role)
	require.Nil(t, turns[1].PostID)

	var blocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[1].Content, &blocks))
	require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
	require.Equal(t, mmtools.WakeUserMessage(plantedWakeReason), blocks[0].Text)

	require.Len(t, lm.requests, 1)
	require.Contains(t, lm.requests[0].Posts[len(lm.requests[0].Posts)-1].Message, plantedWakeReason)

	assertAuditDoesNotContain(t, loggedAudit, rec, plantedWakeReason)
}

func TestResumeConversationFailsPlaceholderWhenConversationPropUpdateFails(t *testing.T) {
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

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{loadedStateBot(&loadedStateLLM{})})

	user := &model.User{Id: "user-id", Username: "user"}
	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
	post := &model.Post{Id: "bot-post-id", UserId: "bot-id", ChannelId: "dm-channel", RootId: "root-post-id"}
	post.AddProp(streaming.ConversationIDProp, conv.ID)

	var wakePost *model.Post
	mmClient := mocks.NewMockClient(t)
	mmClient.On("GetPost", "bot-post-id").Return(post, nil).Once()
	mmClient.On("GetUser", "user-id").Return(user, nil).Once()
	mmClient.On("GetChannel", "dm-channel").Return(channel, nil).Once()
	mmClient.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) {
		wakePost = args.Get(0).(*model.Post)
		wakePost.Id = "wake-post-id"
	}).Return(nil).Once()
	mmClient.On("UpdatePost", mock.Anything).Return(model.NewAppError("UpdatePost", "store.system.update.app_error", nil, "", http.StatusInternalServerError)).Once()
	mmClient.On("UpdatePost", mock.Anything).Return(nil).Once()
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("LogError", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("LogWarn", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetConfig").Return(&model.Config{}).Maybe()
	loggedAudit := captureLogAuditRec(mockAPI)

	c := &Conversations{
		mmClient:       mmClient,
		contextBuilder: loadedStateBuilder(t),
		bots:           botsService,
		convService:    conversation.NewService(convStore, nil, nil, nil),
	}

	rec := plugin.MakeAuditRecord("waitForAsyncWork", model.AuditStatusFail)
	ctx := audit.WithRecord(context.Background(), rec)
	err = c.ResumeConversation(ctx, WakeJob{
		PostID: "bot-post-id",
		Reason: plantedWakeReason,
	})
	require.Error(t, err)
	require.NotNil(t, wakePost)
	require.Contains(t, wakePost.Message, "error occurred")
	assertAuditDoesNotContain(t, loggedAudit, rec, plantedWakeReason)

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 1)
}

func TestResumeConversationDrops(t *testing.T) {
	tests := []struct {
		name string
		post func(convID string) *model.Post
	}{
		{
			name: "post missing",
			post: func(string) *model.Post { return nil },
		},
		{
			name: "post has no conversation prop",
			post: func(string) *model.Post {
				return &model.Post{Id: "bot-post-id", UserId: "bot-id", ChannelId: "dm-channel"}
			},
		},
		{
			name: "conversation missing",
			post: func(string) *model.Post {
				post := &model.Post{Id: "bot-post-id", UserId: "bot-id", ChannelId: "dm-channel"}
				post.AddProp(streaming.ConversationIDProp, "missing-conv")
				return post
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()

			mockAPI := &plugintest.API{}
			pluginAPI := pluginapi.NewClient(mockAPI, nil)
			licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
			botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
			botsService.SetBotsForTesting([]*bots.Bot{loadedStateBot(&loadedStateLLM{})})

			mmClient := mocks.NewMockClient(t)
			if post := tt.post(conv.ID); post != nil {
				mmClient.On("GetPost", "bot-post-id").Return(post, nil).Once()
			} else {
				mmClient.On("GetPost", "bot-post-id").Return(nil, model.NewAppError("GetPost", "not found", nil, "", 404)).Once()
			}
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Return().Once()

			c := &Conversations{
				mmClient:    mmClient,
				bots:        botsService,
				convService: conversation.NewService(convStore, nil, nil, nil),
			}

			require.NoError(t, c.ResumeConversation(context.Background(), WakeJob{
				PostID: "bot-post-id",
				Reason: "cursor cloud agent",
			}))

			turns, err := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, err)
			require.Empty(t, turns)
		})
	}
}

// captureLogAuditRec is the conversations-package analog of
// api.TestEnvironment.CaptureAuditRecords: this package has no HTTP
// TestEnvironment, so we listen on the plugin API mock directly.
func captureLogAuditRec(mockAPI *plugintest.API) *[]*model.AuditRecord {
	records := &[]*model.AuditRecord{}
	mockAPI.On("LogAuditRec", mock.Anything).Run(func(args mock.Arguments) {
		if rec, ok := args.Get(0).(*model.AuditRecord); ok {
			*records = append(*records, rec)
		}
	}).Maybe()
	return records
}

func assertAuditDoesNotContain(t *testing.T, logged *[]*model.AuditRecord, rec *model.AuditRecord, sentinel string) {
	t.Helper()
	toCheck := make([]*model.AuditRecord, 0, 1+len(*logged))
	if rec != nil {
		toCheck = append(toCheck, rec)
	}
	toCheck = append(toCheck, *logged...)
	for _, r := range toCheck {
		raw, err := json.Marshal(r)
		require.NoError(t, err)
		require.NotContains(t, string(raw), sentinel)
	}
	require.Empty(t, *logged)
}
