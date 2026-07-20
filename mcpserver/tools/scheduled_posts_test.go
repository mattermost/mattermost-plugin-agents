// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduledPostToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"list invalid team", func() (string, error) {
			return provider.toolListScheduledPosts(mcpCtx, ListScheduledPostsArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"create invalid channel", func() (string, error) {
			return provider.toolCreateScheduledPost(mcpCtx, CreateScheduledPostArgs{ChannelID: "bad", Message: "x", ScheduledAt: future})
		}, "must be a valid ID"},
		{"create empty message", func() (string, error) {
			return provider.toolCreateScheduledPost(mcpCtx, CreateScheduledPostArgs{ChannelID: model.NewId(), Message: "", ScheduledAt: future})
		}, "message cannot be empty"},
		{"create bad time", func() (string, error) {
			return provider.toolCreateScheduledPost(mcpCtx, CreateScheduledPostArgs{ChannelID: model.NewId(), Message: "x", ScheduledAt: "not-a-time"})
		}, "invalid timestamp"},
		{"update nothing", func() (string, error) {
			return provider.toolUpdateScheduledPost(mcpCtx, UpdateScheduledPostArgs{ScheduledPostID: model.NewId(), ChannelID: model.NewId()})
		}, "provide message"},
		{"delete invalid", func() (string, error) {
			return provider.toolDeleteScheduledPost(mcpCtx, DeleteScheduledPostArgs{ScheduledPostID: "bad"})
		}, "must be a valid ID"},
		{"reminder invalid post", func() (string, error) {
			return provider.toolSetPostReminder(mcpCtx, SetPostReminderArgs{PostID: "bad", RemindAt: future})
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

func TestToolCreateScheduledPost(t *testing.T) {
	channelID := model.NewId()
	scheduledID := model.NewId()
	when := time.Now().Add(2 * time.Hour).UTC()

	mux := http.NewServeMux()
	var got model.ScheduledPost
	mux.HandleFunc("/api/v4/posts/schedule", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		resp := &model.ScheduledPost{Draft: model.Draft{ChannelId: channelID, Message: got.Message}, ScheduledAt: got.ScheduledAt}
		resp.Id = scheduledID
		_ = json.NewEncoder(w).Encode(resp)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolCreateScheduledPost(mcpCtx, CreateScheduledPostArgs{
		ChannelID:   channelID,
		Message:     "later",
		ScheduledAt: when.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Equal(t, "later", got.Message)
	// RFC3339 has second precision, so the round-tripped value is second-truncated.
	assert.Equal(t, when.Truncate(time.Second).UnixMilli(), got.ScheduledAt)
	assert.Contains(t, out, scheduledID)
}

func TestToolUpdateScheduledPostMergesExisting(t *testing.T) {
	teamID := model.NewId()
	channelID := model.NewId()
	scheduledID := model.NewId()
	rootID := model.NewId()
	existingAt := time.Now().Add(time.Hour).UnixMilli()
	newWhen := time.Now().Add(5 * time.Hour).UTC()

	mux := http.NewServeMux()
	// Resolve the channel's team.
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, TeamId: teamID})
	})
	// Existing scheduled post to merge against.
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/scheduled/team/%s", teamID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		sp := &model.ScheduledPost{Draft: model.Draft{ChannelId: channelID, Message: "original", RootId: rootID}, ScheduledAt: existingAt}
		sp.Id = scheduledID
		_ = json.NewEncoder(w).Encode(map[string][]*model.ScheduledPost{channelID: {sp}})
	})
	var got model.ScheduledPost
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/schedule/%s", scheduledID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&got)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	// Update only the send time; message and root_id must be preserved.
	out, err := provider.toolUpdateScheduledPost(mcpCtx, UpdateScheduledPostArgs{
		ScheduledPostID: scheduledID,
		ChannelID:       channelID,
		ScheduledAt:     newWhen.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, out, scheduledID)
	assert.Equal(t, "original", got.Message, "message must be preserved on partial update")
	assert.Equal(t, rootID, got.RootId, "root_id must be preserved on partial update")
	assert.Equal(t, newWhen.Truncate(time.Second).UnixMilli(), got.ScheduledAt)
}

func TestToolUpdateScheduledPostDirectChannel(t *testing.T) {
	userID := model.NewId()
	teamID := model.NewId()
	dmChannelID := model.NewId()
	scheduledID := model.NewId()
	newWhen := time.Now().Add(4 * time.Hour).UTC()

	mux := http.NewServeMux()
	// A DM channel has no team.
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmChannelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: dmChannelID, Type: model.ChannelTypeDirect})
	})
	// The team fallback used to resolve direct-channel scheduled posts.
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams", userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.Team{{Id: teamID}})
	})
	// Direct-channel scheduled posts come back under the "directChannels" key.
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/scheduled/team/%s", teamID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		sp := &model.ScheduledPost{Draft: model.Draft{ChannelId: dmChannelID, Message: "dm original"}, ScheduledAt: time.Now().Add(time.Hour).UnixMilli()}
		sp.Id = scheduledID
		_ = json.NewEncoder(w).Encode(map[string][]*model.ScheduledPost{"directChannels": {sp}})
	})
	var got model.ScheduledPost
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/schedule/%s", scheduledID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&got)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolUpdateScheduledPost(mcpCtx, UpdateScheduledPostArgs{
		ScheduledPostID: scheduledID,
		ChannelID:       dmChannelID,
		ScheduledAt:     newWhen.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, out, scheduledID)
	assert.Equal(t, "dm original", got.Message, "message must be preserved for DM scheduled posts")
	assert.Equal(t, newWhen.Truncate(time.Second).UnixMilli(), got.ScheduledAt)
}

func TestToolSetPostReminder(t *testing.T) {
	postID := model.NewId()
	userID := model.NewId()
	when := time.Now().Add(3 * time.Hour).UTC()

	mux := http.NewServeMux()
	var got model.PostReminder
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/posts/%s/reminder", userID, postID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolSetPostReminder(mcpCtx, SetPostReminderArgs{PostID: postID, RemindAt: when.Format(time.RFC3339)})
	require.NoError(t, err)
	// target_time is expressed in seconds.
	assert.Equal(t, when.Unix(), got.TargetTime)
	assert.Contains(t, out, "Successfully set a reminder")
}

func TestToolListScheduledPosts(t *testing.T) {
	teamID := model.NewId()
	channelID := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/scheduled/team/%s", teamID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		sp := &model.ScheduledPost{Draft: model.Draft{ChannelId: channelID, Message: "queued"}, ScheduledAt: time.Now().Add(time.Hour).UnixMilli()}
		sp.Id = model.NewId()
		_ = json.NewEncoder(w).Encode(map[string][]*model.ScheduledPost{channelID: {sp}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolListScheduledPosts(mcpCtx, ListScheduledPostsArgs{TeamID: teamID})
	require.NoError(t, err)
	assert.Contains(t, out, "queued")
	assert.Contains(t, out, channelID)
}
