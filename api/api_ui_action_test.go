// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
)

// TestUIActionRoute pins how the ui_action route answers requests it refuses
// before any post or turn is created, and that its audit record carries the
// block and form identifiers but never the label or submitted values.
func TestUIActionRoute(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID    = "post12345678901234567890ab"
		channelID = "chan12345678901234567890ab"
		convID    = "conv12345678901234567890ab"
		sentinel  = "SENTINEL-FORM-CONTENT"
	)
	reply := "Pick one.\n\n```openui-lang\nroot = Stack([f])\nf = Form(\"deploy\", [env], [Button(\"Deploy\")])\nenv = TextInput(\"env\", \"Environment\")\n```"
	body := func(label, key string) string {
		return `{"block":0,"form":"deploy","label":"` + label + `","fields":[{"name":"env","label":"Environment","value":"` + sentinel + `"}],"idempotency_key":"` + key + `"}`
	}

	tests := []struct {
		name       string
		body       string
		disabled   bool
		owner      string
		cannotPost bool
		replyTurn  bool
		wantStatus int
	}{
		{name: "generative UI turned off", body: body("Deploy", "key-00000001"), disabled: true, replyTurn: true, wantStatus: http.StatusForbidden},
		{name: "someone else's conversation", body: body("Deploy", "key-00000001"), owner: testOtherUserID, replyTurn: true, wantStatus: http.StatusForbidden},
		{name: "requester cannot post in the channel", body: body("Deploy", "key-00000001"), cannotPost: true, replyTurn: true, wantStatus: http.StatusForbidden},
		{name: "reply not finished", body: body("Deploy", "key-00000001"), wantStatus: http.StatusConflict},
		{name: "button the reply does not offer", body: body(sentinel, "key-00000001"), replyTurn: true, wantStatus: http.StatusBadRequest},
		{name: "missing idempotency key", body: body("Deploy", ""), replyTurn: true, wantStatus: http.StatusBadRequest},
		{name: "malformed body", body: `{"block":"zero"}`, replyTurn: true, wantStatus: http.StatusBadRequest},
		{name: "oversized body", body: `{"label":"` + strings.Repeat("x", 20*1024) + `"}`, replyTurn: true, wantStatus: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			records := e.CaptureAuditRecords()
			e.api.streamingService = &recordingStreamingService{}
			convStore := e.useConversations()
			e.config.enableGenerativeUI = !tc.disabled
			e.setBots(bots.NewBot(
				llm.BotConfig{ID: testBotUserID, Name: "bot", UserAccessLevel: llm.UserAccessLevelAll, ChannelAccessLevel: llm.ChannelAccessLevelAll, Skills: llm.SkillsConfig{GenerativeUI: true}},
				llm.ServiceConfig{},
				&model.Bot{UserId: testBotUserID, Username: "bot"},
				nil,
			))

			owner := testUserID
			if tc.owner != "" {
				owner = tc.owner
			}
			require.NoError(t, convStore.CreateConversation(&store.Conversation{ID: convID, UserID: owner, BotID: testBotUserID, Operation: llm.OperationConversation}))
			if tc.replyTurn {
				content, err := json.Marshal([]map[string]string{{"type": "text", "text": reply}})
				require.NoError(t, err)
				replyPostID := postID
				require.NoError(t, convStore.CreateTurn(&store.Turn{ID: "turn1", ConversationID: convID, PostID: &replyPostID, Role: "assistant", Content: content, Sequence: 1}))
			}

			post := &model.Post{Id: postID, UserId: testBotUserID, ChannelId: channelID, Message: reply}
			post.AddProp(streaming.ConversationIDProp, convID)
			e.mockAPI.On("GetPost", postID).Return(post, nil)
			e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{
				Id:   channelID,
				Name: testBotUserID + "__" + testUserID,
				Type: model.ChannelTypeDirect,
			}, nil)
			e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)
			e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionCreatePost).Return(!tc.cannotPost).Maybe()
			e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Locale: "en"}, nil).Maybe()
			e.mockAPI.On("KVGet", mock.Anything).Return(([]byte)(nil), (*model.AppError)(nil)).Maybe()

			req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/ui_action", strings.NewReader(tc.body))
			req.Header.Add("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, tc.wantStatus, recorder.Result().StatusCode, recorder.Body.String())

			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventGenerativeUISubmit, rec.EventName)
			assert.Equal(t, model.AuditStatusFail, rec.Status)
			assert.Equal(t, tc.wantStatus, rec.Error.Code)
			assert.Equal(t, postID, rec.EventData.Parameters[audit.KeyPostID])
			assert.Equal(t, testBotUserID, rec.EventData.Parameters[audit.KeyAgentID])
			marshaled, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(marshaled), sentinel, "audit records never carry submitted content")
		})
	}
}
