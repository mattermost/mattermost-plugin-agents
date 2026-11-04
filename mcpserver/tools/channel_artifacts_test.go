// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost/server/public/model"
)

type fakeChannelArtifactsService struct {
	entries       []*channelartifacts.Entry
	view          ChannelArtifactContent
	listErr       error
	getErr        error
	canSaveErr    error
	canSavePostID string
	canSaveFileID string
}

func (f *fakeChannelArtifactsService) List(_ context.Context, _, _ string) ([]*channelartifacts.Entry, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.entries, nil
}

func (f *fakeChannelArtifactsService) Get(_ context.Context, _, _ string, _ bool, _ int) (ChannelArtifactContent, error) {
	if f.getErr != nil {
		return ChannelArtifactContent{}, f.getErr
	}
	return f.view, nil
}

func (f *fakeChannelArtifactsService) CanSave(_ context.Context, _, postID, fileID string) error {
	f.canSavePostID = postID
	f.canSaveFileID = fileID
	return f.canSaveErr
}

func TestToolListChannelArtifacts(t *testing.T) {
	channelID := model.NewId()
	artifactID := model.NewId()

	tests := []struct {
		name        string
		service     ChannelArtifactsService
		channelID   string
		wantErr     string
		wantContain string
	}{
		{
			name:      "nil service returns clear error",
			service:   nil,
			channelID: channelID,
			wantErr:   "channel artifacts are not available",
		},
		{
			name:      "invalid channel id",
			service:   &fakeChannelArtifactsService{},
			channelID: "short",
			wantErr:   "channel_id",
		},
		{
			name:      "not found maps to guidance",
			service:   &fakeChannelArtifactsService{listErr: channelartifacts.ErrNotFound},
			channelID: channelID,
			wantErr:   "permission",
		},
		{
			name:        "empty list",
			service:     &fakeChannelArtifactsService{entries: nil},
			channelID:   channelID,
			wantContain: "no channel library artifacts",
		},
		{
			name: "lists entries with metadata",
			service: &fakeChannelArtifactsService{entries: []*channelartifacts.Entry{{
				ID: artifactID, ChannelID: channelID, Title: "Ops Report", Version: 2,
				SHA256: "abcdef0123456789", SourceAgentDisplayName: "Fusion",
			}}},
			channelID:   channelID,
			wantContain: "Ops Report",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MattermostToolProvider{channelArtifactsService: tt.service}
			mcpCtx := &MCPToolContext{Ctx: t.Context(), UserID: model.NewId()}
			result, err := p.toolListChannelArtifacts(mcpCtx, ListChannelArtifactsArgs{ChannelID: tt.channelID})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, result, tt.wantContain)
			if tt.name == "lists entries with metadata" {
				assert.Contains(t, result, "library")
				assert.Contains(t, result, artifactID)
				assert.Contains(t, result, "abcdef012345")
			}
		})
	}
}

func TestToolGetChannelArtifact(t *testing.T) {
	artifactID := model.NewId()

	tests := []struct {
		name        string
		service     ChannelArtifactsService
		args        GetChannelArtifactArgs
		wantErr     string
		wantContain string
	}{
		{
			name:    "nil service",
			service: nil,
			args:    GetChannelArtifactArgs{ArtifactID: artifactID},
			wantErr: "channel artifacts are not available",
		},
		{
			name:    "not found",
			service: &fakeChannelArtifactsService{getErr: channelartifacts.ErrNotFound},
			args:    GetChannelArtifactArgs{ArtifactID: artifactID},
			wantErr: "list_channel_artifacts",
		},
		{
			name: "returns truncated content",
			service: &fakeChannelArtifactsService{view: ChannelArtifactContent{
				Entry:           &channelartifacts.Entry{ID: artifactID, Title: "Deck", Version: 1},
				ContentIncluded: true,
				Content:         "<html>hi</html>",
				TotalRunes:      14,
				Returned:        14,
			}},
			args:        GetChannelArtifactArgs{ArtifactID: artifactID},
			wantContain: "<html>hi</html>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MattermostToolProvider{channelArtifactsService: tt.service}
			mcpCtx := &MCPToolContext{Ctx: t.Context(), UserID: model.NewId()}
			result, err := p.toolGetChannelArtifact(mcpCtx, tt.args)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, result, tt.wantContain)
			assert.Contains(t, result, "library")
		})
	}
}

func TestToolProposeChannelArtifactUpdate(t *testing.T) {
	channelID := model.NewId()
	artifactID := model.NewId()
	postID := model.NewId()
	fileID := model.NewId()
	otherChannel := model.NewId()

	tests := []struct {
		name     string
		service  ChannelArtifactsService
		setupMux func(mux *http.ServeMux)
		args     ProposeChannelArtifactUpdateArgs
		wantErr  string
		wantOK   string
	}{
		{
			name:    "nil service",
			service: nil,
			args: ProposeChannelArtifactUpdateArgs{
				ChannelID: channelID, ArtifactID: artifactID, PostID: postID, FileID: fileID,
			},
			wantErr: "channel artifacts are not available",
		},
		{
			name: "artifact in different channel fails closed",
			service: &fakeChannelArtifactsService{view: ChannelArtifactContent{
				Entry: &channelartifacts.Entry{ID: artifactID, ChannelID: otherChannel, Title: "Wrong"},
			}},
			args: ProposeChannelArtifactUpdateArgs{
				ChannelID: channelID, ArtifactID: artifactID, PostID: postID, FileID: fileID,
			},
			wantErr: "is not in channel",
		},
		{
			name: "posts proposal when artifact and file match",
			service: &fakeChannelArtifactsService{view: ChannelArtifactContent{
				Entry: &channelartifacts.Entry{ID: artifactID, ChannelID: channelID, Title: "Brief"},
			}},
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/api/v4/posts/"+postID, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.Post{Id: postID, ChannelId: channelID, FileIds: model.StringArray{fileID}})
				})
				mux.HandleFunc("/api/v4/files/"+fileID+"/info", func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.FileInfo{Id: fileID, Name: "brief.html"})
				})
				mux.HandleFunc("/api/v4/posts", func(w http.ResponseWriter, r *http.Request) {
					var post model.Post
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&post))
					assert.Equal(t, channelID, post.ChannelId)
					assert.Contains(t, post.Message, "Brief")
					assert.Contains(t, post.Message, artifactID)
					assert.Contains(t, post.Message, "brief.html")
					post.Id = model.NewId()
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&post)
				})
			},
			args: ProposeChannelArtifactUpdateArgs{
				ChannelID: channelID, ArtifactID: artifactID, PostID: postID, FileID: fileID,
			},
			wantOK: "Posted proposal",
		},
		{
			name: "non-html file is rejected",
			service: &fakeChannelArtifactsService{view: ChannelArtifactContent{
				Entry: &channelartifacts.Entry{ID: artifactID, ChannelID: channelID, Title: "Brief"},
			}},
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/api/v4/posts/"+postID, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.Post{Id: postID, ChannelId: channelID, FileIds: model.StringArray{fileID}})
				})
				mux.HandleFunc("/api/v4/files/"+fileID+"/info", func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.FileInfo{Id: fileID, Name: "notes.txt", Extension: "txt"})
				})
			},
			args: ProposeChannelArtifactUpdateArgs{
				ChannelID: channelID, ArtifactID: artifactID, PostID: postID, FileID: fileID,
			},
			wantErr: "is not an HTML artifact",
		},
		{
			name: "human-authored file is rejected by the plugin check",
			service: &fakeChannelArtifactsService{
				view: ChannelArtifactContent{
					Entry: &channelartifacts.Entry{ID: artifactID, ChannelID: channelID, Title: "Brief"},
				},
				canSaveErr: fmt.Errorf("channel artifacts only accept files posted by agents"),
			},
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/api/v4/posts/"+postID, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.Post{Id: postID, ChannelId: channelID, FileIds: model.StringArray{fileID}})
				})
				mux.HandleFunc("/api/v4/files/"+fileID+"/info", func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.FileInfo{Id: fileID, Name: "brief.html", Extension: "html"})
				})
			},
			args: ProposeChannelArtifactUpdateArgs{
				ChannelID: channelID, ArtifactID: artifactID, PostID: postID, FileID: fileID,
			},
			wantErr: "only accept files posted by agents",
		},
		{
			name: "mentions in the title are not posted raw",
			service: &fakeChannelArtifactsService{view: ChannelArtifactContent{
				Entry: &channelartifacts.Entry{ID: artifactID, ChannelID: channelID, Title: "Hello @channel"},
			}},
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/api/v4/posts/"+postID, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.Post{Id: postID, ChannelId: channelID, FileIds: model.StringArray{fileID}})
				})
				mux.HandleFunc("/api/v4/files/"+fileID+"/info", func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.FileInfo{Id: fileID, Name: "brief.html", Extension: "html"})
				})
				mux.HandleFunc("/api/v4/posts", func(w http.ResponseWriter, r *http.Request) {
					var post model.Post
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&post))
					assert.NotContains(t, post.Message, "@channel")
					assert.Contains(t, post.Message, "@\u200bchannel")
					assert.Contains(t, post.Message, "Hello")
					post.Id = model.NewId()
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&post)
				})
			},
			args: ProposeChannelArtifactUpdateArgs{
				ChannelID: channelID, ArtifactID: artifactID, PostID: postID, FileID: fileID,
			},
			wantOK: "Posted proposal",
		},
		{
			name: "file not on post fails closed",
			service: &fakeChannelArtifactsService{view: ChannelArtifactContent{
				Entry: &channelartifacts.Entry{ID: artifactID, ChannelID: channelID, Title: "Brief"},
			}},
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/api/v4/posts/"+postID, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&model.Post{Id: postID, ChannelId: channelID, FileIds: model.StringArray{model.NewId()}})
				})
			},
			args: ProposeChannelArtifactUpdateArgs{
				ChannelID: channelID, ArtifactID: artifactID, PostID: postID, FileID: fileID,
			},
			wantErr: "is not attached",
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

			p := &MattermostToolProvider{channelArtifactsService: tt.service, trackAIGenerated: true}
			mcpCtx := &MCPToolContext{
				Ctx:        t.Context(),
				Client:     newTestClient(ts.URL),
				UserID:     model.NewId(),
				AccessMode: AccessModeRemote,
			}
			result, err := p.toolProposeChannelArtifactUpdate(mcpCtx, tt.args)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, result, tt.wantOK)
			fake, ok := tt.service.(*fakeChannelArtifactsService)
			if ok {
				assert.Equal(t, tt.args.PostID, fake.canSavePostID)
				assert.Equal(t, tt.args.FileID, fake.canSaveFileID)
			}
		})
	}
}

func TestGetChannelArtifactToolsRegistered(t *testing.T) {
	p := &MattermostToolProvider{accessMode: AccessModeRemote, logger: &testLogger{t: t}}
	names := map[string]bool{}
	for _, tool := range p.getChannelArtifactTools() {
		names[tool.Name] = tool.ReadOnly
	}
	assert.Equal(t, map[string]bool{
		"list_channel_artifacts":          true,
		"get_channel_artifact":            true,
		"propose_channel_artifact_update": false,
	}, names)
}
