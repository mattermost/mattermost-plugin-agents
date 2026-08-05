// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/conversations"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestToolDecisionEndpointsNotBlanketLicenseGated reproduces the original
// bug: on an unlicensed (or Professional) server, submitting tool decisions
// failed with 403 "feature not licensed" even for embedded Mattermost MCP
// tools and built-in tools. The endpoints must not blanket-reject on license;
// only decisions involving remote MCP servers are license-gated (enforced in
// conversations.HandleToolCall/HandleToolResult and covered by tests there).
func TestToolDecisionEndpointsNotBlanketLicenseGated(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name     string
		endpoint string
	}{
		{
			name:     "tool_call is not blanket license gated",
			endpoint: "/post/postid/tool_call",
		},
		{
			name:     "tool_result is not blanket license gated",
			endpoint: "/post/postid/tool_result",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.setupTestBot(llm.BotConfig{Name: "licensetest", DisplayName: "License Bot"})

			// No license at all: previously this made the endpoints reject
			// every decision submission with 403 before looking at the tools.
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
			e.mockAPI.On("GetLicense").Return(&model.License{}).Maybe()

			botUserID := testBotUserID
			userID := testUserID

			post := &model.Post{
				Id:        "postid",
				UserId:    botUserID,
				ChannelId: "channelid",
			}
			post.AddProp("conversation_id", "conv-license-toolcall")

			e.conversationStore.conversations["conv-license-toolcall"] = &store.Conversation{
				ID:     "conv-license-toolcall",
				UserID: userID,
				BotID:  botUserID,
			}

			dmChannelName := botUserID + "__" + userID

			e.mockAPI.On("GetPost", "postid").Return(post, nil)
			e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
				Id:   "channelid",
				Name: dmChannelName,
				Type: model.ChannelTypeDirect,
			}, nil)
			e.mockAPI.On("HasPermissionToChannel", userID, "channelid", model.PermissionReadChannel).Return(true)
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			request := httptest.NewRequest(http.MethodPost, test.endpoint, strings.NewReader(`{"accepted_tool_ids": ["tool-1"]}`))
			request.Header.Add("Mattermost-User-ID", userID)

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)
			resp := recorder.Result()

			// The request may fail later for unrelated reasons (missing
			// conversation turns in the test store), but it must not be
			// rejected with 403 by a blanket license check.
			require.NotEqual(t, http.StatusForbidden, resp.StatusCode,
				"tool decision endpoints must not blanket-reject unlicensed servers")
		})
	}
}

func TestToolApprovalHTTPStatusMapsRemoteMCPLicenseError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "remote MCP not licensed maps to forbidden",
			err:  conversations.ErrRemoteMCPNotLicensed,
			want: http.StatusForbidden,
		},
		{
			name: "wrapped remote MCP not licensed maps to forbidden",
			err:  errors.Join(errors.New("context"), conversations.ErrRemoteMCPNotLicensed),
			want: http.StatusForbidden,
		},
		{
			name: "unknown error maps to internal server error",
			err:  errors.New("boom"),
			want: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, toolApprovalHTTPStatus(tc.err))
		})
	}
}
