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

func TestUserToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_user bad", func() (string, error) { return provider.toolGetUser(mcpCtx, GetUserArgs{UserID: "bad"}) }, "must be a valid ID"},
		{"get_user_by_username empty", func() (string, error) {
			return provider.toolGetUserByUsername(mcpCtx, GetUserByUsernameArgs{Username: ""})
		}, "username cannot be empty"},
		{"get_user_by_email empty", func() (string, error) {
			return provider.toolGetUserByEmail(mcpCtx, GetUserByEmailArgs{Email: ""})
		}, "email cannot be empty"},
		{"get_users_by_ids empty", func() (string, error) {
			return provider.toolGetUsersByIDs(mcpCtx, GetUsersByIDsArgs{})
		}, "cannot be empty"},
		{"get_users_by_usernames empty", func() (string, error) {
			return provider.toolGetUsersByUsernames(mcpCtx, GetUsersByUsernamesArgs{})
		}, "cannot be empty"},
		{"get_user_cpa_values bad", func() (string, error) {
			return provider.toolGetUserCPAValues(mcpCtx, GetUserCPAValuesArgs{UserID: "bad"})
		}, "must be a valid ID"},
		{"update_user no fields", func() (string, error) {
			return provider.toolUpdateUser(mcpCtx, UpdateUserArgs{UserID: model.NewId()})
		}, "provide at least one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestToolGetUser(t *testing.T) {
	userID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "dave", Email: "dave@example.com", Position: "Engineer"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetUser(mcpCtx, GetUserArgs{UserID: userID})
	require.NoError(t, err)
	assert.Contains(t, out, "dave")
	assert.Contains(t, out, "Engineer")
}

func TestToolGetUserStats(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/users/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.UsersStats{TotalUsersCount: 4242})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetUserStats(mcpCtx, GetUserStatsArgs{})
	require.NoError(t, err)
	assert.Contains(t, out, "4242")
}

func TestToolUpdateUser(t *testing.T) {
	userID := model.NewId()
	mux := http.NewServeMux()
	var gotPatch model.UserPatch
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/patch", userID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotPatch)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "dave"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	pos := "Staff Engineer"
	out, err := provider.toolUpdateUser(mcpCtx, UpdateUserArgs{UserID: userID, Position: &pos})
	require.NoError(t, err)
	require.NotNil(t, gotPatch.Position)
	assert.Equal(t, "Staff Engineer", *gotPatch.Position)
	assert.Contains(t, out, "Successfully updated user")
}
