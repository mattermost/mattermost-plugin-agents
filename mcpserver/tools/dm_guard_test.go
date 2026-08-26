// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/search"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsVerifiedOpenOrPrivate(t *testing.T) {
	tests := []struct {
		name string
		ch   *model.Channel
		want bool
	}{
		{"nil", nil, false},
		{"open", &model.Channel{Type: model.ChannelTypeOpen}, true},
		{"private", &model.Channel{Type: model.ChannelTypePrivate}, true},
		{"direct", &model.Channel{Type: model.ChannelTypeDirect}, false},
		{"group", &model.Channel{Type: model.ChannelTypeGroup}, false},
		{"empty type", &model.Channel{Type: ""}, false},
		{"board", &model.Channel{Type: model.ChannelTypeOpenBoard}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isVerifiedOpenOrPrivate(tt.ch))
		})
	}
}

func TestWithoutDirectAndGroup(t *testing.T) {
	open := &model.Channel{Id: "o", Type: model.ChannelTypeOpen, DisplayName: "Town Square"}
	dm := &model.Channel{Id: "d", Type: model.ChannelTypeDirect}
	gm := &model.Channel{Id: "g", Type: model.ChannelTypeGroup}
	priv := &model.Channel{Id: "p", Type: model.ChannelTypePrivate, DisplayName: "Secret"}
	unknown := &model.Channel{Id: "x", Type: ""}

	got := withoutDirectAndGroup([]*model.Channel{open, dm, gm, priv, unknown, nil})
	require.Len(t, got, 2)
	assert.Equal(t, "o", got[0].Id)
	assert.Equal(t, "p", got[1].Id)
}

func TestRejectDirectOrGroupArgs(t *testing.T) {
	openID := model.NewId()
	dmID := model.NewId()
	gmID := model.NewId()
	unknownID := model.NewId()
	openPostID := model.NewId()
	dmPostID := model.NewId()
	openFileID := model.NewId()
	dmFileID := model.NewId()

	channels := map[string]*model.Channel{
		openID:    {Id: openID, Type: model.ChannelTypeOpen, DisplayName: "Town Square"},
		dmID:      {Id: dmID, Type: model.ChannelTypeDirect},
		gmID:      {Id: gmID, Type: model.ChannelTypeGroup},
		unknownID: {Id: unknownID, Type: ""},
	}
	posts := map[string]*model.Post{
		openPostID: {Id: openPostID, ChannelId: openID},
		dmPostID:   {Id: dmPostID, ChannelId: dmID},
	}
	files := map[string]*model.FileInfo{
		openFileID: {Id: openFileID, ChannelId: openID},
		dmFileID:   {Id: dmFileID, ChannelId: dmID},
	}

	ts := newIDLookupServer(t, channels, posts, files)
	defer ts.Close()

	botCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), IsBotSession: true}
	humanCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), IsBotSession: false}

	tests := []struct {
		name    string
		ctx     *MCPToolContext
		args    any
		wantErr error
	}{
		{"human session with DM channel_id is allowed", humanCtx, map[string]any{"channel_id": dmID}, nil},
		{"bot session with open channel_id is allowed", botCtx, map[string]any{"channel_id": openID}, nil},
		{"bot session with DM channel_id is rejected", botCtx, map[string]any{"channel_id": dmID}, errDirectOrGroupInaccessible},
		{"bot session with GM channel_id is rejected", botCtx, map[string]any{"channel_id": gmID}, errDirectOrGroupInaccessible},
		{"bot session with mixed channel_ids is rejected", botCtx, map[string]any{"channel_ids": []any{openID, dmID}}, errDirectOrGroupInaccessible},
		{"bot session with open post_id is allowed", botCtx, map[string]any{"post_id": openPostID}, nil},
		{"bot session with DM post_id is rejected", botCtx, map[string]any{"post_id": dmPostID}, errDirectOrGroupInaccessible},
		{"bot session with DM root_id is rejected", botCtx, map[string]any{"root_id": dmPostID}, errDirectOrGroupInaccessible},
		{"bot session with DM thread_id is rejected", botCtx, map[string]any{"thread_id": dmPostID}, errDirectOrGroupInaccessible},
		{"bot session with DM file_id is rejected", botCtx, map[string]any{"file_id": dmFileID}, errDirectOrGroupInaccessible},
		{"bot session with open file_id is allowed", botCtx, map[string]any{"file_id": openFileID}, nil},
		{"bot session with nested DM channel_id is rejected", botCtx, map[string]any{"trigger": map[string]any{"channel_id": dmID}}, errDirectOrGroupInaccessible},
		{"bot session with unresolvable channel_id is rejected", botCtx, map[string]any{"channel_id": model.NewId()}, errUnverifiedChannelReference},
		{"bot session with unknown channel type is rejected", botCtx, map[string]any{"channel_id": unknownID}, errUnverifiedChannelReference},
		{"bot session with uppercase CHANNEL_ID DM is rejected", botCtx, map[string]any{"CHANNEL_ID": dmID}, errDirectOrGroupInaccessible},
		{"bot session with mixed-case Channel_Id DM is rejected", botCtx, map[string]any{"Channel_Id": dmID}, errDirectOrGroupInaccessible},
		{"bot session with nested uppercase CHANNEL_ID is rejected", botCtx, map[string]any{"trigger": map[string]any{"CHANNEL_ID": dmID}}, errDirectOrGroupInaccessible},
		{"bot session with in: DM channel ID is rejected", botCtx, map[string]any{"in": dmID}, errDirectOrGroupInaccessible},
		{"bot session with in: channel name is not resolved as an ID", botCtx, map[string]any{"in": "town-square"}, nil},
		{"bot session with before: DM post ID is rejected", botCtx, map[string]any{"before": dmPostID}, errDirectOrGroupInaccessible},
		{"bot session with after: DM post ID is rejected", botCtx, map[string]any{"after": dmPostID}, errDirectOrGroupInaccessible},
		{"bot session with before: open post ID is allowed", botCtx, map[string]any{"before": openPostID}, nil},
		{"bot session with after: open post ID is allowed", botCtx, map[string]any{"after": openPostID}, nil},
		{"bot session with before: date is not resolved as an ID", botCtx, map[string]any{"before": "2024-01-01"}, nil},
		{"bot session with after: date is not resolved as an ID", botCtx, map[string]any{"after": "2024-01-31"}, nil},
		{"bot session dm tool args (username) are not guarded", botCtx, map[string]any{"username": "alice", "message": "hi"}, nil},
		{"bot session group_message args (usernames) are not guarded", botCtx, map[string]any{"usernames": []any{"alice", "bob"}, "message": "hi"}, nil},
		{"empty args are a no-op", botCtx, map[string]any{}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectDirectOrGroupArgs(tt.ctx, tt.args)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRejectDirectOrGroupArgsMemoizesChannelLookups(t *testing.T) {
	channelID := model.NewId()
	var gets atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), func(w http.ResponseWriter, _ *http.Request) {
		gets.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), IsBotSession: true}
	err := rejectDirectOrGroupArgs(mcpCtx, map[string]any{"channel_ids": []any{channelID, channelID}})
	require.NoError(t, err)
	assert.Equal(t, int32(1), gets.Load(), "repeated channel IDs in one call must share one lookup")
}

func TestCreateMCPToolContextIsBotSession(t *testing.T) {
	human := &model.User{Id: model.NewId(), IsBot: false}
	bot := &model.User{Id: model.NewId(), IsBot: true}

	patAuth := func(t *testing.T, user *model.User) auth.AuthenticationProvider {
		t.Helper()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v4/users/me" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(user)
		}))
		t.Cleanup(ts.Close)
		return auth.NewTokenAuthenticationProvider(ts.URL, "", "pat-token", &testLogger{t: t})
	}

	tests := []struct {
		name       string
		auth       func(t *testing.T) auth.AuthenticationProvider
		wantBot    bool
		wantUserID string
	}{
		{"human user", func(*testing.T) auth.AuthenticationProvider { return identityAuthProvider{user: human} }, false, human.Id},
		{"bot user", func(*testing.T) auth.AuthenticationProvider { return identityAuthProvider{user: bot} }, true, bot.Id},
		{"lookup error fails closed", func(*testing.T) auth.AuthenticationProvider {
			return identityAuthProvider{err: fmt.Errorf("unavailable")}
		}, true, ""},
		{"nil user fails closed", func(*testing.T) auth.AuthenticationProvider { return identityAuthProvider{} }, true, ""},
		{"no identity provider fails closed", func(*testing.T) auth.AuthenticationProvider { return fakeToolAuthProvider{} }, true, ""},
		{"human PAT session is not a bot session", func(t *testing.T) auth.AuthenticationProvider { return patAuth(t, human) }, false, human.Id},
		{"bot PAT session is a bot session", func(t *testing.T) auth.AuthenticationProvider { return patAuth(t, bot) }, true, bot.Id},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &MattermostToolProvider{
				authProvider: tt.auth(t),
				logger:       &testLogger{t: t},
				mmServerURL:  "https://mm.example.com",
				accessMode:   AccessModeRemote,
			}
			mcpCtx, err := provider.createMCPToolContext(t.Context(), nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantBot, mcpCtx.IsBotSession)
			assert.Equal(t, tt.wantUserID, mcpCtx.UserID)
		})
	}
}

func TestToolGetUserChannelsFiltersDirectAndGroupForBot(t *testing.T) {
	userID := model.NewId()
	teamID := model.NewId()
	open := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeOpen, DisplayName: "Town Square", TeamId: teamID}
	dm := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/users/me", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "bot"})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/channels", userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.Channel{open, dm})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams", userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.Team{{Id: teamID, DisplayName: "Engineering"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)

	t.Run("human session includes DMs", func(t *testing.T) {
		out, err := provider.toolGetUserChannels(&MCPToolContext{Client: client, Ctx: t.Context()}, GetUserChannelsArgs{})
		require.NoError(t, err)
		assert.Contains(t, out, open.Id)
		assert.Contains(t, out, dm.Id)
	})
	t.Run("bot session omits DMs", func(t *testing.T) {
		out, err := provider.toolGetUserChannels(&MCPToolContext{Client: client, Ctx: t.Context(), IsBotSession: true}, GetUserChannelsArgs{})
		require.NoError(t, err)
		assert.Contains(t, out, open.Id)
		assert.NotContains(t, out, dm.Id)
	})
}

func TestToolSearchChannelsFiltersDirectAndGroupForBot(t *testing.T) {
	open := &model.ChannelWithTeamData{Channel: model.Channel{Id: model.NewId(), Type: model.ChannelTypeOpen, DisplayName: "Town Square"}}
	dm := &model.ChannelWithTeamData{Channel: model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect, DisplayName: "Direct Message"}}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/search", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.ChannelWithTeamData{open, dm})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)

	t.Run("human session includes DMs", func(t *testing.T) {
		out, err := provider.toolSearchChannels(&MCPToolContext{Client: client, Ctx: t.Context()}, SearchChannelsArgs{Term: "town"})
		require.NoError(t, err)
		assert.Contains(t, out, open.Id)
		assert.Contains(t, out, dm.Id)
	})
	t.Run("bot session omits DMs", func(t *testing.T) {
		out, err := provider.toolSearchChannels(&MCPToolContext{Client: client, Ctx: t.Context(), IsBotSession: true}, SearchChannelsArgs{Term: "town"})
		require.NoError(t, err)
		assert.Contains(t, out, open.Id)
		assert.NotContains(t, out, dm.Id)
	})
}

func TestVisiblePostListFiltersDirectAndGroupForBot(t *testing.T) {
	openID := model.NewId()
	dmID := model.NewId()
	unknownID := model.NewId()
	authorID := model.NewId()
	openPost := &model.Post{Id: model.NewId(), ChannelId: openID, UserId: authorID, Message: "public mention", CreateAt: 2}
	dmPost := &model.Post{Id: model.NewId(), ChannelId: dmID, UserId: authorID, Message: "private mention", CreateAt: 1}
	unknownPost := &model.Post{Id: model.NewId(), ChannelId: unknownID, UserId: authorID, Message: "unclassified", CreateAt: 3}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: openID, Type: model.ChannelTypeOpen})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", unknownID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: unknownID, Type: ""})
	})
	mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.User{{Id: authorID, Username: "alice"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	postList := &model.PostList{
		Order: []string{openPost.Id, dmPost.Id, unknownPost.Id},
		Posts: map[string]*model.Post{openPost.Id: openPost, dmPost.Id: dmPost, unknownPost.Id: unknownPost},
	}

	t.Run("human session keeps DM posts", func(t *testing.T) {
		mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}
		out := provider.formatPostListChrono(mcpCtx, visiblePostList(mcpCtx, postList), "posts mentioning you")
		assert.Contains(t, out, "public mention")
		assert.Contains(t, out, "private mention")
	})
	t.Run("bot session drops DM and unverified posts at the call site", func(t *testing.T) {
		mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), IsBotSession: true}
		out := provider.formatPostListChrono(mcpCtx, visiblePostList(mcpCtx, postList), "posts mentioning you")
		assert.Contains(t, out, "public mention")
		assert.NotContains(t, out, "private mention")
		assert.NotContains(t, out, "unclassified")
	})
	t.Run("formatter itself does not drop DM posts", func(t *testing.T) {
		out := provider.formatPostListChrono(&MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), IsBotSession: true}, postList, "posts mentioning you")
		assert.Contains(t, out, "private mention")
	})
}

func TestToolGetChannelInfoFiltersDirectAndGroupForBotByName(t *testing.T) {
	userID := model.NewId()
	teamID := model.NewId()
	open := &model.ChannelWithTeamData{Channel: model.Channel{Id: model.NewId(), Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen, TeamId: teamID}}
	dm := &model.ChannelWithTeamData{Channel: model.Channel{Id: model.NewId(), Name: "alice__bot", DisplayName: "Town Square", Type: model.ChannelTypeDirect}}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/search", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.ChannelWithTeamData{open, dm})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/teams/%s", teamID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Team{Id: teamID, DisplayName: "Engineering"})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/stats", open.Id), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.ChannelStats{ChannelId: open.Id, MemberCount: 3})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/members/%s", open.Id, userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.ChannelMember{ChannelId: open.Id, UserId: userID, SchemeUser: true})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	out, err := provider.toolGetChannelInfo(&MCPToolContext{
		Client:       newTestClient(ts.URL),
		Ctx:          t.Context(),
		UserID:       userID,
		IsBotSession: true,
	}, GetChannelInfoArgs{ChannelName: "Town Square"})
	require.NoError(t, err)
	assert.Contains(t, out, open.Id)
	assert.NotContains(t, out, dm.Id)
}

func TestEveryToolDeclaresDMandGMEnforcement(t *testing.T) {
	provider := &MattermostToolProvider{
		logger:     &testLogger{t: t},
		accessMode: AccessModeRemote,
		devMode:    true,
	}

	classified := botSessionDMGMEnforcement()
	registered := map[string]bool{}
	for _, tool := range provider.mcpTools() {
		registered[tool.Name] = true
		if _, ok := classified[tool.Name]; !ok {
			t.Errorf("tool %q is registered but missing from botSessionDMGMEnforcement; classify it with a dmGMEnforcement constant (dmGMArgumentGuard, dmGMOutputFilter, dmGMArgumentGuardAndOutputFilter, dmGMCannotReach, or dmGMWriteByUsername)", tool.Name)
		}
	}
	for name := range classified {
		if !registered[name] {
			t.Errorf("botSessionDMGMEnforcement has %q but that tool is not registered; remove the stale classification", name)
		}
	}
}

// dmGMEnforcement is how a registered MCP tool is kept from returning DM/GM
// data to a bot session. A named constant is required, so an ad-hoc string
// classification does not compile.
type dmGMEnforcement int

const (
	dmGMArgumentGuard dmGMEnforcement = iota
	dmGMOutputFilter
	dmGMArgumentGuardAndOutputFilter
	dmGMCannotReach
	dmGMWriteByUsername
)

// botSessionDMGMEnforcement inventories how every MCP tool is kept from
// returning DM/GM data to a bot session. A newly registered tool must be added
// here or TestEveryToolDeclaresDMandGMEnforcement fails.
func botSessionDMGMEnforcement() map[string]dmGMEnforcement {
	return map[string]dmGMEnforcement{
		// posts
		"read_post":           dmGMArgumentGuard,
		"create_post":         dmGMArgumentGuard,
		"dm":                  dmGMWriteByUsername,
		"group_message":       dmGMWriteByUsername,
		"get_post_info":       dmGMArgumentGuard,
		"list_pinned_posts":   dmGMArgumentGuard,
		"list_saved_posts":    dmGMArgumentGuardAndOutputFilter,
		"update_post":         dmGMArgumentGuard,
		"delete_post":         dmGMArgumentGuard,
		"pin_post":            dmGMArgumentGuard,
		"unpin_post":          dmGMArgumentGuard,
		"save_post":           dmGMArgumentGuard,
		"acknowledge_post":    dmGMArgumentGuard,
		"create_post_as_user": dmGMArgumentGuard,
		// scheduled posts
		"list_scheduled_posts":  dmGMOutputFilter,
		"create_scheduled_post": dmGMArgumentGuard,
		"update_scheduled_post": dmGMArgumentGuardAndOutputFilter,
		"delete_scheduled_post": dmGMOutputFilter,
		"set_post_reminder":     dmGMArgumentGuard,
		// reactions
		"get_post_reactions":  dmGMArgumentGuard,
		"get_bulk_reactions":  dmGMArgumentGuard,
		"list_custom_emoji":   dmGMCannotReach,
		"search_custom_emoji": dmGMCannotReach,
		"add_reaction":        dmGMArgumentGuard,
		"remove_reaction":     dmGMArgumentGuard,
		// threads / unreads
		"get_threads":             dmGMOutputFilter,
		"get_mentions":            dmGMOutputFilter,
		"get_unread_counts":       dmGMCannotReach,
		"get_channel_unread":      dmGMArgumentGuard,
		"get_posts_around_unread": dmGMArgumentGuard,
		"mark_channel_read":       dmGMArgumentGuard,
		"mark_channels_viewed":    dmGMArgumentGuard,
		"mark_post_unread":        dmGMArgumentGuard,
		"set_thread_follow":       dmGMArgumentGuard,
		// channels
		"read_channel":              dmGMArgumentGuard,
		"create_channel":            dmGMCannotReach,
		"get_channel_info":          dmGMArgumentGuardAndOutputFilter,
		"get_channel_members":       dmGMArgumentGuard,
		"add_channel_member":        dmGMArgumentGuard,
		"get_user_channels":         dmGMOutputFilter,
		"get_channel_stats":         dmGMArgumentGuard,
		"get_channel_member_counts": dmGMArgumentGuard,
		"search_channels":           dmGMOutputFilter,
		"list_team_channels":        dmGMCannotReach,
		"list_archived_channels":    dmGMCannotReach,
		"update_channel":            dmGMArgumentGuard,
		"archive_channel":           dmGMArgumentGuard,
		"restore_channel":           dmGMArgumentGuard,
		"convert_channel_privacy":   dmGMArgumentGuard,
		// channel members
		"get_channel_member":            dmGMArgumentGuard,
		"get_channel_members_by_ids":    dmGMArgumentGuard,
		"get_channel_members_by_status": dmGMArgumentGuard,
		"get_user_channel_memberships":  dmGMOutputFilter,
		"get_users_not_in_channel":      dmGMArgumentGuard,
		"search_users_in_channel":       dmGMArgumentGuard,
		"list_sidebar_categories":       dmGMOutputFilter,
		"add_channel_members":           dmGMArgumentGuard,
		"remove_channel_member":         dmGMArgumentGuard,
		"set_channel_mute":              dmGMArgumentGuard,
		"set_channel_favorite":          dmGMArgumentGuard,
		"update_channel_notify_props":   dmGMArgumentGuard,
		// bookmarks
		"list_channel_bookmarks":  dmGMArgumentGuard,
		"create_channel_bookmark": dmGMArgumentGuard,
		"update_channel_bookmark": dmGMArgumentGuard,
		"delete_channel_bookmark": dmGMArgumentGuard,
		// users
		"get_me":                 dmGMCannotReach,
		"get_user":               dmGMCannotReach,
		"get_user_by_username":   dmGMCannotReach,
		"get_user_by_email":      dmGMCannotReach,
		"get_users_by_ids":       dmGMCannotReach,
		"get_users_by_usernames": dmGMCannotReach,
		"get_user_stats":         dmGMCannotReach,
		"get_user_cpa_values":    dmGMCannotReach,
		"list_cpa_fields":        dmGMCannotReach,
		"update_user":            dmGMCannotReach,
		"create_user":            dmGMCannotReach,
		// status
		"get_user_status":        dmGMCannotReach,
		"get_users_statuses":     dmGMCannotReach,
		"get_user_custom_status": dmGMCannotReach,
		"set_status":             dmGMCannotReach,
		"set_dnd":                dmGMCannotReach,
		// teams
		"get_team_info":                     dmGMCannotReach,
		"get_team_members":                  dmGMCannotReach,
		"add_team_member":                   dmGMCannotReach,
		"get_team_member":                   dmGMCannotReach,
		"get_team_stats":                    dmGMCannotReach,
		"get_user_teams":                    dmGMCannotReach,
		"get_users_in_team":                 dmGMCannotReach,
		"get_users_not_in_team":             dmGMCannotReach,
		"get_new_users_in_team":             dmGMCannotReach,
		"get_dm_common_teams":               dmGMArgumentGuard,
		"search_teams":                      dmGMCannotReach,
		"search_users_in_team":              dmGMCannotReach,
		"add_team_members":                  dmGMCannotReach,
		"remove_team_member":                dmGMCannotReach,
		"update_team":                       dmGMCannotReach,
		"invite_users_to_team":              dmGMCannotReach,
		"invite_users_to_team_and_channels": dmGMArgumentGuard,
		"create_team":                       dmGMCannotReach,
		// search
		"search_posts": dmGMArgumentGuardAndOutputFilter,
		"search_users": dmGMCannotReach,
		// files
		"read_file":      dmGMArgumentGuard,
		"get_file_info":  dmGMArgumentGuard,
		"get_post_files": dmGMArgumentGuard,
		"get_file_link":  dmGMArgumentGuard,
		"search_files":   dmGMOutputFilter,
		"upload_file":    dmGMArgumentGuard,
		// integrations
		"get_bot":                dmGMCannotReach,
		"list_bots":              dmGMCannotReach,
		"list_incoming_webhooks": dmGMCannotReach,
		"list_outgoing_webhooks": dmGMCannotReach,
		// groups
		"get_group_info":              dmGMCannotReach,
		"list_groups":                 dmGMCannotReach,
		"get_user_groups":             dmGMCannotReach,
		"get_channel_groups":          dmGMArgumentGuard,
		"get_team_groups":             dmGMCannotReach,
		"get_users_in_group_channels": dmGMArgumentGuard,
		// roles
		"get_role":                    dmGMCannotReach,
		"get_channel_moderations":     dmGMArgumentGuard,
		"update_channel_member_roles": dmGMArgumentGuard,
		"update_team_member_roles":    dmGMCannotReach,
		// agents
		"list_agents": dmGMCannotReach,
		// automations
		"list_automations":            dmGMArgumentGuardAndOutputFilter,
		"get_automation_instructions": dmGMCannotReach,
		"create_automation":           dmGMArgumentGuard,
		"update_automation":           dmGMArgumentGuardAndOutputFilter,
		"delete_automation":           dmGMOutputFilter,
	}
}

func TestDMAndGroupMessageSchemasHaveNoGuardedKeys(t *testing.T) {
	provider := &MattermostToolProvider{
		logger:     &testLogger{t: t},
		accessMode: AccessModeRemote,
	}
	for _, tool := range provider.getPostTools() {
		if tool.Name != "dm" && tool.Name != "group_message" {
			continue
		}
		for _, name := range schemaPropertyNames(tool.Schema) {
			_, guarded := guardedArgKeys[name]
			assert.False(t, guarded, "%s schema must not take a guarded channel/post ID (%q); it addresses users by username so the bot can still send DMs/GMs", tool.Name, name)
		}
	}
}

func TestExecuteSemanticSearchFiltersDirectAndGroup(t *testing.T) {
	openID := model.NewId()
	dmID := model.NewId()
	unknownID := model.NewId()
	openPost := model.NewId()
	dmPost := model.NewId()
	unknownPost := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), serveJSON(&model.Channel{Id: openID, Type: model.ChannelTypeOpen, DisplayName: "Town Square"}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), serveJSON(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", unknownID), serveJSON(&model.Channel{Id: unknownID, Type: ""}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	svc := &capturingSearchService{results: []search.RAGResult{
		{PostID: openPost, ChannelID: openID, ChannelName: "Town Square", Content: "public", Score: 0.9},
		{PostID: dmPost, ChannelID: dmID, ChannelName: "Direct Message", Content: "secret", Score: 0.8},
		{PostID: unknownPost, ChannelID: unknownID, ChannelName: "??", Content: "unverified", Score: 0.7},
	}}
	provider := &MattermostToolProvider{logger: &testLogger{t: t}, searchService: svc}
	client := newTestClient(ts.URL)
	args := CombinedSearchArgs{Query: "hello", SemanticLimit: 10}

	t.Run("human session keeps DM hits", func(t *testing.T) {
		got, err := provider.executeSemanticSearch(t.Context(), client, args, "user1", false)
		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.False(t, svc.lastOpts.ExcludeDirectAndGroup)
	})
	t.Run("bot session drops DM and unverified hits", func(t *testing.T) {
		got, err := provider.executeSemanticSearch(t.Context(), client, args, "user1", true)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, openPost, got[0].Post.Id)
		assert.True(t, svc.lastOpts.ExcludeDirectAndGroup)
	})
}

func TestExecuteKeywordSearchPaginatesAfterFiltering(t *testing.T) {
	openID := model.NewId()
	dmID := model.NewId()
	openPost := &model.Post{Id: model.NewId(), ChannelId: openID, UserId: model.NewId(), Message: "public", CreateAt: 1}
	dmPost := &model.Post{Id: model.NewId(), ChannelId: dmID, UserId: model.NewId(), Message: "secret", CreateAt: 3}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/posts/search", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.PostList{
			Order: []string{dmPost.Id, openPost.Id},
			Posts: map[string]*model.Post{openPost.Id: openPost, dmPost.Id: dmPost},
		})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), serveJSON(&model.Channel{Id: openID, Type: model.ChannelTypeOpen, DisplayName: "Town Square"}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), serveJSON(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", openPost.UserId), serveJSON(&model.User{Id: openPost.UserId, Username: "alice"}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", dmPost.UserId), serveJSON(&model.User{Id: dmPost.UserId, Username: "bob"}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)

	t.Run("bot session limit applies to visible posts", func(t *testing.T) {
		got, err := provider.executeKeywordSearch(t.Context(), client, CombinedSearchArgs{Query: "hello", KeywordLimit: 1}, true)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, openPost.Id, got[0].Post.Id)
	})
	t.Run("human session limit can select a DM post", func(t *testing.T) {
		got, err := provider.executeKeywordSearch(t.Context(), client, CombinedSearchArgs{Query: "hello", KeywordLimit: 1}, false)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, dmPost.Id, got[0].Post.Id)
	})
}

func TestRegisterDynamicToolEnforcesBotDMGuard(t *testing.T) {
	openID := model.NewId()
	dmID := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), serveJSON(&model.Channel{Id: openID, Type: model.ChannelTypeOpen}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), serveJSON(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/stats", openID), serveJSON(&model.ChannelStats{ChannelId: openID, MemberCount: 3}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/stats", dmID), serveJSON(&model.ChannelStats{ChannelId: dmID, MemberCount: 2}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := newTestClient(ts.URL)
	human := &model.User{Id: model.NewId(), IsBot: false}
	bot := &model.User{Id: model.NewId(), IsBot: true}

	tests := []struct {
		name      string
		user      *model.User
		args      map[string]any
		wantError bool
		wantText  string
	}{
		{"bot session with DM channel_id is rejected by the handler", bot, map[string]any{"channel_id": dmID}, true, errDirectOrGroupInaccessible.Error()},
		{"bot session with uppercase CHANNEL_ID DM is rejected by the handler", bot, map[string]any{"CHANNEL_ID": dmID}, true, errDirectOrGroupInaccessible.Error()},
		{"bot session with open channel_id reaches the resolver", bot, map[string]any{"channel_id": openID}, false, openID},
		{"human session with DM channel_id reaches the resolver", human, map[string]any{"channel_id": dmID}, false, dmID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &MattermostToolProvider{
				authProvider: sessionAuthProvider{client: client, user: tt.user},
				logger:       &testLogger{t: t},
				accessMode:   AccessModeRemote,
			}
			result, err := callRegisteredMCPTool(t, provider, "get_channel_stats", tt.args)
			require.NoError(t, err)
			require.NotNil(t, result)
			text := mcpToolText(result)
			if tt.wantError {
				assert.True(t, result.IsError)
				assert.Contains(t, text, tt.wantText)
				return
			}
			assert.False(t, result.IsError)
			assert.Contains(t, text, tt.wantText)
		})
	}
}

func TestToolGetThreadsFiltersDirectAndGroupForBot(t *testing.T) {
	teamID := model.NewId()
	userID := model.NewId()
	openID := model.NewId()
	dmID := model.NewId()
	authorID := model.NewId()
	openRoot := &model.Post{Id: model.NewId(), ChannelId: openID, UserId: authorID, Message: "public thread"}
	dmRoot := &model.Post{Id: model.NewId(), ChannelId: dmID, UserId: authorID, Message: "secret thread"}

	var gotExcludeDirect string
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams/%s/threads", userID, teamID), func(w http.ResponseWriter, r *http.Request) {
		gotExcludeDirect = r.URL.Query().Get("excludeDirect")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Threads{
			Total:   2,
			Threads: []*model.ThreadResponse{{PostId: openRoot.Id, Post: openRoot}, {PostId: dmRoot.Id, Post: dmRoot}},
		})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), serveJSON(&model.Channel{Id: openID, Type: model.ChannelTypeOpen}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), serveJSON(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", authorID), serveJSON(&model.User{Id: authorID, Username: "alice"}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)

	t.Run("human session includes DM threads", func(t *testing.T) {
		gotExcludeDirect = ""
		out, err := provider.toolGetThreads(&MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID}, GetThreadsArgs{TeamID: teamID})
		require.NoError(t, err)
		assert.Contains(t, out, "public thread")
		assert.Contains(t, out, "secret thread")
		assert.NotEqual(t, "true", gotExcludeDirect)
	})
	t.Run("bot session omits DM threads", func(t *testing.T) {
		gotExcludeDirect = ""
		out, err := provider.toolGetThreads(&MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID, IsBotSession: true}, GetThreadsArgs{TeamID: teamID})
		require.NoError(t, err)
		assert.Contains(t, out, "public thread")
		assert.NotContains(t, out, "secret thread")
		assert.Equal(t, "true", gotExcludeDirect)
	})
}

func TestToolListScheduledPostsFiltersDirectAndGroupForBot(t *testing.T) {
	teamID := model.NewId()
	openID := model.NewId()
	dmID := model.NewId()
	openSP := &model.ScheduledPost{Draft: model.Draft{ChannelId: openID, Message: "public later"}}
	openSP.Id = model.NewId()
	dmSP := &model.ScheduledPost{Draft: model.Draft{ChannelId: dmID, Message: "secret later"}}
	dmSP.Id = model.NewId()

	var includeDirect string
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/scheduled/team/%s", teamID), func(w http.ResponseWriter, r *http.Request) {
		includeDirect = r.URL.Query().Get("includeDirectChannels")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string][]*model.ScheduledPost{openID: {openSP}, "directChannels": {dmSP}})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), serveJSON(&model.Channel{Id: openID, Type: model.ChannelTypeOpen}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), serveJSON(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)

	t.Run("human session includes DM scheduled posts", func(t *testing.T) {
		includeDirect = ""
		out, err := provider.toolListScheduledPosts(&MCPToolContext{Client: client, Ctx: t.Context()}, ListScheduledPostsArgs{TeamID: teamID})
		require.NoError(t, err)
		assert.Contains(t, out, "public later")
		assert.Contains(t, out, "secret later")
		assert.Equal(t, "true", includeDirect)
	})
	t.Run("bot session omits DM scheduled posts", func(t *testing.T) {
		includeDirect = ""
		out, err := provider.toolListScheduledPosts(&MCPToolContext{Client: client, Ctx: t.Context(), IsBotSession: true}, ListScheduledPostsArgs{TeamID: teamID})
		require.NoError(t, err)
		assert.Contains(t, out, "public later")
		assert.NotContains(t, out, "secret later")
		assert.Equal(t, "false", includeDirect)
	})
}

func TestToolDeleteScheduledPostRejectsDMForBot(t *testing.T) {
	userID := model.NewId()
	teamID := model.NewId()
	openID := model.NewId()
	dmID := model.NewId()
	openSPID := model.NewId()
	dmSPID := model.NewId()
	openSP := &model.ScheduledPost{Draft: model.Draft{ChannelId: openID, Message: "public later"}}
	openSP.Id = openSPID
	deleted := ""

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams", userID), serveJSON([]*model.Team{{Id: teamID}}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/scheduled/team/%s", teamID), serveJSON(map[string][]*model.ScheduledPost{openID: {openSP}}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), serveJSON(&model.Channel{Id: openID, Type: model.ChannelTypeOpen}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), serveJSON(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/posts/schedule/%s", openSPID), func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		deleted = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.ScheduledPost{})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	botCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID, IsBotSession: true}

	t.Run("bot cannot delete a DM scheduled post", func(t *testing.T) {
		deleted = ""
		_, err := provider.toolDeleteScheduledPost(botCtx, DeleteScheduledPostArgs{ScheduledPostID: dmSPID})
		require.Error(t, err)
		assert.ErrorIs(t, err, errDirectOrGroupInaccessible)
		assert.Empty(t, deleted)
	})
	t.Run("bot can delete an open-channel scheduled post", func(t *testing.T) {
		deleted = ""
		out, err := provider.toolDeleteScheduledPost(botCtx, DeleteScheduledPostArgs{ScheduledPostID: openSPID})
		require.NoError(t, err)
		assert.Contains(t, out, openSPID)
		assert.Contains(t, deleted, openSPID)
	})
}

func TestToolListSidebarCategoriesFiltersDirectAndGroupForBot(t *testing.T) {
	teamID := model.NewId()
	userID := model.NewId()
	openID := model.NewId()
	dmID := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams/%s/channels/categories", userID, teamID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.OrderedSidebarCategories{
			Categories: []*model.SidebarCategoryWithChannels{
				{SidebarCategory: model.SidebarCategory{Id: "fav", DisplayName: "Favorites", Type: model.SidebarCategoryFavorites}, Channels: []string{openID, dmID}},
				{SidebarCategory: model.SidebarCategory{Id: "dms", DisplayName: "Direct Messages", Type: model.SidebarCategoryDirectMessages}, Channels: []string{dmID}},
			},
		})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), serveJSON(&model.Channel{Id: openID, Type: model.ChannelTypeOpen}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), serveJSON(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)

	t.Run("human session includes DM channel IDs", func(t *testing.T) {
		out, err := provider.toolListSidebarCategories(&MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID}, ListSidebarCategoriesArgs{TeamID: teamID})
		require.NoError(t, err)
		assert.Contains(t, out, openID)
		assert.Contains(t, out, dmID)
		assert.Contains(t, out, "Direct Messages")
	})
	t.Run("bot session omits DM channel IDs and the DM category", func(t *testing.T) {
		out, err := provider.toolListSidebarCategories(&MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID, IsBotSession: true}, ListSidebarCategoriesArgs{TeamID: teamID})
		require.NoError(t, err)
		assert.Contains(t, out, openID)
		assert.NotContains(t, out, dmID)
		assert.NotContains(t, out, "Direct Messages")
	})
}

func TestToolListSavedPostsRequiresScopeForBot(t *testing.T) {
	userID := model.NewId()
	teamID := model.NewId()
	channelID := model.NewId()
	openPost := &model.Post{Id: model.NewId(), ChannelId: channelID, UserId: userID, Message: "saved in town square", CreateAt: 1}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/posts/flagged", userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.PostList{
			Order: []string{openPost.Id},
			Posts: map[string]*model.Post{openPost.Id: openPost},
		})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), serveJSON(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}))
	mux.HandleFunc("/api/v4/users/ids", serveJSON([]*model.User{{Id: userID, Username: "alice"}}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)

	tests := []struct {
		name         string
		ctx          *MCPToolContext
		args         ListSavedPostsArgs
		wantErr      string
		wantContains string
	}{
		{
			name:    "bot session without scope is rejected",
			ctx:     &MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID, IsBotSession: true},
			args:    ListSavedPostsArgs{},
			wantErr: "team_id or channel_id is required",
		},
		{
			name:         "bot session with team_id succeeds",
			ctx:          &MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID, IsBotSession: true},
			args:         ListSavedPostsArgs{TeamID: teamID},
			wantContains: "saved in town square",
		},
		{
			name:         "human session without scope succeeds",
			ctx:          &MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID},
			args:         ListSavedPostsArgs{},
			wantContains: "saved in town square",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := provider.toolListSavedPosts(tt.ctx, tt.args)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, out, tt.wantContains)
		})
	}
}

func TestToolSearchFilesFiltersDirectAndGroupForBot(t *testing.T) {
	teamID := model.NewId()
	openID := model.NewId()
	dmID := model.NewId()
	openFile := &model.FileInfo{Id: model.NewId(), Name: "public.pdf", Size: 10, ChannelId: openID}
	dmFile := &model.FileInfo{Id: model.NewId(), Name: "secret.pdf", Size: 20, ChannelId: dmID}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/teams/%s/files/search", teamID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.FileInfoList{
			Order:     []string{openFile.Id, dmFile.Id},
			FileInfos: map[string]*model.FileInfo{openFile.Id: openFile, dmFile.Id: dmFile},
		})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), serveJSON(&model.Channel{Id: openID, Type: model.ChannelTypeOpen}))
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), serveJSON(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)

	t.Run("human session includes DM files", func(t *testing.T) {
		out, err := provider.toolSearchFiles(&MCPToolContext{Client: client, Ctx: t.Context()}, SearchFilesArgs{Terms: "pdf", TeamID: teamID})
		require.NoError(t, err)
		assert.Contains(t, out, "public.pdf")
		assert.Contains(t, out, "secret.pdf")
	})
	t.Run("bot session omits DM files", func(t *testing.T) {
		out, err := provider.toolSearchFiles(&MCPToolContext{Client: client, Ctx: t.Context(), IsBotSession: true}, SearchFilesArgs{Terms: "pdf", TeamID: teamID})
		require.NoError(t, err)
		assert.Contains(t, out, "public.pdf")
		assert.NotContains(t, out, "secret.pdf")
	})
}

type identityAuthProvider struct {
	fakeToolAuthProvider
	user *model.User
	err  error
}

func (p identityAuthProvider) GetAuthenticatedUser(context.Context) (*model.User, error) {
	return p.user, p.err
}

type capturingSearchService struct {
	lastOpts search.Options
	results  []search.RAGResult
}

func (c *capturingSearchService) Enabled() bool { return true }

func (c *capturingSearchService) Search(_ context.Context, _ string, opts search.Options) ([]search.RAGResult, error) {
	c.lastOpts = opts
	return c.results, nil
}

type sessionAuthProvider struct {
	client *model.Client4
	user   *model.User
}

func (p sessionAuthProvider) ValidateAuth(context.Context) error { return nil }

func (p sessionAuthProvider) GetAuthenticatedMattermostClient(context.Context) (*model.Client4, error) {
	return p.client, nil
}

func (p sessionAuthProvider) GetAuthenticatedUser(context.Context) (*model.User, error) {
	return p.user, nil
}

func callRegisteredMCPTool(t *testing.T, provider *MattermostToolProvider, name string, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	var tool MCPTool
	for _, candidate := range provider.mcpTools() {
		if candidate.Name == name {
			tool = candidate
			break
		}
	}
	require.NotEmpty(t, tool.Name, "tool %s is not registered", name)

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	provider.registerDynamicTool(server, tool)

	ctx := t.Context()
	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
}

func mcpToolText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var text string
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

func newIDLookupServer(t *testing.T, channels map[string]*model.Channel, posts map[string]*model.Post, files map[string]*model.FileInfo) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for id, ch := range channels {
		mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", id), serveJSON(ch))
	}
	for id, post := range posts {
		mux.HandleFunc(fmt.Sprintf("/api/v4/posts/%s", id), serveJSON(post))
	}
	for id, info := range files {
		mux.HandleFunc(fmt.Sprintf("/api/v4/files/%s/info", id), serveJSON(info))
	}
	return httptest.NewServer(mux)
}

func serveJSON(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

func schemaPropertyNames(schema *jsonschema.Schema) []string {
	seen := map[*jsonschema.Schema]bool{}
	names := map[string]struct{}{}
	collectSchemaPropertyNames(schema, seen, names)
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	return out
}

func collectSchemaPropertyNames(schema *jsonschema.Schema, seen map[*jsonschema.Schema]bool, names map[string]struct{}) {
	if schema == nil || seen[schema] {
		return
	}
	seen[schema] = true
	for name, prop := range schema.Properties {
		names[name] = struct{}{}
		collectSchemaPropertyNames(prop, seen, names)
	}
	for _, prop := range schema.Defs {
		collectSchemaPropertyNames(prop, seen, names)
	}
	for _, prop := range schema.Definitions {
		collectSchemaPropertyNames(prop, seen, names)
	}
	collectSchemaPropertyNames(schema.Items, seen, names)
	for _, s := range schema.PrefixItems {
		collectSchemaPropertyNames(s, seen, names)
	}
}
