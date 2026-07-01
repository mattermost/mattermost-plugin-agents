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

			out, err := provider.toolGetChannelInfo(mcpCtx, GetChannelInfoArgs{ChannelID: channelID})
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
