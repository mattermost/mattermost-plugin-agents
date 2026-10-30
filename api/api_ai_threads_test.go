// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestGetAIThreadsOrdersByLastActivity drives GET /ai_threads against the real
// PostgreSQL conversation store: a conversation continued with a new turn must
// sort by that turn's time, while renaming a conversation must not reorder it.
func TestGetAIThreadsOrdersByLastActivity(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	type convSpec struct {
		title     string
		userID    string
		updatedAt int64
		turns     []int64
		archived  bool
		// renameTo, when set, renames the conversation through the API
		// after its turns are added.
		renameTo string
		// rootPostDeleteAt, when set, roots the conversation at a Mattermost
		// post with that DeleteAt (0 for a live post).
		rootPostDeleteAt *int64
	}
	type wantRow struct {
		title    string
		updateAt int64
	}

	otherUserID := model.NewId()

	tests := []struct {
		name  string
		convs []convSpec
		want  []wantRow
	}{
		{
			name: "continued conversation sorts first with its newest turn time",
			convs: []convSpec{
				{title: "continued", userID: testUserID, updatedAt: 1000, turns: []int64{1000, 5000}},
				{title: "untouched", userID: testUserID, updatedAt: 3000},
			},
			want: []wantRow{{"continued", 5000}, {"untouched", 3000}},
		},
		{
			name: "other users' and archived conversations are excluded",
			convs: []convSpec{
				{title: "continued", userID: testUserID, updatedAt: 1000, turns: []int64{7000}},
				{title: "untouched", userID: testUserID, updatedAt: 2000},
				{title: "theirs", userID: otherUserID, updatedAt: 1000, turns: []int64{9000}},
				{title: "archived", userID: testUserID, updatedAt: 1000, turns: []int64{9000}, archived: true},
			},
			want: []wantRow{{"continued", 7000}, {"untouched", 2000}},
		},
		{
			name: "a rename does not move a conversation up",
			convs: []convSpec{
				{title: "older", userID: testUserID, updatedAt: 1000, turns: []int64{1000}, renameTo: "renamed"},
				{title: "newer", userID: testUserID, updatedAt: 2000, turns: []int64{2000}},
			},
			want: []wantRow{{"newer", 2000}, {"renamed", 1000}},
		},
		{
			name: "conversations whose root post was deleted are excluded",
			convs: []convSpec{
				{title: "live root", userID: testUserID, updatedAt: 3000, rootPostDeleteAt: new(int64(0))},
				{title: "deleted root", userID: testUserID, updatedAt: 4000, rootPostDeleteAt: new(int64(3500))},
				{title: "threadless", userID: testUserID, updatedAt: 2000},
			},
			want: []wantRow{{"live root", 3000}, {"threadless", 2000}},
		},
		{
			name:  "no conversations returns an empty array",
			convs: nil,
			want:  []wantRow{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, convStore := setupPostgresTestStore(t, "ai_threads_api_test")

			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("LogError", mock.Anything).Maybe()
			e.api.conversationStore = convStore
			mmClient := mmapimocks.NewMockClient(t)
			mmClient.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
			e.api.mmClient = mmClient

			for _, spec := range tt.convs {
				conv := &store.Conversation{
					ID:        model.NewId(),
					UserID:    spec.userID,
					BotID:     model.NewId(),
					Title:     spec.title,
					Operation: "conversation",
					CreatedAt: spec.updatedAt,
					UpdatedAt: spec.updatedAt,
				}
				if spec.rootPostDeleteAt != nil {
					rootPostID := model.NewId()
					conv.RootPostID = &rootPostID
					_, err := db.Exec("INSERT INTO Posts (Id, DeleteAt) VALUES ($1, $2)", rootPostID, *spec.rootPostDeleteAt)
					require.NoError(t, err)
				}
				require.NoError(t, convStore.CreateConversation(conv))
				for i, createdAt := range spec.turns {
					require.NoError(t, convStore.CreateTurn(&store.Turn{
						ID:             model.NewId(),
						ConversationID: conv.ID,
						Role:           "user",
						Content:        json.RawMessage(`[{"type":"text","text":"hi"}]`),
						Sequence:       i + 1,
						CreatedAt:      createdAt,
					}))
				}
				if spec.archived {
					require.NoError(t, convStore.ArchiveConversation(conv.ID, spec.updatedAt))
				}
				if spec.renameTo != "" {
					body, err := json.Marshal(renameConversationRequest{Title: spec.renameTo})
					require.NoError(t, err)
					req := httptest.NewRequest(http.MethodPut, "/conversations/"+conv.ID+"/title", bytes.NewReader(body))
					req.Header.Set("Mattermost-User-Id", spec.userID)
					recorder := httptest.NewRecorder()
					e.api.ServeHTTP(&plugin.Context{}, recorder, req)
					require.Equal(t, http.StatusOK, recorder.Code)
				}
			}

			req := httptest.NewRequest(http.MethodGet, "/ai_threads", nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

			var raw []json.RawMessage
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &raw))
			require.NotNil(t, raw, "response must be a JSON array, not null")

			var threads []aiThreadResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &threads))
			got := make([]wantRow, len(threads))
			for i, thread := range threads {
				got[i] = wantRow{thread.Title, thread.UpdateAt}
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
