// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestToolCreatePostWithInlineFiles(t *testing.T) {
	tests := []struct {
		name        string
		files       []InlineFile
		failUploads bool
		wantErr     string // substring of the returned error; empty means success
	}{
		{
			name:  "creates files and attaches them to the post",
			files: []InlineFile{{Name: "report.md", Content: "# Report"}, {Name: "data.csv", Content: "a;b"}},
		},
		{
			name:        "failed file creation aborts the post",
			files:       []InlineFile{{Name: "broken.md", Content: "x"}},
			failUploads: true,
			wantErr:     `"broken.md"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channelID := model.NewId()
			teamID := model.NewId()

			var uploadedIDs []string
			postHit := false
			var postedPost model.Post

			mux := http.NewServeMux()
			mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, TeamId: teamID, DisplayName: "Town Square"})
			})
			mux.HandleFunc("/api/v4/teams/"+teamID, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.Team{Id: teamID, DisplayName: "Core Team"})
			})
			mux.HandleFunc("/api/v4/files", func(w http.ResponseWriter, _ *http.Request) {
				if tt.failUploads {
					http.Error(w, "upload failed", http.StatusInternalServerError)
					return
				}
				id := model.NewId()
				uploadedIDs = append(uploadedIDs, id)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.FileUploadResponse{FileInfos: []*model.FileInfo{{Id: id}}})
			})
			mux.HandleFunc("/api/v4/posts", func(w http.ResponseWriter, r *http.Request) {
				postHit = true
				require.NoError(t, json.NewDecoder(r.Body).Decode(&postedPost))
				postedPost.Id = model.NewId()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&postedPost)
			})

			ts := httptest.NewServer(mux)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Ctx: t.Context(), Client: newTestClient(ts.URL), AccessMode: AccessModeRemote}

			result, err := provider.toolCreatePost(mcpCtx, CreatePostArgs{
				ChannelID:          channelID,
				ChannelDisplayName: "Town Square",
				TeamDisplayName:    "Core Team",
				Message:            "hello",
				Files:              tt.files,
			})

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.False(t, postHit, "post endpoint must not be hit when inline file creation fails")
				return
			}
			require.NoError(t, err)
			assert.True(t, postHit, "post endpoint must be hit on success")
			assert.Equal(t, uploadedIDs, []string(postedPost.FileIds))
			assert.Contains(t, result, fmt.Sprintf("created and attached %d file(s)", len(tt.files)))
		})
	}
}

func TestToolDMWithInlineFiles(t *testing.T) {
	meID := model.NewId()
	targetID := model.NewId()
	dmChannelID := model.NewId()

	var uploadChannelIDs []string
	var uploadedIDs []string
	var postedPost model.Post

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/users/me", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: meID, Username: "agent"})
	})
	mux.HandleFunc("/api/v4/users/username/john", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: targetID, Username: "john"})
	})
	mux.HandleFunc("/api/v4/channels/direct", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: dmChannelID, Type: model.ChannelTypeDirect})
	})
	mux.HandleFunc("/api/v4/files", func(w http.ResponseWriter, r *http.Request) {
		uploadChannelIDs = append(uploadChannelIDs, r.URL.Query().Get("channel_id"))
		id := model.NewId()
		uploadedIDs = append(uploadedIDs, id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.FileUploadResponse{FileInfos: []*model.FileInfo{{Id: id}}})
	})
	mux.HandleFunc("/api/v4/posts", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&postedPost))
		postedPost.Id = model.NewId()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&postedPost)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Ctx: t.Context(), Client: newTestClient(ts.URL), AccessMode: AccessModeRemote}

	result, err := provider.toolDM(mcpCtx, DMArgs{
		Username: "john",
		Message:  "here is the report",
		Files:    []InlineFile{{Name: "report.md", Content: "# Report"}},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{dmChannelID}, uploadChannelIDs, "inline files must be uploaded into the DM channel")
	assert.Equal(t, dmChannelID, postedPost.ChannelId)
	assert.Equal(t, uploadedIDs, []string(postedPost.FileIds))
	assert.Contains(t, result, "created and attached 1 file(s)")
}
