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

func TestReactionToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_post_reactions bad id", func() (string, error) {
			return provider.toolGetPostReactions(mcpCtx, GetPostReactionsArgs{PostID: "bad"})
		}, "must be a valid ID"},
		{"add_reaction bad id", func() (string, error) {
			return provider.toolAddReaction(mcpCtx, AddReactionArgs{PostID: "bad", EmojiName: "x"})
		}, "must be a valid ID"},
		{"add_reaction empty emoji", func() (string, error) {
			return provider.toolAddReaction(mcpCtx, AddReactionArgs{PostID: model.NewId(), EmojiName: ""})
		}, "emoji_name cannot be empty"},
		{"remove_reaction bad id", func() (string, error) {
			return provider.toolRemoveReaction(mcpCtx, RemoveReactionArgs{PostID: "bad", EmojiName: "x"})
		}, "must be a valid ID"},
		{"search_custom_emoji empty", func() (string, error) {
			return provider.toolSearchCustomEmoji(mcpCtx, SearchCustomEmojiArgs{Term: ""})
		}, "term cannot be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestToolGetPostReactions(t *testing.T) {
	postID := model.NewId()
	user1 := model.NewId()
	user2 := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/%s/reactions", postID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.Reaction{
			{UserId: user1, PostId: postID, EmojiName: "thumbsup"},
			{UserId: user2, PostId: postID, EmojiName: "thumbsup"},
			{UserId: user1, PostId: postID, EmojiName: "tada"},
		})
	})
	mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.User{{Id: user1, Username: "alice"}, {Id: user2, Username: "bob"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetPostReactions(mcpCtx, GetPostReactionsArgs{PostID: postID})
	require.NoError(t, err)
	assert.Contains(t, out, ":thumbsup: (2)")
	assert.Contains(t, out, "alice")
	assert.Contains(t, out, "bob")
	assert.Contains(t, out, ":tada: (1)")
}

func TestToolAddReaction(t *testing.T) {
	postID := model.NewId()
	userID := model.NewId()

	mux := http.NewServeMux()
	var got model.Reaction
	mux.HandleFunc("/api/v4/reactions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&got)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolAddReaction(mcpCtx, AddReactionArgs{PostID: postID, EmojiName: ":smile:"})
	require.NoError(t, err)
	assert.Equal(t, "smile", got.EmojiName, "colons should be trimmed")
	assert.Equal(t, userID, got.UserId)
	assert.Contains(t, out, "Successfully added :smile:")
}

func TestToolListCustomEmoji(t *testing.T) {
	creatorID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/emoji", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.Emoji{{Id: model.NewId(), Name: "party_parrot", CreatorId: creatorID}})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", creatorID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: creatorID, Username: "maker"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolListCustomEmoji(mcpCtx, ListCustomEmojiArgs{})
	require.NoError(t, err)
	assert.Contains(t, out, ":party_parrot:")
	assert.Contains(t, out, "maker")
}
