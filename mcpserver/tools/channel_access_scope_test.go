// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newChannelScopeTestServer(t *testing.T, channels []*model.Channel, teams []*model.Team) *httptest.Server {
	t.Helper()

	channelMap := make(map[string]*model.Channel, len(channels))
	for _, channel := range channels {
		channelMap[channel.Id] = channel
	}

	teamMap := make(map[string]*model.Team, len(teams))
	for _, team := range teams {
		teamMap[team.Id] = team
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v4/channels/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v4/channels/")
		switch {
		case strings.HasSuffix(path, "/members"):
			channelID := strings.TrimSuffix(path, "/members")
			if _, ok := channelMap[channelID]; !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]*model.ChannelMember{
				{ChannelId: channelID, UserId: model.NewId()},
			})
		case strings.HasSuffix(path, "/posts"):
			channelID := strings.TrimSuffix(path, "/posts")
			channel, ok := channelMap[channelID]
			if !ok {
				http.NotFound(w, r)
				return
			}
			postID := model.NewId()
			post := &model.Post{
				Id:        postID,
				ChannelId: channelID,
				UserId:    model.NewId(),
				Message:   "hello from " + channel.DisplayName,
			}
			postList := &model.PostList{
				Order: []string{postID},
				Posts: map[string]*model.Post{postID: post},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(postList)
		case strings.HasSuffix(path, "/stats"):
			channelID := strings.TrimSuffix(path, "/stats")
			if _, ok := channelMap[channelID]; !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&model.ChannelStats{ChannelId: channelID, MemberCount: 7})
		default:
			channel, ok := channelMap[path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(channel)
		}
	})

	mux.HandleFunc("/api/v4/teams/", func(w http.ResponseWriter, r *http.Request) {
		teamID := strings.TrimPrefix(r.URL.Path, "/api/v4/teams/")
		team, ok := teamMap[teamID]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(team)
	})

	mux.HandleFunc("/api/v4/posts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var post model.Post
		require.NoError(t, json.Unmarshal(body, &post))

		post.Id = model.NewId()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&post)
	})

	return httptest.NewServer(mux)
}

func newChannelScopeTestClient(serverURL string) *model.Client4 {
	client := model.NewAPIv4Client(serverURL)
	client.SetToken("test-token")
	return client
}

func TestToolReadChannel_RejectsChannelOutsideAllowlist(t *testing.T) {
	teamID := model.NewId()
	allowedChannel := &model.Channel{
		Id:          model.NewId(),
		TeamId:      teamID,
		Type:        model.ChannelTypeOpen,
		DisplayName: "Town Square",
	}
	blockedChannel := &model.Channel{
		Id:          model.NewId(),
		TeamId:      teamID,
		Type:        model.ChannelTypePrivate,
		DisplayName: "Private Ops",
	}

	ts := newChannelScopeTestServer(t, []*model.Channel{allowedChannel, blockedChannel}, nil)
	defer ts.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpCtx := &MCPToolContext{
		Ctx:    context.Background(),
		Client: newChannelScopeTestClient(ts.URL),
		MattermostAccessScope: &llm.MattermostAccessScope{
			TeamID:            teamID,
			AllowedChannelIDs: []string{allowedChannel.Id},
		},
	}

	result, err := provider.toolReadChannel(mcpCtx, func(target any) error {
		return json.Unmarshal([]byte(fmt.Sprintf(`{"channel_id":%q}`, blockedChannel.Id)), target)
	})
	require.Error(t, err)
	assert.Equal(t, "channel is outside execution scope", result)
	assert.Contains(t, err.Error(), blockedChannel.Id)
}

func TestToolGetChannelInfo_FiltersChannelsOutsideAllowlist(t *testing.T) {
	teamID := model.NewId()
	allowedChannel := &model.Channel{
		Id:          model.NewId(),
		TeamId:      teamID,
		Type:        model.ChannelTypeOpen,
		DisplayName: "Town Square",
	}
	blockedChannel := &model.Channel{
		Id:          model.NewId(),
		TeamId:      teamID,
		Type:        model.ChannelTypePrivate,
		DisplayName: "Private Ops",
	}

	ts := newChannelScopeTestServer(t, []*model.Channel{allowedChannel, blockedChannel}, nil)
	defer ts.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpCtx := &MCPToolContext{
		Ctx:    context.Background(),
		Client: newChannelScopeTestClient(ts.URL),
		MattermostAccessScope: &llm.MattermostAccessScope{
			TeamID:            teamID,
			AllowedChannelIDs: []string{allowedChannel.Id},
		},
	}

	result, err := provider.toolGetChannelInfo(mcpCtx, func(target any) error {
		return json.Unmarshal([]byte(fmt.Sprintf(`{"channel_id":%q}`, blockedChannel.Id)), target)
	})
	require.NoError(t, err)
	assert.Equal(t, "no channels found matching criteria within the execution scope", result)
}

func TestToolGetChannelMembers_RejectsChannelOutsideAllowlist(t *testing.T) {
	teamID := model.NewId()
	allowedChannel := &model.Channel{
		Id:          model.NewId(),
		TeamId:      teamID,
		Type:        model.ChannelTypeOpen,
		DisplayName: "Town Square",
	}
	blockedChannel := &model.Channel{
		Id:          model.NewId(),
		TeamId:      teamID,
		Type:        model.ChannelTypePrivate,
		DisplayName: "Private Ops",
	}

	ts := newChannelScopeTestServer(t, []*model.Channel{allowedChannel, blockedChannel}, nil)
	defer ts.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpCtx := &MCPToolContext{
		Ctx:    context.Background(),
		Client: newChannelScopeTestClient(ts.URL),
		MattermostAccessScope: &llm.MattermostAccessScope{
			TeamID:            teamID,
			AllowedChannelIDs: []string{allowedChannel.Id},
		},
	}

	result, err := provider.toolGetChannelMembers(mcpCtx, func(target any) error {
		return json.Unmarshal([]byte(fmt.Sprintf(`{"channel_id":%q}`, blockedChannel.Id)), target)
	})
	require.Error(t, err)
	assert.Equal(t, "channel is outside execution scope", result)
	assert.Contains(t, err.Error(), blockedChannel.Id)
}

func TestFilterSearchResultsByScope_RemovesChannelsOutsideAllowlist(t *testing.T) {
	teamID := model.NewId()
	publicChannel := &model.Channel{
		Id:          model.NewId(),
		TeamId:      teamID,
		Type:        model.ChannelTypeOpen,
		DisplayName: "Town Square",
	}
	privateChannel := &model.Channel{
		Id:          model.NewId(),
		TeamId:      teamID,
		Type:        model.ChannelTypePrivate,
		DisplayName: "Private Ops",
	}

	ts := newChannelScopeTestServer(t, []*model.Channel{publicChannel, privateChannel}, nil)
	defer ts.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpCtx := &MCPToolContext{
		Ctx:    context.Background(),
		Client: newChannelScopeTestClient(ts.URL),
		MattermostAccessScope: &llm.MattermostAccessScope{
			TeamID:            teamID,
			AllowedChannelIDs: []string{publicChannel.Id},
		},
	}

	results := []searchPostResult{
		{Post: &model.Post{Id: "public-post", ChannelId: publicChannel.Id}},
		{Post: &model.Post{Id: "private-post", ChannelId: privateChannel.Id}},
	}

	filtered := provider.filterSearchResultsByScope(mcpCtx, results)
	require.Len(t, filtered, 1)
	assert.Equal(t, "public-post", filtered[0].Post.Id)
}
