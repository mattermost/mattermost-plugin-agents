// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
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
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type noToolsTestToolProvider struct{}

func (p *noToolsTestToolProvider) GetTools(*bots.Bot) []llm.Tool {
	return nil
}

type noToolsTestMCPProvider struct {
	calls int
	tools []llm.Tool
}

func (p *noToolsTestMCPProvider) GetToolsForUser(context.Context, string) ([]llm.Tool, *mcp.Errors) {
	p.calls++
	return p.tools, nil
}

type noToolsTestContextConfigProvider struct{}

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

func (s *noToolsStreamingService) StreamToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}

func (s *noToolsStreamingService) StreamContinuationToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}

func (s *noToolsStreamingService) StopStreaming(string) {}

func (s *noToolsStreamingService) GetStreamingContext(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}

func (s *noToolsStreamingService) FinishStreaming(string) {}

// mockConvServiceStore is a simple in-memory implementation of conversation.Store
// for API-layer tests that exercise the thread analysis path.
type mockConvServiceStore struct {
	conversations map[string]*store.Conversation
	turns         map[string][]store.Turn
}

func newMockConvServiceStore() *mockConvServiceStore {
	return &mockConvServiceStore{
		conversations: make(map[string]*store.Conversation),
		turns:         make(map[string][]store.Turn),
	}
}

func (m *mockConvServiceStore) CreateConversation(conv *store.Conversation) error {
	m.conversations[conv.ID] = conv
	return nil
}

func (m *mockConvServiceStore) GetConversation(id string) (*store.Conversation, error) {
	conv, ok := m.conversations[id]
	if !ok {
		return nil, store.ErrConversationNotFound
	}
	return conv, nil
}

func (m *mockConvServiceStore) GetConversationByThreadBotUser(_, _, _ string) (*store.Conversation, error) {
	return nil, store.ErrConversationNotFound
}

func (m *mockConvServiceStore) UpdateConversationTitle(id, title string) error {
	if conv, ok := m.conversations[id]; ok {
		conv.Title = title
	}
	return nil
}

func (m *mockConvServiceStore) UpdateConversationRootPostID(id string, rootPostID string) error {
	if conv, ok := m.conversations[id]; ok {
		conv.RootPostID = &rootPostID
	}
	return nil
}

func (m *mockConvServiceStore) CreateTurn(turn *store.Turn) error {
	m.turns[turn.ConversationID] = append(m.turns[turn.ConversationID], *turn)
	return nil
}

func (m *mockConvServiceStore) CreateTurnAutoSequence(turn *store.Turn) error {
	turn.Sequence = len(m.turns[turn.ConversationID]) + 1
	return m.CreateTurn(turn)
}

func (m *mockConvServiceStore) GetTurnsForConversation(conversationID string) ([]store.Turn, error) {
	return m.turns[conversationID], nil
}

func (m *mockConvServiceStore) UpdateTurnContent(id string, content json.RawMessage) error {
	return nil
}

func (m *mockConvServiceStore) UpdateTurnTokens(_ string, _, _ int64) error {
	return nil
}

func (m *mockConvServiceStore) GetTurnByPostID(_ string) (*store.Turn, error) {
	return nil, nil
}

func (m *mockConvServiceStore) UpdateTurnPostID(_ string, _ *string) error {
	return nil
}

func (m *mockConvServiceStore) DeleteResponseTurns(_ string, _ string) error {
	return nil
}

func (m *mockConvServiceStore) GetMaxSequenceForConversation(conversationID string) (int, error) {
	return len(m.turns[conversationID]), nil
}

func setupNoToolsAPI(t *testing.T, mcpProvider *noToolsTestMCPProvider, mmClient *mmapimocks.MockClient) (*TestEnvironment, *noToolsStreamingService, *mockConvServiceStore) {
	t.Helper()

	e := SetupTestEnvironment(t)
	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.OverrideLicense(&model.License{SkuShortName: "advanced"})
	siteName := "Mattermost"
	siteURL := "https://example.com"
	e.OverrideConfig(&model.Config{
		TeamSettings:    model.TeamSettings{SiteName: &siteName},
		ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
	})

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

	// Wire up a conversation service with an in-memory store so that
	// thread analysis can create conversation entities.
	convStore := newMockConvServiceStore()
	convService := conversation.NewService(convStore, promptsObj, mmClient, e.bots)
	e.api.SetConversationService(convService)

	fakeLLM := NewFakeLLM("summary response")
	bot := bots.NewBot(
		llm.BotConfig{ID: testBotUserID, Name: "matty", DisplayName: "Matty"},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: testBotUserID, Username: "matty", DisplayName: "Matty"},
		fakeLLM,
	)
	e.bots.SetBotsForTesting([]*bots.Bot{bot})

	return e, streamingService, convStore
}

func TestHandleThreadAnalysisDoesNotLoadToolsWhenToolsAreDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	mcpProvider := &noToolsTestMCPProvider{}
	mmClient := mmapimocks.NewMockClient(t)
	e, streamingService, _ := setupNoToolsAPI(t, mcpProvider, mmClient)
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
	e, streamingService, _ := setupNoToolsAPI(t, mcpProvider, mmClient)
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

func TestHandleChannelAnalysisAcceptsNamespacedEmbeddedTools(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	mcpProvider := &noToolsTestMCPProvider{
		tools: []llm.Tool{
			{Name: "mattermost__read_channel", ServerOrigin: mcp.EmbeddedClientKey},
			{Name: "mattermost__get_channel_info", ServerOrigin: mcp.EmbeddedClientKey},
		},
	}
	mmClient := mmapimocks.NewMockClient(t)
	e, streamingService, _ := setupNoToolsAPI(t, mcpProvider, mmClient)
	defer e.Cleanup(t)

	requestingUser := &model.User{Id: testUserID, Username: "requester", Locale: "en"}
	channel := &model.Channel{Id: testChannelID, Type: model.ChannelTypeOpen, TeamId: "teamid"}

	e.mockAPI.On("GetChannel", testChannelID).Return(channel, nil)
	e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil)
	e.mockAPI.On("HasPermissionToChannel", testUserID, testChannelID, model.PermissionReadChannel).Return(true)
	e.mockAPI.On("GetUser", testUserID).Return(requestingUser, nil)

	request := httptest.NewRequest(http.MethodPost, "/channel/"+testChannelID+"/analyze", strings.NewReader(`{"analysis_type":"custom","prompt":"summarize this channel"}`))
	request.Header.Add("Mattermost-User-ID", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())
	require.Equal(t, 1, streamingService.newDMCalls)
	require.Equal(t, 1, mcpProvider.calls)
}

// TestHandleIntervalSetsConversationRootPostID verifies that after an
// interval summary completes, the conversation's RootPostID is set to the
// newly-created response post. Without this, interval summaries appear as
// conversation entities with root_post_id == nil and the RHS threads list
// filters them out — so they disappear from history entirely.
func TestHandleIntervalSetsConversationRootPostID(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	mcpProvider := &noToolsTestMCPProvider{}
	mmClient := mmapimocks.NewMockClient(t)
	e, streamingService, convStore := setupNoToolsAPI(t, mcpProvider, mmClient)
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
	require.Equal(t, 1, streamingService.newDMCalls, "interval summary should have streamed a response post")

	// Locate the conversation that was created by the interval summary and
	// assert its RootPostID points at the streamed response post.
	require.Len(t, convStore.conversations, 1, "interval summary should create exactly one conversation")
	var conv *store.Conversation
	for _, c := range convStore.conversations {
		conv = c
	}
	require.NotNil(t, conv)
	require.NotNil(t, conv.RootPostID,
		"interval summary must set RootPostID so the RHS history can navigate to it; without this, the RHS filter drops the entry")
	assert.Equal(t, "response-post-id", *conv.RootPostID,
		"RootPostID should match the post ID assigned by the streaming service")
}
