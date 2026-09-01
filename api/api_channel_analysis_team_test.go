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
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Sentinel values for the team named by the request body, chosen so their
// presence in the rendered prompt is unambiguous.
const (
	analysisRequestTeamID          = "team12345678901234567890ab"
	analysisRequestTeamName        = "unrelated-team-slug"
	analysisRequestTeamDisplayName = "Unrelated Team Display"
)

// setupChannelAnalysisAPIForChannel mirrors setupChannelAnalysisAPI but lets the
// caller supply the channel the analysis runs against, so DM and group channels
// can be exercised. It deliberately registers no team expectations; tests add
// the ones matching the scenario they set up.
func setupChannelAnalysisAPIForChannel(t *testing.T, channel *model.Channel) (*TestEnvironment, *noToolsStreamingService, *channelAnalysisSequenceLLM) {
	t.Helper()

	e := SetupTestEnvironment(t)
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.OverrideLicense(&model.License{SkuShortName: "advanced"})
	siteName := "Mattermost"
	siteURL := "https://example.com"
	e.mockAPI.On("GetConfig").Return(&model.Config{
		TeamSettings:    model.TeamSettings{SiteName: &siteName},
		ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
	}).Maybe()

	e.api.prompts = promptsObj
	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&noToolsTestToolProvider{},
		&channelAnalysisMCPProvider{tools: []llm.Tool{
			channelAnalysisMCPTool("read_channel"),
			channelAnalysisMCPTool("get_channel_info"),
		}},
		&noToolsTestContextConfigProvider{},
	)

	mmClient := mmapimocks.NewMockClient(t)
	e.api.mmClient = mmClient
	streamingService := &noToolsStreamingService{}
	e.api.streamingService = streamingService
	convStore := newMockConvServiceStore()
	e.api.SetConversationService(conversation.NewService(convStore, promptsObj, mmClient, e.bots))

	fakeLLM := &channelAnalysisSequenceLLM{
		calls: [][]llm.TextStreamEvent{
			channelAnalysisTextEvents("Channel summary."),
		},
	}
	bot := bots.NewBot(
		llm.BotConfig{
			ID:                    testBotUserID,
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
		},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: testBotUserID, Username: "matty", DisplayName: "Matty"},
		fakeLLM,
	)
	e.bots.SetBotsForTesting([]*bots.Bot{bot})

	e.mockAPI.On("GetChannel", channel.Id).Return(channel, nil)
	e.mockAPI.On("HasPermissionToChannel", testUserID, channel.Id, model.PermissionReadChannel).Return(true)
	e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Username: "requester", Locale: "en"}, nil).Maybe()

	return e, streamingService, fakeLLM
}

// channelAnalysisPromptText concatenates every message the request would send
// to the model, which is where team details would surface if attached.
func channelAnalysisPromptText(request llm.CompletionRequest) string {
	var builder strings.Builder
	for _, post := range request.Posts {
		builder.WriteString(post.Message)
		builder.WriteString("\n")
	}
	return builder.String()
}

func TestChannelAnalysisTeamScoping(t *testing.T) {
	prevMode := gin.Mode()
	prevWriter := gin.DefaultWriter
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	t.Cleanup(func() {
		gin.SetMode(prevMode)
		gin.DefaultWriter = prevWriter
	})

	tests := []struct {
		name           string
		channelType    model.ChannelType
		requestTeamID  string
		teamAuthorized bool
		expectTeam     bool
	}{
		{
			name:           "direct channel attaches a team the requester belongs to",
			channelType:    model.ChannelTypeDirect,
			requestTeamID:  analysisRequestTeamID,
			teamAuthorized: true,
			expectTeam:     true,
		},
		{
			name:           "group channel attaches a team the requester belongs to",
			channelType:    model.ChannelTypeGroup,
			requestTeamID:  analysisRequestTeamID,
			teamAuthorized: true,
			expectTeam:     true,
		},
		{
			name:           "direct channel does not attach a team the requester is not a member of",
			channelType:    model.ChannelTypeDirect,
			requestTeamID:  analysisRequestTeamID,
			teamAuthorized: false,
			expectTeam:     false,
		},
		{
			name:           "group channel does not attach a team the requester is not a member of",
			channelType:    model.ChannelTypeGroup,
			requestTeamID:  analysisRequestTeamID,
			teamAuthorized: false,
			expectTeam:     false,
		},
		{
			name:          "direct channel without a team id attaches no team",
			channelType:   model.ChannelTypeDirect,
			requestTeamID: "",
			expectTeam:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &model.Channel{
				Id:   testChannelID,
				Type: tt.channelType,
				Name: testBotUserID + "__" + testUserID,
			}
			e, streamingService, fakeLLM := setupChannelAnalysisAPIForChannel(t, channel)
			defer e.Cleanup(t)

			// The handler may or may not fetch the team, and the membership
			// check may be expressed either as a team member lookup or as a
			// team permission check, so allow all three.
			e.mockAPI.On("GetTeam", analysisRequestTeamID).Return(&model.Team{
				Id:          analysisRequestTeamID,
				Name:        analysisRequestTeamName,
				DisplayName: analysisRequestTeamDisplayName,
			}, nil).Maybe()
			if tt.teamAuthorized {
				e.mockAPI.On("GetTeamMember", analysisRequestTeamID, testUserID).Return(&model.TeamMember{
					TeamId: analysisRequestTeamID,
					UserId: testUserID,
				}, nil).Maybe()
				e.mockAPI.On("HasPermissionToTeam", testUserID, analysisRequestTeamID, mock.Anything).Return(true).Maybe()
			} else {
				e.mockAPI.On("GetTeamMember", analysisRequestTeamID, testUserID).Return(nil, &model.AppError{
					StatusCode: http.StatusNotFound,
				}).Maybe()
				e.mockAPI.On("HasPermissionToTeam", testUserID, analysisRequestTeamID, mock.Anything).Return(false).Maybe()
			}

			body := `{"analysis_type":"summarize_channel","days":1}`
			if tt.requestTeamID != "" {
				body = `{"analysis_type":"summarize_channel","days":1,"team_id":"` + tt.requestTeamID + `"}`
			}

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

			if tt.expectTeam {
				require.NotNil(t, completion.Context.Team, "team the requester belongs to should be attached")
				require.Equal(t, analysisRequestTeamID, completion.Context.Team.Id)
				assert.Contains(t, promptText, analysisRequestTeamName, "attached team should be rendered into the prompt")
				return
			}

			assert.NotContains(t, promptText, analysisRequestTeamName, "team name should not reach the prompt")
			assert.NotContains(t, promptText, analysisRequestTeamDisplayName, "team display name should not reach the prompt")
			assert.Nil(t, completion.Context.Team, "team should not be attached")
		})
	}
}
