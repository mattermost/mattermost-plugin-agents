// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelcontext"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeChannelContextService struct {
	state         channelcontext.State
	getErr        error
	saveErr       error
	gotChannel    string
	gotUser       string
	gotUpdate     channelcontext.Update
	getCallCount  int
	saveCallCount int
}

func (s *fakeChannelContextService) Get(channelID string) (channelcontext.State, error) {
	s.getCallCount++
	s.gotChannel = channelID
	return s.state, s.getErr
}

func (s *fakeChannelContextService) Save(channelID, userID string, update channelcontext.Update) (channelcontext.State, error) {
	s.saveCallCount++
	s.gotChannel = channelID
	s.gotUser = userID
	s.gotUpdate = update
	return s.state, s.saveErr
}

func TestChannelContextAuthorization(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()

	tests := []struct {
		name       string
		path       string
		userID     string
		channel    *model.Channel
		channelErr *model.AppError
		permission *model.Permission
		allowed    bool
		wantStatus int
		wantCall   bool
	}{
		{
			name: "public channel manager", path: "/channel/" + channelID + "/context", userID: userID,
			channel:    &model.Channel{Id: channelID, Type: model.ChannelTypeOpen},
			permission: model.PermissionManagePublicChannelProperties, allowed: true,
			wantStatus: http.StatusOK, wantCall: true,
		},
		{
			name: "private channel manager", path: "/channel/" + channelID + "/context", userID: userID,
			channel:    &model.Channel{Id: channelID, Type: model.ChannelTypePrivate},
			permission: model.PermissionManagePrivateChannelProperties, allowed: true,
			wantStatus: http.StatusOK, wantCall: true,
		},
		{
			name: "regular channel member", path: "/channel/" + channelID + "/context", userID: userID,
			channel:    &model.Channel{Id: channelID, Type: model.ChannelTypeOpen},
			permission: model.PermissionManagePublicChannelProperties,
			wantStatus: http.StatusForbidden,
		},
		{
			name: "direct channel rejected", path: "/channel/" + channelID + "/context", userID: userID,
			channel:    &model.Channel{Id: channelID, Type: model.ChannelTypeDirect},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "invalid channel id", path: "/channel/bad/context", userID: userID,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing channel", path: "/channel/" + channelID + "/context", userID: userID,
			channelErr: model.NewAppError("test", "test", nil, "", http.StatusNotFound),
			wantStatus: http.StatusNotFound,
		},
		{
			name: "unauthenticated", path: "/channel/" + channelID + "/context",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			service := &fakeChannelContextService{state: channelcontext.State{Files: []channelcontext.KnowledgeFile{}}}
			e.api.channelContextService = service

			if tt.channel != nil || tt.channelErr != nil {
				e.mockAPI.On("GetChannel", channelID).Return(tt.channel, tt.channelErr).Once()
			}
			if tt.permission != nil {
				e.mockAPI.On("HasPermissionToChannel", tt.userID, channelID, tt.permission).Return(tt.allowed).Once()
			}

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.userID != "" {
				req.Header.Set("Mattermost-User-Id", tt.userID)
			}
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			assert.Equal(t, tt.wantStatus, recorder.Code)
			if tt.wantCall {
				assert.Equal(t, 1, service.getCallCount)
				assert.Equal(t, channelID, service.gotChannel)
			} else {
				assert.Zero(t, service.getCallCount)
			}
		})
	}
}

func TestSaveChannelContext(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	fileID := model.NewId()
	update := channelcontext.Update{
		CustomInstructions: "Use the channel glossary.",
		FileIDs:            []string{fileID},
	}

	tests := []struct {
		name       string
		body       []byte
		serviceErr error
		wantStatus int
		wantSave   bool
		wantError  string
	}{
		{
			name: "success", body: mustJSON(t, update),
			wantStatus: http.StatusOK, wantSave: true,
		},
		{
			name: "malformed request", body: []byte("{"),
			wantStatus: http.StatusBadRequest, wantError: "invalid request body",
		},
		{
			name: "validation error", body: mustJSON(t, update),
			serviceErr: &channelcontext.ValidationError{Message: "unsupported file"},
			wantStatus: http.StatusBadRequest, wantSave: true, wantError: "unsupported file",
		},
		{
			name: "storage error", body: mustJSON(t, update),
			serviceErr: errors.New("database credentials leaked"),
			wantStatus: http.StatusInternalServerError, wantSave: true, wantError: "failed to manage channel context",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			service := &fakeChannelContextService{
				state: channelcontext.State{
					CustomInstructions: update.CustomInstructions,
					Files: []channelcontext.KnowledgeFile{{
						ID: fileID, Name: "guide.pdf", MimeType: "application/pdf", Size: 10,
					}},
				},
				saveErr: tt.serviceErr,
			}
			e.api.channelContextService = service
			e.mockAPI.On("GetChannel", channelID).Return(
				&model.Channel{Id: channelID, Type: model.ChannelTypeOpen},
				(*model.AppError)(nil),
			).Once()
			e.mockAPI.On(
				"HasPermissionToChannel",
				userID,
				channelID,
				model.PermissionManagePublicChannelProperties,
			).Return(true).Once()

			req := httptest.NewRequest(http.MethodPut, "/channel/"+channelID+"/context", bytes.NewReader(tt.body))
			req.Header.Set("Mattermost-User-Id", userID)
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			assert.Equal(t, tt.wantStatus, recorder.Code)
			if tt.wantSave {
				assert.Equal(t, 1, service.saveCallCount)
				assert.Equal(t, channelID, service.gotChannel)
				assert.Equal(t, userID, service.gotUser)
				assert.Equal(t, update, service.gotUpdate)
			} else {
				assert.Zero(t, service.saveCallCount)
			}
			if tt.wantError != "" {
				var response map[string]string
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				assert.Contains(t, response["error"], tt.wantError)
				assert.NotContains(t, response["error"], "credentials leaked")
			}
		})
	}
}

func TestChannelContextServiceUnavailable(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	channelID := model.NewId()
	userID := model.NewId()
	e.mockAPI.On("GetChannel", channelID).Return(
		&model.Channel{Id: channelID, Type: model.ChannelTypeOpen},
		(*model.AppError)(nil),
	).Once()
	e.mockAPI.On(
		"HasPermissionToChannel",
		userID,
		channelID,
		model.PermissionManagePublicChannelProperties,
	).Return(true).Once()

	recorder := doRequest(e.api, http.MethodGet, "/channel/"+channelID+"/context", nil, userID)
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
