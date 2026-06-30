// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelMemberToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_channel_member bad channel", func() (string, error) {
			return provider.toolGetChannelMember(mcpCtx, GetChannelMemberArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"get_channel_members_by_ids empty", func() (string, error) {
			return provider.toolGetChannelMembersByIds(mcpCtx, GetChannelMembersByIdsArgs{ChannelID: model.NewId()})
		}, "cannot be empty"},
		{"get_channel_members_by_status bad", func() (string, error) {
			return provider.toolGetChannelMembersByStatus(mcpCtx, GetChannelMembersByStatusArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"get_user_channel_memberships bad team", func() (string, error) {
			return provider.toolGetUserChannelMemberships(mcpCtx, GetUserChannelMembershipsArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"get_users_not_in_channel bad", func() (string, error) {
			return provider.toolGetUsersNotInChannel(mcpCtx, GetUsersNotInChannelArgs{TeamID: model.NewId(), ChannelID: "bad"})
		}, "must be a valid ID"},
		{"search_users_in_channel empty", func() (string, error) {
			return provider.toolSearchUsersInChannel(mcpCtx, SearchUsersInChannelArgs{Term: "", ChannelID: model.NewId()})
		}, "term cannot be empty"},
		{"list_sidebar_categories bad", func() (string, error) {
			return provider.toolListSidebarCategories(mcpCtx, ListSidebarCategoriesArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"add_channel_members empty", func() (string, error) {
			return provider.toolAddChannelMembers(mcpCtx, AddChannelMembersArgs{ChannelID: model.NewId()})
		}, "cannot be empty"},
		{"remove_channel_member bad", func() (string, error) {
			return provider.toolRemoveChannelMember(mcpCtx, RemoveChannelMemberArgs{ChannelID: model.NewId(), UserID: "bad"})
		}, "must be a valid ID"},
		{"set_channel_mute bad", func() (string, error) {
			return provider.toolSetChannelMute(mcpCtx, SetChannelMuteArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"set_channel_favorite bad", func() (string, error) {
			return provider.toolSetChannelFavorite(mcpCtx, SetChannelFavoriteArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"update_channel_notify_props no props", func() (string, error) {
			return provider.toolUpdateChannelNotifyProps(mcpCtx, UpdateChannelNotifyPropsArgs{ChannelID: model.NewId()})
		}, "provide at least one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestToolGetChannelMember(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/members/%s", channelID, userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.ChannelMember{ChannelId: channelID, UserId: userID, SchemeAdmin: true, SchemeUser: true, NotifyProps: model.StringMap{"mark_unread": "mention"}})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "carol"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolGetChannelMember(mcpCtx, GetChannelMemberArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Contains(t, out, "Role: admin")
	assert.Contains(t, out, "Muted: true")
	assert.Contains(t, out, "carol")
}

func TestToolAddChannelMembers(t *testing.T) {
	channelID := model.NewId()
	u1 := model.NewId()
	u2 := model.NewId()
	mux := http.NewServeMux()
	var body map[string]any
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/members", channelID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.ChannelMember{{ChannelId: channelID, UserId: u1}, {ChannelId: channelID, UserId: u2}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolAddChannelMembers(mcpCtx, AddChannelMembersArgs{ChannelID: channelID, UserIDs: []string{u1, u2}})
	require.NoError(t, err)
	assert.Contains(t, out, "Successfully added 2 user(s)")
}

func TestToolSetChannelMute(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	mux := http.NewServeMux()
	var gotProps map[string]string
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/members/%s/notify_props", channelID, userID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotProps)
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolSetChannelMute(mcpCtx, SetChannelMuteArgs{ChannelID: channelID, Muted: true})
	require.NoError(t, err)
	assert.Equal(t, model.ChannelMarkUnreadMention, gotProps["mark_unread"])
	assert.Contains(t, out, "muted")
}

func TestToolSetChannelFavorite(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	mux := http.NewServeMux()
	var gotPrefs model.Preferences
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/preferences", userID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotPrefs)
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolSetChannelFavorite(mcpCtx, SetChannelFavoriteArgs{ChannelID: channelID, Favorite: true})
	require.NoError(t, err)
	require.Len(t, gotPrefs, 1)
	assert.Equal(t, model.PreferenceCategoryFavoriteChannel, gotPrefs[0].Category)
	assert.Equal(t, channelID, gotPrefs[0].Name)
	assert.Contains(t, out, "favorited")
}
