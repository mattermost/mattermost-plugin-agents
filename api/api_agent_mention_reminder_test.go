// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandleAgentMentionReminderOptOut(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		userID         string
		body           string
		kvSetErr       error
		expectKVSet    bool
		expectedStatus int
	}{
		{
			name:           "success stores the per-user preference",
			userID:         testUserID,
			expectKVSet:    true,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "unauthenticated request is rejected before any storage",
			userID:         "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "non-empty body is rejected",
			userID:         testUserID,
			body:           `{"unexpected":true}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "storage failure surfaces a 500",
			userID:         testUserID,
			kvSetErr:       errors.New("kv unavailable"),
			expectKVSet:    true,
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			mmClient := mmapimocks.NewMockClient(t)
			if tt.expectKVSet {
				mmClient.On("KVSet", mock.MatchedBy(func(key string) bool {
					return strings.Contains(key, tt.userID)
				}), true).Return(tt.kvSetErr).Once()
			}
			e.api.mmClient = mmClient

			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			request := httptest.NewRequest(http.MethodPost, "/agent_mention_reminder/opt_out", body)
			if tt.userID != "" {
				request.Header.Set("Mattermost-User-Id", tt.userID)
			}
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
		})
	}
}
