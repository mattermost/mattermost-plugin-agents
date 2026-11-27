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

func TestHTTPChannelArtifactsServiceList(t *testing.T) {
	channelID := model.NewId()
	artifactID := model.NewId()

	tests := []struct {
		name    string
		status  int
		body    any
		wantErr error
		wantMsg string
	}{
		{name: "success", status: http.StatusOK, body: ChannelArtifactListResponse{Entries: []*channelartifacts.Entry{{ID: artifactID, ChannelID: channelID, Title: "A", Available: true}}}},
		{name: "404 maps to ErrNotFound", status: http.StatusNotFound, body: ChannelArtifactListResponse{Error: "missing"}, wantErr: channelartifacts.ErrNotFound},
		{name: "503 says the library is off", status: http.StatusServiceUnavailable, body: ChannelArtifactListResponse{Error: "channel artifacts are not available"}, wantMsg: "not available"},
		{name: "other statuses carry the server message", status: http.StatusInternalServerError, body: ChannelArtifactListResponse{Error: "boom"}, wantMsg: "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, ChannelArtifactsListPath, r.URL.Path)
				assert.Equal(t, "user1", r.Header.Get("Mattermost-User-Id"))
				var req ChannelArtifactListRequest
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				assert.Equal(t, channelID, req.ChannelID)
				w.WriteHeader(tt.status)
				assert.NoError(t, json.NewEncoder(w).Encode(tt.body))
			}))
			defer server.Close()

			entries, err := NewHTTPChannelArtifactsService(server.URL).List(context.WithValue(t.Context(), auth.UserIDContextKey, "user1"), "ignored", channelID)
			switch {
			case tt.wantErr != nil:
				require.ErrorIs(t, err, tt.wantErr)
			case tt.wantMsg != "":
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantMsg)
			default:
				require.NoError(t, err)
				require.Len(t, entries, 1)
				assert.Equal(t, artifactID, entries[0].ID)
				assert.True(t, entries[0].Available)
			}
		})
	}
}

func TestHTTPChannelArtifactsServiceGet(t *testing.T) {
	artifactID := model.NewId()
	include := true

	tests := []struct {
		name    string
		status  int
		body    any
		wantErr error
		want    ChannelArtifactContent
	}{
		{
			name: "window comes back with paging fields", status: http.StatusOK,
			body: ChannelArtifactGetResponse{ChannelArtifactContent: ChannelArtifactContent{
				Entry: &channelartifacts.Entry{ID: artifactID}, ContentIncluded: true, Content: "<h", Offset: 4, TotalRunes: 20, Returned: 2, HasMore: true, NextOffset: 6,
			}},
			want: ChannelArtifactContent{Entry: &channelartifacts.Entry{ID: artifactID}, ContentIncluded: true, Content: "<h", Offset: 4, TotalRunes: 20, Returned: 2, HasMore: true, NextOffset: 6},
		},
		{name: "404 maps to ErrNotFound", status: http.StatusNotFound, body: ChannelArtifactGetResponse{Error: "missing"}, wantErr: channelartifacts.ErrNotFound},
		{name: "410 maps to ErrUnavailable", status: http.StatusGone, body: ChannelArtifactGetResponse{Error: "gone"}, wantErr: channelartifacts.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, ChannelArtifactsGetPath, r.URL.Path)
				var req ChannelArtifactGetRequest
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				assert.Equal(t, artifactID, req.ArtifactID)
				assert.Equal(t, 4, req.Offset)
				assert.Equal(t, 2, req.ContentLimit)
				assert.Equal(t, 3, req.Version)
				if assert.NotNil(t, req.IncludeContent) {
					assert.True(t, *req.IncludeContent)
				}
				w.WriteHeader(tt.status)
				assert.NoError(t, json.NewEncoder(w).Encode(tt.body))
			}))
			defer server.Close()

			got, err := NewHTTPChannelArtifactsService(server.URL).Get(t.Context(), "user1", artifactID, ChannelArtifactReadOptions{IncludeContent: include, Offset: 4, Limit: 2, Version: 3})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
