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

func TestRoleToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_role missing", func() (string, error) { return provider.toolGetRole(mcpCtx, GetRoleArgs{}) }, "provide role_id or role_name"},
		{"get_channel_moderations bad", func() (string, error) {
			return provider.toolGetChannelModerations(mcpCtx, GetChannelModerationsArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"update_channel_member_roles bad user", func() (string, error) {
			return provider.toolUpdateChannelMemberRoles(mcpCtx, UpdateChannelMemberRolesArgs{ChannelID: model.NewId(), UserID: "bad"})
		}, "must be a valid ID"},
		{"update_team_member_roles bad user", func() (string, error) {
			return provider.toolUpdateTeamMemberRoles(mcpCtx, UpdateTeamMemberRolesArgs{TeamID: model.NewId(), UserID: "bad"})
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

func TestToolGetRoleByName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/roles/name/channel_admin", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Role{Id: model.NewId(), Name: "channel_admin", DisplayName: "Channel Admin", Permissions: []string{"manage_channel_roles", "delete_post"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetRole(mcpCtx, GetRoleArgs{RoleName: "channel_admin"})
	require.NoError(t, err)
	assert.Contains(t, out, "channel_admin")
	assert.Contains(t, out, "manage_channel_roles")
}

func TestToolGetChannelModerations(t *testing.T) {
	channelID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/moderations", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.ChannelModeration{
			{Name: "create_post", Roles: &model.ChannelModeratedRoles{
				Members: &model.ChannelModeratedRole{Value: true, Enabled: true},
				Guests:  &model.ChannelModeratedRole{Value: false, Enabled: true},
			}},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetChannelModerations(mcpCtx, GetChannelModerationsArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Contains(t, out, "create_post")
	assert.Contains(t, out, "members=true")
	assert.Contains(t, out, "guests=false")
}

func TestToolUpdateChannelMemberRoles(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	mux := http.NewServeMux()
	var got model.SchemeRoles
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/members/%s/schemeRoles", channelID, userID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolUpdateChannelMemberRoles(mcpCtx, UpdateChannelMemberRolesArgs{ChannelID: channelID, UserID: userID, Admin: true})
	require.NoError(t, err)
	assert.True(t, got.SchemeAdmin)
	assert.True(t, got.SchemeUser)
	assert.Contains(t, out, "channel admin")
}
