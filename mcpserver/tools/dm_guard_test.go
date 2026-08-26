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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDirectOrGroupChannel(t *testing.T) {
	tests := []struct {
		name string
		ch   *model.Channel
		want bool
	}{
		{"nil", nil, false},
		{"open", &model.Channel{Type: model.ChannelTypeOpen}, false},
		{"private", &model.Channel{Type: model.ChannelTypePrivate}, false},
		{"direct", &model.Channel{Type: model.ChannelTypeDirect}, true},
		{"group", &model.Channel{Type: model.ChannelTypeGroup}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isDirectOrGroupChannel(tt.ch))
		})
	}
}

func TestWithoutDirectAndGroup(t *testing.T) {
	open := &model.Channel{Id: "o", Type: model.ChannelTypeOpen, DisplayName: "Town Square"}
	dm := &model.Channel{Id: "d", Type: model.ChannelTypeDirect}
	gm := &model.Channel{Id: "g", Type: model.ChannelTypeGroup}
	priv := &model.Channel{Id: "p", Type: model.ChannelTypePrivate, DisplayName: "Secret"}

	got := withoutDirectAndGroup([]*model.Channel{open, dm, gm, priv})
	require.Len(t, got, 2)
	assert.Equal(t, "o", got[0].Id)
	assert.Equal(t, "p", got[1].Id)
}

func TestRejectDirectOrGroupArgs(t *testing.T) {
	openID := model.NewId()
	dmID := model.NewId()
	gmID := model.NewId()
	openPostID := model.NewId()
	dmPostID := model.NewId()
	openFileID := model.NewId()
	dmFileID := model.NewId()

	channels := map[string]*model.Channel{
		openID: {Id: openID, Type: model.ChannelTypeOpen, DisplayName: "Town Square"},
		dmID:   {Id: dmID, Type: model.ChannelTypeDirect},
		gmID:   {Id: gmID, Type: model.ChannelTypeGroup},
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
		{"bot session dm tool args (username) are not guarded", botCtx, map[string]any{"username": "alice", "message": "hi"}, nil},
		{"bot session group_message args (usernames) are not guarded", botCtx, map[string]any{"usernames": []any{"alice", "bob"}, "message": "hi"}, nil},
		{"nil context is a no-op", nil, map[string]any{"channel_id": dmID}, nil},
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

	tests := []struct {
		name       string
		auth       auth.AuthenticationProvider
		wantBot    bool
		wantUserID string
	}{
		{"human user", identityAuthProvider{user: human}, false, human.Id},
		{"bot user", identityAuthProvider{user: bot}, true, bot.Id},
		{"lookup error fails closed", identityAuthProvider{err: fmt.Errorf("unavailable")}, true, ""},
		{"nil user fails closed", identityAuthProvider{}, true, ""},
		{"no identity provider fails closed", fakeToolAuthProvider{}, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &MattermostToolProvider{
				authProvider: tt.auth,
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

func TestExecuteSemanticSearchSetsExcludeDirectAndGroup(t *testing.T) {
	tests := []struct {
		name    string
		exclude bool
	}{
		{"bot session", true},
		{"human session", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capturing := &capturingSearchService{}
			provider := &MattermostToolProvider{
				logger:        &testLogger{t: t},
				searchService: capturing,
			}
			_, err := provider.executeSemanticSearch(t.Context(), newTestClient("https://mm.example.com"), CombinedSearchArgs{
				Query:         "hello",
				SemanticLimit: 10,
			}, "user1", tt.exclude)
			require.NoError(t, err)
			assert.Equal(t, tt.exclude, capturing.lastOpts.ExcludeDirectAndGroup)
		})
	}
}

func TestExecuteKeywordSearchFiltersDirectAndGroup(t *testing.T) {
	openID := model.NewId()
	dmID := model.NewId()
	openPost := &model.Post{Id: model.NewId(), ChannelId: openID, UserId: model.NewId(), Message: "public", CreateAt: 2}
	dmPost := &model.Post{Id: model.NewId(), ChannelId: dmID, UserId: model.NewId(), Message: "secret", CreateAt: 1}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/posts/search", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.PostList{
			Order: []string{openPost.Id, dmPost.Id},
			Posts: map[string]*model.Post{openPost.Id: openPost, dmPost.Id: dmPost},
		})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: openID, Type: model.ChannelTypeOpen, DisplayName: "Town Square"})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", openPost.UserId), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: openPost.UserId, Username: "alice"})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", dmPost.UserId), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: dmPost.UserId, Username: "bob"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	args := CombinedSearchArgs{Query: "hello", KeywordLimit: 10}

	t.Run("human session keeps DM posts", func(t *testing.T) {
		got, err := provider.executeKeywordSearch(t.Context(), client, args, false)
		require.NoError(t, err)
		require.Len(t, got, 2)
	})
	t.Run("bot session drops DM posts", func(t *testing.T) {
		got, err := provider.executeKeywordSearch(t.Context(), client, args, true)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, openPost.Id, got[0].Post.Id)
	})
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

func TestFormatPostListChronoFiltersDirectAndGroupForBot(t *testing.T) {
	openID := model.NewId()
	dmID := model.NewId()
	authorID := model.NewId()
	openPost := &model.Post{Id: model.NewId(), ChannelId: openID, UserId: authorID, Message: "public mention", CreateAt: 2}
	dmPost := &model.Post{Id: model.NewId(), ChannelId: dmID, UserId: authorID, Message: "private mention", CreateAt: 1}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", openID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: openID, Type: model.ChannelTypeOpen})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", dmID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect})
	})
	mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.User{{Id: authorID, Username: "alice"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	postList := &model.PostList{
		Order: []string{openPost.Id, dmPost.Id},
		Posts: map[string]*model.Post{openPost.Id: openPost, dmPost.Id: dmPost},
	}

	t.Run("human session keeps DM posts", func(t *testing.T) {
		out := provider.formatPostListChrono(&MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}, postList, "posts mentioning you")
		assert.Contains(t, out, "public mention")
		assert.Contains(t, out, "private mention")
	})
	t.Run("bot session drops DM posts", func(t *testing.T) {
		out := provider.formatPostListChrono(&MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), IsBotSession: true}, postList, "posts mentioning you")
		assert.Contains(t, out, "public mention")
		assert.NotContains(t, out, "private mention")
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

func TestToolSchemasGuardKnownIdentifiers(t *testing.T) {
	provider := &MattermostToolProvider{
		logger:     &testLogger{t: t},
		accessMode: AccessModeRemote,
		devMode:    true,
	}

	known := map[string]bool{}
	for name := range guardedArgKeys {
		known[name] = true
	}
	for name := range identifierArgsNotResolvedByGuard {
		known[name] = true
	}

	for _, tool := range provider.mcpTools() {
		for _, name := range schemaPropertyNames(tool.Schema) {
			if !looksLikeChannelOrPostIdentifier(name) {
				continue
			}
			if known[name] {
				continue
			}
			t.Errorf("tool %q has property %q which looks like a channel or post identifier but is not handled by the DM/GM guard. Add it to guardedArgKeys in dm_guard.go (resolved via GetChannel, GetPost, or GetFileInfo), or to identifierArgsNotResolvedByGuard if it cannot reach a D/G channel — and document why.", tool.Name, name)
		}
	}

	assert.True(t, identifierArgsNotResolvedByGuard["channel_name"], "channel_name must stay in the known-identifier set")
	_, guarded := guardedArgKeys["channel_name"]
	assert.False(t, guarded, "channel_name is not an ID; get_channel_info filters D/G results instead of resolving the name")
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
}

func (c *capturingSearchService) Enabled() bool { return true }

func (c *capturingSearchService) Search(_ context.Context, _ string, opts search.Options) ([]search.RAGResult, error) {
	c.lastOpts = opts
	return nil, nil
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
