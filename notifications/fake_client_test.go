// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package notifications

import (
	"bytes"
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
	prefs   map[string]model.Preference
	config  *model.Config

	// onDeliver, when set, answers DeliverPostNotifications instead of
	// deliverErr.
	onDeliver  func(call deliverCall) error
	deliverErr error
	delivered  []deliverCall
	pushes     []pushCall
	// onPush runs inside SendPushNotification, where the server runs the
	// NotificationWillBePushed hook.
	onPush func(msg *model.PushNotification, userID string) bool

	errorLogs []string
	warnLogs  []string
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
		prefs:   map[string]model.Preference{},
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
	if f.onDeliver != nil {
		return f.onDeliver(call)
	}
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
	if out, ok := value.(*[]byte); ok {
		*out = raw
		return nil
	}
	return json.Unmarshal(raw, value)
}

// KVCompareAndSet follows the server: a nil newValue deletes the key when its
// value matches oldValue.
func (f *fakeClient) KVCompareAndSet(key string, oldValue, newValue any) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kvErr != nil {
		return false, f.kvErr
	}
	oldRaw, err := json.Marshal(oldValue)
	if err != nil {
		return false, err
	}
	current, ok := f.kv[key]
	if oldValue == nil {
		if ok {
			return false, nil
		}
	} else if !ok || !bytes.Equal(current, oldRaw) {
		return false, nil
	}
	if newValue == nil {
		delete(f.kv, key)
		delete(f.kvTTL, key)
		return true, nil
	}
	newRaw, err := json.Marshal(newValue)
	if err != nil {
		return false, err
	}
	f.kv[key] = newRaw
	return true, nil
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

func (f *fakeClient) GetPreferenceForUser(userID, category, name string) (model.Preference, error) {
	if p, ok := f.prefs[userID+category+name]; ok {
		return p, nil
	}
	return model.Preference{}, errors.New("preference not found")
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
func (f *fakeClient) LogWarn(msg string, _ ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.warnLogs = append(f.warnLogs, msg)
}
func (f *fakeClient) LogDebug(string, ...any) {}
