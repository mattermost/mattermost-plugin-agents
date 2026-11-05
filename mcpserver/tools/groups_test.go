// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
		{"create_group empty display name", func() (string, error) {
			return provider.toolCreateGroup(mcpCtx, CreateGroupArgs{})
		}, "display_name cannot be empty"},
		{"create_group display name too long", func() (string, error) {
			return provider.toolCreateGroup(mcpCtx, CreateGroupArgs{DisplayName: strings.Repeat("a", model.GroupDisplayNameMaxLength+1)})
		}, "display_name must be at most"},
		{"create_group bad name", func() (string, error) {
			return provider.toolCreateGroup(mcpCtx, CreateGroupArgs{DisplayName: "Engineering", Name: "Engineering"})
		}, "name must be"},
		{"create_group reserved name", func() (string, error) {
			return provider.toolCreateGroup(mcpCtx, CreateGroupArgs{DisplayName: "Everyone", Name: "all"})
		}, "name must be"},
		{"create_group mention without name", func() (string, error) {
			return provider.toolCreateGroup(mcpCtx, CreateGroupArgs{DisplayName: "Engineering", AllowReference: true})
		}, "name is required when allow_reference is true"},
		{"create_group description too long", func() (string, error) {
			return provider.toolCreateGroup(mcpCtx, CreateGroupArgs{DisplayName: "Engineering", Description: strings.Repeat("a", model.GroupDescriptionMaxLength+1)})
		}, "description must be at most"},
		{"update_group bad id", func() (string, error) {
			name := "engineering"
			return provider.toolUpdateGroup(mcpCtx, UpdateGroupArgs{GroupID: "bad", Name: &name})
		}, "must be a valid ID"},
		{"update_group no fields", func() (string, error) {
			return provider.toolUpdateGroup(mcpCtx, UpdateGroupArgs{GroupID: model.NewId()})
		}, "provide at least one"},
		{"update_group empty display name", func() (string, error) {
			empty := ""
			return provider.toolUpdateGroup(mcpCtx, UpdateGroupArgs{GroupID: model.NewId(), DisplayName: &empty})
		}, "display_name cannot be empty"},
		{"update_group bad name", func() (string, error) {
			name := "Not Valid"
			return provider.toolUpdateGroup(mcpCtx, UpdateGroupArgs{GroupID: model.NewId(), Name: &name})
		}, "name must be"},
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

func TestToolCreateGroup(t *testing.T) {
	groupID := model.NewId()
	mux := http.NewServeMux()
	var got model.Group
	var method string
	mux.HandleFunc("/api/v4/groups", func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		var body model.Group
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		got = body
		w.Header().Set("Content-Type", "application/json")
		created := body
		created.Id = groupID
		_ = json.NewEncoder(w).Encode(&created)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolCreateGroup(mcpCtx, CreateGroupArgs{
		DisplayName:    "Engineering",
		Name:           "engineering",
		Description:    "Platform team",
		AllowReference: true,
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, model.GroupSourceCustom, got.Source)
	assert.Nil(t, got.RemoteId)
	require.NotNil(t, got.Name)
	assert.Equal(t, "engineering", *got.Name)
	assert.Equal(t, "Engineering", got.DisplayName)
	assert.Equal(t, "Platform team", got.Description)
	assert.True(t, got.AllowReference)
	assert.Contains(t, out, "Successfully created custom group 'Engineering'")
	assert.Contains(t, out, groupID)

	out, err = provider.toolCreateGroup(mcpCtx, CreateGroupArgs{DisplayName: "No Handle"})
	require.NoError(t, err)
	assert.Nil(t, got.Name)
	assert.Equal(t, model.GroupSourceCustom, got.Source)
	assert.False(t, got.AllowReference)
	assert.Contains(t, out, "No Handle")
}

func TestToolCreateGroupAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/groups", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"id":"api.context.permissions.app_error","message":"You do not have the appropriate permissions.","status_code":403}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	_, err := provider.toolCreateGroup(mcpCtx, CreateGroupArgs{DisplayName: "Engineering"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error creating group")
}

func TestToolUpdateGroup(t *testing.T) {
	groupID := model.NewId()
	mux := http.NewServeMux()
	var got model.GroupPatch
	var method string
	mux.HandleFunc(fmt.Sprintf("/api/v4/groups/%s/patch", groupID), func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		var body model.GroupPatch
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		got = body
		w.Header().Set("Content-Type", "application/json")
		name := "engineering"
		if body.Name != nil {
			name = *body.Name
		}
		displayName := "Engineering"
		if body.DisplayName != nil {
			displayName = *body.DisplayName
		}
		_ = json.NewEncoder(w).Encode(&model.Group{Id: groupID, Name: &name, DisplayName: displayName, Source: model.GroupSourceCustom})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	displayName := "Platform Engineering"
	name := "platform-eng"
	allow := true
	out, err := provider.toolUpdateGroup(mcpCtx, UpdateGroupArgs{
		GroupID:        groupID,
		DisplayName:    &displayName,
		Name:           &name,
		AllowReference: &allow,
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, method)
	require.NotNil(t, got.DisplayName)
	assert.Equal(t, "Platform Engineering", *got.DisplayName)
	require.NotNil(t, got.Name)
	assert.Equal(t, "platform-eng", *got.Name)
	require.NotNil(t, got.AllowReference)
	assert.True(t, *got.AllowReference)
	assert.Nil(t, got.Description)
	assert.Contains(t, out, "Successfully updated group 'Platform Engineering'")
	assert.Contains(t, out, groupID)

	cleared := ""
	out, err = provider.toolUpdateGroup(mcpCtx, UpdateGroupArgs{GroupID: groupID, Description: &cleared})
	require.NoError(t, err)
	require.NotNil(t, got.Description)
	assert.Empty(t, *got.Description)
	assert.Contains(t, out, groupID)
}
