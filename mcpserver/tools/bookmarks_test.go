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

func TestBookmarkToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"list bad channel", func() (string, error) {
			return provider.toolListChannelBookmarks(mcpCtx, ListChannelBookmarksArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"create empty name", func() (string, error) {
			return provider.toolCreateChannelBookmark(mcpCtx, CreateChannelBookmarkArgs{ChannelID: model.NewId(), DisplayName: "", LinkURL: "https://x"})
		}, "display_name cannot be empty"},
		{"create both link and file", func() (string, error) {
			return provider.toolCreateChannelBookmark(mcpCtx, CreateChannelBookmarkArgs{ChannelID: model.NewId(), DisplayName: "x", LinkURL: "https://x", FileID: model.NewId()})
		}, "exactly one of link_url or file_id"},
		{"create neither link nor file", func() (string, error) {
			return provider.toolCreateChannelBookmark(mcpCtx, CreateChannelBookmarkArgs{ChannelID: model.NewId(), DisplayName: "x"})
		}, "exactly one of link_url or file_id"},
		{"update no fields", func() (string, error) {
			return provider.toolUpdateChannelBookmark(mcpCtx, UpdateChannelBookmarkArgs{ChannelID: model.NewId(), BookmarkID: model.NewId()})
		}, "provide at least one"},
		{"delete bad", func() (string, error) {
			return provider.toolDeleteChannelBookmark(mcpCtx, DeleteChannelBookmarkArgs{ChannelID: model.NewId(), BookmarkID: "bad"})
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

func TestToolListChannelBookmarks(t *testing.T) {
	channelID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/bookmarks", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.ChannelBookmarkWithFileInfo{
			{ChannelBookmark: &model.ChannelBookmark{Id: model.NewId(), ChannelId: channelID, DisplayName: "Docs", Type: model.ChannelBookmarkLink, LinkUrl: "https://docs.example.com"}},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolListChannelBookmarks(mcpCtx, ListChannelBookmarksArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Contains(t, out, "Docs")
	assert.Contains(t, out, "https://docs.example.com")
}

func TestToolCreateChannelBookmarkLink(t *testing.T) {
	channelID := model.NewId()
	mux := http.NewServeMux()
	var got model.ChannelBookmark
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/bookmarks", channelID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		resp := &model.ChannelBookmarkWithFileInfo{ChannelBookmark: &model.ChannelBookmark{Id: model.NewId(), ChannelId: channelID, DisplayName: got.DisplayName, Type: got.Type, LinkUrl: got.LinkUrl}}
		_ = json.NewEncoder(w).Encode(resp)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolCreateChannelBookmark(mcpCtx, CreateChannelBookmarkArgs{ChannelID: channelID, DisplayName: "Wiki", LinkURL: "https://wiki.example.com"})
	require.NoError(t, err)
	assert.Equal(t, model.ChannelBookmarkLink, got.Type)
	assert.Equal(t, "https://wiki.example.com", got.LinkUrl)
	assert.Contains(t, out, "Successfully created bookmark 'Wiki'")
}
