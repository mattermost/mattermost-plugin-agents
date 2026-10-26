// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
)

// newPassthroughAccessChecker builds an ABAC checker that always reports
// no_policy, so tests exercise pure legacy permission behavior.
func newPassthroughAccessChecker() *accesscontrol.Checker {
	return accesscontrol.New(accesscontrol.PassthroughClient{}, nil, accesscontrol.NoMCPServerIDs, nil)
}

type fakeWebSocketEvent struct {
	event     string
	payload   map[string]any
	broadcast *model.WebsocketBroadcast
}

type fakeMMClient struct {
	users                map[string]*model.User
	postThreads          map[string]*model.PostList
	kv                   map[string]any
	createdPosts         []*model.Post
	allowCreatePost      bool
	updatedPosts         []*model.Post
	kvDeletes            []string
	posts                map[string]*model.Post
	channels             map[string]*model.Channel
	ephemeralPosts       []*model.Post
	ephemeralPostUserIDs []string
	fileInfos            map[string]*model.FileInfo
	files                map[string][]byte
	onUpdatePost         func(*model.Post)
	websocketEvents      []fakeWebSocketEvent
	// dmChannelID, when set, is the channel DM posts are created in.
	dmChannelID string
	dmPosts     []*model.Post
	// directChannelRequests counts GetDirectChannel calls, which create the
	// DM in production.
	directChannelRequests int
	// postErrors are returned by GetPost for their post IDs.
	postErrors map[string]error
	// unreadableChannels are channels HasPermissionToChannel denies.
	unreadableChannels map[string]bool

	// logMu guards logErrors and logWarns: background goroutines (e.g. title generation)
	// may log while the test goroutine reads.
	logMu     sync.Mutex
	logErrors []string
	logWarns  []string
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
	if !c.allowCreatePost {
		return errors.New("not implemented")
	}
	c.createdPosts = append(c.createdPosts, post.Clone())
	return nil
}

func (c *fakeMMClient) UpdatePost(post *model.Post) error {
	c.updatedPosts = append(c.updatedPosts, post.Clone())
	if c.onUpdatePost != nil {
		c.onUpdatePost(post)
	}
	return nil
}

func (c *fakeMMClient) KVGet(key string, value any) error {
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

func (c *fakeMMClient) KVSet(key string, value any) error {
	if c.kv == nil {
		c.kv = make(map[string]any)
	}
	c.kv[key] = value
	return nil
}

func (c *fakeMMClient) KVSetWithExpiry(key string, value any, _ time.Duration) error {
	return c.KVSet(key, value)
}

func (c *fakeMMClient) KVCompareAndSet(key string, oldValue, newValue any) (bool, error) {
	if c.kv == nil {
		c.kv = make(map[string]any)
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

func (c *fakeMMClient) KVCompareAndSetWithExpiry(key string, oldValue, newValue any, _ time.Duration) (bool, error) {
	return c.KVCompareAndSet(key, oldValue, newValue)
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
	if err := c.postErrors[postID]; err != nil {
		return nil, err
	}
	if c.posts != nil {
		if post, ok := c.posts[postID]; ok {
			return post, nil
		}
	}
	return nil, pluginapi.ErrNotFound
}

func (c *fakeMMClient) GetPostsSince(string, int64) (*model.PostList, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) GetPostsBefore(string, string, int, int) (*model.PostList, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) DM(senderID, _ string, post *model.Post) error {
	if c.dmChannelID == "" {
		return errors.New("not implemented")
	}
	if post.Id == "" {
		post.Id = model.NewId()
	}
	post.UserId = senderID
	post.ChannelId = c.dmChannelID
	c.dmPosts = append(c.dmPosts, post)
	return nil
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
	c.directChannelRequests++
	if c.dmChannelID == "" {
		return nil, errors.New("not implemented")
	}
	return c.GetChannel(c.dmChannelID)
}

func (c *fakeMMClient) PublishWebSocketEvent(event string, payload map[string]any, broadcast *model.WebsocketBroadcast) {
	c.websocketEvents = append(c.websocketEvents, fakeWebSocketEvent{
		event:     event,
		payload:   payload,
		broadcast: broadcast,
	})
}

func (c *fakeMMClient) GetConfig() *model.Config {
	return &model.Config{LocalizationSettings: model.LocalizationSettings{DefaultServerLocale: model.NewPointer("en")}}
}

func (c *fakeMMClient) LogError(msg string, _ ...any) {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	c.logErrors = append(c.logErrors, msg)
}

// loggedErrors returns a snapshot of the error-level log messages.
func (c *fakeMMClient) loggedErrors() []string {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	return append([]string(nil), c.logErrors...)
}

func (c *fakeMMClient) LogWarn(msg string, keyValuePairs ...any) {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	c.logWarns = append(c.logWarns, fmt.Sprintf("%s %v", msg, keyValuePairs))
}

// loggedWarnings returns a snapshot of the warn-level log lines, each the
// message followed by its key/value pairs.
func (c *fakeMMClient) loggedWarnings() []string {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	return append([]string(nil), c.logWarns...)
}

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

func (c *fakeMMClient) RolesGrantPermission([]string, string) bool {
	return false
}

func (c *fakeMMClient) PluginHTTP(*http.Request) *http.Response {
	return nil
}

func (c *fakeMMClient) LogDebug(string, ...any) {}

func (c *fakeMMClient) GetChannelByName(string, string, bool) (*model.Channel, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) HasPermissionToChannel(_, channelID string, _ *model.Permission) bool {
	return !c.unreadableChannels[channelID]
}

func (c *fakeMMClient) HasPermissionToFileAction(sessionID, _, _ string) bool {
	return sessionID != ""
}

func (c *fakeMMClient) GetFileInfo(fileID string) (*model.FileInfo, error) {
	if c.fileInfos != nil {
		if info, ok := c.fileInfos[fileID]; ok {
			return info, nil
		}
	}
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) GetFile(fileID string) (io.ReadCloser, error) {
	if data, ok := c.files[fileID]; ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) UploadFile(io.Reader, string, string) (*model.FileInfo, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeMMClient) SendEphemeralPost(userID string, post *model.Post) {
	c.ephemeralPosts = append(c.ephemeralPosts, post.Clone())
	c.ephemeralPostUserIDs = append(c.ephemeralPostUserIDs, userID)
}

type fakeStreamingService struct {
	streamedPosts []*model.Post
	// finished, when set, receives the ID of each post whose stream ends.
	finished chan string
}

func (s *fakeStreamingService) StreamToNewPost(_ context.Context, _ string, _ string, _ *llm.TextStreamResult, post *model.Post, _ string) error {
	s.streamedPosts = append(s.streamedPosts, post.Clone())
	return nil
}

func (s *fakeStreamingService) StreamToNewDM(context.Context, string, *llm.TextStreamResult, string, *model.Post, string) error {
	return nil
}

func (s *fakeStreamingService) StreamToPost(_ context.Context, stream *llm.TextStreamResult, _ *model.Post, _ string, _ string) {
	if stream != nil {
		_, _ = stream.ReadAll()
	}
}

func (s *fakeStreamingService) StreamContinuationToPost(_ context.Context, stream *llm.TextStreamResult, _ *model.Post, _ string, _ string) {
	if stream != nil {
		_, _ = stream.ReadAll()
	}
}

func (s *fakeStreamingService) StopStreaming(string) {}

func (s *fakeStreamingService) GetStreamingContext(inCtx context.Context, _ string) (context.Context, error) {
	return inCtx, nil
}

func (s *fakeStreamingService) FinishStreaming(postID string) {
	if s.finished != nil {
		s.finished <- postID
	}
}

type testToolProvider struct {
	tools []llm.Tool
}

func (p *testToolProvider) GetTools(_ *bots.Bot, _ *llm.Context) []llm.Tool {
	return p.tools
}

// testToolCallingConfig implements conversations.ConfigProvider for testing
type testToolCallingConfig struct {
	enableChannelMentionToolCalling bool
	enableAmbientAutoReply          bool
}

func (c *testToolCallingConfig) EnableChannelMentionToolCalling() bool {
	return c.enableChannelMentionToolCalling
}

func (c *testToolCallingConfig) EnableAmbientAutoReply() bool {
	return c.enableAmbientAutoReply
}

func (c *testToolCallingConfig) AllowNativeWebSearchInChannels() bool {
	return false
}

func (c *testToolCallingConfig) MCP() mcp.Config {
	return mcp.Config{}
}

// overrideMockLicense replaces GetLicense expectations so a LicenseChecker
// built over mockAPI reports the given license.
func overrideMockLicense(mockAPI *plugintest.API, license *model.License) {
	filtered := make([]*mock.Call, 0, len(mockAPI.ExpectedCalls))
	for _, call := range mockAPI.ExpectedCalls {
		if call.Method != "GetLicense" {
			filtered = append(filtered, call)
		}
	}
	mockAPI.ExpectedCalls = filtered
	mockAPI.On("GetLicense").Return(license).Maybe()
}
