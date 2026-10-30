// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestGetAIThreadsOrdersByLastActivity drives GET /ai_threads against the real
// PostgreSQL conversation store: a conversation continued with a new turn must
// sort by that turn, not by the conversation row's UpdatedAt.
func TestGetAIThreadsOrdersByLastActivity(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	type convSpec struct {
		title     string
		userID    string
		updatedAt int64
		turns     []int64
		deleted   bool
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
			name: "other users' and deleted conversations are excluded",
			convs: []convSpec{
				{title: "continued", userID: testUserID, updatedAt: 1000, turns: []int64{7000}},
				{title: "untouched", userID: testUserID, updatedAt: 2000},
				{title: "theirs", userID: otherUserID, updatedAt: 1000, turns: []int64{9000}},
				{title: "deleted", userID: testUserID, updatedAt: 1000, turns: []int64{9000}, deleted: true},
			},
			want: []wantRow{{"continued", 7000}, {"untouched", 2000}},
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
				if spec.deleted {
					// Soft-delete directly so UpdatedAt is not bumped past the turns.
					_, err := db.Exec("UPDATE LLM_Conversations SET DeleteAt = $1 WHERE ID = $2", spec.updatedAt, conv.ID)
					require.NoError(t, err)
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
