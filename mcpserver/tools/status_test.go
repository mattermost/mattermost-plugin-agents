// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_user_status bad", func() (string, error) {
			return provider.toolGetUserStatus(mcpCtx, GetUserStatusArgs{UserID: "bad"})
		}, "must be a valid ID"},
		{"get_users_statuses empty", func() (string, error) {
			return provider.toolGetUsersStatuses(mcpCtx, GetUsersStatusesArgs{})
		}, "cannot be empty"},
		{"get_user_custom_status bad", func() (string, error) {
			return provider.toolGetUserCustomStatus(mcpCtx, GetUserCustomStatusArgs{UserID: "bad"})
		}, "must be a valid ID"},
		{"set_status invalid", func() (string, error) {
			return provider.toolSetStatus(mcpCtx, SetStatusArgs{Status: "sleeping"})
		}, "invalid status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestToolGetUserStatus(t *testing.T) {
	userID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/status", userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Status{UserId: userID, Status: model.StatusOnline})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "erin"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetUserStatus(mcpCtx, GetUserStatusArgs{UserID: userID})
	require.NoError(t, err)
	assert.Contains(t, out, "Status: online")
	assert.Contains(t, out, "erin")
}

func TestToolSetStatus(t *testing.T) {
	userID := model.NewId()
	mux := http.NewServeMux()
	var got model.Status
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/status", userID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&got)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolSetStatus(mcpCtx, SetStatusArgs{Status: model.StatusAway})
	require.NoError(t, err)
	assert.Equal(t, model.StatusAway, got.Status)
	assert.Contains(t, out, "away")
}

func TestToolSetDnd(t *testing.T) {
	userID := model.NewId()
	when := time.Now().Add(time.Hour).UTC()
	mux := http.NewServeMux()
	var got model.Status
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/status", userID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&got)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolSetDnd(mcpCtx, SetDndArgs{EndTime: when.Format(time.RFC3339)})
	require.NoError(t, err)
	assert.Equal(t, model.StatusDnd, got.Status)
	assert.Equal(t, when.Unix(), got.DNDEndTime, "dnd_end_time should be in seconds")
	assert.Contains(t, out, "Do Not Disturb")
}

func TestToolGetUserCustomStatus(t *testing.T) {
	userID := model.NewId()
	cs := &model.CustomStatus{Emoji: "rocket", Text: "shipping"}
	csJSON, _ := json.Marshal(cs)

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s", userID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "frank", Props: model.StringMap{"customStatus": string(csJSON)}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetUserCustomStatus(mcpCtx, GetUserCustomStatusArgs{UserID: userID})
	require.NoError(t, err)
	assert.Contains(t, out, ":rocket:")
	assert.Contains(t, out, "shipping")
}
