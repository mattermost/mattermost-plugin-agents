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

// boolPtr returns a pointer to b for use in optional *bool args.
func boolPtr(b bool) *bool {
	return &b
}

func TestStampAIGenerated(t *testing.T) {
	botID := model.NewId()
	fallbackID := model.NewId()
	getMeID := model.NewId()

	// Server that stubs /api/v4/users/me for the GetMe fallback case.
	meServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/users/me" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": getMeID})
			return
		}
		http.NotFound(w, r)
	}))
	defer meServer.Close()

	tests := []struct {
		name             string
		trackAIGenerated bool
		mcpContext       *MCPToolContext
		fallbackUserID   string
		wantProp         any // nil means the prop must be absent
	}{
		{
			name:             "tracking disabled is a no-op",
			trackAIGenerated: false,
			mcpContext:       &MCPToolContext{BotUserID: botID},
			fallbackUserID:   fallbackID,
			wantProp:         nil,
		},
		{
			name:             "valid bot user id wins",
			trackAIGenerated: true,
			mcpContext:       &MCPToolContext{BotUserID: botID},
			fallbackUserID:   fallbackID,
			wantProp:         botID,
		},
		{
			name:             "invalid bot user id falls back to fallbackUserID",
			trackAIGenerated: true,
			mcpContext:       &MCPToolContext{BotUserID: "notanid"},
			fallbackUserID:   fallbackID,
			wantProp:         fallbackID,
		},
		{
			name:             "no bot or fallback falls back to GetMe",
			trackAIGenerated: true,
			mcpContext:       &MCPToolContext{Ctx: context.Background(), Client: newTestClient(meServer.URL)},
			fallbackUserID:   "",
			wantProp:         getMeID,
		},
		{
			name:             "no id available leaves prop absent without panic",
			trackAIGenerated: true,
			mcpContext:       &MCPToolContext{Ctx: context.Background(), Client: nil},
			fallbackUserID:   "",
			wantProp:         nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MattermostToolProvider{trackAIGenerated: tt.trackAIGenerated, logger: &testLogger{t: t}}
			post := &model.Post{}

			require.NotPanics(t, func() {
				p.stampAIGenerated(post, tt.mcpContext, tt.fallbackUserID)
			})

			got := post.GetProp("ai_generated_by")
			if tt.wantProp == nil {
				assert.Nil(t, got, "ai_generated_by prop should be absent")
			} else {
				assert.Equal(t, tt.wantProp, got)
			}
		})
	}
}

// newTestReadPostServer builds an httptest server stubbing the endpoints
// toolReadPost touches. The thread endpoint returns the root post plus a
// sibling reply; the single-post endpoint returns only the root post. Which
// endpoint was hit is recorded on the returned *readPostHits.
func newTestReadPostServer(t *testing.T, rootID, channelID, teamID, userID string) (*httptest.Server, *readPostHits) {
	t.Helper()

	hits := &readPostHits{}
	siblingID := model.NewId()

	rootPost := &model.Post{Id: rootID, ChannelId: channelID, UserId: userID, Message: rootPostMessage}
	siblingPost := &model.Post{Id: siblingID, ChannelId: channelID, UserId: userID, RootId: rootID, Message: siblingPostMessage}

	mux := http.NewServeMux()

	// Thread endpoint: /api/v4/posts/{id}/thread
	mux.HandleFunc("/api/v4/posts/"+rootID+"/thread", func(w http.ResponseWriter, r *http.Request) {
		hits.thread = true
		postList := &model.PostList{
			Order: []string{rootID, siblingID},
			Posts: map[string]*model.Post{rootID: rootPost, siblingID: siblingPost},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(postList)
	})

	// Single-post endpoint: /api/v4/posts/{id}
	mux.HandleFunc("/api/v4/posts/"+rootID, func(w http.ResponseWriter, r *http.Request) {
		hits.single = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rootPost)
	})

	// Formatting stubs.
	mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, TeamId: teamID, DisplayName: "Town Square"})
	})
	mux.HandleFunc("/api/v4/teams/"+teamID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Team{Id: teamID, DisplayName: "Core Team"})
	})
	mux.HandleFunc("/api/v4/users/"+userID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "alice"})
	})

	return httptest.NewServer(mux), hits
}

type readPostHits struct {
	thread bool
	single bool
}

const (
	rootPostMessage    = "this is the root post"
	siblingPostMessage = "this is a thread reply sibling"
)

func TestResolveThreadRoot(t *testing.T) {
	channelID := model.NewId()
	otherChannelID := model.NewId()
	rootID := model.NewId()
	replyID := model.NewId()
	foreignID := model.NewId()
	missingID := model.NewId()

	mux := http.NewServeMux()
	postsByID := map[string]*model.Post{
		rootID:    {Id: rootID, ChannelId: channelID},
		replyID:   {Id: replyID, ChannelId: channelID, RootId: rootID},
		foreignID: {Id: foreignID, ChannelId: otherChannelID},
	}
	for id, post := range postsByID {
		mux.HandleFunc("/api/v4/posts/"+id, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(post)
		})
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: newTestClient(ts.URL)}

	tests := []struct {
		name     string
		rootID   string
		wantRoot string
		wantErr  bool
	}{
		{
			name:     "empty root id resolves to empty",
			rootID:   "",
			wantRoot: "",
		},
		{
			name:    "malformed root id is rejected",
			rootID:  "notanid",
			wantErr: true,
		},
		{
			name:     "root post resolves to itself",
			rootID:   rootID,
			wantRoot: rootID,
		},
		{
			name:     "reply resolves to its thread root",
			rootID:   replyID,
			wantRoot: rootID,
		},
		{
			name:    "post from another conversation is rejected",
			rootID:  foreignID,
			wantErr: true,
		},
		{
			name:    "missing post is rejected",
			rootID:  missingID,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveThreadRoot(mcpCtx, channelID, tt.rootID)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantRoot, got)
		})
	}
}

func TestApplyPostPriority(t *testing.T) {
	tests := []struct {
		name       string
		post       *model.Post
		priority   string
		requestAck bool
		wantErr    bool
		wantMeta   bool
	}{
		{
			name:     "no priority and no ack is a no-op",
			post:     &model.Post{},
			wantMeta: false,
		},
		{
			name:     "important priority is applied",
			post:     &model.Post{},
			priority: "important",
			wantMeta: true,
		},
		{
			name:       "urgent with ack is applied",
			post:       &model.Post{},
			priority:   "urgent",
			requestAck: true,
			wantMeta:   true,
		},
		{
			name:       "ack alone is applied",
			post:       &model.Post{},
			requestAck: true,
			wantMeta:   true,
		},
		{
			name:     "invalid priority value is rejected",
			post:     &model.Post{},
			priority: "critical",
			wantErr:  true,
		},
		{
			name:     "priority on a thread reply is rejected",
			post:     &model.Post{RootId: model.NewId()},
			priority: "important",
			wantErr:  true,
		},
		{
			name:       "ack on a thread reply is rejected",
			post:       &model.Post{RootId: model.NewId()},
			requestAck: true,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := applyPostPriority(tt.post, tt.priority, tt.requestAck)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if !tt.wantMeta {
				assert.Nil(t, tt.post.Metadata)
				return
			}
			require.NotNil(t, tt.post.Metadata)
			require.NotNil(t, tt.post.Metadata.Priority)
			assert.Equal(t, tt.priority, *tt.post.Metadata.Priority.Priority)
			assert.Equal(t, tt.requestAck, *tt.post.Metadata.Priority.RequestedAck)
		})
	}
}

func TestToolCreatePostChannelValidation(t *testing.T) {
	teamID := model.NewId()

	tests := []struct {
		name               string
		channelType        model.ChannelType
		channelDisplayName string
		channelTeamID      string
		argsDisplayName    string
		argsTeamName       string
		wantErr            bool
		wantTeamFetch      bool
	}{
		{
			name:               "direct channel skips display name validation",
			channelType:        model.ChannelTypeDirect,
			channelDisplayName: "",
			channelTeamID:      "",
			argsDisplayName:    "Direct Message",
			argsTeamName:       "N/A",
			wantErr:            false,
			wantTeamFetch:      false,
		},
		{
			name:               "group channel skips display name validation",
			channelType:        model.ChannelTypeGroup,
			channelDisplayName: "alice, bob, carol",
			channelTeamID:      "",
			argsDisplayName:    "Group Message",
			argsTeamName:       "N/A",
			wantErr:            false,
			wantTeamFetch:      false,
		},
		{
			name:               "regular channel still validates display name",
			channelType:        model.ChannelTypeOpen,
			channelDisplayName: "Town Square",
			channelTeamID:      teamID,
			argsDisplayName:    "Wrong Name",
			argsTeamName:       "Core Team",
			wantErr:            true,
			wantTeamFetch:      false,
		},
		{
			name:               "regular channel with matching names succeeds",
			channelType:        model.ChannelTypeOpen,
			channelDisplayName: "Town Square",
			channelTeamID:      teamID,
			argsDisplayName:    "Town Square",
			argsTeamName:       "Core Team",
			wantErr:            false,
			wantTeamFetch:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channelID := model.NewId()
			teamFetched := false

			mux := http.NewServeMux()
			mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.Channel{
					Id:          channelID,
					Type:        tt.channelType,
					DisplayName: tt.channelDisplayName,
					TeamId:      tt.channelTeamID,
				})
			})
			mux.HandleFunc("/api/v4/teams/"+teamID, func(w http.ResponseWriter, r *http.Request) {
				teamFetched = true
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&model.Team{Id: teamID, DisplayName: "Core Team"})
			})
			mux.HandleFunc("/api/v4/posts", func(w http.ResponseWriter, r *http.Request) {
				var post model.Post
				require.NoError(t, json.NewDecoder(r.Body).Decode(&post))
				post.Id = model.NewId()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&post)
			})
			ts := httptest.NewServer(mux)
			t.Cleanup(ts.Close)

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: newTestClient(ts.URL)}

			result, err := provider.toolCreatePost(mcpCtx, CreatePostArgs{
				ChannelID:          channelID,
				ChannelDisplayName: tt.argsDisplayName,
				TeamDisplayName:    tt.argsTeamName,
				Message:            "hello",
			})

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, result, "Successfully created post")
			assert.Equal(t, tt.wantTeamFetch, teamFetched, "team endpoint hit")
		})
	}
}

func TestReadPostIncludeThread(t *testing.T) {
	rootID := model.NewId()
	channelID := model.NewId()
	teamID := model.NewId()
	userID := model.NewId()

	tests := []struct {
		name          string
		includeThread *bool
		wantThreadHit bool
		wantSingleHit bool
	}{
		{
			name:          "nil includes the thread",
			includeThread: nil,
			wantThreadHit: true,
			wantSingleHit: false,
		},
		{
			name:          "true includes the thread",
			includeThread: boolPtr(true),
			wantThreadHit: true,
			wantSingleHit: false,
		},
		{
			name:          "false fetches only the single post",
			includeThread: boolPtr(false),
			wantThreadHit: false,
			wantSingleHit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, hits := newTestReadPostServer(t, rootID, channelID, teamID, userID)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: newTestClient(ts.URL)}

			result, err := provider.toolReadPost(mcpCtx, ReadPostArgs{PostID: rootID, IncludeThread: tt.includeThread})
			require.NoError(t, err)

			assert.Equal(t, tt.wantThreadHit, hits.thread, "thread endpoint hit")
			assert.Equal(t, tt.wantSingleHit, hits.single, "single-post endpoint hit")

			// The root post message is always present.
			assert.Contains(t, result, rootPostMessage)

			if tt.wantThreadHit {
				// The thread sibling must be included.
				assert.Contains(t, result, siblingPostMessage)
				assert.Contains(t, result, "Thread with")
			} else {
				// Only the single post — the sibling must NOT appear.
				assert.NotContains(t, result, siblingPostMessage)
			}
		})
	}
}
