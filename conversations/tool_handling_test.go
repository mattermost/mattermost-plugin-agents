// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
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

func (c *fakeMMClient) KVDelete(key string) error {
	delete(c.kv, key)
	c.kvDeletes = append(c.kvDeletes, key)
	return nil
}

func (c *fakeMMClient) AddReaction(*model.Reaction) error {
	return errors.New("not implemented")
}

func (c *fakeMMClient) GetPost(string) (*model.Post, error) {
	return nil, errors.New("not implemented")
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

func (c *fakeMMClient) GetChannel(string) (*model.Channel, error) {
	return nil, errors.New("not implemented")
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
