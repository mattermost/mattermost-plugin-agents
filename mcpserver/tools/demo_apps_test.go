// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviewPostToolMeta(t *testing.T) {
	provider := newTestProvider(t, "http://localhost")
	provider.accessMode = AccessModeRemote
	tools := provider.getDemoAppTools()
	require.Len(t, tools, 1)

	ui, ok := tools[0].Meta["ui"].(map[string]any)
	require.True(t, ok, "expected ui meta map")
	assert.Equal(t, previewPostResourceURI, ui["resourceUri"])

	require.NotEmpty(t, previewPostHTML)
	assert.Contains(t, previewPostHTML, "ui/notifications/initialized")
	assert.Contains(t, previewPostHTML, "preview-post-toggle")
	assert.Contains(t, previewPostHTML, "ui/notifications/tool-result")
	// Params are the CallToolResult itself (AppBridge.sendToolResult), not {result:…}.
	// Pin of c61c9097 regression — do not relax to a looser substring.
	assert.Contains(t, previewPostHTML, "var result = data.params")
	assert.NotContains(t, previewPostHTML, "params.result")

	lower := strings.ToLower(previewPostHTML)
	for _, attr := range []string{`src="http://`, `src="https://`, `href="http://`, `href="https://`} {
		assert.NotContains(t, lower, attr, "demo HTML must not reference external %s", attr)
	}
}

func TestPreviewPostGuestProtocolConformance(t *testing.T) {
	require.NotEmpty(t, previewPostHTML)

	const advertised = "ADVERTISED_PROTOCOL_VERSION = '2026-01-26'"
	assert.Contains(t, previewPostHTML, advertised, "must match ext-apps SUPPORTED_PROTOCOL_VERSIONS")
	assert.Contains(t, previewPostHTML, "event.source !== window.parent")
	assert.Contains(t, previewPostHTML, "data.result.protocolVersion")
	assert.Contains(t, previewPostHTML, "ResizeObserver")
	assert.Contains(t, previewPostHTML, "document.documentElement.lang")
	assert.Contains(t, previewPostHTML, "var STRINGS =")
}

func TestDemoAppToolsIncludedInProviderToolNames(t *testing.T) {
	provider := newTestProvider(t, "http://localhost")
	provider.accessMode = AccessModeRemote
	namesOff := provider.ToolNames()
	assert.NotContains(t, namesOff, "preview_post")

	provider.SetEnableDemoApps(true)
	namesOn := provider.ToolNames()
	assert.Contains(t, namesOn, "preview_post")
}

func TestToolPreviewPost(t *testing.T) {
	postID := model.NewId()
	userID := model.NewId()
	channelID := model.NewId()
	missingID := model.NewId()

	tests := []struct {
		name     string
		args     PreviewPostArgs
		wantErr  string
		wantMsgs []string
		setupMux func(mux *http.ServeMux)
	}{
		{
			name: "post found returns JSON with message and username",
			args: PreviewPostArgs{PostID: postID},
			wantMsgs: []string{
				"hello from preview",
				"alice",
				"Town Square",
			},
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/api/v4/posts/"+postID, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.Post{
						Id:        postID,
						UserId:    userID,
						ChannelId: channelID,
						Message:   "hello from preview",
						CreateAt:  1710878490000,
					})
				})
				mux.HandleFunc("/api/v4/users/"+userID, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "alice"})
				})
				mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, DisplayName: "Town Square"})
				})
			},
		},
		{
			name:    "post missing returns error",
			args:    PreviewPostArgs{PostID: missingID},
			wantErr: "error fetching post",
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/api/v4/posts/"+missingID, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"not found"}`))
				})
			},
		},
		{
			name:    "invalid ID returns error",
			args:    PreviewPostArgs{PostID: "bad"},
			wantErr: "must be a valid ID",
			setupMux: func(mux *http.ServeMux) {
				// no endpoints needed
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			if tt.setupMux != nil {
				tt.setupMux(mux)
			}
			ts := httptest.NewServer(mux)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

			out, err := provider.toolPreviewPost(mcpCtx, tt.args)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			for _, want := range tt.wantMsgs {
				assert.Contains(t, out, want)
			}
			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &payload), "output should be JSON: %s", out)
			assert.Equal(t, postID, payload["post_id"])
		})
	}
}

func TestToolPreviewPostBestEffortUsernameChannel(t *testing.T) {
	postID := model.NewId()
	userID := model.NewId()
	channelID := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/posts/"+postID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Post{
			Id:        postID,
			UserId:    userID,
			ChannelId: channelID,
			Message:   "still works",
			CreateAt:  1,
		})
	})
	mux.HandleFunc("/api/v4/users/"+userID, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolPreviewPost(mcpCtx, PreviewPostArgs{PostID: postID})
	require.NoError(t, err)
	assert.Contains(t, out, "still works")
	assert.NotContains(t, out, `"username"`)
	assert.NotContains(t, out, `"channel_display_name"`)
}
