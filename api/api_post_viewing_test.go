// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// viewingKVClient backs the real notifications service with an in-memory
// KV. The embedded mmapi.Client is nil: the viewing route must only touch KV.
type viewingKVClient struct {
	mmapi.Client
	kv  map[string][]byte
	ttl map[string]time.Duration
}

func newViewingKVClient() *viewingKVClient {
	return &viewingKVClient{kv: map[string][]byte{}, ttl: map[string]time.Duration{}}
}

func (c *viewingKVClient) KVGet(key string, value any) error {
	raw, ok := c.kv[key]
	if !ok {
		return mmapi.ErrKVNotFound
	}
	if out, ok := value.(*[]byte); ok {
		*out = raw
		return nil
	}
	return json.Unmarshal(raw, value)
}

// KVCompareAndSet follows the server: a nil oldValue requires the key to be
// absent, and a nil newValue deletes it.
func (c *viewingKVClient) KVCompareAndSet(key string, oldValue, newValue any) (bool, error) {
	return c.KVCompareAndSetWithExpiry(key, oldValue, newValue, 0)
}

func (c *viewingKVClient) KVCompareAndSetWithExpiry(key string, oldValue, newValue any, ttl time.Duration) (bool, error) {
	current, ok := c.kv[key]
	if oldValue == nil {
		if ok {
			return false, nil
		}
	} else {
		oldRaw, err := json.Marshal(oldValue)
		if err != nil {
			return false, err
		}
		if !ok || string(current) != string(oldRaw) {
			return false, nil
		}
	}
	if newValue == nil {
		delete(c.kv, key)
		delete(c.ttl, key)
		return true, nil
	}
	raw, err := json.Marshal(newValue)
	if err != nil {
		return false, err
	}
	c.kv[key] = raw
	c.ttl[key] = ttl
	return true, nil
}

func (c *viewingKVClient) GetPreferenceForUser(string, string, string) (model.Preference, error) {
	return model.Preference{}, errors.New("not used")
}

func (c *viewingKVClient) GetChannelMember(string, string) (*model.ChannelMember, error) {
	return nil, errors.New("not used")
}

func (c *viewingKVClient) SendPushNotification(*model.PushNotification, string) error {
	return errors.New("not used")
}

func (c *viewingKVClient) LogError(string, ...any) {}
func (c *viewingKVClient) LogWarn(string, ...any)  {}
func (c *viewingKVClient) LogDebug(string, ...any) {}

func TestHandleViewing(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID    = "post12345678901234567890ab"
		channelID = "chan12345678901234567890ab"
	)

	tests := []struct {
		name          string
		postUserID    string
		canRead       bool
		alreadyViewed bool
		viewedByTab   string
		// alsoViewedByTab is a second tab that reported viewing after viewedByTab.
		alsoViewedByTab string
		body            string
		wantStatus      int
		wantViewing     bool
	}{
		{name: "viewing true records the viewer", postUserID: testBotUserID, canRead: true, body: `{"viewing":true}`, wantStatus: http.StatusOK, wantViewing: true},
		{name: "viewing false clears the viewer", postUserID: testBotUserID, canRead: true, alreadyViewed: true, body: `{"viewing":false}`, wantStatus: http.StatusOK},
		{name: "viewing true from a tab records the viewer", postUserID: testBotUserID, canRead: true, body: `{"viewing":true,"tab_id":"tab-A_1"}`, wantStatus: http.StatusOK, wantViewing: true},
		{name: "viewing false from the viewing tab clears the viewer", postUserID: testBotUserID, canRead: true, alreadyViewed: true, viewedByTab: "tabA", body: `{"viewing":false,"tab_id":"tabA"}`, wantStatus: http.StatusOK},
		{name: "viewing false from another tab keeps the viewer", postUserID: testBotUserID, canRead: true, alreadyViewed: true, viewedByTab: "tabA", body: `{"viewing":false,"tab_id":"tabB"}`, wantStatus: http.StatusOK, wantViewing: true},
		{name: "viewing false from the latest tab keeps an earlier tab viewing", postUserID: testBotUserID, canRead: true, alreadyViewed: true, viewedByTab: "tabA", alsoViewedByTab: "tabB", body: `{"viewing":false,"tab_id":"tabB"}`, wantStatus: http.StatusOK, wantViewing: true},
		{name: "tab id with other characters is rejected", postUserID: testBotUserID, canRead: true, body: `{"viewing":true,"tab_id":"tab/../x"}`, wantStatus: http.StatusBadRequest},
		{name: "overlong tab id is rejected", postUserID: testBotUserID, canRead: true, body: `{"viewing":true,"tab_id":"` + strings.Repeat("a", 65) + `"}`, wantStatus: http.StatusBadRequest},
		{name: "non-string tab id is rejected", postUserID: testBotUserID, canRead: true, body: `{"viewing":true,"tab_id":7}`, wantStatus: http.StatusBadRequest},
		{name: "user who cannot read the channel is forbidden", postUserID: testBotUserID, body: `{"viewing":true}`, wantStatus: http.StatusForbidden},
		{name: "non-agent post is rejected", postUserID: testOtherUserID, canRead: true, body: `{"viewing":true}`, wantStatus: http.StatusBadRequest},
		{name: "missing viewing is rejected", postUserID: testBotUserID, canRead: true, alreadyViewed: true, body: `{}`, wantStatus: http.StatusBadRequest, wantViewing: true},
		{name: "non-boolean viewing is rejected", postUserID: testBotUserID, canRead: true, body: `{"viewing":"yes"}`, wantStatus: http.StatusBadRequest},
		{name: "malformed body is rejected", postUserID: testBotUserID, canRead: true, body: `not json`, wantStatus: http.StatusBadRequest},
		{name: "oversized body is rejected", postUserID: testBotUserID, canRead: true, body: `{"viewing":true,"pad":"` + strings.Repeat("x", maxViewingBodyBytes) + `"}`, wantStatus: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.setupTestBot(llm.BotConfig{Name: "thebot", DisplayName: "The Bot"})

			kv := newViewingKVClient()
			service := notifications.NewService(kv, i18n.Init(), true)
			e.api.SetNotifications(service)
			if tc.alreadyViewed {
				require.NoError(t, service.SetViewing(postID, testUserID, tc.viewedByTab, true))
			}
			if tc.alsoViewedByTab != "" {
				require.NoError(t, service.SetViewing(postID, testUserID, tc.alsoViewedByTab, true))
			}

			e.mockAPI.On("GetPost", postID).Return(&model.Post{Id: postID, UserId: tc.postUserID, ChannelId: channelID}, nil)
			e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen, TeamId: "teamid"}, nil)
			e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(tc.canRead)
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

			req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/viewing", strings.NewReader(tc.body))
			req.Header.Add("Mattermost-User-ID", testUserID)
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, rec, req)

			require.Equal(t, tc.wantStatus, rec.Result().StatusCode)
			viewing, err := service.IsViewing(postID, testUserID)
			require.NoError(t, err)
			require.Equal(t, tc.wantViewing, viewing)
			if tc.wantViewing && tc.wantStatus == http.StatusOK {
				require.Len(t, kv.ttl, 1)
				for _, ttl := range kv.ttl {
					require.Equal(t, notifications.ViewingTTL, ttl, "viewing expires without a heartbeat")
				}
			}
		})
	}
}

func TestHandleGetAIBotsReportsDeferredNotifications(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name          string
		notifications AgentPostNotifications
		want          bool
	}{
		{name: "supporting server", notifications: notifications.NewService(newViewingKVClient(), i18n.Init(), true), want: true},
		{name: "server without support", notifications: notifications.NewService(newViewingKVClient(), i18n.Init(), false)},
		{name: "notifications not wired"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.searchService = nil
			if tc.notifications != nil {
				e.api.SetNotifications(tc.notifications)
			}
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})
			e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			req := httptest.NewRequest(http.MethodGet, "/ai_bots", nil)
			req.Header.Add("Mattermost-User-ID", testUserID)
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, rec, req)

			require.Equal(t, http.StatusOK, rec.Result().StatusCode)
			var response AIBotsResponse
			require.NoError(t, json.NewDecoder(rec.Result().Body).Decode(&response))
			require.Equal(t, tc.want, response.DeferredNotifications)
		})
	}
}
