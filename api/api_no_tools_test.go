// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	mmapimocks "github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/require"
)

type noToolsTestToolProvider struct{}

func (p *noToolsTestToolProvider) GetTools(*bots.Bot) []llm.Tool {
	return nil
}

type noToolsTestMCPProvider struct {
	calls int
}

func (p *noToolsTestMCPProvider) GetToolsForUser(string) ([]llm.Tool, *mcp.Errors) {
	p.calls++
	return nil, nil
}

type noToolsTestContextConfigProvider struct{}

func (p *noToolsTestContextConfigProvider) GetEnableLLMTrace() bool {
	return false
}

func (p *noToolsTestContextConfigProvider) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

type noToolsStreamingService struct {
	newDMCalls int
}

func (s *noToolsStreamingService) StreamToNewPost(context.Context, string, string, *llm.TextStreamResult, *model.Post, string) error {
	return nil
}

func (s *noToolsStreamingService) StreamToNewDM(_ context.Context, botID string, _ *llm.TextStreamResult, _ string, post *model.Post, _ string) error {
	s.newDMCalls++
	post.Id = "response-post-id"
	post.ChannelId = model.GetDMNameFromIds("user12345678901234567890ab", botID)
	return nil
}

func (s *noToolsStreamingService) StreamToPost(context.Context, *llm.TextStreamResult, *model.Post, string) {
}

func (s *noToolsStreamingService) StopStreaming(string) {}

func (s *noToolsStreamingService) GetStreamingContext(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}

func (s *noToolsStreamingService) FinishStreaming(string) {}

func setupNoToolsAPI(t *testing.T, mcpProvider *noToolsTestMCPProvider, mmClient *mmapimocks.MockClient) (*TestEnvironment, *noToolsStreamingService) {
	t.Helper()

	e := SetupTestEnvironment(t)
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
	siteName := "Mattermost"
	siteURL := "https://example.com"
	e.mockAPI.On("GetConfig").Return(&model.Config{
		TeamSettings:    model.TeamSettings{SiteName: &siteName},
		ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
	}).Maybe()

	e.api.prompts = promptsObj
	e.api.mmClient = mmClient
	e.api.contextBuilder = llmcontext.NewLLMContextBuilder(
		e.client,
		&noToolsTestToolProvider{},
		mcpProvider,
		&noToolsTestContextConfigProvider{},
	)

	streamingService := &noToolsStreamingService{}
	e.api.streamingService = streamingService

	fakeLLM := NewFakeLLM("summary response")
	bot := bots.NewBot(
		llm.BotConfig{ID: testBotUserID, Name: "matty", DisplayName: "Matty"},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: testBotUserID, Username: "matty", DisplayName: "Matty"},
		fakeLLM,
	)
	e.bots.SetBotsForTesting([]*bots.Bot{bot})

	return e, streamingService
}

func TestHandleThreadAnalysisDoesNotLoadToolsWhenToolsAreDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	mcpProvider := &noToolsTestMCPProvider{}
	mmClient := mmapimocks.NewMockClient(t)
	e, streamingService := setupNoToolsAPI(t, mcpProvider, mmClient)
	defer e.Cleanup(t)

	requestingUser := &model.User{Id: testUserID, Username: "requester", Locale: "en"}
	threadPost := &model.Post{Id: "postid", ChannelId: testChannelID, UserId: testOtherUserID, Message: "hello"}
	channel := &model.Channel{Id: testChannelID, Type: model.ChannelTypeOpen, TeamId: "teamid"}

	e.mockAPI.On("GetPost", "postid").Return(threadPost, nil)
	e.mockAPI.On("GetChannel", testChannelID).Return(channel, nil)
	e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil)
	e.mockAPI.On("HasPermissionToChannel", testUserID, testChannelID, model.PermissionReadChannel).Return(true)
	e.mockAPI.On("GetUser", testUserID).Return(requestingUser, nil)
	e.mockAPI.On("LogError", "failed to get provider from bot's LLM").Maybe()
	e.mockAPI.On("LogError", testUserID).Maybe()
	e.mockAPI.On("LogError", testChannelID).Maybe()

	postList := &model.PostList{
		Order: []string{"postid"},
		Posts: map[string]*model.Post{"postid": threadPost},
	}
	mmClient.On("GetPostThread", "postid").Return(postList, nil)
	mmClient.On("GetUser", testOtherUserID).Return(&model.User{Id: testOtherUserID, Username: "author"}, nil)

	request := httptest.NewRequest(http.MethodPost, "/post/postid/analyze", strings.NewReader(`{"analysis_type":"summarize_thread"}`))
	request.Header.Add("Mattermost-User-ID", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	require.Equal(t, 1, streamingService.newDMCalls)
	require.Equal(t, 0, mcpProvider.calls, "thread analysis should not build MCP tools when the LLM call disables tools")
}

func TestHandleIntervalDoesNotLoadToolsWhenToolsAreDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	mcpProvider := &noToolsTestMCPProvider{}
	mmClient := mmapimocks.NewMockClient(t)
	e, streamingService := setupNoToolsAPI(t, mcpProvider, mmClient)
	defer e.Cleanup(t)

	requestingUser := &model.User{Id: testUserID, Username: "requester", Locale: "en"}
	channel := &model.Channel{Id: testChannelID, Type: model.ChannelTypeOpen, TeamId: "teamid"}
	channelPost := &model.Post{Id: "post-1", ChannelId: testChannelID, UserId: testOtherUserID, Message: "hello", CreateAt: 2}

	e.mockAPI.On("GetChannel", testChannelID).Return(channel, nil)
	e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil)
	e.mockAPI.On("HasPermissionToChannel", testUserID, testChannelID, model.PermissionReadChannel).Return(true)
	e.mockAPI.On("GetUser", testUserID).Return(requestingUser, nil)

	postList := &model.PostList{
		Order: []string{"post-1"},
		Posts: map[string]*model.Post{"post-1": channelPost},
	}
	mmClient.On("GetPostsSince", testChannelID, int64(1)).Return(postList, nil)
	mmClient.On("GetUser", testOtherUserID).Return(&model.User{Id: testOtherUserID, Username: "author"}, nil)

	request := httptest.NewRequest(http.MethodPost, "/channel/"+testChannelID+"/interval", strings.NewReader(`{"start_time":1,"end_time":0,"preset_prompt":"summarize_range"}`))
	request.Header.Add("Mattermost-User-ID", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	require.Equal(t, 1, streamingService.newDMCalls)
	require.Equal(t, 0, mcpProvider.calls, "channel interval should not build MCP tools when the LLM call disables tools")
}
