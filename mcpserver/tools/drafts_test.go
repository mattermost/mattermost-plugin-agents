// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDraftToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"list invalid team", func() (string, error) {
			return provider.toolListDrafts(mcpCtx, ListDraftsArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"create invalid channel", func() (string, error) {
			return provider.toolCreateDraft(mcpCtx, CreateDraftArgs{ChannelID: "bad", Message: "x"})
		}, "must be a valid ID"},
		{"create empty message", func() (string, error) {
			return provider.toolCreateDraft(mcpCtx, CreateDraftArgs{ChannelID: model.NewId(), Message: ""})
		}, "message cannot be empty"},
		{"create invalid root", func() (string, error) {
			return provider.toolCreateDraft(mcpCtx, CreateDraftArgs{ChannelID: model.NewId(), Message: "x", RootID: "bad"})
		}, "must be a valid ID"},
		{"delete invalid channel", func() (string, error) {
			return provider.toolDeleteDraft(mcpCtx, DeleteDraftArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"delete invalid root", func() (string, error) {
			return provider.toolDeleteDraft(mcpCtx, DeleteDraftArgs{ChannelID: model.NewId(), RootID: "bad"})
		}, "must be a valid ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestToolCreateDraft(t *testing.T) {
	channelID := model.NewId()
	rootID := model.NewId()
	botID := model.NewId()

	tests := []struct {
		name             string
		rootID           string
		trackAIGenerated bool
		wantOut          string
	}{
		{"channel draft", "", false, "draft in channel " + channelID},
		{"thread reply draft with AI attribution", rootID, true, "draft reply in thread " + rootID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			var got model.Draft
			mux.HandleFunc("/api/v4/drafts", func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				_ = json.NewDecoder(r.Body).Decode(&got)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(&got)
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			provider.trackAIGenerated = tt.trackAIGenerated
			mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), BotUserID: botID}

			out, err := provider.toolCreateDraft(mcpCtx, CreateDraftArgs{
				ChannelID: channelID,
				Message:   "reply for review",
				RootID:    tt.rootID,
			})
			require.NoError(t, err)
			assert.Contains(t, out, tt.wantOut)
			assert.Equal(t, channelID, got.ChannelId)
			assert.Equal(t, tt.rootID, got.RootId)
			assert.Equal(t, "reply for review", got.Message)
			if tt.trackAIGenerated {
				assert.Equal(t, botID, got.GetProps()["ai_generated_by"])
			} else {
				assert.Nil(t, got.GetProps()["ai_generated_by"])
			}
		})
	}
}

func TestToolDeleteDraft(t *testing.T) {
	userID := model.NewId()
	channelID := model.NewId()
	rootID := model.NewId()

	tests := []struct {
		name     string
		rootID   string
		wantPath string
	}{
		{"channel draft", "", fmt.Sprintf("/api/v4/users/%s/channels/%s/drafts", userID, channelID)},
		// A thread draft must target the thread route, never the channel-level draft.
		{"thread reply draft", rootID, fmt.Sprintf("/api/v4/users/%s/channels/%s/drafts/%s", userID, channelID, rootID)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.Draft{ChannelId: channelID, RootId: tt.rootID})
			}))
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

			out, err := provider.toolDeleteDraft(mcpCtx, DeleteDraftArgs{ChannelID: channelID, RootID: tt.rootID})
			require.NoError(t, err)
			assert.Contains(t, out, "Successfully deleted draft")
			assert.Equal(t, http.MethodDelete, gotMethod)
			assert.Equal(t, tt.wantPath, gotPath)
		})
	}
}

func TestToolDeleteDraftPropagatesError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(&model.AppError{Id: "api.drafts.disabled.app_error", StatusCode: http.StatusNotImplemented})
	}))
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: model.NewId()}

	_, err := provider.toolDeleteDraft(mcpCtx, DeleteDraftArgs{ChannelID: model.NewId()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error deleting draft")
}

func TestToolListDrafts(t *testing.T) {
	userID := model.NewId()
	teamID := model.NewId()
	olderChannel := model.NewId()
	newerChannel := model.NewId()

	tests := []struct {
		name     string
		drafts   []*model.Draft
		wantOut  []string
		wantSort bool
	}{
		{"no drafts", []*model.Draft{}, []string{"no drafts found"}, false},
		{"most recent first", []*model.Draft{
			{ChannelId: olderChannel, Message: "older draft", UpdateAt: 1000},
			{ChannelId: newerChannel, Message: "newer draft", UpdateAt: 2000},
		}, []string{"Found 2 draft(s)", "older draft", "newer draft", olderChannel, newerChannel}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams/%s/drafts", userID, teamID), func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tt.drafts)
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

			out, err := provider.toolListDrafts(mcpCtx, ListDraftsArgs{TeamID: teamID})
			require.NoError(t, err)
			for _, want := range tt.wantOut {
				assert.Contains(t, out, want)
			}
			if tt.wantSort {
				assert.Less(t, strings.Index(out, "newer draft"), strings.Index(out, "older draft"))
			}
		})
	}
}
