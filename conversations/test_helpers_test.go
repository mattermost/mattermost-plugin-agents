// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

type fakeMMClient struct {
	users                map[string]*model.User
	postThreads          map[string]*model.PostList
	kv                   map[string]interface{}
	updatedPosts         []*model.Post
	kvDeletes            []string
	posts                map[string]*model.Post
	channels             map[string]*model.Channel
	ephemeralPosts       []*model.Post
	ephemeralPostUserIDs []string
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

func (c *fakeMMClient) CreatePost(_ *model.Post) error {
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

func (c *fakeMMClient) KVCompareAndSet(key string, oldValue, newValue interface{}) (bool, error) {
	if c.kv == nil {
		c.kv = make(map[string]interface{})
	}
	current, ok := c.kv[key]
	if oldValue == nil {
		if ok {
			return false, nil
		}
		c.kv[key] = newValue
		return true, nil
	}
	if !ok {
		return false, nil
	}
	currentJSON, err := json.Marshal(current)
	if err != nil {
		return false, err
	}
	oldJSON, err := json.Marshal(oldValue)
	if err != nil {
		return false, err
	}
	if string(currentJSON) != string(oldJSON) {
		return false, nil
	}
	c.kv[key] = newValue
	return true, nil
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

func (c *fakeMMClient) SendEphemeralPost(userID string, post *model.Post) {
	c.ephemeralPosts = append(c.ephemeralPosts, post.Clone())
	c.ephemeralPostUserIDs = append(c.ephemeralPostUserIDs, userID)
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

func (s *fakeStreamingService) StreamToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}

func (s *fakeStreamingService) StreamContinuationToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}

func (s *fakeStreamingService) StopStreaming(string) {}

func (s *fakeStreamingService) GetStreamingContext(inCtx context.Context, _ string) (context.Context, error) {
	return inCtx, nil
}

func (s *fakeStreamingService) FinishStreaming(string) {}

type testToolProvider struct {
	tools []llm.Tool
}

func (p *testToolProvider) GetTools(_ *bots.Bot) []llm.Tool {
	return p.tools
}

// testToolCallingConfig implements conversations.ConfigProvider for testing
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
