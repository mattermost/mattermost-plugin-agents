// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readChannelTestServer builds a mock Mattermost server for read_channel tests.
// The posts handler returns numPosts posts (all authored by the same user) and
// records the page / per_page query params it was called with.
func readChannelTestServer(t *testing.T, channelID, teamID, userID string, numPosts int) (*httptest.Server, *url.Values) {
	t.Helper()
	captured := &url.Values{}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{
			Id:          channelID,
			Name:        "general",
			DisplayName: "General",
			Type:        model.ChannelTypeOpen,
			TeamId:      teamID,
		})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/teams/%s", teamID), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Team{Id: teamID, Name: "eng", DisplayName: "Engineering"})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/posts", channelID), func(w http.ResponseWriter, r *http.Request) {
		*captured = r.URL.Query()
		list := &model.PostList{Order: make([]string, 0, numPosts), Posts: make(map[string]*model.Post, numPosts)}
		for i := 0; i < numPosts; i++ {
			id := fmt.Sprintf("post%023d", i)
			list.Order = append(list.Order, id)
			list.Posts[id] = &model.Post{Id: id, ChannelId: channelID, UserId: userID, Message: fmt.Sprintf("message %d", i), CreateAt: int64(1000 + i)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.User{{Id: userID, Username: "author"}})
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, captured
}

func TestToolReadChannelPagination(t *testing.T) {
	channelID := model.NewId()
	teamID := model.NewId()
	userID := model.NewId()

	tests := []struct {
		name            string
		args            string
		numPosts        int
		expectPage      string
		expectPerPage   string
		expectMoreHint  bool
		expectPageLabel string
	}{
		{
			name:            "defaults to page 0 per_page 20",
			args:            fmt.Sprintf(`{"channel_id":%q}`, channelID),
			numPosts:        5,
			expectPage:      "0",
			expectPerPage:   "20",
			expectMoreHint:  false,
			expectPageLabel: "(page 0)",
		},
		{
			name:            "per_page and page are forwarded",
			args:            fmt.Sprintf(`{"channel_id":%q,"per_page":50,"page":2}`, channelID),
			numPosts:        10,
			expectPage:      "2",
			expectPerPage:   "50",
			expectMoreHint:  false,
			expectPageLabel: "(page 2)",
		},
		{
			name:           "legacy limit aliases per_page",
			args:           fmt.Sprintf(`{"channel_id":%q,"limit":35}`, channelID),
			numPosts:       5,
			expectPage:     "0",
			expectPerPage:  "35",
			expectMoreHint: false,
		},
		{
			name:           "per_page is capped at 200",
			args:           fmt.Sprintf(`{"channel_id":%q,"per_page":500}`, channelID),
			numPosts:       5,
			expectPage:     "0",
			expectPerPage:  "200",
			expectMoreHint: false,
		},
		{
			name:           "full page surfaces a next-page hint",
			args:           fmt.Sprintf(`{"channel_id":%q,"per_page":5}`, channelID),
			numPosts:       5,
			expectPage:     "0",
			expectPerPage:  "5",
			expectMoreHint: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, captured := readChannelTestServer(t, channelID, teamID, userID, tt.numPosts)
			provider := newTestProvider(t, ts.URL)
			client := newTestClient(ts.URL)
			mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID}

			argsGetter := func(target any) error {
				return json.Unmarshal([]byte(tt.args), target)
			}

			out, err := provider.toolReadChannel(mcpCtx, argsGetter)
			require.NoError(t, err)

			assert.Equal(t, tt.expectPage, captured.Get("page"), "page query param")
			assert.Equal(t, tt.expectPerPage, captured.Get("per_page"), "per_page query param")

			if tt.expectPageLabel != "" {
				assert.Contains(t, out, tt.expectPageLabel)
			}
			if tt.expectMoreHint {
				assert.Contains(t, out, "More posts available")
				assert.Contains(t, out, fmt.Sprintf("page=%s", "1"))
			} else {
				assert.NotContains(t, out, "More posts available")
			}
		})
	}
}

func TestToolGetChannelInfoChannelRole(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	teamID := model.NewId()

	tests := []struct {
		name         string
		memberStatus int
		schemeAdmin  bool
		schemeGuest  bool
		expectedRole string
		expectInMap  bool
	}{
		{name: "scheme admin", memberStatus: http.StatusOK, schemeAdmin: true, expectedRole: "admin", expectInMap: true},
		{name: "scheme guest", memberStatus: http.StatusOK, schemeGuest: true, expectedRole: "guest", expectInMap: true},
		{name: "regular member", memberStatus: http.StatusOK, expectedRole: "member", expectInMap: true},
		{name: "not a member", memberStatus: http.StatusNotFound, expectedRole: "not_member", expectInMap: true},
		{name: "server error omits role", memberStatus: http.StatusInternalServerError, expectInMap: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.Channel{
					Id:          channelID,
					Name:        "general",
					DisplayName: "General",
					Type:        model.ChannelTypeOpen,
					TeamId:      teamID,
				})
			})
			mux.HandleFunc(fmt.Sprintf("/api/v4/teams/%s", teamID), func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.Team{Id: teamID, Name: "eng", DisplayName: "Engineering"})
			})
			mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/stats", channelID), func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.ChannelStats{ChannelId: channelID, MemberCount: 7})
			})
			mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/members/%s", channelID, userID), func(w http.ResponseWriter, r *http.Request) {
				if tt.memberStatus != http.StatusOK {
					http.Error(w, "err", tt.memberStatus)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.ChannelMember{
					ChannelId:   channelID,
					UserId:      userID,
					SchemeAdmin: tt.schemeAdmin,
					SchemeGuest: tt.schemeGuest,
					SchemeUser:  !tt.schemeAdmin && !tt.schemeGuest,
				})
			})

			ts := httptest.NewServer(mux)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			client := newTestClient(ts.URL)
			mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: userID}

			argsGetter := func(target any) error {
				return json.Unmarshal([]byte(fmt.Sprintf(`{"channel_id":%q}`, channelID)), target)
			}

			out, err := provider.toolGetChannelInfo(mcpCtx, argsGetter)
			require.NoError(t, err)
			assert.Contains(t, out, channelID, "expected channel ID in formatted output")

			roleLine := fmt.Sprintf("Your role: %s", tt.expectedRole)
			if tt.expectInMap {
				assert.Contains(t, out, roleLine, "expected role line in formatted output")
			} else {
				assert.NotContains(t, out, "Your role:", "expected role line to be omitted on error")
			}
		})
	}
}
