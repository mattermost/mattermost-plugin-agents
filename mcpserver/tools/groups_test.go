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
		{"create_group missing name", func() (string, error) {
			return provider.toolCreateGroup(mcpCtx, CreateGroupArgs{DisplayName: "Engineering"})
		}, "name is required"},
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
		DisplayName: "Engineering",
		Name:        "engineering",
		Description: "Platform team",
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

	_, err := provider.toolCreateGroup(mcpCtx, CreateGroupArgs{DisplayName: "Engineering", Name: "engineering"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error creating group")
}

func TestToolUpdateGroup(t *testing.T) {
	displayName := "Platform Engineering"
	name := "platform-eng"
	allow := true
	deny := false
	cleared := ""

	tests := []struct {
		name         string
		stored       *model.Group
		args         UpdateGroupArgs
		wantErr      string
		wantNoPatch  bool
		wantText     string
		wantName     string
		wantNameNil  bool
		wantAllow    *bool
		wantAllowNil bool
		wantDisplay  string
		wantDescNil  bool
		wantDesc     *string
		wantExact    bool
	}{
		{
			name:        "patches supplied fields",
			args:        UpdateGroupArgs{DisplayName: &displayName, Name: &name, AllowReference: &allow},
			wantText:    "Successfully updated group 'Platform Engineering'",
			wantName:    "platform-eng",
			wantAllow:   &allow,
			wantDisplay: displayName,
			wantDescNil: true,
		},
		{
			name:      "clears description",
			args:      UpdateGroupArgs{Description: &cleared},
			wantText:  "Successfully updated group",
			wantExact: true,
		},
		{
			name: "enabling mentions keeps the stored handle",
			stored: &model.Group{
				Name:           &name,
				DisplayName:    displayName,
				Source:         model.GroupSourceLdap,
				AllowReference: false,
			},
			args:        UpdateGroupArgs{AllowReference: &allow},
			wantText:    "Successfully updated group",
			wantName:    "platform-eng",
			wantAllow:   &allow,
			wantDescNil: true,
		},
		{
			name: "already mentionable does not rewrite the handle",
			stored: &model.Group{
				Name:           &name,
				DisplayName:    displayName,
				Source:         model.GroupSourceCustom,
				AllowReference: true,
			},
			args:        UpdateGroupArgs{AllowReference: &allow},
			wantNoPatch: true,
			wantText:    "already mentionable",
		},
		{
			name: "other field updates do not resend mentionability",
			stored: &model.Group{
				Name:           &name,
				DisplayName:    "Engineering",
				Source:         model.GroupSourceCustom,
				AllowReference: true,
			},
			args:         UpdateGroupArgs{DisplayName: &displayName, AllowReference: &allow},
			wantText:     "Successfully updated group 'Platform Engineering'",
			wantNameNil:  true,
			wantAllowNil: true,
			wantDisplay:  displayName,
		},
		{
			name: "repeating the current handle still applies other fields",
			stored: &model.Group{
				Name:           &name,
				DisplayName:    "Engineering",
				Source:         model.GroupSourceCustom,
				AllowReference: true,
			},
			args:         UpdateGroupArgs{Name: &name, AllowReference: &allow, Description: &cleared},
			wantText:     "Successfully updated group",
			wantNameNil:  true,
			wantAllowNil: true,
			wantDesc:     &cleared,
		},
		{
			name: "repeating only the current handle does not patch",
			stored: &model.Group{
				Name:           &name,
				DisplayName:    displayName,
				Source:         model.GroupSourceCustom,
				AllowReference: true,
			},
			args:        UpdateGroupArgs{Name: &name, AllowReference: &allow},
			wantNoPatch: true,
			wantText:    "already mentionable",
		},
		{
			name: "custom group cannot turn mentions off",
			stored: &model.Group{
				Name:           &name,
				DisplayName:    displayName,
				Source:         model.GroupSourceCustom,
				AllowReference: true,
			},
			args:        UpdateGroupArgs{AllowReference: &deny},
			wantErr:     "must remain mentionable",
			wantNoPatch: true,
		},
		{
			name: "ldap group can turn mentions off",
			stored: &model.Group{
				Name:           &name,
				DisplayName:    displayName,
				Source:         model.GroupSourceLdap,
				AllowReference: true,
			},
			args:        UpdateGroupArgs{AllowReference: &deny},
			wantText:    "Successfully updated group",
			wantNameNil: true,
			wantAllow:   &deny,
		},
		{
			name: "enabling mentions without a handle asks for one",
			stored: &model.Group{
				DisplayName:    "All",
				Source:         model.GroupSourceLdap,
				AllowReference: false,
			},
			args:        UpdateGroupArgs{AllowReference: &allow},
			wantErr:     "name is required to allow mentions",
			wantNoPatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupID := model.NewId()
			var got *model.GroupPatch
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v4/groups/"+groupID, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				stored := model.Group{Id: groupID, DisplayName: "Engineering", Source: model.GroupSourceCustom}
				if tt.stored != nil {
					stored = *tt.stored
					stored.Id = groupID
				}
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(&stored))
			})
			mux.HandleFunc("/api/v4/groups/"+groupID+"/patch", func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPut, r.Method)
				var body model.GroupPatch
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				got = &body
				patchedName := "engineering"
				if body.Name != nil {
					patchedName = *body.Name
				}
				patchedDisplay := "Engineering"
				if body.DisplayName != nil {
					patchedDisplay = *body.DisplayName
				}
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(&model.Group{
					Id:          groupID,
					Name:        &patchedName,
					DisplayName: patchedDisplay,
					Source:      model.GroupSourceCustom,
				}))
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}
			args := tt.args
			args.GroupID = groupID

			out, err := provider.toolUpdateGroup(mcpCtx, args)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Contains(t, out, tt.wantText)
				assert.Contains(t, out, groupID)
			}
			if tt.wantNoPatch {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			if tt.wantExact {
				assert.Equal(t, model.GroupPatch{Description: &cleared}, *got)
				return
			}
			if tt.wantNameNil {
				assert.Nil(t, got.Name)
			} else if tt.wantName != "" {
				require.NotNil(t, got.Name)
				assert.Equal(t, tt.wantName, *got.Name)
			}
			if tt.wantAllowNil {
				assert.Nil(t, got.AllowReference)
			} else if tt.wantAllow != nil {
				require.NotNil(t, got.AllowReference)
				assert.Equal(t, *tt.wantAllow, *got.AllowReference)
			}
			if tt.wantDisplay != "" {
				require.NotNil(t, got.DisplayName)
				assert.Equal(t, tt.wantDisplay, *got.DisplayName)
			}
			if tt.wantDescNil {
				assert.Nil(t, got.Description)
			}
			if tt.wantDesc != nil {
				require.NotNil(t, got.Description)
				assert.Equal(t, *tt.wantDesc, *got.Description)
			}
		})
	}
}
