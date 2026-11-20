// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// groupAPIRecorder is a fake group endpoint that records the write requests it receives.
type groupAPIRecorder struct {
	mu      sync.Mutex
	patches []map[string]any
	deletes int
}

func (r *groupAPIRecorder) snapshot() (patches []map[string]any, deletes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.patches...), r.deletes
}

// newGroupAPI serves GET, PUT /patch and DELETE for one group. A zero
// lookupStatus returns the stored group; any other value fails the lookup.
func newGroupAPI(t *testing.T, stored model.Group, lookupStatus, writeStatus int) (*groupAPIRecorder, *httptest.Server) {
	t.Helper()
	rec := &groupAPIRecorder{}
	respond := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"id":"api.error","message":"upstream detail","status_code":` + fmt.Sprint(status) + `}`))
			return
		}
		assert.NoError(t, json.NewEncoder(w).Encode(body))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/groups/"+stored.Id, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			respond(w, lookupStatus, &stored)
		case http.MethodDelete:
			rec.mu.Lock()
			rec.deletes++
			rec.mu.Unlock()
			respond(w, writeStatus, &stored)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
		}
	})
	mux.HandleFunc("/api/v4/groups/"+stored.Id+"/patch", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		// GroupPatch encodes unset fields as null, which the server treats as unchanged.
		maps.DeleteFunc(body, func(_ string, v any) bool { return v == nil })
		rec.mu.Lock()
		rec.patches = append(rec.patches, body)
		rec.mu.Unlock()

		patched := stored
		if v, ok := body["display_name"].(string); ok {
			patched.DisplayName = v
		}
		respond(w, writeStatus, &patched)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return rec, ts
}

func newRegisteredProvider(t *testing.T, serverURL string) *MattermostToolProvider {
	t.Helper()
	provider := newTestProvider(t, serverURL)
	provider.accessMode = AccessModeRemote
	return provider
}

// callRegisteredTool runs a tool through the provider's registration, so the
// registered name, schema and argument decoding are all exercised.
func callRegisteredTool(t *testing.T, provider *MattermostToolProvider, mcpCtx *MCPToolContext, name string, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	for _, tool := range provider.mcpTools() {
		if tool.Name == name {
			return tool.Resolver(mcpCtx, func(dst any) error { return json.Unmarshal(raw, dst) })
		}
	}
	require.Failf(t, "tool not registered", "%s", name)
	return "", nil
}

func TestGroupWriteToolsRegistration(t *testing.T) {
	provider := newRegisteredProvider(t, "https://mm.example.com")
	tools := map[string]MCPTool{}
	for _, tool := range provider.mcpTools() {
		tools[tool.Name] = tool
	}

	tests := []struct {
		name          string
		wantRequired  []string
		wantAbsent    []string
		wantPropCount int
	}{
		{"create_group", []string{"display_name", "name"}, []string{"allow_reference", "group_id"}, 3},
		{"update_group", []string{"group_id"}, []string{"allow_reference"}, 4},
		{"delete_group", []string{"group_id"}, []string{"allow_reference", "display_name", "name"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, ok := tools[tt.name]
			require.True(t, ok, "tool is not registered")
			assert.False(t, tool.ReadOnly, "group write tools must stay state-changing so they require approval")
			require.NotNil(t, tool.Schema)
			assert.ElementsMatch(t, tt.wantRequired, tool.Schema.Required)
			assert.Len(t, tool.Schema.Properties, tt.wantPropCount)
			for _, absent := range tt.wantAbsent {
				assert.NotContains(t, tool.Schema.Properties, absent)
			}
		})
	}
}

func TestUpdateGroupTool(t *testing.T) {
	customGroup := func(id string) model.Group {
		name := "platform-eng"
		return model.Group{Id: id, Name: &name, DisplayName: "Engineering", Source: model.GroupSourceCustom, AllowReference: true}
	}
	withSource := func(source model.GroupSource) func(string) model.Group {
		return func(id string) model.Group {
			g := customGroup(id)
			g.Source = source
			return g
		}
	}

	tests := []struct {
		name         string
		stored       func(id string) model.Group
		lookupStatus int
		writeStatus  int
		args         map[string]any
		wantErr      string
		wantPatch    map[string]any
		wantText     string
	}{
		{
			name:      "patches supplied fields without mentionability",
			stored:    customGroup,
			args:      map[string]any{"display_name": "Platform Engineering", "name": "platform", "description": "Platform team"},
			wantPatch: map[string]any{"display_name": "Platform Engineering", "name": "platform", "description": "Platform team"},
			wantText:  "Successfully updated group 'Platform Engineering'",
		},
		{
			name:      "clears the description",
			stored:    customGroup,
			args:      map[string]any{"description": ""},
			wantPatch: map[string]any{"description": ""},
			wantText:  "Successfully updated group",
		},
		{
			name:      "allow_reference is not forwarded",
			stored:    customGroup,
			args:      map[string]any{"display_name": "Platform", "allow_reference": false},
			wantPatch: map[string]any{"display_name": "Platform"},
			wantText:  "Successfully updated group",
		},
		{
			name:    "ldap group is refused",
			stored:  withSource(model.GroupSourceLdap),
			args:    map[string]any{"display_name": "Platform"},
			wantErr: "only custom groups can be updated",
		},
		{
			name:    "plugin group is refused",
			stored:  withSource(model.GroupSource("plugin_example")),
			args:    map[string]any{"display_name": "Platform"},
			wantErr: "only custom groups can be updated",
		},
		{
			name:         "failed lookup fails closed",
			stored:       customGroup,
			lookupStatus: http.StatusInternalServerError,
			args:         map[string]any{"display_name": "Platform"},
			wantErr:      "error fetching group",
		},
		{
			name:         "missing group fails closed",
			stored:       customGroup,
			lookupStatus: http.StatusNotFound,
			args:         map[string]any{"display_name": "Platform"},
			wantErr:      "error fetching group",
		},
		{
			name:        "patch permission error is returned",
			stored:      customGroup,
			writeStatus: http.StatusForbidden,
			args:        map[string]any{"display_name": "Platform"},
			wantErr:     "error updating group",
			wantPatch:   map[string]any{"display_name": "Platform"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupID := model.NewId()
			rec, ts := newGroupAPI(t, tt.stored(groupID), tt.lookupStatus, tt.writeStatus)
			provider := newRegisteredProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), AccessMode: AccessModeRemote}

			args := map[string]any{"group_id": groupID}
			maps.Copy(args, tt.args)
			out, err := callRegisteredTool(t, provider, mcpCtx, "update_group", args)

			patches, deletes := rec.snapshot()
			assert.Zero(t, deletes)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Contains(t, out, tt.wantText)
				assert.Contains(t, out, groupID)
			}
			if tt.wantPatch == nil {
				assert.Empty(t, patches)
				return
			}
			require.Len(t, patches, 1)
			assert.Equal(t, tt.wantPatch, patches[0])
		})
	}
}

func TestDeleteGroupTool(t *testing.T) {
	groupWithSource := func(source model.GroupSource) func(string) model.Group {
		return func(id string) model.Group {
			name := "platform-eng"
			return model.Group{Id: id, Name: &name, DisplayName: "Engineering", Source: source, AllowReference: true}
		}
	}

	tests := []struct {
		name         string
		stored       func(id string) model.Group
		lookupStatus int
		writeStatus  int
		args         map[string]any
		wantErr      string
		wantDeletes  int
	}{
		{name: "deletes a custom group", stored: groupWithSource(model.GroupSourceCustom), wantDeletes: 1},
		{name: "ldap group is refused", stored: groupWithSource(model.GroupSourceLdap), wantErr: "only custom groups can be deleted"},
		{name: "plugin group is refused", stored: groupWithSource(model.GroupSource("plugin_example")), wantErr: "only custom groups can be deleted"},
		{name: "failed lookup fails closed", stored: groupWithSource(model.GroupSourceCustom), lookupStatus: http.StatusInternalServerError, wantErr: "error fetching group"},
		{name: "missing group fails closed", stored: groupWithSource(model.GroupSourceCustom), lookupStatus: http.StatusNotFound, wantErr: "error fetching group"},
		{name: "delete permission error is returned", stored: groupWithSource(model.GroupSourceCustom), writeStatus: http.StatusForbidden, wantErr: "error deleting group", wantDeletes: 1},
		{name: "malformed id is rejected before any request", stored: groupWithSource(model.GroupSourceCustom), args: map[string]any{"group_id": "bad"}, wantErr: "must be a valid ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupID := model.NewId()
			rec, ts := newGroupAPI(t, tt.stored(groupID), tt.lookupStatus, tt.writeStatus)
			provider := newRegisteredProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), AccessMode: AccessModeRemote}

			args := map[string]any{"group_id": groupID}
			maps.Copy(args, tt.args)
			out, err := callRegisteredTool(t, provider, mcpCtx, "delete_group", args)

			patches, deletes := rec.snapshot()
			assert.Empty(t, patches)
			assert.Equal(t, tt.wantDeletes, deletes)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, out, "Successfully deleted custom group 'Engineering'")
			assert.Contains(t, out, groupID)
		})
	}
}
