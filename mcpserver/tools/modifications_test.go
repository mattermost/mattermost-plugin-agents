// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadChannelCursorMutualExclusivity verifies before/after/since cannot be combined.
func TestReadChannelCursorMutualExclusivity(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context()}

	tests := []struct {
		name string
		args ReadChannelArgs
	}{
		{name: "before and after", args: ReadChannelArgs{ChannelID: model.NewId(), Before: model.NewId(), After: model.NewId()}},
		{name: "before and since", args: ReadChannelArgs{ChannelID: model.NewId(), Before: model.NewId(), Since: "2024-01-01T00:00:00Z"}},
		{name: "after and since", args: ReadChannelArgs{ChannelID: model.NewId(), After: model.NewId(), Since: "2024-01-01T00:00:00Z"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := provider.toolReadChannel(mcpCtx, tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "mutually exclusive")
		})
	}
}

// TestReadChannelCursorRouting verifies before/after route to the cursor endpoints
// by forwarding the corresponding query parameter to the posts API.
func TestReadChannelCursorRouting(t *testing.T) {
	channelID := model.NewId()
	teamID := model.NewId()
	cursorPostID := model.NewId()
	authorID := model.NewId()

	tests := []struct {
		name      string
		args      ReadChannelArgs
		wantParam string // query parameter expected to carry the cursor post ID
	}{
		{name: "before", args: ReadChannelArgs{ChannelID: channelID, Before: cursorPostID}, wantParam: "before"},
		{name: "after", args: ReadChannelArgs{ChannelID: channelID, After: cursorPostID}, wantParam: "after"},
		{name: "default no cursor", args: ReadChannelArgs{ChannelID: channelID}, wantParam: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotQuery url.Values
			mux := http.NewServeMux()
			mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, Name: "general", DisplayName: "General", Type: model.ChannelTypeOpen, TeamId: teamID})
			})
			mux.HandleFunc(fmt.Sprintf("/api/v4/teams/%s", teamID), func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.Team{Id: teamID, Name: "eng", DisplayName: "Engineering"})
			})
			mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/posts", channelID), func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.PostList{
					Order: []string{"p1"},
					Posts: map[string]*model.Post{"p1": {Id: "p1", ChannelId: channelID, UserId: authorID, Message: "hello", CreateAt: 1}},
				})
			})
			mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]*model.User{{Id: authorID, Username: "author"}})
			})

			ts := httptest.NewServer(mux)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			client := newTestClient(ts.URL)
			mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context()}

			out, err := provider.toolReadChannel(mcpCtx, tt.args)
			require.NoError(t, err)
			assert.Contains(t, out, "hello")

			if tt.wantParam == "" {
				assert.Empty(t, gotQuery.Get("before"))
				assert.Empty(t, gotQuery.Get("after"))
			} else {
				assert.Equal(t, cursorPostID, gotQuery.Get(tt.wantParam), "cursor post ID should be forwarded as %s", tt.wantParam)
			}
		})
	}
}

// TestApplySearchModifiers verifies the keyword-search modifier prefixing.
func TestApplySearchModifiers(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")

	tests := []struct {
		name string
		args CombinedSearchArgs
		want string
	}{
		{name: "no modifiers", args: CombinedSearchArgs{Query: "hello"}, want: "hello"},
		{name: "from strips at", args: CombinedSearchArgs{From: "@john"}, want: "from:john hello"},
		{name: "in by name", args: CombinedSearchArgs{In: "general"}, want: "in:general hello"},
		{name: "before/after", args: CombinedSearchArgs{Before: "2024-01-31", After: "2024-01-01"}, want: "before:2024-01-31 after:2024-01-01 hello"},
		{name: "combined", args: CombinedSearchArgs{From: "jane", In: "town-square"}, want: "from:jane in:town-square hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := provider.applySearchModifiers(t.Context(), client, "hello", tt.args)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestApplySearchModifiersResolvesChannelID verifies that an ID passed to `in`
// is resolved to the channel URL name.
func TestApplySearchModifiersResolvesChannelID(t *testing.T) {
	channelID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, Name: "resolved-name", DisplayName: "Resolved"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)

	got := provider.applySearchModifiers(t.Context(), client, "hello", CombinedSearchArgs{In: channelID})
	assert.Equal(t, "in:resolved-name hello", got)
}
