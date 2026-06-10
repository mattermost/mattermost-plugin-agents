// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type channelAnalysisMCPProvider struct {
	tools []llm.Tool
}

func (p *channelAnalysisMCPProvider) GetToolsForUser(context.Context, string) ([]llm.Tool, *mcp.Errors) {
	return p.tools, nil
}

type channelAnalysisSequenceLLM struct {
	calls    [][]llm.TextStreamEvent
	requests []llm.CompletionRequest
	callIdx  int
}

func (f *channelAnalysisSequenceLLM) ChatCompletion(_ context.Context, request llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	if f.callIdx >= len(f.calls) {
		return nil, fmt.Errorf("unexpected call #%d to ChatCompletion", f.callIdx)
	}
	f.requests = append(f.requests, request)
	events := f.calls[f.callIdx]
	f.callIdx++

	ch := make(chan llm.TextStreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return &llm.TextStreamResult{Stream: ch}, nil
}

func (f *channelAnalysisSequenceLLM) ChatCompletionNoStream(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (f *channelAnalysisSequenceLLM) CountTokens(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}
func (f *channelAnalysisSequenceLLM) InputTokenLimit() int  { return 100000 }
func (f *channelAnalysisSequenceLLM) OutputTokenLimit() int { return 8192 }

func channelAnalysisMCPTool(name string) llm.Tool {
	return llm.Tool{
		Name:         llm.NamespaceMCPToolName("mattermost", name),
		Description:  name + " test tool",
		ServerOrigin: mcp.EmbeddedClientKey,
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return name + " result", nil
		},
	}
}

func channelAnalysisTextEvents(text string) []llm.TextStreamEvent {
	return []llm.TextStreamEvent{
		{Type: llm.EventTypeText, Value: text},
		{Type: llm.EventTypeEnd},
	}
}

func channelAnalysisToolCallEvents(id, name string) []llm.TextStreamEvent {
	return []llm.TextStreamEvent{
		{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
			{ID: id, Name: name, Arguments: []byte(`{}`)},
		}},
		{Type: llm.EventTypeEnd},
	}
}

func setupChannelAnalysisAPI(t *testing.T, dynamicLoading bool) (*TestEnvironment, *noToolsStreamingService, *channelAnalysisSequenceLLM) {
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
	e.mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()

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
			channelAnalysisToolCallEvents("tc1", "read_channel"),
			channelAnalysisTextEvents("Channel summary."),
		},
	}
	bot := bots.NewBot(
		llm.BotConfig{
			ID:                    testBotUserID,
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
			MCPDynamicToolLoading: dynamicLoading,
		},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: testBotUserID, Username: "matty", DisplayName: "Matty"},
		fakeLLM,
	)
	e.bots.SetBotsForTesting([]*bots.Bot{bot})

	channel := &model.Channel{Id: testChannelID, Type: model.ChannelTypeOpen, TeamId: "teamid", DisplayName: "Test Channel"}
	e.mockAPI.On("GetChannel", testChannelID).Return(channel, nil)
	e.mockAPI.On("HasPermissionToChannel", testUserID, testChannelID, model.PermissionReadChannel).Return(true)
	e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil).Maybe()
	e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Username: "requester", Locale: "en"}, nil).Maybe()

	return e, streamingService, fakeLLM
}

func TestHandleChannelAnalysisPreloadsRequiredMCPTools(t *testing.T) {
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
		dynamicLoading bool
	}{
		{name: "dynamic loading enabled", dynamicLoading: true},
		{name: "dynamic loading disabled", dynamicLoading: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, streamingService, fakeLLM := setupChannelAnalysisAPI(t, tt.dynamicLoading)
			defer e.Cleanup(t)

			request := httptest.NewRequest(http.MethodPost, "/channel/"+testChannelID+"/analyze?botUsername=matty", strings.NewReader(`{"analysis_type":"summarize_channel","days":1}`))
			request.Header.Add("Mattermost-User-ID", testUserID)

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)
			resp := recorder.Result()

			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, 1, streamingService.newDMCalls)
			require.NotEmpty(t, fakeLLM.requests)
			require.ElementsMatch(t, []string{"read_channel", "get_channel_info"}, channelAnalysisVisibleToolNames(fakeLLM.requests[0].Context.Tools))
		})
	}
}

func TestChannelAnalysisToolAvailabilityRequiresEmbeddedOrigin(t *testing.T) {
	tools := llm.NewToolStore()
	tools.AddTools([]llm.Tool{
		{Name: "read_channel", ServerOrigin: "https://remote.example.com"},
		{Name: "get_channel_info", ServerOrigin: mcp.EmbeddedClientKey},
	})

	_, missing := channelAnalysisToolAvailability(tools)
	require.ElementsMatch(t, []string{"read_channel"}, missing)

	tools.AddTools([]llm.Tool{
		{Name: "read_channel", ServerOrigin: mcp.EmbeddedClientKey},
	})

	_, missing = channelAnalysisToolAvailability(tools)
	require.Empty(t, missing)
}

func TestChannelAnalysisToolAvailabilityMatchesNamespacedBareName(t *testing.T) {
	tools := llm.NewToolStore()
	tools.AddTools([]llm.Tool{
		channelAnalysisMCPTool("read_channel"),
		channelAnalysisMCPTool("get_channel_info"),
	})

	_, missing := channelAnalysisToolAvailability(tools)
	require.Empty(t, missing)
}

func channelAnalysisVisibleToolNames(store *llm.ToolStore) []string {
	if store == nil {
		return nil
	}
	tools := store.GetTools()
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
