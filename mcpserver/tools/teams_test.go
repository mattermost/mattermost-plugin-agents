// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestTeamLookupServer builds an httptest server stubbing the endpoints
// resolveTeamByName touches: users/me, the user's teams, and teams/search.
// userTeams is what GET /api/v4/users/{id}/teams returns; searchStatus and
// searchResults control POST /api/v4/teams/search.
func newTestTeamLookupServer(t *testing.T, userTeams []*model.Team, searchStatus int, searchResults []*model.Team) *httptest.Server {
	t.Helper()

	userID := model.NewId()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v4/users/me", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "me"})
	})

	mux.HandleFunc("/api/v4/users/"+userID+"/teams", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(userTeams)
	})

	mux.HandleFunc("/api/v4/teams/search", func(w http.ResponseWriter, r *http.Request) {
		if searchStatus != http.StatusOK {
			http.Error(w, `{"message":"internal error"}`, searchStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(searchResults)
	})

	return httptest.NewServer(mux)
}

func TestGetTeamInfoByName(t *testing.T) {
	tests := []struct {
		name          string
		userTeams     []*model.Team
		searchStatus  int
		searchResults []*model.Team
		teamName      string
		wantErr       bool
		wantErrSubstr string
		wantContains  []string
	}{
		{
			name:          "no match returns guidance, not an error",
			userTeams:     []*model.Team{},
			searchStatus:  http.StatusOK,
			searchResults: []*model.Team{},
			teamName:      "Nonexistent",
			wantErr:       false,
			wantContains:  []string{"No team found matching", "ACTION REQUIRED"},
		},
		{
			name:          "search API failure returns an error",
			userTeams:     []*model.Team{},
			searchStatus:  http.StatusInternalServerError,
			searchResults: nil,
			teamName:      "Anything",
			wantErr:       true,
			wantErrSubstr: "error searching teams",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestTeamLookupServer(t, tt.userTeams, tt.searchStatus, tt.searchResults)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: newTestClient(ts.URL)}

			result, err := provider.toolGetTeamInfo(mcpCtx, GetTeamInfoArgs{TeamName: tt.teamName})

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSubstr)
				return
			}

			require.NoError(t, err)
			for _, substr := range tt.wantContains {
				assert.Contains(t, result, substr)
			}
		})
	}
}
