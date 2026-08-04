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

func TestTeamExtraToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_team_member bad", func() (string, error) { return provider.toolGetTeamMember(mcpCtx, GetTeamMemberArgs{TeamID: "bad"}) }, "must be a valid ID"},
		{"get_team_stats bad", func() (string, error) { return provider.toolGetTeamStats(mcpCtx, GetTeamStatsArgs{TeamID: "bad"}) }, "must be a valid ID"},
		{"get_users_in_team bad", func() (string, error) {
			return provider.toolGetUsersInTeam(mcpCtx, GetUsersInTeamArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"get_users_not_in_team bad", func() (string, error) {
			return provider.toolGetUsersNotInTeam(mcpCtx, GetUsersNotInTeamArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"get_new_users_in_team bad", func() (string, error) {
			return provider.toolGetNewUsersInTeam(mcpCtx, GetNewUsersInTeamArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"get_dm_common_teams bad", func() (string, error) {
			return provider.toolGetDMCommonTeams(mcpCtx, GetDMCommonTeamsArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"search_teams empty", func() (string, error) { return provider.toolSearchTeams(mcpCtx, SearchTeamsArgs{Term: ""}) }, "term cannot be empty"},
		{"search_users_in_team bad team", func() (string, error) {
			return provider.toolSearchUsersInTeam(mcpCtx, SearchUsersInTeamArgs{Term: "x", TeamID: "bad"})
		}, "must be a valid ID"},
		{"add_team_members empty", func() (string, error) {
			return provider.toolAddTeamMembers(mcpCtx, AddTeamMembersArgs{TeamID: model.NewId()})
		}, "cannot be empty"},
		{"remove_team_member bad", func() (string, error) {
			return provider.toolRemoveTeamMember(mcpCtx, RemoveTeamMemberArgs{TeamID: model.NewId(), UserID: "bad"})
		}, "must be a valid ID"},
		{"update_team no fields", func() (string, error) {
			return provider.toolUpdateTeam(mcpCtx, UpdateTeamArgs{TeamID: model.NewId()})
		}, "provide at least one"},
		{"invite empty", func() (string, error) {
			return provider.toolInviteUsersToTeam(mcpCtx, InviteUsersToTeamArgs{TeamID: model.NewId()})
		}, "emails cannot be empty"},
		{"invite_and_channels no channels", func() (string, error) {
			return provider.toolInviteUsersToTeamAndChannels(mcpCtx, InviteUsersToTeamAndChannelsArgs{TeamID: model.NewId(), Emails: []string{"a@b.com"}})
		}, "channel_ids cannot be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestToolGetTeamStats(t *testing.T) {
	teamID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/teams/%s/stats", teamID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.TeamStats{TeamId: teamID, TotalMemberCount: 50, ActiveMemberCount: 42})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetTeamStats(mcpCtx, GetTeamStatsArgs{TeamID: teamID})
	require.NoError(t, err)
	assert.Contains(t, out, "Total members: 50")
	assert.Contains(t, out, "Active members: 42")
}

func TestToolGetTeamMember(t *testing.T) {
	teamID := model.NewId()
	userID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/teams/%s/members/%s", teamID, userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.TeamMember{TeamId: teamID, UserId: userID, SchemeAdmin: true, SchemeUser: true})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "grace"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolGetTeamMember(mcpCtx, GetTeamMemberArgs{TeamID: teamID})
	require.NoError(t, err)
	assert.Contains(t, out, "Role: admin")
	assert.Contains(t, out, "grace")
}

func TestToolSearchTeams(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/teams/search", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.Team{{Id: model.NewId(), Name: "eng", DisplayName: "Engineering"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolSearchTeams(mcpCtx, SearchTeamsArgs{Term: "eng"})
	require.NoError(t, err)
	assert.Contains(t, out, "Engineering")
}

func TestToolGetDMCommonTeams(t *testing.T) {
	channelID := model.NewId()
	u1 := model.NewId()
	u2 := model.NewId()
	teamCommon := model.NewId()
	teamOnlyU1 := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/members", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.ChannelMembers{{ChannelId: channelID, UserId: u1}, {ChannelId: channelID, UserId: u2}})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams", u1), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.Team{{Id: teamCommon, DisplayName: "Common"}, {Id: teamOnlyU1, DisplayName: "OnlyU1"}})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams", u2), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.Team{{Id: teamCommon, DisplayName: "Common"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetDMCommonTeams(mcpCtx, GetDMCommonTeamsArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Contains(t, out, "Common")
	assert.NotContains(t, out, "OnlyU1")
}
