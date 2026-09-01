// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sentinel values distinguishing the team a channel belongs to from the team
// named by the request body, so the rendered prompt shows which one was used.
const (
	contextChannelTeamID   = "chnlteam5678901234567890ab"
	contextChannelTeamName = "channel-owning-team"

	contextRequestTeamID   = "reqteam45678901234567890ab"
	contextRequestTeamName = "request-only-team"
)

// TestChannelAnalysisTeamContextResolution covers the paths around the team
// membership check that TestChannelAnalysisTeamScoping does not: membership
// lookups that fail rather than report absence, memberships that are no longer
// current, and channels that already carry a team of their own.
func TestChannelAnalysisTeamContextResolution(t *testing.T) {
	prevMode := gin.Mode()
	prevWriter := gin.DefaultWriter
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	t.Cleanup(func() {
		gin.SetMode(prevMode)
		gin.DefaultWriter = prevWriter
	})

	tests := []struct {
		name          string
		channelType   model.ChannelType
		channelTeamID string
		member        *model.TeamMember
		memberErr     *model.AppError
		expectTeamID  string
	}{
		{
			name:        "membership lookup failure attaches no team",
			channelType: model.ChannelTypeDirect,
			memberErr:   &model.AppError{Message: "store unavailable", StatusCode: http.StatusInternalServerError},
		},
		{
			name:        "unusable team id attaches no team",
			channelType: model.ChannelTypeDirect,
			memberErr:   &model.AppError{Message: "invalid team id", StatusCode: http.StatusBadRequest},
		},
		{
			name:        "membership that is no longer current attaches no team",
			channelType: model.ChannelTypeGroup,
			member: &model.TeamMember{
				TeamId:   contextRequestTeamID,
				UserId:   testUserID,
				DeleteAt: 1700000000000,
			},
		},
		{
			name:          "public channel uses its own team",
			channelType:   model.ChannelTypeOpen,
			channelTeamID: contextChannelTeamID,
			member: &model.TeamMember{
				TeamId: contextRequestTeamID,
				UserId: testUserID,
			},
			expectTeamID: contextChannelTeamID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &model.Channel{
				Id:          testChannelID,
				Type:        tt.channelType,
				TeamId:      tt.channelTeamID,
				Name:        testBotUserID + "__" + testUserID,
				DisplayName: "Test Channel",
			}
			e, streamingService, fakeLLM := setupChannelAnalysisAPIForChannel(t, channel)
			defer e.Cleanup(t)

			if tt.channelTeamID != "" {
				e.mockAPI.On("GetTeam", tt.channelTeamID).Return(&model.Team{
					Id:          tt.channelTeamID,
					Name:        contextChannelTeamName,
					DisplayName: contextChannelTeamName,
				}, nil).Maybe()
			}
			e.mockAPI.On("GetTeam", contextRequestTeamID).Return(&model.Team{
				Id:          contextRequestTeamID,
				Name:        contextRequestTeamName,
				DisplayName: contextRequestTeamName,
			}, nil).Maybe()
			e.mockAPI.On("GetTeamMember", contextRequestTeamID, testUserID).
				Return(tt.member, tt.memberErr).Maybe()

			body := `{"analysis_type":"summarize_channel","days":1,"team_id":"` + contextRequestTeamID + `"}`
			request := httptest.NewRequest(http.MethodPost, "/channel/"+testChannelID+"/analyze?botUsername=matty", strings.NewReader(body))
			request.Header.Add("Mattermost-User-ID", testUserID)

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)
			resp := recorder.Result()

			require.Equal(t, http.StatusOK, resp.StatusCode, recorder.Body.String())
			require.Equal(t, 1, streamingService.newDMCalls)
			require.NotEmpty(t, fakeLLM.requests)

			completion := fakeLLM.requests[0]
			require.NotNil(t, completion.Context)
			promptText := channelAnalysisPromptText(completion)

			assert.NotContains(t, promptText, contextRequestTeamName, "team named by the request body should not be rendered into the prompt")

			if tt.expectTeamID == "" {
				assert.Nil(t, completion.Context.Team, "no team should be attached")
				return
			}

			require.NotNil(t, completion.Context.Team)
			assert.Equal(t, tt.expectTeamID, completion.Context.Team.Id)
			assert.Contains(t, promptText, contextChannelTeamName, "the channel's own team should be rendered into the prompt")
		})
	}
}
