// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi

import (
	"io"
	"net/http"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type Client interface {
	GetUser(userID string) (*model.User, error)
	GetPost(postID string) (*model.Post, error)
	AddReaction(*model.Reaction) error
	GetPostThread(postID string) (*model.PostList, error)
	GetPostsSince(channelID string, since int64) (*model.PostList, error)
	GetPostsBefore(channelID, postID string, page, perPage int) (*model.PostList, error)
	CreatePost(post *model.Post) error
	UpdatePost(post *model.Post) error
	DM(senderID, receiverID string, post *model.Post) error
	GetChannel(channelID string) (*model.Channel, error)
	GetDirectChannel(userID1, userID2 string) (*model.Channel, error)
	PublishWebSocketEvent(event string, payload map[string]interface{}, broadcast *model.WebsocketBroadcast)
	GetConfig() *model.Config
	LogError(msg string, keyValuePairs ...interface{})
	LogWarn(msg string, keyValuePairs ...interface{})
	KVGet(key string, value interface{}) error
	KVSet(key string, value interface{}) error
	KVDelete(key string) error
	GetUserByUsername(username string) (*model.User, error)
	GetUserStatus(userID string) (*model.Status, error)
	HasPermissionTo(userID string, permission *model.Permission) bool
	GetPluginStatus(pluginID string) (*model.PluginStatus, error)
	PluginHTTP(req *http.Request) *http.Response
	LogDebug(msg string, keyValuePairs ...interface{})
	GetChannelByName(teamID, name string, includeDeleted bool) (*model.Channel, error)
	HasPermissionToChannel(userID, channelID string, permission *model.Permission) bool
	GetFileInfo(fileID string) (*model.FileInfo, error)
	GetFile(fileID string) (io.ReadCloser, error)
	SendEphemeralPost(userID string, post *model.Post)
}

func NewClient(pluginAPI *pluginapi.Client) Client {
	return &client{
		PostService:          pluginAPI.Post,
		UserService:          pluginAPI.User,
		FrontendService:      pluginAPI.Frontend,
		ConfigurationService: pluginAPI.Configuration,
		pluginAPI:            pluginAPI,
	}
}

type client struct {
	pluginapi.PostService
	pluginapi.UserService
	pluginapi.FrontendService
	pluginapi.ConfigurationService
	pluginAPI *pluginapi.Client
}

func (m *client) GetUser(userID string) (*model.User, error) {
	return m.pluginAPI.User.Get(userID)
}

func (m *client) GetChannel(channelID string) (*model.Channel, error) {
	return m.pluginAPI.Channel.Get(channelID)
}

func (m *client) GetDirectChannel(userID1, userID2 string) (*model.Channel, error) {
	return m.pluginAPI.Channel.GetDirect(userID1, userID2)
}

func (m *client) LogError(msg string, keyValuePairs ...interface{}) {
	m.pluginAPI.Log.Error(msg, keyValuePairs...)
}

func (m *client) LogWarn(msg string, keyValuePairs ...interface{}) {
	m.pluginAPI.Log.Warn(msg, keyValuePairs...)
}

func (m *client) KVGet(key string, value interface{}) error {
	return m.pluginAPI.KV.Get(key, value)
}

func (m *client) KVSet(key string, value interface{}) error {
	_, err := m.pluginAPI.KV.Set(key, value)
	return err
}

func (m *client) KVDelete(key string) error {
	return m.pluginAPI.KV.Delete(key)
}

func (m *client) GetUserByUsername(username string) (*model.User, error) {
	return m.pluginAPI.User.GetByUsername(username)
}

func (m *client) GetUserStatus(userID string) (*model.Status, error) {
	return m.pluginAPI.User.GetStatus(userID)
}

func (m *client) GetPluginStatus(pluginID string) (*model.PluginStatus, error) {
	return m.pluginAPI.Plugin.GetPluginStatus(pluginID)
}

func (m *client) PluginHTTP(req *http.Request) *http.Response {
	return m.pluginAPI.Plugin.HTTP(req)
}

func (m *client) LogDebug(msg string, keyValuePairs ...interface{}) {
	m.pluginAPI.Log.Debug(msg, keyValuePairs...)
}

func (m *client) GetChannelByName(teamID, name string, includeDeleted bool) (*model.Channel, error) {
	return m.pluginAPI.Channel.GetByName(teamID, name, includeDeleted)
}

func (m *client) GetFileInfo(fileID string) (*model.FileInfo, error) {
	return m.pluginAPI.File.GetInfo(fileID)
}

func (m *client) GetFile(fileID string) (io.ReadCloser, error) {
	file, err := m.pluginAPI.File.Get(fileID)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(file), nil
}

func (m *client) SendEphemeralPost(userID string, post *model.Post) {
	m.PostService.SendEphemeralPost(userID, post)
}
