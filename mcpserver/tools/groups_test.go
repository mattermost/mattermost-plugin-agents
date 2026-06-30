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

func TestGroupToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_group_info bad", func() (string, error) { return provider.toolGetGroupInfo(mcpCtx, GetGroupInfoArgs{GroupID: "bad"}) }, "must be a valid ID"},
		{"get_channel_groups bad", func() (string, error) {
			return provider.toolGetChannelGroups(mcpCtx, GetChannelGroupsArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"get_team_groups bad", func() (string, error) { return provider.toolGetTeamGroups(mcpCtx, GetTeamGroupsArgs{TeamID: "bad"}) }, "must be a valid ID"},
		{"get_users_in_group_channels empty", func() (string, error) {
			return provider.toolGetUsersInGroupChannels(mcpCtx, GetUsersInGroupChannelsArgs{})
		}, "cannot be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestToolGetGroupInfo(t *testing.T) {
	groupID := model.NewId()
	name := "engineering-team"
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/groups/%s", groupID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Group{Id: groupID, Name: &name, DisplayName: "Engineering", Source: model.GroupSourceLdap})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetGroupInfo(mcpCtx, GetGroupInfoArgs{GroupID: groupID})
	require.NoError(t, err)
	assert.Contains(t, out, "Engineering")
	assert.Contains(t, out, "engineering-team")
}

func TestToolGetUsersInGroupChannels(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/users/group_channels", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string][]*model.User{channelID: {{Id: userID, Username: "heidi"}}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetUsersInGroupChannels(mcpCtx, GetUsersInGroupChannelsArgs{ChannelIDs: []string{channelID}})
	require.NoError(t, err)
	assert.Contains(t, out, "heidi")
}
