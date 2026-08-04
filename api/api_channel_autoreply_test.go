// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// setupChannelAutoReplyTest prepares a test environment with a registered bot
// (so aiBotRequired resolves), a real license checker backed by the mock API's
// default Enterprise license, and a channel of the given type resolvable by
// the channelAuthorizationRequired middleware.
func setupChannelAutoReplyTest(t *testing.T, channelType model.ChannelType) *TestEnvironment {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	e.setupTestBot(llm.BotConfig{Name: "permtest"})
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
		Id:     "channelid",
		Type:   channelType,
		TeamId: "teamid",
	}, nil)
	return e
}

func (e *TestEnvironment) doChannelAutoReplyRequest(t *testing.T, method, body string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, "/channel/channelid/autoreply", reader)
	request.Header.Add("Mattermost-User-ID", "userid")
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)
	return recorder.Result()
}

func TestGetChannelAutoReply(t *testing.T) {
	tests := []struct {
		name           string
		envSetup       func(e *TestEnvironment)
		expectedStatus int
		expectedBody   string // asserted only when non-empty
	}{
		{
			name: "unset channel returns off with empty bot",
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"bot_id":"","mode":"off"}`,
		},
		{
			name: "existing setting is echoed",
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.autoReplyStore.settings["channelid"] = &autoreply.Setting{
					ChannelID: "channelid",
					BotID:     testBotUserID,
					Mode:      autoreply.ModeThreads,
				}
			},
			expectedStatus: http.StatusOK,
			expectedBody:   fmt.Sprintf(`{"bot_id":%q,"mode":"threads"}`, testBotUserID),
		},
		{
			name: "store error returns 500",
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.autoReplyStore.getErr = errors.New("db unavailable")
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name: "no read permission returns 403",
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(false)
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name: "unlicensed server can still read",
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.OverrideLicense(&model.License{})
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"bot_id":"","mode":"off"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupChannelAutoReplyTest(t, model.ChannelTypeOpen)
			defer e.Cleanup(t)
			tc.envSetup(e)

			resp := e.doChannelAutoReplyRequest(t, http.MethodGet, "")
			require.Equal(t, tc.expectedStatus, resp.StatusCode)

			if tc.expectedBody != "" {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.JSONEq(t, tc.expectedBody, string(body))
			}
		})
	}
}

func TestPutChannelAutoReplyPermissionMatrix(t *testing.T) {
	validBody := fmt.Sprintf(`{"bot_id":%q,"mode":"root_posts"}`, testBotUserID)

	tests := []struct {
		name           string
		channelType    model.ChannelType
		envSetup       func(e *TestEnvironment)
		expectedStatus int
		expectPersist  bool
	}{
		{
			name:        "open channel with manage public properties permission saves",
			channelType: model.ChannelTypeOpen,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(true)
			},
			expectedStatus: http.StatusOK,
			expectPersist:  true,
		},
		{
			name:        "open channel without manage public properties permission is forbidden",
			channelType: model.ChannelTypeOpen,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(false)
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:        "private channel with manage private properties permission saves",
			channelType: model.ChannelTypePrivate,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePrivateChannelProperties).Return(true)
			},
			expectedStatus: http.StatusOK,
			expectPersist:  true,
		},
		{
			name:        "private channel without manage private properties permission is forbidden",
			channelType: model.ChannelTypePrivate,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePrivateChannelProperties).Return(false)
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:        "direct message channel is rejected",
			channelType: model.ChannelTypeDirect,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:        "group message channel is rejected",
			channelType: model.ChannelTypeGroup,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:        "unlicensed server is forbidden",
			channelType: model.ChannelTypeOpen,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(true).Maybe()
				e.OverrideLicense(&model.License{})
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:        "no read permission is forbidden by middleware",
			channelType: model.ChannelTypeOpen,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(false)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(true).Maybe()
			},
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupChannelAutoReplyTest(t, tc.channelType)
			defer e.Cleanup(t)
			tc.envSetup(e)

			resp := e.doChannelAutoReplyRequest(t, http.MethodPut, validBody)
			require.Equal(t, tc.expectedStatus, resp.StatusCode)

			if tc.expectPersist {
				require.Len(t, e.autoReplyStore.setCalls, 1)
				saved := e.autoReplyStore.setCalls[0]
				require.Equal(t, "channelid", saved.ChannelID)
				require.Equal(t, testBotUserID, saved.BotID)
				require.Equal(t, autoreply.ModeRootPosts, saved.Mode)
				require.Equal(t, "userid", saved.UpdatedBy)

				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.JSONEq(t, validBody, string(body))
			} else {
				require.Empty(t, e.autoReplyStore.setCalls, "rejected request must not persist")
				require.Empty(t, e.autoReplyStore.deleteCalls, "rejected request must not delete")
			}
		})
	}
}

func TestPutChannelAutoReplyValidation(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		storeSetup     func(store *fakeChannelAutoReplyStore)
		expectedStatus int
	}{
		{
			name:           "unknown mode",
			body:           fmt.Sprintf(`{"bot_id":%q,"mode":"banana"}`, testBotUserID),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "root_posts without bot_id",
			body:           `{"bot_id":"","mode":"root_posts"}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "threads with unregistered bot",
			body:           fmt.Sprintf(`{"bot_id":%q,"mode":"threads"}`, testNonexistentBot),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "malformed JSON body",
			body:           `{not json`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "body larger than limit",
			body:           fmt.Sprintf(`{"bot_id":%q,"mode":"threads"}`, strings.Repeat("a", 2048)),
			expectedStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "service validation rejection maps to 400",
			body: fmt.Sprintf(`{"bot_id":%q,"mode":"threads"}`, testBotUserID),
			storeSetup: func(store *fakeChannelAutoReplyStore) {
				store.setErr = fmt.Errorf("bot is not allowed in this channel: %w", autoreply.ErrValidation)
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "generic save failure maps to 500",
			body: fmt.Sprintf(`{"bot_id":%q,"mode":"threads"}`, testBotUserID),
			storeSetup: func(store *fakeChannelAutoReplyStore) {
				store.setErr = errors.New("db unavailable")
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name: "generic delete failure maps to 500",
			body: `{"bot_id":"","mode":"off"}`,
			storeSetup: func(store *fakeChannelAutoReplyStore) {
				store.deleteErr = errors.New("db unavailable")
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupChannelAutoReplyTest(t, model.ChannelTypeOpen)
			defer e.Cleanup(t)
			e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
			e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(true)
			if tc.storeSetup != nil {
				tc.storeSetup(e.autoReplyStore)
			}

			resp := e.doChannelAutoReplyRequest(t, http.MethodPut, tc.body)
			require.Equal(t, tc.expectedStatus, resp.StatusCode)
			require.Empty(t, e.autoReplyStore.settings, "failed request must not leave a persisted setting")
		})
	}
}

func TestPutChannelAutoReplyPersistsAndPublishes(t *testing.T) {
	e := setupChannelAutoReplyTest(t, model.ChannelTypeOpen)
	defer e.Cleanup(t)
	e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
	e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(true)

	type wsEvent struct {
		name      string
		payload   map[string]interface{}
		broadcast *model.WebsocketBroadcast
	}
	var events []wsEvent
	mmClient := mmapimocks.NewMockClient(t)
	mmClient.On("PublishWebSocketEvent", mock.AnythingOfType("string"), mock.AnythingOfType("map[string]interface {}"), mock.AnythingOfType("*model.WebsocketBroadcast")).
		Run(func(args mock.Arguments) {
			payload, _ := args.Get(1).(map[string]interface{})
			broadcast, _ := args.Get(2).(*model.WebsocketBroadcast)
			events = append(events, wsEvent{name: args.String(0), payload: payload, broadcast: broadcast})
		}).Return()
	e.api.mmClient = mmClient

	doPut := func(t *testing.T, body string) (*http.Response, ChannelAutoReply) {
		t.Helper()
		resp := e.doChannelAutoReplyRequest(t, http.MethodPut, body)
		var echoed ChannelAutoReply
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&echoed))
		return resp, echoed
	}

	t.Run("enabling root_posts persists and publishes", func(t *testing.T) {
		resp, echoed := doPut(t, fmt.Sprintf(`{"bot_id":%q,"mode":"root_posts"}`, testBotUserID))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, ChannelAutoReply{BotID: testBotUserID, Mode: "root_posts"}, echoed)

		require.Len(t, e.autoReplyStore.setCalls, 1)
		require.Equal(t, "userid", e.autoReplyStore.setCalls[0].UpdatedBy)
		stored := e.autoReplyStore.settings["channelid"]
		require.NotNil(t, stored)
		require.Equal(t, autoreply.ModeRootPosts, stored.Mode)

		require.Len(t, events, 1)
		require.Equal(t, WebsocketEventChannelAutoReplyUpdated, events[0].name)
		require.Equal(t, "channelid", events[0].payload["channel_id"])
		require.Equal(t, testBotUserID, events[0].payload["bot_id"])
		require.Equal(t, "root_posts", events[0].payload["mode"])
		require.NotNil(t, events[0].broadcast)
		require.Equal(t, "channelid", events[0].broadcast.ChannelId)
	})

	t.Run("switching to threads overwrites the stored mode", func(t *testing.T) {
		resp, echoed := doPut(t, fmt.Sprintf(`{"bot_id":%q,"mode":"threads"}`, testBotUserID))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, ChannelAutoReply{BotID: testBotUserID, Mode: "threads"}, echoed)

		stored := e.autoReplyStore.settings["channelid"]
		require.NotNil(t, stored)
		require.Equal(t, autoreply.ModeThreads, stored.Mode)

		require.Len(t, events, 2)
		require.Equal(t, "threads", events[1].payload["mode"])
	})

	t.Run("turning off deletes the row and publishes off", func(t *testing.T) {
		resp, echoed := doPut(t, `{"bot_id":"","mode":"off"}`)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, ChannelAutoReply{BotID: "", Mode: "off"}, echoed)

		require.Equal(t, []string{"channelid"}, e.autoReplyStore.deleteCalls)
		require.NotContains(t, e.autoReplyStore.settings, "channelid")

		require.Len(t, events, 3)
		require.Equal(t, WebsocketEventChannelAutoReplyUpdated, events[2].name)
		require.Equal(t, "channelid", events[2].payload["channel_id"])
		require.Equal(t, "", events[2].payload["bot_id"])
		require.Equal(t, "off", events[2].payload["mode"])
		require.NotNil(t, events[2].broadcast)
		require.Equal(t, "channelid", events[2].broadcast.ChannelId)
	})
}
