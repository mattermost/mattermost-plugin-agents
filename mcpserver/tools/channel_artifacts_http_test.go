// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost/server/public/model"
)

func TestHTTPChannelArtifactsService_List(t *testing.T) {
	channelID := model.NewId()
	artifactID := model.NewId()

	tests := []struct {
		name           string
		handler        http.HandlerFunc
		expectError    bool
		expectNotFound bool
		errorContains  string
		validate       func(t *testing.T, entries []*channelartifacts.Entry)
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, ChannelArtifactsListPath, r.URL.Path)
				_ = json.NewEncoder(w).Encode(httpChannelArtifactsListResponse{
					Entries: []httpChannelArtifactEntry{{ID: artifactID, ChannelID: channelID, Title: "A"}},
				})
			},
			validate: func(t *testing.T, entries []*channelartifacts.Entry) {
				require.Len(t, entries, 1)
				assert.Equal(t, artifactID, entries[0].ID)
				assert.Equal(t, "A", entries[0].Title)
			},
		},
		{
			name: "404 maps to ErrNotFound",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(httpChannelArtifactsListResponse{Error: "missing"})
			},
			expectError:    true,
			expectNotFound: true,
		},
		{
			name: "503 surfaces availability message",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(httpChannelArtifactsListResponse{Error: "channel artifacts are not available"})
			},
			expectError:   true,
			errorContains: "channel artifacts are not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			svc := NewHTTPChannelArtifactsService(server.URL)
			ctx := context.WithValue(context.Background(), auth.AuthTokenContextKey, "tok")
			entries, err := svc.List(ctx, model.NewId(), channelID)
			if tt.expectError {
				require.Error(t, err)
				if tt.expectNotFound {
					assert.ErrorIs(t, err, channelartifacts.ErrNotFound)
				}
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				return
			}
			require.NoError(t, err)
			tt.validate(t, entries)
		})
	}
}

func TestHTTPChannelArtifactsService_Get(t *testing.T) {
	artifactID := model.NewId()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, ChannelArtifactsGetPath, r.URL.Path)
		var req httpChannelArtifactsGetRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, artifactID, req.ArtifactID)
		require.NotNil(t, req.IncludeContent)
		assert.True(t, *req.IncludeContent)
		assert.Equal(t, 100, req.ContentLimit)
		_ = json.NewEncoder(w).Encode(httpChannelArtifactsGetResponse{
			Entry:           httpChannelArtifactEntry{ID: artifactID, Title: "Doc"},
			ContentIncluded: true,
			Content:         "<p>x</p>",
			TotalRunes:      8,
			Returned:        8,
		})
	}))
	defer server.Close()

	svc := NewHTTPChannelArtifactsService(server.URL)
	view, err := svc.Get(context.Background(), model.NewId(), artifactID, true, 100)
	require.NoError(t, err)
	assert.Equal(t, artifactID, view.Entry.ID)
	assert.True(t, view.ContentIncluded)
	assert.Equal(t, "<p>x</p>", view.Content)
	assert.Equal(t, 8, view.TotalRunes)
}
