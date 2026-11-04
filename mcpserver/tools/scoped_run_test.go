// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scopedServer answers like a Mattermost server does a scoped session: the
// lookups a scoped run may make succeed, and anything else, such as the
// bot's own team and channel memberships, is refused.
//
// The returned func lists the requests that were refused. A scoped run should
// make none: a refusal is either a route that always fails or a lookup that
// should have been skipped.
func scopedServer(t *testing.T, team *model.Team, channel *model.Channel) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var refused []string
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(v))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v4/channels/search", func(w http.ResponseWriter, r *http.Request) {
		var search model.ChannelSearch
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&search))
		if len(search.TeamIds) > 0 && search.TeamIds[0] != team.Id {
			write(w, model.ChannelListWithTeamData{})
			return
		}
		write(w, model.ChannelListWithTeamData{{Channel: *channel}})
	})
	mux.HandleFunc("GET /api/v4/teams/{team}/channels/name/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("name") != channel.Name {
			http.Error(w, scopedRefusal, http.StatusForbidden)
			return
		}
		write(w, channel)
	})
	mux.HandleFunc("GET /api/v4/channels/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		write(w, &model.ChannelStats{ChannelId: channel.Id, MemberCount: 3})
	})
	mux.HandleFunc("GET /api/v4/teams/{first}/{second}", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.PathValue("first") == "name":
			if !strings.EqualFold(r.PathValue("second"), team.Name) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			write(w, team)
		case r.PathValue("second") == "channels":
			write(w, []*model.Channel{channel})
		default:
			mu.Lock()
			refused = append(refused, r.Method+" "+r.URL.Path)
			mu.Unlock()
			http.Error(w, scopedRefusal, http.StatusForbidden)
		}
	})
	mux.HandleFunc("POST /api/v4/teams/search", func(w http.ResponseWriter, r *http.Request) {
		write(w, []*model.Team{team})
	})
	mux.HandleFunc("GET /api/v4/teams/{id}", func(w http.ResponseWriter, r *http.Request) {
		write(w, team)
	})
	mux.HandleFunc("GET /api/v4/channels/{id}", func(w http.ResponseWriter, r *http.Request) {
		write(w, channel)
	})
	mux.HandleFunc("POST /api/v4/posts/search", func(w http.ResponseWriter, r *http.Request) {
		write(w, model.NewPostList())
	})
	mux.HandleFunc("POST /api/v4/teams/{id}/posts/search", func(w http.ResponseWriter, r *http.Request) {
		write(w, model.NewPostList())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		refused = append(refused, r.Method+" "+r.URL.Path)
		mu.Unlock()
		http.Error(w, scopedRefusal, http.StatusForbidden)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), refused...)
	}
}

const scopedRefusal = `{"id":"api.context.scoped_session_out_of_scope.app_error","message":"route not allowed for scoped sessions","status_code":403}`

func scopedContext(t *testing.T) context.Context {
	t.Helper()
	scope := scopedsession.NewRunScope(model.NewId(), []string{model.NewId()}, model.NewId())
	return scopedsession.WithBinding(t.Context(), scopedsession.NewBinding(scope))
}

func TestScopedRunLooksThingsUpByName(t *testing.T) {
	team := &model.Team{Id: model.NewId(), Name: "eng", DisplayName: "Engineering"}
	channel := &model.Channel{Id: model.NewId(), TeamId: team.Id, Name: "ops-room", DisplayName: "Ops", Type: model.ChannelTypeOpen}

	tests := []struct {
		name string
		run  func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error)
		want []string
	}{
		{
			name: "get_channel_info by display name on a team",
			run: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolGetChannelInfo(mcpCtx, GetChannelInfoArgs{ChannelName: "ops", TeamID: team.Id})
			},
			want: []string{channel.Id},
		},
		{
			name: "get_channel_info by partial name on a team",
			run: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolGetChannelInfo(mcpCtx, GetChannelInfoArgs{ChannelName: "op", TeamID: team.Id})
			},
			want: []string{channel.Id},
		},
		{
			name: "get_channel_info not found points at tools a scoped run can call",
			run: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolGetChannelInfo(mcpCtx, GetChannelInfoArgs{ChannelName: "nothing-like-it", TeamID: team.Id})
			},
			want: []string{"search_channels", "list_team_channels"},
		},
		{
			name: "get_channel_info by ID skips the membership lookup",
			run: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolGetChannelInfo(mcpCtx, GetChannelInfoArgs{ChannelID: channel.Id})
			},
			want: []string{channel.Id},
		},
		{
			name: "list_team_channels skips the private channel listing",
			run: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolListTeamChannels(mcpCtx, ListTeamChannelsArgs{TeamID: team.Id})
			},
			want: []string{channel.Id},
		},
		{
			name: "get_team_info by URL name",
			run: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolGetTeamInfo(mcpCtx, GetTeamInfoArgs{TeamName: "eng"})
			},
			want: []string{team.Id},
		},
		{
			name: "get_team_info by display name",
			run: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolGetTeamInfo(mcpCtx, GetTeamInfoArgs{TeamName: "Engineering"})
			},
			want: []string{team.Id},
		},
		{
			name: "search_posts in a channel named by URL name",
			run: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolCombinedSearch(mcpCtx, CombinedSearchArgs{Query: "deploy", In: "ops-room"})
			},
			want: []string{"deploy"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, refused := scopedServer(t, team, channel)
			provider := newTestProvider(t, server.URL)
			mcpCtx := &MCPToolContext{Client: newTestClient(server.URL), Ctx: scopedContext(t), UserID: model.NewId()}

			out, err := tt.run(provider, mcpCtx)
			require.NoError(t, err)
			for _, want := range tt.want {
				assert.Contains(t, out, want)
			}
			assert.NotContains(t, out, "get_user_channels", "a scoped run cannot call get_user_channels")
			assert.Empty(t, refused(), "a scoped run skips lookups that are always refused")
		})
	}
}

func TestUnscopedRunStillListsItsOwnMemberships(t *testing.T) {
	userID := model.NewId()
	team := &model.Team{Id: model.NewId(), Name: "eng", DisplayName: "Engineering"}
	channel := &model.Channel{Id: model.NewId(), TeamId: team.Id, Name: "ops-room", DisplayName: "Ops", Type: model.ChannelTypeOpen}

	var paths []string
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(v))
	}
	mux.HandleFunc("GET /api/v4/users/me", func(w http.ResponseWriter, r *http.Request) {
		write(w, &model.User{Id: userID})
	})
	mux.HandleFunc("GET /api/v4/users/{id}/teams", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		write(w, []*model.Team{team})
	})
	mux.HandleFunc("GET /api/v4/users/{id}/teams/{team}/channels", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		write(w, []*model.Channel{channel})
	})
	mux.HandleFunc("GET /api/v4/channels/{id}", func(w http.ResponseWriter, r *http.Request) { write(w, channel) })
	mux.HandleFunc("GET /api/v4/teams/{id}", func(w http.ResponseWriter, r *http.Request) { write(w, team) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusNotFound) })
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestProvider(t, server.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(server.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolGetChannelInfo(mcpCtx, GetChannelInfoArgs{ChannelName: "ops", TeamID: team.Id})
	require.NoError(t, err)
	assert.Contains(t, out, channel.Id)
	out, err = provider.toolGetTeamInfo(mcpCtx, GetTeamInfoArgs{TeamName: "eng"})
	require.NoError(t, err)
	assert.Contains(t, out, team.Id)
	assert.Len(t, paths, 2, "an unscoped run reads the user's own memberships")
}

// The server answers a missing item and an out-of-scope one the same way, and
// the model must be told just that, not the server's internal error.
func TestScopedOutOfScopeErrorsReachTheModelAsNotFound(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantText   string
		wantHidden string
	}{
		{
			name:       "out of scope",
			status:     http.StatusForbidden,
			body:       `{"id":"api.context.scoped_session_out_of_scope.app_error","message":"route not allowed for scoped sessions","status_code":403}`,
			wantText:   "Error: not found, or not accessible in this agent's scope",
			wantHidden: "scoped_session_out_of_scope",
		},
		{
			name:     "other failures keep their text",
			status:   http.StatusInternalServerError,
			body:     `{"id":"app.channel.get.find.app_error","message":"channel store exploded","status_code":500}`,
			wantText: "channel store exploded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := model.NewId()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v4/users/me" {
					w.Header().Set("Content-Type", "application/json")
					assert.NoError(t, json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "bot"}))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			provider := newTestProvider(t, server.URL)
			provider.authProvider = auth.NewSessionAuthenticationProvider(server.URL, "", &testLogger{t: t})
			provider.accessMode = AccessModeRemote
			mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
			provider.ProvideTools(mcpServer)

			ctx := context.WithValue(t.Context(), auth.SessionIDContextKey, "session-id")
			ctx = context.WithValue(ctx, auth.TokenResolverContextKey, auth.TokenResolver(func(string) (string, error) { return "token", nil }))
			serverTransport, clientTransport := mcp.NewInMemoryTransports()
			serverSession, err := mcpServer.Connect(ctx, serverTransport, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = serverSession.Close() })
			session, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0"}, nil).Connect(t.Context(), clientTransport, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.Close() })

			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
				Name:      "get_channel_info",
				Arguments: map[string]any{"channel_id": model.NewId()},
			})
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.Len(t, result.Content, 1)
			text := result.Content[0].(*mcp.TextContent).Text
			assert.Contains(t, text, tt.wantText)
			if tt.wantHidden != "" {
				assert.NotContains(t, text, tt.wantHidden)
			}
		})
	}
}
