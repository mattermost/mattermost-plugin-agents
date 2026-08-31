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
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// setupChannelAutoReplyTest prepares a test environment with a registered bot
// (the selectable target for PUT bodies), a real license checker backed by the
// mock API's default Enterprise license, and a channel of the given type
// resolvable by the channelReadAuthorizationRequired middleware.
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

			if !tc.expectPersist {
				// A rejected PUT must not publish a websocket event: the
				// strict mock has no expectations, so any
				// PublishWebSocketEvent call fails the test.
				e.api.mmClient = mmapimocks.NewMockClient(t)
			}

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

// TestPutChannelAutoReplyValidation covers the rejections the handler owns —
// the mode enum and the request body — plus how it maps the auto-reply
// service's failures onto status codes. Validating the requested setting
// itself (bot_id present, known, and allowed in the channel) is the service's
// job; autoreply.TestServiceSetValidation covers it against the real service.
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

			// A rejected PUT must not publish a websocket event: the strict
			// mock has no expectations, so any PublishWebSocketEvent call
			// fails the test.
			e.api.mmClient = mmapimocks.NewMockClient(t)

			resp := e.doChannelAutoReplyRequest(t, http.MethodPut, tc.body)
			require.Equal(t, tc.expectedStatus, resp.StatusCode)
			require.Empty(t, e.autoReplyStore.settings, "failed request must not leave a persisted setting")
		})
	}
}

// TestChannelAutoReplyIndependentOfDefaultBot pins that the auto-reply
// endpoints have no default-agent dependency: a restricted default agent or an
// instance with zero configured agents must not block reading or clearing
// channel auto-reply settings. The PUT hands the *selected* bot to the
// auto-reply service for validation and never consults the default one.
func TestChannelAutoReplyIndependentOfDefaultBot(t *testing.T) {
	// "ai" is the configured default bot name in the test environment; this
	// variant is restricted from every user and channel.
	restrictedDefaultBot := func() *bots.Bot {
		return bots.NewBot(
			llm.BotConfig{
				Name:               "ai",
				DisplayName:        "Restricted Default",
				ChannelAccessLevel: llm.ChannelAccessLevelNone,
				UserAccessLevel:    llm.UserAccessLevelNone,
			},
			llm.ServiceConfig{},
			&model.Bot{UserId: "defaultbotuserid1234567890", Username: "ai", DisplayName: "Restricted Default"},
			nil,
		)
	}
	permittedBot := func() *bots.Bot {
		return bots.NewBot(
			llm.BotConfig{Name: "permtest"},
			llm.ServiceConfig{},
			&model.Bot{UserId: testBotUserID, Username: "permtest"},
			nil,
		)
	}

	tests := []struct {
		name           string
		method         string
		body           string
		envSetup       func(e *TestEnvironment)
		expectedStatus int
		expectedBody   string
	}{
		{
			name:   "GET works when the default agent is restricted",
			method: http.MethodGet,
			envSetup: func(e *TestEnvironment) {
				e.bots.SetBotsForTesting([]*bots.Bot{restrictedDefaultBot(), permittedBot()})
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"bot_id":"","mode":"off"}`,
		},
		{
			name:   "PUT selecting a permitted agent works when the default agent is restricted",
			method: http.MethodPut,
			body:   fmt.Sprintf(`{"bot_id":%q,"mode":"root_posts"}`, testBotUserID),
			envSetup: func(e *TestEnvironment) {
				e.bots.SetBotsForTesting([]*bots.Bot{restrictedDefaultBot(), permittedBot()})
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(true)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   fmt.Sprintf(`{"bot_id":%q,"mode":"root_posts"}`, testBotUserID),
		},
		{
			name:   "GET works with zero configured agents",
			method: http.MethodGet,
			envSetup: func(e *TestEnvironment) {
				e.bots.SetBotsForTesting(nil)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"bot_id":"","mode":"off"}`,
		},
		{
			name:   "PUT off works with zero configured agents",
			method: http.MethodPut,
			body:   `{"bot_id":"","mode":"off"}`,
			envSetup: func(e *TestEnvironment) {
				e.bots.SetBotsForTesting(nil)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(true)
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

			resp := e.doChannelAutoReplyRequest(t, tc.method, tc.body)
			require.Equal(t, tc.expectedStatus, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.JSONEq(t, tc.expectedBody, string(body))
		})
	}
}

// TestPutChannelAutoReplyUnlicensed pins the license gate to the enabling
// modes only: after a license downgrade an existing setting must remain
// clearable (mode "off" deletes the row), while enabling stays forbidden.
func TestPutChannelAutoReplyUnlicensed(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		expectedStatus int
		expectDeletes  []string
	}{
		{
			name:           "off deletes the existing setting without a license",
			body:           `{"bot_id":"","mode":"off"}`,
			expectedStatus: http.StatusOK,
			expectDeletes:  []string{"channelid"},
		},
		{
			name:           "root_posts is forbidden without a license",
			body:           fmt.Sprintf(`{"bot_id":%q,"mode":"root_posts"}`, testBotUserID),
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "threads is forbidden without a license",
			body:           fmt.Sprintf(`{"bot_id":%q,"mode":"threads"}`, testBotUserID),
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupChannelAutoReplyTest(t, model.ChannelTypeOpen)
			defer e.Cleanup(t)
			e.OverrideLicense(&model.License{})
			e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
			e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(true)
			// Simulate a setting written while the server was still licensed.
			e.autoReplyStore.settings["channelid"] = &autoreply.Setting{
				ChannelID: "channelid",
				BotID:     testBotUserID,
				Mode:      autoreply.ModeThreads,
			}

			resp := e.doChannelAutoReplyRequest(t, http.MethodPut, tc.body)
			require.Equal(t, tc.expectedStatus, resp.StatusCode)

			require.Equal(t, tc.expectDeletes, e.autoReplyStore.deleteCalls)
			if tc.expectDeletes == nil {
				require.NotEmpty(t, e.autoReplyStore.settings, "a forbidden request must not delete the setting")
				require.Empty(t, e.autoReplyStore.setCalls, "a forbidden request must not persist")
			} else {
				require.NotContains(t, e.autoReplyStore.settings, "channelid")
			}
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

// TestChannelAutoReplyAuditRecords pins the audit contract for the auto-reply
// routes: the PUT is registered in the audit event registry (successes and
// denials both emit a record, enriched with the channel and requested
// setting), while the read-only GET stays unaudited per the registry's
// per-route opt-in philosophy.
func TestChannelAutoReplyAuditRecords(t *testing.T) {
	putBody := fmt.Sprintf(`{"bot_id":%q,"mode":"root_posts"}`, testBotUserID)

	tests := []struct {
		name           string
		method         string
		body           string
		envSetup       func(e *TestEnvironment)
		expectedStatus int
		validate       func(t *testing.T, records []*model.AuditRecord)
	}{
		{
			name:   "enabling emits a success record with channel, mode, and bot",
			method: http.MethodPut,
			body:   putBody,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, records []*model.AuditRecord) {
				require.Len(t, records, 1)
				rec := records[0]
				require.Equal(t, AuditEventUpdateChannelAutoReply, rec.EventName)
				require.Equal(t, model.AuditStatusSuccess, rec.Status)
				require.Equal(t, "userid", rec.Actor.UserId)
				require.Equal(t, "channelid", rec.EventData.Parameters[audit.KeyChannelID])
				require.Equal(t, "root_posts", rec.EventData.Parameters["mode"])
				require.Equal(t, testBotUserID, rec.EventData.Parameters["bot_user_id"])
			},
		},
		{
			name:   "turning off emits a success record without a bot id",
			method: http.MethodPut,
			body:   `{"bot_id":"","mode":"off"}`,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, records []*model.AuditRecord) {
				require.Len(t, records, 1)
				rec := records[0]
				require.Equal(t, model.AuditStatusSuccess, rec.Status)
				require.Equal(t, "off", rec.EventData.Parameters["mode"])
				require.NotContains(t, rec.EventData.Parameters, "bot_user_id")
			},
		},
		{
			name:   "denied write emits a fail record with the status code",
			method: http.MethodPut,
			body:   putBody,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(false)
			},
			expectedStatus: http.StatusForbidden,
			validate: func(t *testing.T, records []*model.AuditRecord) {
				require.Len(t, records, 1)
				rec := records[0]
				require.Equal(t, AuditEventUpdateChannelAutoReply, rec.EventName)
				require.Equal(t, model.AuditStatusFail, rec.Status)
				require.Equal(t, http.StatusForbidden, rec.Error.Code)
				require.Equal(t, "channelid", rec.EventData.Parameters[audit.KeyChannelID])
			},
		},
		{
			name:   "read emits no audit record",
			method: http.MethodGet,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, records []*model.AuditRecord) {
				require.Empty(t, records, "read-only routes must not emit audit records")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupChannelAutoReplyTest(t, model.ChannelTypeOpen)
			defer e.Cleanup(t)
			tc.envSetup(e)
			records := e.CaptureAuditRecords()

			resp := e.doChannelAutoReplyRequest(t, tc.method, tc.body)
			require.Equal(t, tc.expectedStatus, resp.StatusCode)
			tc.validate(t, *records)
		})
	}
}
