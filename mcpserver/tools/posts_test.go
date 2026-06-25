// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readPostThreadServer builds a mock Mattermost server whose thread endpoint
// returns a thread of threadSize posts authored by a single user.
func readPostThreadServer(t *testing.T, rootID, channelID, teamID, userID string, threadSize int) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/%s/thread", rootID), func(w http.ResponseWriter, r *http.Request) {
		list := &model.PostList{Order: make([]string, 0, threadSize), Posts: make(map[string]*model.Post, threadSize)}
		for i := 0; i < threadSize; i++ {
			id := fmt.Sprintf("tpost%022d", i)
			rootRef := rootID
			if i == 0 {
				rootRef = ""
			}
			list.Order = append(list.Order, id)
			list.Posts[id] = &model.Post{Id: id, ChannelId: channelID, UserId: userID, RootId: rootRef, Message: fmt.Sprintf("reply %d", i), CreateAt: int64(1000 + i)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, Name: "general", DisplayName: "General", Type: model.ChannelTypeOpen, TeamId: teamID})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/teams/%s", teamID), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Team{Id: teamID, Name: "eng", DisplayName: "Engineering"})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", userID), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "author"})
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestToolReadPostThreadPagination(t *testing.T) {
	rootID := model.NewId()
	channelID := model.NewId()
	teamID := model.NewId()
	userID := model.NewId()
	const threadSize = 10

	tests := []struct {
		name           string
		args           string
		expectHeader   string
		expectPostNums []string
		expectMoreHint bool
		expectError    bool
		expectMsg      string
	}{
		{
			name:           "no pagination returns whole thread",
			args:           fmt.Sprintf(`{"post_id":%q}`, rootID),
			expectHeader:   fmt.Sprintf("Thread with %d posts:", threadSize),
			expectPostNums: []string{"Post 1", "Post 10"},
			expectMoreHint: false,
		},
		{
			name:           "first page is limited and hints at more",
			args:           fmt.Sprintf(`{"post_id":%q,"per_page":4}`, rootID),
			expectHeader:   fmt.Sprintf("Thread with %d posts (page 0, showing 4):", threadSize),
			expectPostNums: []string{"Post 1", "Post 4"},
			expectMoreHint: true,
		},
		{
			name:           "second page continues numbering",
			args:           fmt.Sprintf(`{"post_id":%q,"per_page":4,"page":1}`, rootID),
			expectHeader:   fmt.Sprintf("Thread with %d posts (page 1, showing 4):", threadSize),
			expectPostNums: []string{"Post 5", "Post 8"},
			expectMoreHint: true,
		},
		{
			name:           "final partial page has no hint",
			args:           fmt.Sprintf(`{"post_id":%q,"per_page":4,"page":2}`, rootID),
			expectHeader:   fmt.Sprintf("Thread with %d posts (page 2, showing 2):", threadSize),
			expectPostNums: []string{"Post 9", "Post 10"},
			expectMoreHint: false,
		},
		{
			name:        "page beyond the thread reports emptiness",
			args:        fmt.Sprintf(`{"post_id":%q,"per_page":4,"page":5}`, rootID),
			expectError: false,
			expectMsg:   fmt.Sprintf("no posts found on page 5 (thread has %d posts)", threadSize),
		},
		{
			name:           "negative page clamps to zero",
			args:           fmt.Sprintf(`{"post_id":%q,"per_page":4,"page":-2}`, rootID),
			expectHeader:   fmt.Sprintf("Thread with %d posts (page 0, showing 4):", threadSize),
			expectPostNums: []string{"Post 1", "Post 4"},
			expectMoreHint: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := readPostThreadServer(t, rootID, channelID, teamID, userID, threadSize)
			provider := newTestProvider(t, ts.URL)
			client := newTestClient(ts.URL)
			mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context()}

			argsGetter := func(target any) error {
				return json.Unmarshal([]byte(tt.args), target)
			}

			out, err := provider.toolReadPost(mcpCtx, argsGetter)
			require.NoError(t, err)

			if tt.expectMsg != "" {
				assert.Equal(t, tt.expectMsg, out)
				return
			}

			assert.Contains(t, out, tt.expectHeader)
			for _, label := range tt.expectPostNums {
				assert.Contains(t, out, label)
			}
			if tt.expectMoreHint {
				assert.Contains(t, out, "More posts in this thread")
			} else {
				assert.NotContains(t, out, "More posts in this thread")
			}
		})
	}
}

// TestToolReadPostThreadPerPageCap verifies per_page is clamped to the 200 max,
// so a single page never returns more than the cap even when a larger value is
// requested for an oversized thread.
func TestToolReadPostThreadPerPageCap(t *testing.T) {
	rootID := model.NewId()
	channelID := model.NewId()
	teamID := model.NewId()
	userID := model.NewId()
	const threadSize = 205

	ts := readPostThreadServer(t, rootID, channelID, teamID, userID, threadSize)
	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context()}

	argsGetter := func(target any) error {
		return json.Unmarshal([]byte(fmt.Sprintf(`{"post_id":%q,"per_page":500}`, rootID)), target)
	}

	out, err := provider.toolReadPost(mcpCtx, argsGetter)
	require.NoError(t, err)

	assert.Contains(t, out, fmt.Sprintf("Thread with %d posts (page 0, showing 200):", threadSize),
		"per_page above 200 should be clamped to 200 posts per page")
	assert.Contains(t, out, "More posts in this thread", "a capped first page should hint at more posts")
}

func TestToolReadPostThreadPageOverflow(t *testing.T) {
	rootID := model.NewId()
	channelID := model.NewId()
	teamID := model.NewId()
	userID := model.NewId()
	const threadSize = 10

	ts := readPostThreadServer(t, rootID, channelID, teamID, userID, threadSize)
	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context()}

	argsGetter := func(target any) error {
		return json.Unmarshal([]byte(fmt.Sprintf(`{"post_id":%q,"per_page":4,"page":%d}`, rootID, math.MaxInt64)), target)
	}

	out, err := provider.toolReadPost(mcpCtx, argsGetter)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("no posts found on page %d (thread has %d posts)", math.MaxInt64, threadSize), out)
}
