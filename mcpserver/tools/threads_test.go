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

func TestThreadToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_threads bad team", func() (string, error) { return provider.toolGetThreads(mcpCtx, GetThreadsArgs{TeamID: "bad"}) }, "must be a valid ID"},
		{"get_channel_unread bad", func() (string, error) {
			return provider.toolGetChannelUnread(mcpCtx, GetChannelUnreadArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"get_posts_around_unread bad", func() (string, error) {
			return provider.toolGetPostsAroundUnread(mcpCtx, GetPostsAroundUnreadArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"mark_channel_read bad", func() (string, error) {
			return provider.toolMarkChannelRead(mcpCtx, MarkChannelReadArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"mark_channels_viewed empty", func() (string, error) {
			return provider.toolMarkChannelsViewed(mcpCtx, MarkChannelsViewedArgs{})
		}, "cannot be empty"},
		{"mark_post_unread bad", func() (string, error) {
			return provider.toolMarkPostUnread(mcpCtx, MarkPostUnreadArgs{PostID: "bad"})
		}, "must be a valid ID"},
		{"set_thread_follow bad thread", func() (string, error) {
			return provider.toolSetThreadFollow(mcpCtx, SetThreadFollowArgs{TeamID: model.NewId(), ThreadID: "bad"})
		}, "must be a valid ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestMentionKeywords(t *testing.T) {
	user := &model.User{Username: "john", NotifyProps: model.StringMap{"mention_keys": "john,@john,jb, "}}
	got := mentionKeywords(user)
	// @john (username) deduped with the mention_keys @john; jb added; blanks dropped.
	assert.Contains(t, got, "@john")
	assert.Contains(t, got, "john")
	assert.Contains(t, got, "jb")
	for _, k := range got {
		assert.NotEqual(t, "", k)
	}
}

func TestToolGetThreads(t *testing.T) {
	teamID := model.NewId()
	userID := model.NewId()
	authorID := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams/%s/threads", userID, teamID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Threads{
			Total:               1,
			TotalUnreadThreads:  1,
			TotalUnreadMentions: 2,
			Threads: []*model.ThreadResponse{{
				PostId:        "root123",
				ReplyCount:    3,
				UnreadReplies: 1,
				LastReplyAt:   1700000000000,
				Post:          &model.Post{Id: "root123", UserId: authorID, Message: "thread root"},
			}},
		})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", authorID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: authorID, Username: "starter"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolGetThreads(mcpCtx, GetThreadsArgs{TeamID: teamID})
	require.NoError(t, err)
	assert.Contains(t, out, "Root Post ID: root123")
	assert.Contains(t, out, "thread root")
	assert.Contains(t, out, "starter")
}

func TestToolGetChannelUnread(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	teamID := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/channels/%s/unread", userID, channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.ChannelUnread{ChannelId: channelID, TeamId: teamID, MsgCount: 7, MentionCount: 2})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolGetChannelUnread(mcpCtx, GetChannelUnreadArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Contains(t, out, "Unread messages: 7")
	assert.Contains(t, out, "Mentions: 2")
}

func TestToolMarkChannelRead(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()

	mux := http.NewServeMux()
	var gotView model.ChannelView
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/members/%s/view", userID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotView)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.ChannelViewResponse{Status: "OK"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolMarkChannelRead(mcpCtx, MarkChannelReadArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Equal(t, channelID, gotView.ChannelId)
	assert.Contains(t, out, "Successfully marked channel")
}
