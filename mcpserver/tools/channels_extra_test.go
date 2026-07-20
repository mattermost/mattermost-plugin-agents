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

func TestChannelExtraToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_channel_stats", func() (string, error) {
			return provider.toolGetChannelStats(mcpCtx, GetChannelStatsArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"get_channel_member_counts empty", func() (string, error) {
			return provider.toolGetChannelMemberCounts(mcpCtx, GetChannelMemberCountsArgs{})
		}, "cannot be empty"},
		{"search_channels empty term", func() (string, error) {
			return provider.toolSearchChannels(mcpCtx, SearchChannelsArgs{Term: ""})
		}, "term cannot be empty"},
		{"list_team_channels bad", func() (string, error) {
			return provider.toolListTeamChannels(mcpCtx, ListTeamChannelsArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"list_archived_channels bad", func() (string, error) {
			return provider.toolListArchivedChannels(mcpCtx, ListArchivedChannelsArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"update_channel no fields", func() (string, error) {
			return provider.toolUpdateChannel(mcpCtx, UpdateChannelArgs{ChannelID: model.NewId()})
		}, "provide at least one"},
		{"archive_channel bad", func() (string, error) {
			return provider.toolArchiveChannel(mcpCtx, ArchiveChannelArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"restore_channel bad", func() (string, error) {
			return provider.toolRestoreChannel(mcpCtx, RestoreChannelArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"convert_channel_privacy bad privacy", func() (string, error) {
			return provider.toolConvertChannelPrivacy(mcpCtx, ConvertChannelPrivacyArgs{ChannelID: model.NewId(), Privacy: "X"})
		}, "invalid privacy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestToolGetChannelStats(t *testing.T) {
	channelID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/stats", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.ChannelStats{ChannelId: channelID, MemberCount: 12, GuestCount: 1, PinnedPostCount: 3, FilesCount: 5})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetChannelStats(mcpCtx, GetChannelStatsArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Contains(t, out, "Members: 12")
	assert.Contains(t, out, "Pinned posts: 3")
}

func TestToolUpdateChannel(t *testing.T) {
	channelID := model.NewId()
	mux := http.NewServeMux()
	var gotPatch model.ChannelPatch
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/patch", channelID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotPatch)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, DisplayName: *gotPatch.DisplayName})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	newName := "Renamed"
	out, err := provider.toolUpdateChannel(mcpCtx, UpdateChannelArgs{ChannelID: channelID, DisplayName: &newName})
	require.NoError(t, err)
	require.NotNil(t, gotPatch.DisplayName)
	assert.Equal(t, "Renamed", *gotPatch.DisplayName)
	assert.Contains(t, out, "Successfully updated channel")
}

func TestToolConvertChannelPrivacy(t *testing.T) {
	channelID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/privacy", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, DisplayName: "C", Type: model.ChannelTypePrivate})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolConvertChannelPrivacy(mcpCtx, ConvertChannelPrivacyArgs{ChannelID: channelID, Privacy: "P"})
	require.NoError(t, err)
	assert.Contains(t, out, "type P")
}

func TestToolGetChannelMemberCounts(t *testing.T) {
	c1 := model.NewId()
	c2 := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/stats/member_count", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{c1: 4, c2: 9})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetChannelMemberCounts(mcpCtx, GetChannelMemberCountsArgs{ChannelIDs: []string{c1, c2}})
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("%s: 4", c1))
	assert.Contains(t, out, fmt.Sprintf("%s: 9", c2))
}
