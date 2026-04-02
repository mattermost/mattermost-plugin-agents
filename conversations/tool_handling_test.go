// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

type fakeMMClient struct {
	users        map[string]*model.User
	postThreads  map[string]*model.PostList
	kv           map[string]interface{}
	updatedPosts []*model.Post
	kvDeletes    []string
	posts        map[string]*model.Post
	channels     map[string]*model.Channel
}

func (c *fakeMMClient) GetUser(userID string) (*model.User, error) {
	user, ok := c.users[userID]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}

func (c *fakeMMClient) GetPostThread(postID string) (*model.PostList, error) {
	postList, ok := c.postThreads[postID]
	if !ok {
		return nil, errors.New("thread not found")
	}
	return postList, nil
}

func (c *fakeMMClient) CreatePost(post *model.Post) error {
	return errors.New("not implemented")
}

func (c *fakeMMClient) UpdatePost(post *model.Post) error {
	c.updatedPosts = append(c.updatedPosts, post.Clone())
	return nil
}

func (c *fakeMMClient) KVGet(key string, value interface{}) error {
	stored, ok := c.kv[key]
	if !ok {
		return errors.New("not found")
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (c *fakeMMClient) KVSet(key string, value interface{}) error {
	if c.kv == nil {
		c.kv = make(map[string]interface{})
	}
	c.kv[key] = value
	return nil
}

func (c *fakeMMClient) KVSetWithExpiry(key string, value interface{}, _ time.Duration) error {
	return c.KVSet(key, value)
}

func (c *fakeMMClient) KVDelete(key string) error {
	delete(c.kv, key)
	c.kvDeletes = append(c.kvDeletes, key)
	return nil
}

func (c *fakeMMClient) AddReaction(*model.Reaction) error {
	return errors.New("not implemented")
}

func (c *fakeMMClient) GetPost(postID string) (*model.Post, error) {
	if c.posts != nil {
		if post, ok := c.posts[postID]; ok {
			return post, nil
		}
	}
	return nil, errors.New("post not found")
}

func (c *fakeMMClient) GetPostsSince(string, int64) (*model.PostList, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) GetPostsBefore(string, string, int, int) (*model.PostList, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) DM(string, string, *model.Post) error {
	return errors.New("not implemented")
}

func (c *fakeMMClient) GetTeam(string) (*model.Team, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) GetChannel(channelID string) (*model.Channel, error) {
	if c.channels != nil {
		if ch, ok := c.channels[channelID]; ok {
			return ch, nil
		}
	}
	return nil, errors.New("channel not found")
}

func (c *fakeMMClient) GetDirectChannel(string, string) (*model.Channel, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) PublishWebSocketEvent(string, map[string]interface{}, *model.WebsocketBroadcast) {
}

func (c *fakeMMClient) GetConfig() *model.Config {
	return &model.Config{}
}

func (c *fakeMMClient) LogError(string, ...interface{}) {}

func (c *fakeMMClient) LogWarn(string, ...interface{}) {}

func (c *fakeMMClient) GetUserByUsername(string) (*model.User, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) GetUserStatus(string) (*model.Status, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) HasPermissionTo(string, *model.Permission) bool {
	return true
}

func (c *fakeMMClient) GetPluginStatus(string) (*model.PluginStatus, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) PluginHTTP(*http.Request) *http.Response {
	return nil
}

func (c *fakeMMClient) LogDebug(string, ...interface{}) {}

func (c *fakeMMClient) GetChannelByName(string, string, bool) (*model.Channel, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) HasPermissionToChannel(string, string, *model.Permission) bool {
	return true
}

func (c *fakeMMClient) GetFileInfo(string) (*model.FileInfo, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) GetFile(string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) SendEphemeralPost(string, *model.Post) {}

type capturingLanguageModel struct {
	autoRunTools []string
}

func (m *capturingLanguageModel) ChatCompletion(_ llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	var cfg llm.LanguageModelConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	m.autoRunTools = append([]string{}, cfg.AutoRunTools...)
	return llm.NewStreamFromString("follow-up response"), nil
}

func (m *capturingLanguageModel) ChatCompletionNoStream(_ llm.CompletionRequest, _ ...llm.LanguageModelOption) (string, error) {
	return "", nil
}

func (m *capturingLanguageModel) CountTokens(string) int {
	return 0
}

func (m *capturingLanguageModel) InputTokenLimit() int {
	return 0
}

type fakeStreamingService struct {
	streamedPosts []*model.Post
}

func (s *fakeStreamingService) StreamToNewPost(_ context.Context, _ string, _ string, _ *llm.TextStreamResult, post *model.Post, _ string) error {
	s.streamedPosts = append(s.streamedPosts, post.Clone())
	return nil
}

func (s *fakeStreamingService) StreamToNewDM(context.Context, string, *llm.TextStreamResult, string, *model.Post, string) error {
	return nil
}

func (s *fakeStreamingService) StreamToPost(context.Context, *llm.TextStreamResult, *model.Post, string) {
}

func (s *fakeStreamingService) StopStreaming(string) {}

func (s *fakeStreamingService) GetStreamingContext(inCtx context.Context, _ string) (context.Context, error) {
	return inCtx, nil
}

func (s *fakeStreamingService) FinishStreaming(string) {}

type testToolProvider struct {
	tools []llm.Tool
}

func (p *testToolProvider) GetTools(bot *bots.Bot) []llm.Tool {
	return p.tools
}

type testConfigProvider struct{}

func (p *testConfigProvider) GetEnableLLMTrace() bool {
	return false
}

func (p *testConfigProvider) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

// testToolCallingConfig implements ToolCallingConfig for testing
type testToolCallingConfig struct {
	enableChannelMentionToolCalling bool
}

func (c *testToolCallingConfig) EnableChannelMentionToolCalling() bool {
	return c.enableChannelMentionToolCalling
}

func (c *testToolCallingConfig) AllowNativeWebSearchInChannels() bool {
	return false
}

func (c *testToolCallingConfig) MCP() mcp.Config {
	return mcp.Config{}
}

type toolArgs struct {
	Value string `json:"value"`
}

func TestHandleToolCallChannelStoresInKVAndRedactsProps(t *testing.T) {
	const (
		postID      = "post-id"
		channelID   = "channel-id"
		teamID      = "team-id"
		botID       = "bot-id"
		requesterID = "requester-id"
	)

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)

	siteName := "Mattermost"
	mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
	mockAPI.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil).Maybe()

	tool := llm.Tool{
		Name:        "test_tool",
		Description: "test tool",
		Schema:      llm.NewJSONSchemaFromStruct[toolArgs](),
		Resolver: func(_ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
			var parsed toolArgs
			if err := args(&parsed); err != nil {
				return "", err
			}
			return "ok:" + parsed.Value, nil
		},
	}
	contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: []llm.Tool{tool}}, nil, &testConfigProvider{})

	botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
	bot := bots.NewBot(llm.BotConfig{ID: botID, Name: "test-bot"}, llm.ServiceConfig{}, &model.Bot{UserId: botID, Username: "test-bot"}, nil)
	botService.SetBotsForTesting([]*bots.Bot{bot})

	post := &model.Post{
		Id:        postID,
		UserId:    botID,
		ChannelId: channelID,
		CreateAt:  1,
	}
	post.AddProp(streaming.LLMRequesterUserID, requesterID)
	post.AddProp(streaming.AllowToolsInChannelProp, "true")

	toolCalls := []llm.ToolCall{
		{
			ID:        "tool-1",
			Name:      "test_tool",
			Arguments: json.RawMessage(`{"value":"secret"}`),
		},
	}
	redactedToolCalls := streaming.RedactToolCalls(toolCalls)
	redactedJSON, err := json.Marshal(redactedToolCalls)
	require.NoError(t, err)
	post.AddProp(streaming.ToolCallProp, string(redactedJSON))

	postList := &model.PostList{
		Order: []string{postID},
		Posts: map[string]*model.Post{postID: post},
	}

	fakeClient := &fakeMMClient{
		users: map[string]*model.User{
			requesterID: {Id: requesterID, Locale: "en"},
			botID:       {Id: botID, Locale: "en"},
		},
		postThreads: map[string]*model.PostList{
			postID: postList,
		},
		kv: map[string]interface{}{},
	}
	toolCallKVKey := streaming.ToolCallPrivateKVKey(postID, requesterID)
	fakeClient.kv[toolCallKVKey] = toolCalls

	toolCallingConfig := &testToolCallingConfig{enableChannelMentionToolCalling: true}
	conversationService := conversations.New(nil, fakeClient, nil, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, toolCallingConfig)

	channel := &model.Channel{
		Id:     channelID,
		Type:   model.ChannelTypeOpen,
		TeamId: teamID,
	}

	err = conversationService.HandleToolCall(requesterID, post, channel, []string{"tool-1"})
	require.NoError(t, err)

	resultKVKey := streaming.ToolResultPrivateKVKey(postID, requesterID)
	storedResults, ok := fakeClient.kv[resultKVKey]
	require.True(t, ok)
	resultCalls, ok := storedResults.([]llm.ToolCall)
	require.True(t, ok)
	require.Equal(t, "ok:secret", resultCalls[0].Result)
	require.Contains(t, string(resultCalls[0].Arguments), "secret")

	_, stillPresent := fakeClient.kv[toolCallKVKey]
	require.True(t, stillPresent)
	require.NotContains(t, fakeClient.kvDeletes, toolCallKVKey)

	toolCallProp, ok := post.GetProp(streaming.ToolCallProp).(string)
	require.True(t, ok)
	require.NotContains(t, toolCallProp, "secret")
	require.NotContains(t, toolCallProp, "ok:secret")

	var storedCalls []llm.ToolCall
	require.NoError(t, json.Unmarshal([]byte(toolCallProp), &storedCalls))
	require.Len(t, storedCalls, 1)
	require.Equal(t, "{}", string(storedCalls[0].Arguments))
	require.Empty(t, storedCalls[0].Result)
	require.Equal(t, "true", post.GetProp(streaming.ToolCallRedactedProp))
	require.Equal(t, "true", post.GetProp(streaming.PendingToolResultProp))
	require.Len(t, fakeClient.updatedPosts, 1)
}

func TestHandleToolCallPreservesResolvedToolCallsWhenApprovingPendingSubset(t *testing.T) {
	const (
		postID      = "post-id"
		channelID   = "channel-id"
		teamID      = "team-id"
		botID       = "bot-id"
		requesterID = "requester-id"
	)

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)

	siteName := "Mattermost"
	mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
	mockAPI.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil).Maybe()

	tool := llm.Tool{
		Name:        "create_post",
		Description: "test tool",
		Schema:      llm.NewJSONSchemaFromStruct[toolArgs](),
		Resolver: func(_ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
			var parsed toolArgs
			if err := args(&parsed); err != nil {
				return "", err
			}
			return "posted:" + parsed.Value, nil
		},
	}
	contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: []llm.Tool{tool}}, nil, &testConfigProvider{})

	botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
	bot := bots.NewBot(llm.BotConfig{ID: botID, Name: "test-bot"}, llm.ServiceConfig{}, &model.Bot{UserId: botID, Username: "test-bot"}, nil)
	botService.SetBotsForTesting([]*bots.Bot{bot})

	post := &model.Post{
		Id:        postID,
		UserId:    botID,
		ChannelId: channelID,
		CreateAt:  1,
	}
	post.AddProp(streaming.LLMRequesterUserID, requesterID)
	post.AddProp(streaming.AllowToolsInChannelProp, "true")

	toolCalls := []llm.ToolCall{
		{
			ID:        "tool-1",
			Name:      "get_channel_info",
			Arguments: json.RawMessage(`{"channel_id":"channel-123"}`),
			Result:    "existing lookup result",
			Status:    llm.ToolCallStatusSuccess,
		},
		{
			ID:        "tool-2",
			Name:      "create_post",
			Arguments: json.RawMessage(`{"value":"secret-message"}`),
			Status:    llm.ToolCallStatusPending,
		},
	}
	redactedToolCalls := streaming.RedactToolCalls(toolCalls)
	redactedJSON, err := json.Marshal(redactedToolCalls)
	require.NoError(t, err)
	post.AddProp(streaming.ToolCallProp, string(redactedJSON))

	postList := &model.PostList{
		Order: []string{postID},
		Posts: map[string]*model.Post{postID: post},
	}

	fakeClient := &fakeMMClient{
		users: map[string]*model.User{
			requesterID: {Id: requesterID, Locale: "en"},
			botID:       {Id: botID, Locale: "en"},
		},
		postThreads: map[string]*model.PostList{
			postID: postList,
		},
		kv: map[string]interface{}{},
	}
	toolCallKVKey := streaming.ToolCallPrivateKVKey(postID, requesterID)
	fakeClient.kv[toolCallKVKey] = toolCalls

	toolCallingConfig := &testToolCallingConfig{enableChannelMentionToolCalling: true}
	conversationService := conversations.New(nil, fakeClient, nil, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, toolCallingConfig)

	channel := &model.Channel{
		Id:     channelID,
		Type:   model.ChannelTypeOpen,
		TeamId: teamID,
	}

	err = conversationService.HandleToolCall(requesterID, post, channel, []string{"tool-2"})
	require.NoError(t, err)

	resultKVKey := streaming.ToolResultPrivateKVKey(postID, requesterID)
	storedResults, ok := fakeClient.kv[resultKVKey]
	require.True(t, ok)
	resultCalls, ok := storedResults.([]llm.ToolCall)
	require.True(t, ok)
	require.Len(t, resultCalls, 2)

	require.Equal(t, "tool-1", resultCalls[0].ID)
	require.Equal(t, llm.ToolCallStatusSuccess, resultCalls[0].Status)
	require.Equal(t, "existing lookup result", resultCalls[0].Result)

	require.Equal(t, "tool-2", resultCalls[1].ID)
	require.Equal(t, llm.ToolCallStatusSuccess, resultCalls[1].Status)
	require.Equal(t, "posted:secret-message", resultCalls[1].Result)
}

func TestHandleToolCallDMFollowupIncludesAutoRunTools(t *testing.T) {
	const (
		postID      = "post-id"
		channelID   = "channel-id"
		botID       = "bot-id"
		requesterID = "requester-id"
	)

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)

	siteName := "Mattermost"
	mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()

	tools := []llm.Tool{
		{
			Name:         "get_channel_info",
			Description:  "manual tool",
			Schema:       llm.NewJSONSchemaFromStruct[toolArgs](),
			ServerOrigin: mcp.EmbeddedClientKey,
			Resolver: func(_ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
				var parsed toolArgs
				if err := args(&parsed); err != nil {
					return "", err
				}
				return "info:" + parsed.Value, nil
			},
		},
		{
			Name:         "read_channel",
			Description:  "auto-run tool",
			Schema:       llm.NewJSONSchemaFromStruct[toolArgs](),
			ServerOrigin: mcp.EmbeddedClientKey,
			Resolver: func(_ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
				var parsed toolArgs
				if err := args(&parsed); err != nil {
					return "", err
				}
				return "read:" + parsed.Value, nil
			},
		},
	}
	contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: tools}, nil, &testConfigProvider{})
	promptSet, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	streamingService := &fakeStreamingService{}
	capturingLLM := &capturingLanguageModel{}

	botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
	bot := bots.NewBot(llm.BotConfig{ID: botID, Name: "test-bot"}, llm.ServiceConfig{}, &model.Bot{UserId: botID, Username: "test-bot"}, capturingLLM)
	botService.SetBotsForTesting([]*bots.Bot{bot})

	post := &model.Post{
		Id:        postID,
		UserId:    botID,
		ChannelId: channelID,
		CreateAt:  1,
	}
	post.AddProp(streaming.LLMRequesterUserID, requesterID)

	toolCalls := []llm.ToolCall{
		{
			ID:        "tool-1",
			Name:      "get_channel_info",
			Arguments: json.RawMessage(`{"value":"town-square"}`),
		},
	}
	toolsJSON, err := json.Marshal(toolCalls)
	require.NoError(t, err)
	post.AddProp(streaming.ToolCallProp, string(toolsJSON))

	postList := &model.PostList{
		Order: []string{postID},
		Posts: map[string]*model.Post{postID: post},
	}

	channel := &model.Channel{
		Id:   channelID,
		Type: model.ChannelTypeDirect,
		Name: botID + "__" + requesterID,
	}

	fakeClient := &fakeMMClient{
		users: map[string]*model.User{
			requesterID: {Id: requesterID, Username: "user", Locale: "en"},
			botID:       {Id: botID, Username: "bot", Locale: "en"},
		},
		postThreads: map[string]*model.PostList{
			postID: postList,
		},
		kv:       map[string]interface{}{},
		posts:    map[string]*model.Post{postID: post},
		channels: map[string]*model.Channel{channelID: channel},
	}

	conversationService := conversations.New(promptSet, fakeClient, streamingService, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, &testToolCallingConfig{})
	conversationService.SetToolPolicyChecker(streaming.ToolPolicyFunc(func(serverBaseURL, toolName string) (string, bool) {
		if serverBaseURL != mcp.EmbeddedClientKey {
			return mcp.ToolPolicyAsk, false
		}
		if toolName == "read_channel" {
			return mcp.ToolPolicyAutoRun, true
		}
		return mcp.ToolPolicyAsk, true
	}))

	err = conversationService.HandleToolCall(requesterID, post, channel, []string{"tool-1"})
	require.NoError(t, err)
	require.Equal(t, []string{llm.ToolAutoRunKey(mcp.EmbeddedClientKey, "read_channel")}, capturingLLM.autoRunTools)
	require.Len(t, streamingService.streamedPosts, 1)
}

func TestHandleToolCallChannelBlockedWhenConfigDisabled(t *testing.T) {
	postID := "post1"
	channelID := "channel1"
	requesterID := "user1"
	botID := "bot1"
	teamID := "team1"

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)

	siteName := "test"
	mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
	mockAPI.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil).Maybe()

	contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: []llm.Tool{}}, nil, &testConfigProvider{})

	botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
	bot := bots.NewBot(llm.BotConfig{ID: botID, Name: "test-bot"}, llm.ServiceConfig{}, &model.Bot{UserId: botID, Username: "test-bot"}, nil)
	botService.SetBotsForTesting([]*bots.Bot{bot})

	post := &model.Post{
		Id:        postID,
		UserId:    botID,
		ChannelId: channelID,
		CreateAt:  1,
	}
	post.AddProp(streaming.LLMRequesterUserID, requesterID)
	post.AddProp(streaming.AllowToolsInChannelProp, "true") // Post has prop but config is disabled

	toolCalls := []llm.ToolCall{
		{
			ID:        "tool-1",
			Name:      "test_tool",
			Arguments: json.RawMessage(`{"value":"secret"}`),
		},
	}
	redactedToolCalls := streaming.RedactToolCalls(toolCalls)
	redactedJSON, err := json.Marshal(redactedToolCalls)
	require.NoError(t, err)
	post.AddProp(streaming.ToolCallProp, string(redactedJSON))

	postList := &model.PostList{
		Order: []string{postID},
		Posts: map[string]*model.Post{postID: post},
	}

	fakeClient := &fakeMMClient{
		users: map[string]*model.User{
			requesterID: {Id: requesterID, Locale: "en"},
			botID:       {Id: botID, Locale: "en"},
		},
		postThreads: map[string]*model.PostList{
			postID: postList,
		},
		kv: map[string]interface{}{},
	}
	toolCallKVKey := streaming.ToolCallPrivateKVKey(postID, requesterID)
	fakeClient.kv[toolCallKVKey] = toolCalls

	// Create conversation service with config that disables channel tool calling
	toolCallingConfig := &testToolCallingConfig{enableChannelMentionToolCalling: false}
	conversationService := conversations.New(nil, fakeClient, nil, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, toolCallingConfig)

	channel := &model.Channel{
		Id:     channelID,
		Type:   model.ChannelTypeOpen,
		TeamId: teamID,
	}

	// Should return error because config flag is off
	err = conversationService.HandleToolCall(requesterID, post, channel, []string{"tool-1"})
	require.Error(t, err)
	require.ErrorIs(t, err, conversations.ErrChannelToolCallingDisabled)
}

func TestHandleToolCallChannelBlockedWhenPostPropMissing(t *testing.T) {
	postID := "post1"
	channelID := "channel1"
	requesterID := "user1"
	botID := "bot1"
	teamID := "team1"

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)

	siteName := "test"
	mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
	mockAPI.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil).Maybe()

	contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: []llm.Tool{}}, nil, &testConfigProvider{})

	botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
	bot := bots.NewBot(llm.BotConfig{ID: botID, Name: "test-bot"}, llm.ServiceConfig{}, &model.Bot{UserId: botID, Username: "test-bot"}, nil)
	botService.SetBotsForTesting([]*bots.Bot{bot})

	post := &model.Post{
		Id:        postID,
		UserId:    botID,
		ChannelId: channelID,
		CreateAt:  1,
	}
	post.AddProp(streaming.LLMRequesterUserID, requesterID)
	// NOTE: NOT setting AllowToolsInChannelProp - simulating old post without the prop

	toolCalls := []llm.ToolCall{
		{
			ID:        "tool-1",
			Name:      "test_tool",
			Arguments: json.RawMessage(`{"value":"secret"}`),
		},
	}
	redactedToolCalls := streaming.RedactToolCalls(toolCalls)
	redactedJSON, err := json.Marshal(redactedToolCalls)
	require.NoError(t, err)
	post.AddProp(streaming.ToolCallProp, string(redactedJSON))

	postList := &model.PostList{
		Order: []string{postID},
		Posts: map[string]*model.Post{postID: post},
	}

	fakeClient := &fakeMMClient{
		users: map[string]*model.User{
			requesterID: {Id: requesterID, Locale: "en"},
			botID:       {Id: botID, Locale: "en"},
		},
		postThreads: map[string]*model.PostList{
			postID: postList,
		},
		kv: map[string]interface{}{},
	}
	toolCallKVKey := streaming.ToolCallPrivateKVKey(postID, requesterID)
	fakeClient.kv[toolCallKVKey] = toolCalls

	// Create conversation service with config that enables channel tool calling
	toolCallingConfig := &testToolCallingConfig{enableChannelMentionToolCalling: true}
	conversationService := conversations.New(nil, fakeClient, nil, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, toolCallingConfig)

	channel := &model.Channel{
		Id:     channelID,
		Type:   model.ChannelTypeOpen,
		TeamId: teamID,
	}

	// Should return error because post doesn't have the allow_tools_in_channel prop
	err = conversationService.HandleToolCall(requesterID, post, channel, []string{"tool-1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool calling not allowed for this post")
}

func TestAutoExecuteApprovedToolCalls(t *testing.T) {
	const (
		postID      = "post-id"
		channelID   = "channel-id"
		teamID      = "team-id"
		botID       = "bot-id"
		requesterID = "requester-id"
	)

	t.Run("auto approved channel tools still require result review by default", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		client := pluginapi.NewClient(mockAPI, nil)
		licenseChecker := enterprise.NewLicenseChecker(client)

		siteName := "Mattermost"
		mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
		mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
		mockAPI.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil).Maybe()

		tool := llm.Tool{
			Name:        "test_tool",
			Description: "test tool",
			Schema:      llm.NewJSONSchemaFromStruct[toolArgs](),
			Resolver: func(_ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
				var parsed toolArgs
				if err := args(&parsed); err != nil {
					return "", err
				}
				return "result:" + parsed.Value, nil
			},
		}
		contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: []llm.Tool{tool}}, nil, &testConfigProvider{})

		botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
		bot := bots.NewBot(llm.BotConfig{ID: botID, Name: "test-bot"}, llm.ServiceConfig{}, &model.Bot{UserId: botID, Username: "test-bot"}, nil)
		botService.SetBotsForTesting([]*bots.Bot{bot})

		post := &model.Post{
			Id:        postID,
			UserId:    botID,
			ChannelId: channelID,
			CreateAt:  1,
		}
		post.AddProp(streaming.LLMRequesterUserID, requesterID)
		post.AddProp(streaming.AllowToolsInChannelProp, "true")
		post.AddProp(streaming.AutoApprovedToolCallProp, "true")

		toolCalls := []llm.ToolCall{
			{
				ID:        "tool-1",
				Name:      "test_tool",
				Arguments: json.RawMessage(`{"value":"auto-data"}`),
			},
		}
		// Set redacted tool calls on the post (as would happen in streaming)
		redactedToolCalls := streaming.RedactToolCalls(toolCalls)
		redactedJSON, err := json.Marshal(redactedToolCalls)
		require.NoError(t, err)
		post.AddProp(streaming.ToolCallProp, string(redactedJSON))
		post.AddProp(streaming.ToolCallRedactedProp, "true")

		postList := &model.PostList{
			Order: []string{postID},
			Posts: map[string]*model.Post{postID: post},
		}

		channel := &model.Channel{
			Id:     channelID,
			Type:   model.ChannelTypeOpen,
			TeamId: teamID,
		}

		fakeClient := &fakeMMClient{
			users: map[string]*model.User{
				requesterID: {Id: requesterID, Locale: "en"},
				botID:       {Id: botID, Locale: "en"},
			},
			posts:       map[string]*model.Post{postID: post},
			channels:    map[string]*model.Channel{channelID: channel},
			postThreads: map[string]*model.PostList{postID: postList},
			kv:          map[string]interface{}{},
		}

		// Store tool calls in KV (as streaming would)
		toolCallKVKey := streaming.ToolCallPrivateKVKey(postID, requesterID)
		fakeClient.kv[toolCallKVKey] = toolCalls

		toolCallingConfig := &testToolCallingConfig{enableChannelMentionToolCalling: true}
		conversationService := conversations.New(nil, fakeClient, nil, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, toolCallingConfig)

		// Call AutoExecuteApprovedToolCalls with pre-approved tool IDs.
		// Without the everywhere policy, channels should still land in result review.
		conversationService.AutoExecuteApprovedToolCalls(postID, requesterID, []string{"tool-1"})

		// Verify results stored in KV
		resultKVKey := streaming.ToolResultPrivateKVKey(postID, requesterID)
		storedResults, ok := fakeClient.kv[resultKVKey]
		require.True(t, ok, "tool results should be stored in KV")
		resultCalls, ok := storedResults.([]llm.ToolCall)
		require.True(t, ok)
		require.Len(t, resultCalls, 1)
		require.Equal(t, "result:auto-data", resultCalls[0].Result)
		require.Equal(t, llm.ToolCallStatusSuccess, resultCalls[0].Status)

		// Verify post updated with pending_tool_result
		require.NotEmpty(t, fakeClient.updatedPosts)
		lastUpdated := fakeClient.updatedPosts[len(fakeClient.updatedPosts)-1]
		require.Equal(t, "true", lastUpdated.GetProp(streaming.PendingToolResultProp))
		require.Nil(t, lastUpdated.GetProp(streaming.AutoShareToolResultProp))

		// Verify tool calls on post are still redacted
		toolCallProp, ok := lastUpdated.GetProp(streaming.ToolCallProp).(string)
		require.True(t, ok)
		require.NotContains(t, toolCallProp, "auto-data")
	})

	t.Run("auto run everywhere auto shares channel tool results", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		client := pluginapi.NewClient(mockAPI, nil)
		licenseChecker := enterprise.NewLicenseChecker(client)

		siteName := "Mattermost"
		mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
		mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
		mockAPI.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil).Maybe()

		tool := llm.Tool{
			Name:         "test_tool",
			Description:  "test tool",
			ServerOrigin: mcp.EmbeddedClientKey,
			Schema:       llm.NewJSONSchemaFromStruct[toolArgs](),
			Resolver: func(_ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
				var parsed toolArgs
				if err := args(&parsed); err != nil {
					return "", err
				}
				return "result:" + parsed.Value, nil
			},
		}
		contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: []llm.Tool{tool}}, nil, &testConfigProvider{})
		promptSet, err := llm.NewPrompts(prompts.PromptsFolder)
		require.NoError(t, err)

		streamingService := &fakeStreamingService{}
		capturingLLM := &capturingLanguageModel{}

		botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
		bot := bots.NewBot(llm.BotConfig{ID: botID, Name: "test-bot"}, llm.ServiceConfig{}, &model.Bot{UserId: botID, Username: "test-bot"}, capturingLLM)
		botService.SetBotsForTesting([]*bots.Bot{bot})

		post := &model.Post{
			Id:        postID,
			UserId:    botID,
			ChannelId: channelID,
			CreateAt:  1,
		}
		post.AddProp(streaming.LLMRequesterUserID, requesterID)
		post.AddProp(streaming.AllowToolsInChannelProp, "true")
		post.AddProp(streaming.AutoApprovedToolCallProp, "true")
		post.AddProp(streaming.AutoShareToolResultProp, "true")

		toolCalls := []llm.ToolCall{
			{
				ID:           "tool-1",
				Name:         "test_tool",
				ServerOrigin: mcp.EmbeddedClientKey,
				Arguments:    json.RawMessage(`{"value":"auto-data"}`),
			},
		}
		redactedToolCalls := streaming.RedactToolCalls(toolCalls)
		redactedJSON, err := json.Marshal(redactedToolCalls)
		require.NoError(t, err)
		post.AddProp(streaming.ToolCallProp, string(redactedJSON))
		post.AddProp(streaming.ToolCallRedactedProp, "true")
		post.AddProp(streaming.PendingToolResultProp, "true")

		postList := &model.PostList{
			Order: []string{postID},
			Posts: map[string]*model.Post{postID: post},
		}

		channel := &model.Channel{
			Id:     channelID,
			Type:   model.ChannelTypeOpen,
			TeamId: teamID,
		}

		fakeClient := &fakeMMClient{
			users: map[string]*model.User{
				requesterID: {Id: requesterID, Username: "user", Locale: "en"},
				botID:       {Id: botID, Username: "bot", Locale: "en"},
			},
			posts:       map[string]*model.Post{postID: post},
			channels:    map[string]*model.Channel{channelID: channel},
			postThreads: map[string]*model.PostList{postID: postList},
			kv:          map[string]interface{}{},
		}

		toolCallKVKey := streaming.ToolCallPrivateKVKey(postID, requesterID)
		fakeClient.kv[toolCallKVKey] = toolCalls

		toolCallingConfig := &testToolCallingConfig{enableChannelMentionToolCalling: true}
		conversationService := conversations.New(promptSet, fakeClient, streamingService, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, toolCallingConfig)
		conversationService.SetToolPolicyChecker(streaming.ToolPolicyFunc(func(serverBaseURL, toolName string) (string, bool) {
			if serverBaseURL == mcp.EmbeddedClientKey && toolName == "test_tool" {
				return mcp.ToolPolicyAutoRunEverywhere, true
			}
			return mcp.ToolPolicyAsk, true
		}))

		conversationService.AutoExecuteApprovedToolCalls(postID, requesterID, []string{"tool-1"})

		require.NotEmpty(t, fakeClient.updatedPosts)
		lastUpdated := fakeClient.updatedPosts[len(fakeClient.updatedPosts)-1]
		require.Nil(t, lastUpdated.GetProp(streaming.PendingToolResultProp))
		require.Nil(t, lastUpdated.GetProp(streaming.AutoShareToolResultProp))
		require.Nil(t, lastUpdated.GetProp(streaming.ToolCallRedactedProp))

		require.Len(t, streamingService.streamedPosts, 1)
		require.NotNil(t, capturingLLM.autoRunTools)

		resultKVKey := streaming.ToolResultPrivateKVKey(postID, requesterID)
		require.Contains(t, fakeClient.kvDeletes, resultKVKey)
		require.Contains(t, fakeClient.kvDeletes, toolCallKVKey)
	})

	t.Run("tool execution error - result still stored", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		client := pluginapi.NewClient(mockAPI, nil)
		licenseChecker := enterprise.NewLicenseChecker(client)

		siteName := "Mattermost"
		mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
		mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
		mockAPI.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil).Maybe()

		tool := llm.Tool{
			Name:        "failing_tool",
			Description: "tool that fails",
			Schema:      llm.NewJSONSchemaFromStruct[toolArgs](),
			Resolver: func(_ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
				return "", errors.New("tool execution failed")
			},
		}
		contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: []llm.Tool{tool}}, nil, &testConfigProvider{})

		botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
		bot := bots.NewBot(llm.BotConfig{ID: botID, Name: "test-bot"}, llm.ServiceConfig{}, &model.Bot{UserId: botID, Username: "test-bot"}, nil)
		botService.SetBotsForTesting([]*bots.Bot{bot})

		post := &model.Post{
			Id:        postID,
			UserId:    botID,
			ChannelId: channelID,
			CreateAt:  1,
		}
		post.AddProp(streaming.LLMRequesterUserID, requesterID)
		post.AddProp(streaming.AllowToolsInChannelProp, "true")
		post.AddProp(streaming.AutoApprovedToolCallProp, "true")

		toolCalls := []llm.ToolCall{
			{
				ID:        "tool-1",
				Name:      "failing_tool",
				Arguments: json.RawMessage(`{"value":"test"}`),
			},
		}
		redactedToolCalls := streaming.RedactToolCalls(toolCalls)
		redactedJSON, err := json.Marshal(redactedToolCalls)
		require.NoError(t, err)
		post.AddProp(streaming.ToolCallProp, string(redactedJSON))
		post.AddProp(streaming.ToolCallRedactedProp, "true")

		postList := &model.PostList{
			Order: []string{postID},
			Posts: map[string]*model.Post{postID: post},
		}

		channel := &model.Channel{
			Id:     channelID,
			Type:   model.ChannelTypeOpen,
			TeamId: teamID,
		}

		fakeClient := &fakeMMClient{
			users: map[string]*model.User{
				requesterID: {Id: requesterID, Locale: "en"},
				botID:       {Id: botID, Locale: "en"},
			},
			posts:       map[string]*model.Post{postID: post},
			channels:    map[string]*model.Channel{channelID: channel},
			postThreads: map[string]*model.PostList{postID: postList},
			kv:          map[string]interface{}{},
		}

		toolCallKVKey := streaming.ToolCallPrivateKVKey(postID, requesterID)
		fakeClient.kv[toolCallKVKey] = toolCalls

		toolCallingConfig := &testToolCallingConfig{enableChannelMentionToolCalling: true}
		conversationService := conversations.New(nil, fakeClient, nil, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, toolCallingConfig)

		conversationService.AutoExecuteApprovedToolCalls(postID, requesterID, []string{"tool-1"})

		// Tool had an error, but results should still be stored
		resultKVKey := streaming.ToolResultPrivateKVKey(postID, requesterID)
		storedResults, ok := fakeClient.kv[resultKVKey]
		require.True(t, ok, "tool results should be stored even on error")
		resultCalls, ok := storedResults.([]llm.ToolCall)
		require.True(t, ok)
		require.Len(t, resultCalls, 1)
		require.Equal(t, llm.ToolCallStatusError, resultCalls[0].Status)
		require.Equal(t, "tool execution failed", resultCalls[0].Result)
	})

	t.Run("missing post - logs error and returns", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		client := pluginapi.NewClient(mockAPI, nil)
		licenseChecker := enterprise.NewLicenseChecker(client)

		siteName := "Mattermost"
		mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
		mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()

		contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: []llm.Tool{}}, nil, &testConfigProvider{})
		botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)

		fakeClient := &fakeMMClient{
			posts: map[string]*model.Post{}, // empty - post not found
			kv:    map[string]interface{}{},
		}

		toolCallingConfig := &testToolCallingConfig{enableChannelMentionToolCalling: true}
		conversationService := conversations.New(nil, fakeClient, nil, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, toolCallingConfig)

		// Should not panic, just log error
		conversationService.AutoExecuteApprovedToolCalls("nonexistent-post", requesterID, []string{"tool-1"})

		// No posts updated since post was not found
		require.Empty(t, fakeClient.updatedPosts)
	})

	t.Run("missing KV data - logs error and returns", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		client := pluginapi.NewClient(mockAPI, nil)
		licenseChecker := enterprise.NewLicenseChecker(client)

		siteName := "Mattermost"
		mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
		mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()

		contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: []llm.Tool{}}, nil, &testConfigProvider{})
		botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)

		post := &model.Post{
			Id:        postID,
			UserId:    botID,
			ChannelId: channelID,
			CreateAt:  1,
		}
		post.AddProp(streaming.LLMRequesterUserID, requesterID)
		post.AddProp(streaming.AllowToolsInChannelProp, "true")

		channel := &model.Channel{
			Id:     channelID,
			Type:   model.ChannelTypeOpen,
			TeamId: teamID,
		}

		fakeClient := &fakeMMClient{
			posts:    map[string]*model.Post{postID: post},
			channels: map[string]*model.Channel{channelID: channel},
			kv:       map[string]interface{}{}, // empty - no tool calls stored
		}

		toolCallingConfig := &testToolCallingConfig{enableChannelMentionToolCalling: true}
		conversationService := conversations.New(nil, fakeClient, nil, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, toolCallingConfig)

		// Should not panic, just log error
		conversationService.AutoExecuteApprovedToolCalls(postID, requesterID, []string{"tool-1"})

		// No posts updated since KV data was missing
		require.Empty(t, fakeClient.updatedPosts)
	})
}

func TestHandleToolResultDoesNotContinueWhenNoToolCallSucceeded(t *testing.T) {
	const (
		postID      = "post-id"
		channelID   = "channel-id"
		teamID      = "team-id"
		botID       = "bot-id"
		requesterID = "requester-id"
	)

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)

	siteName := "Mattermost"
	mockAPI.On("GetConfig").Return(&model.Config{TeamSettings: model.TeamSettings{SiteName: &siteName}}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
	mockAPI.On("GetTeam", teamID).Return(&model.Team{Id: teamID}, nil).Maybe()

	contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{tools: []llm.Tool{}}, nil, &testConfigProvider{})

	botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil)
	bot := bots.NewBot(llm.BotConfig{ID: botID, Name: "test-bot"}, llm.ServiceConfig{}, &model.Bot{UserId: botID, Username: "test-bot"}, nil)
	botService.SetBotsForTesting([]*bots.Bot{bot})

	post := &model.Post{
		Id:        postID,
		UserId:    botID,
		ChannelId: channelID,
		CreateAt:  1,
	}
	post.AddProp(streaming.LLMRequesterUserID, requesterID)
	post.AddProp(streaming.AllowToolsInChannelProp, "true")
	post.AddProp(streaming.PendingToolResultProp, "true")

	// Tools with all errors - no successful tool call
	toolsWithErrors := []llm.ToolCall{
		{
			ID:        "tool-1",
			Name:      "failing_tool",
			Arguments: json.RawMessage(`{"value":"test"}`),
			Result:    "tool execution failed",
			Status:    llm.ToolCallStatusError,
		},
	}
	toolsJSON, err := json.Marshal(toolsWithErrors)
	require.NoError(t, err)
	post.AddProp(streaming.ToolCallProp, string(toolsJSON))

	postList := &model.PostList{
		Order: []string{postID},
		Posts: map[string]*model.Post{postID: post},
	}

	channel := &model.Channel{
		Id:     channelID,
		Type:   model.ChannelTypeOpen,
		TeamId: teamID,
	}

	resultKVKey := streaming.ToolResultPrivateKVKey(postID, requesterID)
	toolCallKVKey := streaming.ToolCallPrivateKVKey(postID, requesterID)

	fakeClient := &fakeMMClient{
		users: map[string]*model.User{
			requesterID: {Id: requesterID, Locale: "en"},
			botID:       {Id: botID, Locale: "en"},
		},
		posts:       map[string]*model.Post{postID: post},
		channels:    map[string]*model.Channel{channelID: channel},
		postThreads: map[string]*model.PostList{postID: postList},
		kv: map[string]interface{}{
			resultKVKey:   toolsWithErrors,
			toolCallKVKey: toolsWithErrors,
		},
	}

	toolCallingConfig := &testToolCallingConfig{enableChannelMentionToolCalling: true}
	// Nil streaming service: completeAndStreamToolResponse would panic if invoked
	conversationService := conversations.New(nil, fakeClient, nil, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, toolCallingConfig)

	err = conversationService.HandleToolResult(requesterID, post, channel, []string{"tool-1"})
	require.NoError(t, err)

	// Post was updated with final tool results
	require.Len(t, fakeClient.updatedPosts, 1)
	updatedPost := fakeClient.updatedPosts[0]
	require.Nil(t, updatedPost.GetProp(streaming.PendingToolResultProp))

	// KV entries were cleaned up (no continuation = no need to keep them)
	require.Contains(t, fakeClient.kvDeletes, resultKVKey)
	require.Contains(t, fakeClient.kvDeletes, toolCallKVKey)
}
