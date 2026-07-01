// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
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

// boolPtr returns a pointer to b for use in optional *bool args.
func boolPtr(b bool) *bool {
	return &b
}

func TestStampAIGenerated(t *testing.T) {
	botID := model.NewId()
	fallbackID := model.NewId()
	getMeID := model.NewId()

	// Server that stubs /api/v4/users/me for the GetMe fallback case.
	meServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/users/me" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": getMeID})
			return
		}
		http.NotFound(w, r)
	}))
	defer meServer.Close()

	tests := []struct {
		name             string
		trackAIGenerated bool
		mcpContext       *MCPToolContext
		fallbackUserID   string
		wantProp         any // nil means the prop must be absent
	}{
		{
			name:             "tracking disabled is a no-op",
			trackAIGenerated: false,
			mcpContext:       &MCPToolContext{BotUserID: botID},
			fallbackUserID:   fallbackID,
			wantProp:         nil,
		},
		{
			name:             "valid bot user id wins",
			trackAIGenerated: true,
			mcpContext:       &MCPToolContext{BotUserID: botID},
			fallbackUserID:   fallbackID,
			wantProp:         botID,
		},
		{
			name:             "invalid bot user id falls back to fallbackUserID",
			trackAIGenerated: true,
			mcpContext:       &MCPToolContext{BotUserID: "notanid"},
			fallbackUserID:   fallbackID,
			wantProp:         fallbackID,
		},
		{
			name:             "no bot or fallback falls back to GetMe",
			trackAIGenerated: true,
			mcpContext:       &MCPToolContext{Ctx: context.Background(), Client: newTestClient(meServer.URL)},
			fallbackUserID:   "",
			wantProp:         getMeID,
		},
		{
			name:             "no id available leaves prop absent without panic",
			trackAIGenerated: true,
			mcpContext:       &MCPToolContext{Ctx: context.Background(), Client: nil},
			fallbackUserID:   "",
			wantProp:         nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MattermostToolProvider{trackAIGenerated: tt.trackAIGenerated, logger: &testLogger{t: t}}
			post := &model.Post{}

			require.NotPanics(t, func() {
				p.stampAIGenerated(post, tt.mcpContext, tt.fallbackUserID)
			})

			got := post.GetProp("ai_generated_by")
			if tt.wantProp == nil {
				assert.Nil(t, got, "ai_generated_by prop should be absent")
			} else {
				assert.Equal(t, tt.wantProp, got)
			}
		})
	}
}

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

func unmarshalReadPostArgs(t *testing.T, argsJSON string) ReadPostArgs {
	t.Helper()
	var args ReadPostArgs
	require.NoError(t, json.Unmarshal([]byte(argsJSON), &args))
	return args
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
			mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID}

			out, err := provider.toolReadPost(mcpCtx, unmarshalReadPostArgs(t, tt.args))
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
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID}

	out, err := provider.toolReadPost(mcpCtx, unmarshalReadPostArgs(t, fmt.Sprintf(`{"post_id":%q,"per_page":500}`, rootID)))
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
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID}

	out, err := provider.toolReadPost(mcpCtx, unmarshalReadPostArgs(t, fmt.Sprintf(`{"post_id":%q,"per_page":4,"page":%d}`, rootID, math.MaxInt64)))
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("no posts found on page %d (thread has %d posts)", math.MaxInt64, threadSize), out)
}

// newTestReadPostServer builds an httptest server stubbing the endpoints
// toolReadPost touches. The thread endpoint returns the root post plus a
// sibling reply; the single-post endpoint returns only the root post. Which
// endpoint was hit is recorded on the returned *readPostHits.
func newTestReadPostServer(t *testing.T, rootID, channelID, teamID, userID string) (*httptest.Server, *readPostHits) {
	t.Helper()

	hits := &readPostHits{}
	siblingID := model.NewId()

	rootPost := &model.Post{Id: rootID, ChannelId: channelID, UserId: userID, Message: rootPostMessage}
	siblingPost := &model.Post{Id: siblingID, ChannelId: channelID, UserId: userID, RootId: rootID, Message: siblingPostMessage}

	mux := http.NewServeMux()

	// Thread endpoint: /api/v4/posts/{id}/thread
	mux.HandleFunc("/api/v4/posts/"+rootID+"/thread", func(w http.ResponseWriter, r *http.Request) {
		hits.thread = true
		postList := &model.PostList{
			Order: []string{rootID, siblingID},
			Posts: map[string]*model.Post{rootID: rootPost, siblingID: siblingPost},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(postList)
	})

	// Single-post endpoint: /api/v4/posts/{id}
	mux.HandleFunc("/api/v4/posts/"+rootID, func(w http.ResponseWriter, r *http.Request) {
		hits.single = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rootPost)
	})

	// Formatting stubs.
	mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, TeamId: teamID, DisplayName: "Town Square"})
	})
	mux.HandleFunc("/api/v4/teams/"+teamID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Team{Id: teamID, DisplayName: "Core Team"})
	})
	mux.HandleFunc("/api/v4/users/"+userID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "alice"})
	})

	return httptest.NewServer(mux), hits
}

type readPostHits struct {
	thread bool
	single bool
}

const (
	rootPostMessage    = "this is the root post"
	siblingPostMessage = "this is a thread reply sibling"
)

func TestReadPostIncludeThread(t *testing.T) {
	rootID := model.NewId()
	channelID := model.NewId()
	teamID := model.NewId()
	userID := model.NewId()

	tests := []struct {
		name          string
		includeThread *bool
		wantThreadHit bool
		wantSingleHit bool
	}{
		{
			name:          "nil includes the thread",
			includeThread: nil,
			wantThreadHit: true,
			wantSingleHit: false,
		},
		{
			name:          "true includes the thread",
			includeThread: boolPtr(true),
			wantThreadHit: true,
			wantSingleHit: false,
		},
		{
			name:          "false fetches only the single post",
			includeThread: boolPtr(false),
			wantThreadHit: false,
			wantSingleHit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, hits := newTestReadPostServer(t, rootID, channelID, teamID, userID)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: newTestClient(ts.URL)}

			result, err := provider.toolReadPost(mcpCtx, ReadPostArgs{PostID: rootID, IncludeThread: tt.includeThread})
			require.NoError(t, err)

			assert.Equal(t, tt.wantThreadHit, hits.thread, "thread endpoint hit")
			assert.Equal(t, tt.wantSingleHit, hits.single, "single-post endpoint hit")

			// The root post message is always present.
			assert.Contains(t, result, rootPostMessage)

			if tt.wantThreadHit {
				// The thread sibling must be included.
				assert.Contains(t, result, siblingPostMessage)
				assert.Contains(t, result, "Thread with")
			} else {
				// Only the single post — the sibling must NOT appear.
				assert.NotContains(t, result, siblingPostMessage)
			}
		})
	}
}
