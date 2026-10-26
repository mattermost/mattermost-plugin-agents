// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package notifications

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

type deliverCall struct {
	postID  string
	userIDs []string
}

type pushCall struct {
	userID string
	msg    *model.PushNotification
}

type fakeClient struct {
	mu sync.Mutex

	kv      map[string][]byte
	kvTTL   map[string]time.Duration
	kvErr   error
	posts   map[string]*model.Post
	users   map[string]*model.User
	status  map[string]*model.Status
	chans   map[string]*model.Channel
	members map[string]*model.ChannelMember
	config  *model.Config

	deliverErr error
	delivered  []deliverCall
	pushes     []pushCall
	// onPush runs inside SendPushNotification, where the server runs the
	// NotificationWillBePushed hook.
	onPush func(msg *model.PushNotification, userID string) bool

	errorLogs []string
}

func newFakeClient() *fakeClient {
	cfg := &model.Config{}
	cfg.SetDefaults()
	return &fakeClient{
		kv:      map[string][]byte{},
		kvTTL:   map[string]time.Duration{},
		posts:   map[string]*model.Post{},
		users:   map[string]*model.User{},
		status:  map[string]*model.Status{},
		chans:   map[string]*model.Channel{},
		members: map[string]*model.ChannelMember{},
		config:  cfg,
	}
}

func (f *fakeClient) DeliverPostNotifications(postID string, options *model.DeliverPostNotificationsOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := deliverCall{postID: postID}
	if options != nil {
		call.userIDs = options.UserIDs
	}
	f.delivered = append(f.delivered, call)
	return f.deliverErr
}

func (f *fakeClient) KVGet(key string, value any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kvErr != nil {
		return f.kvErr
	}
	raw, ok := f.kv[key]
	if !ok {
		return mmapi.ErrKVNotFound
	}
	return json.Unmarshal(raw, value)
}

func (f *fakeClient) KVSetWithExpiry(key string, value any, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kvErr != nil {
		return f.kvErr
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f.kv[key] = raw
	f.kvTTL[key] = ttl
	return nil
}

func (f *fakeClient) KVDelete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kvErr != nil {
		return f.kvErr
	}
	delete(f.kv, key)
	delete(f.kvTTL, key)
	return nil
}

func (f *fakeClient) GetPost(postID string) (*model.Post, error) {
	if p, ok := f.posts[postID]; ok {
		return p, nil
	}
	return nil, errors.New("post not found")
}

func (f *fakeClient) GetUser(userID string) (*model.User, error) {
	if u, ok := f.users[userID]; ok {
		return u, nil
	}
	return nil, errors.New("user not found")
}

func (f *fakeClient) GetUserStatus(userID string) (*model.Status, error) {
	if s, ok := f.status[userID]; ok {
		return s, nil
	}
	return nil, errors.New("status not found")
}

func (f *fakeClient) GetChannel(channelID string) (*model.Channel, error) {
	if c, ok := f.chans[channelID]; ok {
		return c, nil
	}
	return nil, errors.New("channel not found")
}

func (f *fakeClient) GetChannelMember(channelID, userID string) (*model.ChannelMember, error) {
	if m, ok := f.members[channelID+userID]; ok {
		return m, nil
	}
	return nil, errors.New("member not found")
}

func (f *fakeClient) GetConfig() *model.Config { return f.config }

func (f *fakeClient) SendPushNotification(msg *model.PushNotification, userID string) error {
	if f.onPush != nil && !f.onPush(msg, userID) {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes = append(f.pushes, pushCall{userID: userID, msg: msg})
	return nil
}

func (f *fakeClient) LogError(msg string, _ ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errorLogs = append(f.errorLogs, msg)
}
func (f *fakeClient) LogWarn(string, ...any)  {}
func (f *fakeClient) LogDebug(string, ...any) {}
