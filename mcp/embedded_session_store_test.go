// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestEnsureEmbeddedSessionIDCreatedFlag verifies the created return that the
// MCP session-grant audit event keys off: silent reuse of a valid session is
// the only path that reports false; minting and re-enabling a lapsed session
// both report true.
func TestEnsureEmbeddedSessionIDCreatedFlag(t *testing.T) {
	const (
		userID          = "useraaaaaaaaaaaaaaaaaaaaaa"
		storedSessionID = "sessbbbbbbbbbbbbbbbbbbbbbb"
		mintedSessionID = "sesscccccccccccccccccccccc"
	)
	sessionKey := buildEmbeddedSessionKey(userID)

	tests := []struct {
		name          string
		setupMocks    func(api *plugintest.API)
		wantSessionID string
		wantCreated   bool
	}{
		{
			name: "valid stored session is reused silently",
			setupMocks: func(api *plugintest.API) {
				api.On("KVGet", sessionKey).Return([]byte(storedSessionID), (*model.AppError)(nil))
				api.On("GetSession", storedSessionID).Return(&model.Session{
					Id:        storedSessionID,
					UserId:    userID,
					ExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
				}, (*model.AppError)(nil))
			},
			wantSessionID: storedSessionID,
			wantCreated:   false,
		},
		{
			name: "missing session is minted and reported as created",
			setupMocks: func(api *plugintest.API) {
				api.On("KVGet", sessionKey).Return(([]byte)(nil), (*model.AppError)(nil))
				api.On("GetConfig").Return(&model.Config{})
				api.On("CreateSession", mock.AnythingOfType("*model.Session")).Return(&model.Session{
					Id:     mintedSessionID,
					UserId: userID,
				}, (*model.AppError)(nil))
				api.On("KVSetWithOptions", sessionKey, []byte(mintedSessionID), mock.Anything).Return(true, (*model.AppError)(nil))
			},
			wantSessionID: mintedSessionID,
			wantCreated:   true,
		},
		{
			name: "extending a lapsed session is a new grant, not reuse",
			setupMocks: func(api *plugintest.API) {
				api.On("KVGet", sessionKey).Return([]byte(storedSessionID), (*model.AppError)(nil))
				api.On("GetSession", storedSessionID).Return(&model.Session{
					Id:        storedSessionID,
					UserId:    userID,
					ExpiresAt: time.Now().Add(-time.Hour).UnixMilli(),
				}, (*model.AppError)(nil))
				api.On("GetConfig").Return(&model.Config{})
				api.On("ExtendSessionExpiry", storedSessionID, mock.AnythingOfType("int64")).Return((*model.AppError)(nil))
			},
			wantSessionID: storedSessionID,
			wantCreated:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)

			for i := 1; i <= 10; i++ {
				args := make([]any, i)
				for j := range args {
					args[j] = mock.Anything
				}
				mockAPI.On("LogDebug", args...).Maybe()
			}
			mockAPI.On("GetUser", userID).Return(&model.User{Id: userID}, (*model.AppError)(nil))
			tt.setupMocks(mockAPI)

			client := pluginapi.NewClient(mockAPI, nil)
			m := &ClientManager{
				log:       client.Log,
				pluginAPI: client,
			}

			sessionID, created, err := m.ensureEmbeddedSessionID(userID)
			require.NoError(t, err)
			assert.Equal(t, tt.wantSessionID, sessionID)
			assert.Equal(t, tt.wantCreated, created)
		})
	}
}
