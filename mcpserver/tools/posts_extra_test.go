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

// TestPostToolsInvalidID verifies each post tool rejects a malformed post/channel ID.
func TestPostToolsInvalidID(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name string
		call func() (string, error)
	}{
		{"get_post_info", func() (string, error) { return provider.toolGetPostInfo(mcpCtx, GetPostInfoArgs{PostID: "bad"}) }},
		{"list_pinned_posts", func() (string, error) {
			return provider.toolListPinnedPosts(mcpCtx, ListPinnedPostsArgs{ChannelID: "bad"})
		}},
		{"update_post", func() (string, error) {
			return provider.toolUpdatePost(mcpCtx, UpdatePostArgs{PostID: "bad", Message: "x"})
		}},
		{"delete_post", func() (string, error) { return provider.toolDeletePost(mcpCtx, DeletePostArgs{PostID: "bad"}) }},
		{"pin_post", func() (string, error) { return provider.toolPinPost(mcpCtx, PinPostArgs{PostID: "bad"}) }},
		{"unpin_post", func() (string, error) { return provider.toolUnpinPost(mcpCtx, UnpinPostArgs{PostID: "bad"}) }},
		{"save_post", func() (string, error) { return provider.toolSavePost(mcpCtx, SavePostArgs{PostID: "bad"}) }},
		{"acknowledge_post", func() (string, error) {
			return provider.toolAcknowledgePost(mcpCtx, AcknowledgePostArgs{PostID: "bad"})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "must be a valid ID")
		})
	}
}

func TestToolUpdatePostEmptyMessage(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context()}

	_, err := provider.toolUpdatePost(mcpCtx, UpdatePostArgs{PostID: model.NewId(), Message: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message cannot be empty")
}

func TestToolGetPostInfo(t *testing.T) {
	postID := model.NewId()
	channelID := model.NewId()
	teamID := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/%s/info", postID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.PostInfo{
			ChannelId:          channelID,
			ChannelType:        model.ChannelTypeOpen,
			ChannelDisplayName: "General",
			TeamId:             teamID,
			TeamDisplayName:    "Engineering",
			HasJoinedChannel:   true,
			HasJoinedTeam:      true,
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetPostInfo(mcpCtx, GetPostInfoArgs{PostID: postID})
	require.NoError(t, err)
	assert.Contains(t, out, channelID)
	assert.Contains(t, out, "Engineering")
	assert.Contains(t, out, "General")
}

func TestToolPinPost(t *testing.T) {
	postID := model.NewId()
	mux := http.NewServeMux()
	var hit bool
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/%s/pin", postID), func(w http.ResponseWriter, r *http.Request) {
		hit = true
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolPinPost(mcpCtx, PinPostArgs{PostID: postID})
	require.NoError(t, err)
	assert.True(t, hit, "pin endpoint should be called")
	assert.Contains(t, out, "Successfully pinned")
}

func TestToolUpdatePost(t *testing.T) {
	postID := model.NewId()
	channelID := model.NewId()

	mux := http.NewServeMux()
	var gotMessage string
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/%s", postID), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(&model.Post{Id: postID, ChannelId: channelID, Message: "old"})
		case http.MethodPut:
			var p model.Post
			_ = json.NewDecoder(r.Body).Decode(&p)
			gotMessage = p.Message
			_ = json.NewEncoder(w).Encode(&model.Post{Id: postID, ChannelId: channelID, Message: p.Message})
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolUpdatePost(mcpCtx, UpdatePostArgs{PostID: postID, Message: "new message"})
	require.NoError(t, err)
	assert.Equal(t, "new message", gotMessage)
	assert.Contains(t, out, "Successfully updated post")
}

func TestToolSavePost(t *testing.T) {
	postID := model.NewId()
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

	out, err := provider.toolSavePost(mcpCtx, SavePostArgs{PostID: postID})
	require.NoError(t, err)
	require.Len(t, gotPrefs, 1)
	assert.Equal(t, model.PreferenceCategoryFlaggedPost, gotPrefs[0].Category)
	assert.Equal(t, postID, gotPrefs[0].Name)
	assert.Contains(t, out, "Successfully saved post")
}

func TestToolListPinnedPosts(t *testing.T) {
	channelID := model.NewId()
	authorID := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/pinned", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.PostList{
			Order: []string{"p1"},
			Posts: map[string]*model.Post{"p1": {Id: "p1", ChannelId: channelID, UserId: authorID, Message: "pinned!", CreateAt: 5}},
		})
	})
	mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.User{{Id: authorID, Username: "pinner"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolListPinnedPosts(mcpCtx, ListPinnedPostsArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Contains(t, out, "pinned!")
	assert.Contains(t, out, "pinner")
}
