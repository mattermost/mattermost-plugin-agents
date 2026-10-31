// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	testManagedConvID   = "conv1234567890123456789012"
	plantedStoredTitle  = "PLANTED-STORED-TITLE-plugh"
	plantedRequestTitle = "PLANTED-REQUEST-TITLE-xyzzy"
)

func TestConversationManagementRoutes(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	ownedConversation := func(deleteAt int64) *store.Conversation {
		dmChannelID := testChannelID
		rootPostID := "root1234567890123456789012"
		return &store.Conversation{
			ID:         testManagedConvID,
			UserID:     testUserID,
			BotID:      testBotUserID,
			ChannelID:  &dmChannelID,
			RootPostID: &rootPostID,
			Title:      plantedStoredTitle,
			Operation:  "conversation",
			DeleteAt:   deleteAt,
		}
	}
	renameBody := func(title string) string {
		raw, _ := json.Marshal(renameConversationRequest{Title: title})
		return string(raw)
	}

	tests := []struct {
		name           string
		event          string
		method         string
		path           string
		body           string
		userID         string
		conv           *store.Conversation
		storeErr       error
		expectedStatus int
		// expectConvParams is false when validation rejects the request before
		// the conversation is looked up.
		expectConvParams bool
		expectBotID      bool
		validate         func(t *testing.T, e *TestEnvironment)
	}{
		{
			name:             "owner renames conversation, title trimmed",
			event:            AuditEventRenameConversation,
			method:           http.MethodPut,
			path:             "/conversations/" + testManagedConvID + "/title",
			body:             renameBody("  " + plantedRequestTitle + "  "),
			userID:           testUserID,
			conv:             ownedConversation(0),
			expectedStatus:   http.StatusOK,
			expectConvParams: true,
			expectBotID:      true,
			validate: func(t *testing.T, e *TestEnvironment) {
				assert.Equal(t, plantedRequestTitle, e.conversationStore.conversations[testManagedConvID].Title)
			},
		},
		{
			name:             "other user cannot rename and learns nothing about existence",
			event:            AuditEventRenameConversation,
			method:           http.MethodPut,
			path:             "/conversations/" + testManagedConvID + "/title",
			body:             renameBody(plantedRequestTitle),
			userID:           testOtherUserID,
			conv:             ownedConversation(0),
			expectedStatus:   http.StatusNotFound,
			expectConvParams: true,
			validate: func(t *testing.T, e *TestEnvironment) {
				assert.Equal(t, plantedStoredTitle, e.conversationStore.conversations[testManagedConvID].Title)
			},
		},
		{
			name:             "rename of missing conversation",
			event:            AuditEventRenameConversation,
			method:           http.MethodPut,
			path:             "/conversations/" + testManagedConvID + "/title",
			body:             renameBody(plantedRequestTitle),
			userID:           testUserID,
			expectedStatus:   http.StatusNotFound,
			expectConvParams: true,
		},
		{
			name:             "rename of deleted conversation",
			event:            AuditEventRenameConversation,
			method:           http.MethodPut,
			path:             "/conversations/" + testManagedConvID + "/title",
			body:             renameBody(plantedRequestTitle),
			userID:           testUserID,
			conv:             ownedConversation(1700000000000),
			expectedStatus:   http.StatusNotFound,
			expectConvParams: true,
			validate: func(t *testing.T, e *TestEnvironment) {
				assert.Equal(t, plantedStoredTitle, e.conversationStore.conversations[testManagedConvID].Title)
			},
		},
		{
			name:           "whitespace-only title rejected",
			event:          AuditEventRenameConversation,
			method:         http.MethodPut,
			path:           "/conversations/" + testManagedConvID + "/title",
			body:           renameBody(" \t\n "),
			userID:         testUserID,
			conv:           ownedConversation(0),
			expectedStatus: http.StatusBadRequest,
			validate: func(t *testing.T, e *TestEnvironment) {
				assert.Equal(t, plantedStoredTitle, e.conversationStore.conversations[testManagedConvID].Title)
			},
		},
		{
			name:           "oversized title rejected",
			event:          AuditEventRenameConversation,
			method:         http.MethodPut,
			path:           "/conversations/" + testManagedConvID + "/title",
			body:           renameBody(plantedRequestTitle + strings.Repeat("é", MaxConversationTitleLength)),
			userID:         testUserID,
			conv:           ownedConversation(0),
			expectedStatus: http.StatusBadRequest,
			validate: func(t *testing.T, e *TestEnvironment) {
				assert.Equal(t, plantedStoredTitle, e.conversationStore.conversations[testManagedConvID].Title)
			},
		},
		{
			name:             "title of exactly the maximum length accepted",
			event:            AuditEventRenameConversation,
			method:           http.MethodPut,
			path:             "/conversations/" + testManagedConvID + "/title",
			body:             renameBody(strings.Repeat("é", MaxConversationTitleLength)),
			userID:           testUserID,
			conv:             ownedConversation(0),
			expectedStatus:   http.StatusOK,
			expectConvParams: true,
			expectBotID:      true,
		},
		{
			name:             "rename store error is a 500, not a 404",
			event:            AuditEventRenameConversation,
			method:           http.MethodPut,
			path:             "/conversations/" + testManagedConvID + "/title",
			body:             renameBody(plantedRequestTitle),
			userID:           testUserID,
			conv:             ownedConversation(0),
			storeErr:         errors.New("db down"),
			expectedStatus:   http.StatusInternalServerError,
			expectConvParams: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			records := e.CaptureAuditRecords()
			if tt.conv != nil {
				e.conversationStore.conversations[tt.conv.ID] = tt.conv
			}
			e.conversationStore.err = tt.storeErr

			type wsEvent struct {
				name      string
				payload   map[string]any
				broadcast *model.WebsocketBroadcast
			}
			var events []wsEvent
			mmClient := mmapimocks.NewMockClient(t)
			mmClient.On("PublishWebSocketEvent", mock.AnythingOfType("string"), mock.AnythingOfType("map[string]interface {}"), mock.AnythingOfType("*model.WebsocketBroadcast")).
				Run(func(args mock.Arguments) {
					payload, _ := args.Get(1).(map[string]any)
					broadcast, _ := args.Get(2).(*model.WebsocketBroadcast)
					events = append(events, wsEvent{name: args.String(0), payload: payload, broadcast: broadcast})
				}).Return().Maybe()
			e.api.mmClient = mmClient

			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Mattermost-User-Id", tt.userID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			if tt.expectedStatus == http.StatusOK {
				assert.JSONEq(t, `{}`, recorder.Body.String())
				// The owner, and only the owner, learns the new title.
				stored := e.conversationStore.conversations[testManagedConvID].Title
				require.Len(t, events, 1, "a successful rename publishes exactly one event")
				assert.Equal(t, "conversation_updated", events[0].name)
				assert.Equal(t, map[string]any{"conversation_id": testManagedConvID, "title": stored}, events[0].payload)
				assert.Equal(t, &model.WebsocketBroadcast{UserId: testUserID}, events[0].broadcast)
			} else {
				assert.Empty(t, events, "a failed rename publishes nothing")
			}
			if tt.validate != nil {
				tt.validate(t, e)
			}

			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, tt.event, rec.EventName)
			assert.Equal(t, tt.userID, rec.Actor.UserId)
			if tt.expectedStatus == http.StatusOK {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
			} else {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, tt.expectedStatus, rec.Error.Code)
			}
			if tt.expectConvParams {
				assert.Equal(t, testManagedConvID, rec.EventData.Parameters[audit.KeyConversationID])
			}
			if tt.expectBotID {
				assert.Equal(t, testBotUserID, rec.EventData.Parameters[audit.KeyAgentID])
			} else {
				assert.NotContains(t, rec.EventData.Parameters, audit.KeyAgentID,
					"a caller who does not own the conversation must not learn its agent from the record")
			}

			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), plantedRequestTitle, "audit record must never carry the requested title")
			assert.NotContains(t, string(raw), plantedStoredTitle, "audit record must never carry the stored title")
		})
	}
}
