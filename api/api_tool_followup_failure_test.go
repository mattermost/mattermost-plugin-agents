// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type runEndRecorder struct {
	mu   sync.Mutex
	ends []notifications.StreamEnd
}

func (r *runEndRecorder) MarkDeferred(*model.Post) {}

func (r *runEndRecorder) Notify(_ context.Context, end notifications.StreamEnd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends = append(r.ends, end)
}

func (r *runEndRecorder) snapshot() []notifications.StreamEnd {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notifications.StreamEnd(nil), r.ends...)
}

// TestToolFollowUpFailingBeforeStreamingNotifiesTheRequester drives the share
// decision route with an LLM that fails before any stream starts. The run
// never reaches the streaming service, which notifies for runs it streams, so
// the follow-up itself must end the run for the requester.
func TestToolFollowUpFailingBeforeStreamingNotifiesTheRequester(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID = "post12345678901234567890ab"
		convID = "conv12345678901234567890ab"
	)
	openChannel := &model.Channel{Id: testChannelID, Type: model.ChannelTypeOpen, TeamId: "teamid"}

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.api.streamingService = &streamingContextRecorder{}
	convStore := e.useConversations()
	notifier := &runEndRecorder{}
	e.api.conversationsService.SetNotifier(notifier)
	e.config.enableChannelMentionToolCalling = true
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.OverrideLicense(&model.License{SkuShortName: "advanced"})

	fakeLLM := NewFakeLLM("unused")
	fakeLLM.Error = errors.New("llm unavailable")
	e.setBots(bots.NewBot(
		llm.BotConfig{ID: testBotUserID, Name: "thebot", DisplayName: "The Bot", UserAccessLevel: llm.UserAccessLevelAll, ChannelAccessLevel: llm.ChannelAccessLevelAll},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: testBotUserID, Username: "thebot"},
		fakeLLM,
	))

	require.NoError(t, convStore.CreateConversation(&store.Conversation{ID: convID, UserID: testUserID, BotID: testBotUserID, Operation: "conversation"}))
	userContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "look up MM-1"}})
	require.NoError(t, err)
	require.NoError(t, convStore.CreateTurnAutoSequence(&store.Turn{ID: "user-turn", ConversationID: convID, Role: "user", Content: userContent}))
	toolUse := []conversation.ContentBlock{{
		Type:   conversation.BlockTypeToolUse,
		ID:     "tool-use-1",
		Name:   "jira__get_issue",
		Input:  []byte(`{"issue_key":"MM-1"}`),
		Status: conversation.StatusSuccess,
		Shared: new(true),
	}}
	toolResult := []conversation.ContentBlock{{
		Type:      conversation.BlockTypeToolResult,
		ToolUseID: "tool-use-1",
		Content:   "issue details",
		Status:    conversation.StatusSuccess,
		Shared:    new(true),
	}}
	for _, turn := range []store.Turn{
		assistantToolTurn(t, postID, toolUse),
		toolResultTurn(t, toolResult),
	} {
		turn.ConversationID = convID
		require.NoError(t, convStore.CreateTurnAutoSequence(&turn))
	}

	post := &model.Post{Id: postID, UserId: testBotUserID, ChannelId: openChannel.Id}
	post.AddProp(streaming.ConversationIDProp, convID)
	post.AddProp(model.PostPropsDeferredNotification, true)

	e.mockAPI.On("GetPost", postID).Return(post, nil)
	e.mockAPI.On("GetChannel", openChannel.Id).Return(openChannel, nil)
	e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil).Maybe()
	e.mockAPI.On("HasPermissionToChannel", testUserID, openChannel.Id, model.PermissionReadChannel).Return(true)
	e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Username: "requester", Locale: "en"}, nil).Maybe()
	e.mockAPI.On("KVGet", mock.Anything).Return([]byte(nil), (*model.AppError)(nil)).Maybe()

	req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/tool_result", strings.NewReader(`{"accepted_tool_ids":["tool-use-1"]}`))
	req.Header.Add("Mattermost-User-Id", testUserID)
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, req)
	require.NotEqual(t, http.StatusOK, recorder.Result().StatusCode, "the failed follow-up is reported to the requester")

	ends := notifier.snapshot()
	require.Len(t, ends, 1, "the failed follow-up notifies exactly once")
	assert.Equal(t, postID, ends[0].PostID)
	assert.Equal(t, testUserID, ends[0].RequesterID)
	assert.Equal(t, notifications.OutcomeErrored, ends[0].Outcome)
	assert.True(t, ends[0].PostDeferred)
}
