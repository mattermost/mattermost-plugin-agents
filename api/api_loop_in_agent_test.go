// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandleLoopInAgentCreatesUserPostMentioningBot(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	cases := []struct {
		name           string
		post           *model.Post
		expectedRootID string
	}{
		{
			name: "thread reply uses existing RootId",
			post: &model.Post{
				Id:        "postid",
				ChannelId: "channelid",
				RootId:    "rootid",
				UserId:    "botid",
			},
			expectedRootID: "rootid",
		},
		{
			name: "top-level post uses post Id as RootId",
			post: &model.Post{
				Id:        "postid",
				ChannelId: "channelid",
				UserId:    "botid",
			},
			expectedRootID: "postid",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.setupTestBot(llm.BotConfig{Name: "test-bot"})

			e.mockAPI.On("GetPost", "postid").Return(tc.post, nil)
			e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
				Id:     "channelid",
				Type:   model.ChannelTypeOpen,
				TeamId: "teamid",
			}, nil)
			e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
			e.mockAPI.On("LogError", mock.Anything).Maybe()
			e.mockAPI.On("LogDebug", mock.Anything).Maybe()

			var captured *model.Post
			e.mockAPI.On("CreatePost", mock.MatchedBy(func(p *model.Post) bool {
				captured = p.Clone()
				return true
			})).Return(func(p *model.Post) *model.Post {
				clone := p.Clone()
				clone.Id = "newpostid"
				return clone
			}, nil)

			req := httptest.NewRequest(http.MethodPost, "/post/postid/loop_in_agent?botUsername=test-bot", nil)
			req.Header.Add("Mattermost-User-ID", "userid")
			recorder := httptest.NewRecorder()

			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, http.StatusOK, recorder.Result().StatusCode, "body: %s", recorder.Body.String())
			require.NotNil(t, captured, "CreatePost was not called")
			require.Equal(t, "userid", captured.UserId)
			require.Equal(t, "channelid", captured.ChannelId)
			require.Equal(t, tc.expectedRootID, captured.RootId)
			require.Equal(t, "@test-bot", captured.Message)
			require.Equal(t, "true", captured.GetProp("activate_ai"))
		})
	}
}
