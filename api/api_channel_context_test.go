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
	"github.com/mattermost/mattermost-plugin-agents/v2/channelcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testContextPostID = "post12345678901234567890ab"

// setupChannelContextTest prepares a test environment at Enterprise Advanced
// with a channel of the given type resolvable by the
// channelReadAuthorizationRequired middleware. No agents are registered: the
// channel context routes must not depend on one.
func setupChannelContextTest(t *testing.T, channelType model.ChannelType) *TestEnvironment {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.OverrideLicense(enterprisetest.LicenseFor(enterprise.LevelEnterpriseAdvanced))
	e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
		Id:     "channelid",
		Type:   channelType,
		TeamId: "teamid",
	}, nil)
	return e
}

func (e *TestEnvironment) allowChannelContextPermissions(canRead, canManage bool) {
	e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(canRead)
	e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePublicChannelProperties).Return(canManage).Maybe()
	e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionManagePrivateChannelProperties).Return(canManage).Maybe()
}

func (e *TestEnvironment) doChannelContextRequest(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, "/channel/channelid"+path, reader)
	request.Header.Add("Mattermost-User-ID", "userid")
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)
	return recorder.Result()
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var out T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func TestChannelContextReadRoutes(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		envSetup       func(e *TestEnvironment)
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "instructions are empty when unset",
			path:           "/instructions",
			envSetup:       func(e *TestEnvironment) { e.allowChannelContextPermissions(true, false) },
			expectedStatus: http.StatusOK,
			expectedBody:   `{"instructions":""}`,
		},
		{
			name: "stored instructions are returned on an unlicensed server",
			path: "/instructions",
			envSetup: func(e *TestEnvironment) {
				e.allowChannelContextPermissions(true, false)
				e.OverrideLicense(&model.License{})
				e.channelContext.instructions["channelid"] = "Deploys freeze on Fridays."
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"instructions":"Deploys freeze on Fridays."}`,
		},
		{
			name: "instructions store error returns 500",
			path: "/instructions",
			envSetup: func(e *TestEnvironment) {
				e.allowChannelContextPermissions(true, false)
				e.channelContext.getErr = errors.New("db unavailable")
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "instructions require read permission",
			path:           "/instructions",
			envSetup:       func(e *TestEnvironment) { e.allowChannelContextPermissions(false, false) },
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "no pinned posts",
			path:           "/context_posts",
			envSetup:       func(e *TestEnvironment) { e.allowChannelContextPermissions(true, false) },
			expectedStatus: http.StatusOK,
			expectedBody:   fmt.Sprintf(`{"posts":[],"max_posts":%d}`, channelcontext.MaxPinnedPosts),
		},
		{
			name: "pinned posts are returned with their content",
			path: "/context_posts",
			envSetup: func(e *TestEnvironment) {
				e.allowChannelContextPermissions(true, false)
				e.channelContext.pins["channelid"] = []channelcontext.PinnedPost{{
					Pin:  channelcontext.Pin{ChannelID: "channelid", PostID: testContextPostID, PinnedBy: "pinner", PinnedAt: 20},
					Post: &model.Post{Id: testContextPostID, ChannelId: "channelid", UserId: "author", RootId: "root", Message: "hello", CreateAt: 10},
				}}
			},
			expectedStatus: http.StatusOK,
			expectedBody: fmt.Sprintf(`{"posts":[{"post_id":%q,"user_id":"author","root_id":"root","message":"hello","create_at":10,"pinned_by":"pinner","pinned_at":20}],"max_posts":%d}`,
				testContextPostID, channelcontext.MaxPinnedPosts),
		},
		{
			name:           "pinned posts require read permission",
			path:           "/context_posts",
			envSetup:       func(e *TestEnvironment) { e.allowChannelContextPermissions(false, false) },
			expectedStatus: http.StatusForbidden,
		},
		{
			name: "pinned posts store error returns 500",
			path: "/context_posts",
			envSetup: func(e *TestEnvironment) {
				e.allowChannelContextPermissions(true, false)
				e.channelContext.listErr = errors.New("db unavailable")
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupChannelContextTest(t, model.ChannelTypeOpen)
			defer e.Cleanup(t)
			tc.envSetup(e)

			resp := e.doChannelContextRequest(t, http.MethodGet, tc.path, "")
			require.Equal(t, tc.expectedStatus, resp.StatusCode)
			if tc.expectedBody != "" {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.JSONEq(t, tc.expectedBody, string(body))
			}
		})
	}
}

// TestChannelContextWritePermissions pins that every write route requires the
// channel-management permission for the channel's type and rejects DM/GM
// channels, without touching the store when denied.
func TestChannelContextWritePermissions(t *testing.T) {
	writes := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "set instructions", method: http.MethodPut, path: "/instructions", body: `{"instructions":"hi"}`},
		{name: "pin post", method: http.MethodPost, path: "/context_posts", body: fmt.Sprintf(`{"post_id":%q}`, testContextPostID)},
		{name: "unpin post", method: http.MethodDelete, path: "/context_posts/" + testContextPostID},
	}
	channels := []struct {
		name           string
		channelType    model.ChannelType
		canManage      bool
		expectedStatus int
	}{
		{name: "public channel manager", channelType: model.ChannelTypeOpen, canManage: true, expectedStatus: http.StatusOK},
		{name: "public channel non-manager", channelType: model.ChannelTypeOpen, canManage: false, expectedStatus: http.StatusForbidden},
		{name: "private channel manager", channelType: model.ChannelTypePrivate, canManage: true, expectedStatus: http.StatusOK},
		{name: "private channel non-manager", channelType: model.ChannelTypePrivate, canManage: false, expectedStatus: http.StatusForbidden},
		{name: "direct message", channelType: model.ChannelTypeDirect, canManage: true, expectedStatus: http.StatusBadRequest},
		{name: "group message", channelType: model.ChannelTypeGroup, canManage: true, expectedStatus: http.StatusBadRequest},
	}

	for _, write := range writes {
		for _, ch := range channels {
			t.Run(write.name+"/"+ch.name, func(t *testing.T) {
				e := setupChannelContextTest(t, ch.channelType)
				defer e.Cleanup(t)
				e.allowChannelContextPermissions(true, ch.canManage)

				resp := e.doChannelContextRequest(t, write.method, write.path, write.body)
				require.Equal(t, ch.expectedStatus, resp.StatusCode)

				wrote := len(e.channelContext.setCalls) + len(e.channelContext.pinCalls) + len(e.channelContext.unpinCalls)
				if ch.expectedStatus == http.StatusOK {
					require.Equal(t, 1, wrote)
				} else {
					require.Zero(t, wrote, "a rejected write must not reach the store")
				}
			})
		}
	}
}

func TestChannelContextWritesByLicenseLevel(t *testing.T) {
	writes := []struct {
		name          string
		method        string
		path          string
		body          string
		alwaysAllowed bool
	}{
		{name: "set instructions", method: http.MethodPut, path: "/instructions", body: `{"instructions":"Deploys freeze on Fridays."}`},
		{name: "pin post", method: http.MethodPost, path: "/context_posts", body: fmt.Sprintf(`{"post_id":%q}`, testContextPostID)},
		{name: "clear instructions", method: http.MethodPut, path: "/instructions", body: `{"instructions":"  "}`, alwaysAllowed: true},
		{name: "unpin post", method: http.MethodDelete, path: "/context_posts/" + testContextPostID, alwaysAllowed: true},
	}

	for _, write := range writes {
		for _, level := range enterprisetest.AllLevels {
			t.Run(write.name+"/"+level.String(), func(t *testing.T) {
				e := setupChannelContextTest(t, model.ChannelTypeOpen)
				defer e.Cleanup(t)
				e.OverrideLicense(enterprisetest.LicenseFor(level))
				e.allowChannelContextPermissions(true, true)

				resp := e.doChannelContextRequest(t, write.method, write.path, write.body)
				if !write.alwaysAllowed && level < enterprise.LevelEnterpriseAdvanced {
					requireLicenseDenied(t, resp, enterprise.CapChannelContext)
					require.Empty(t, e.channelContext.setCalls)
					require.Empty(t, e.channelContext.pinCalls)
					return
				}
				require.Equal(t, http.StatusOK, resp.StatusCode)
			})
		}
	}
}

func TestChannelContextWriteErrors(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		envSetup       func(e *TestEnvironment)
		expectedStatus int
	}{
		{
			name:   "instructions validation failure returns 400",
			method: http.MethodPut,
			path:   "/instructions",
			body:   `{"instructions":"too long"}`,
			envSetup: func(e *TestEnvironment) {
				e.channelContext.setErr = fmt.Errorf("too long: %w", channelcontext.ErrValidation)
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "instructions store failure returns 500",
			method:         http.MethodPut,
			path:           "/instructions",
			body:           `{"instructions":"hello"}`,
			envSetup:       func(e *TestEnvironment) { e.channelContext.setErr = errors.New("db unavailable") },
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "malformed instructions body returns 400",
			method:         http.MethodPut,
			path:           "/instructions",
			body:           `{"instructions":`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "oversized instructions body returns 413",
			method:         http.MethodPut,
			path:           "/instructions",
			body:           fmt.Sprintf(`{"instructions":%q}`, strings.Repeat("a", channelInstructionsMaxRequestBodyBytes)),
			expectedStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:   "invalid post returns 400",
			method: http.MethodPost,
			path:   "/context_posts",
			body:   fmt.Sprintf(`{"post_id":%q}`, testContextPostID),
			envSetup: func(e *TestEnvironment) {
				e.channelContext.pinErr = fmt.Errorf("not here: %w", channelcontext.ErrValidation)
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "full channel returns 409",
			method:         http.MethodPost,
			path:           "/context_posts",
			body:           fmt.Sprintf(`{"post_id":%q}`, testContextPostID),
			envSetup:       func(e *TestEnvironment) { e.channelContext.pinErr = channelcontext.ErrPinLimitReached },
			expectedStatus: http.StatusConflict,
		},
		{
			name:           "pin store failure returns 500",
			method:         http.MethodPost,
			path:           "/context_posts",
			body:           fmt.Sprintf(`{"post_id":%q}`, testContextPostID),
			envSetup:       func(e *TestEnvironment) { e.channelContext.pinErr = errors.New("db unavailable") },
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "oversized pin body returns 413",
			method:         http.MethodPost,
			path:           "/context_posts",
			body:           fmt.Sprintf(`{"post_id":%q}`, strings.Repeat("a", channelContextPinMaxRequestBodyBytes)),
			expectedStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:           "unpin store failure returns 500",
			method:         http.MethodDelete,
			path:           "/context_posts/" + testContextPostID,
			envSetup:       func(e *TestEnvironment) { e.channelContext.unpinErr = errors.New("db unavailable") },
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupChannelContextTest(t, model.ChannelTypeOpen)
			defer e.Cleanup(t)
			e.allowChannelContextPermissions(true, true)
			if tc.envSetup != nil {
				tc.envSetup(e)
			}

			resp := e.doChannelContextRequest(t, tc.method, tc.path, tc.body)
			require.Equal(t, tc.expectedStatus, resp.StatusCode)
		})
	}
}

func TestChannelContextWritesPersistAndPublish(t *testing.T) {
	e := setupChannelContextTest(t, model.ChannelTypeOpen)
	defer e.Cleanup(t)
	e.allowChannelContextPermissions(true, true)

	var published []map[string]any
	mmClient := mmapimocks.NewMockClient(t)
	mmClient.On("PublishWebSocketEvent", WebsocketEventChannelContextUpdated, mock.AnythingOfType("map[string]interface {}"), mock.AnythingOfType("*model.WebsocketBroadcast")).
		Run(func(args mock.Arguments) {
			broadcast, _ := args.Get(2).(*model.WebsocketBroadcast)
			require.Equal(t, "channelid", broadcast.ChannelId)
			published = append(published, args.Get(1).(map[string]any))
		}).Return()
	e.api.mmClient = mmClient

	resp := e.doChannelContextRequest(t, http.MethodPut, "/instructions", `{"instructions":"  Deploys freeze on Fridays. "}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, ChannelInstructions{Instructions: "Deploys freeze on Fridays."}, decodeJSON[ChannelInstructions](t, resp))
	require.Equal(t, "Deploys freeze on Fridays.", e.channelContext.instructions["channelid"])

	resp = e.doChannelContextRequest(t, http.MethodPost, "/context_posts", fmt.Sprintf(`{"post_id":%q}`, testContextPostID))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	pinned := decodeJSON[ChannelContextPosts](t, resp)
	require.Len(t, pinned.Posts, 1)
	require.Equal(t, testContextPostID, pinned.Posts[0].PostID)
	require.Equal(t, "userid", pinned.Posts[0].PinnedBy)

	resp = e.doChannelContextRequest(t, http.MethodDelete, "/context_posts/"+testContextPostID, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, decodeJSON[ChannelContextPosts](t, resp).Posts)

	require.Equal(t, []map[string]any{{"channel_id": "channelid"}, {"channel_id": "channelid"}, {"channel_id": "channelid"}}, published,
		"every write notifies channel members with only the channel ID")
}

// TestChannelContextAuditRecords pins the audit contract: each write emits one
// record carrying object identifiers only, and instructions text never lands
// in a record.
func TestChannelContextAuditRecords(t *testing.T) {
	const sentinel = "SENTINEL-instructions-content-must-not-be-audited"

	tests := []struct {
		name                string
		method              string
		path                string
		body                string
		canManage           bool
		expectedStatus      int
		expectedEvent       string
		expectedAuditStatus string
		expectedPostID      string
	}{
		{
			name:                "set instructions",
			method:              http.MethodPut,
			path:                "/instructions",
			body:                fmt.Sprintf(`{"instructions":%q}`, sentinel),
			canManage:           true,
			expectedStatus:      http.StatusOK,
			expectedEvent:       AuditEventUpdateChannelInstructions,
			expectedAuditStatus: model.AuditStatusSuccess,
		},
		{
			name:                "denied instructions write",
			method:              http.MethodPut,
			path:                "/instructions",
			body:                fmt.Sprintf(`{"instructions":%q}`, sentinel),
			canManage:           false,
			expectedStatus:      http.StatusForbidden,
			expectedEvent:       AuditEventUpdateChannelInstructions,
			expectedAuditStatus: model.AuditStatusFail,
		},
		{
			name:                "pin post",
			method:              http.MethodPost,
			path:                "/context_posts",
			body:                fmt.Sprintf(`{"post_id":%q}`, testContextPostID),
			canManage:           true,
			expectedStatus:      http.StatusOK,
			expectedEvent:       AuditEventPinChannelContextPost,
			expectedAuditStatus: model.AuditStatusSuccess,
			expectedPostID:      testContextPostID,
		},
		{
			name:                "unpin post",
			method:              http.MethodDelete,
			path:                "/context_posts/" + testContextPostID,
			canManage:           true,
			expectedStatus:      http.StatusOK,
			expectedEvent:       AuditEventUnpinChannelContextPost,
			expectedAuditStatus: model.AuditStatusSuccess,
			expectedPostID:      testContextPostID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupChannelContextTest(t, model.ChannelTypeOpen)
			defer e.Cleanup(t)
			e.allowChannelContextPermissions(true, tc.canManage)
			records := e.CaptureAuditRecords()

			resp := e.doChannelContextRequest(t, tc.method, tc.path, tc.body)
			require.Equal(t, tc.expectedStatus, resp.StatusCode)

			require.Len(t, *records, 1)
			rec := (*records)[0]
			require.Equal(t, tc.expectedEvent, rec.EventName)
			require.Equal(t, tc.expectedAuditStatus, rec.Status)
			require.Equal(t, "userid", rec.Actor.UserId)
			require.Equal(t, "channelid", rec.EventData.Parameters[audit.KeyChannelID])
			if tc.expectedPostID != "" {
				require.Equal(t, tc.expectedPostID, rec.EventData.Parameters[audit.KeyPostID])
			}

			marshaled, err := json.Marshal(rec)
			require.NoError(t, err)
			require.NotContains(t, string(marshaled), sentinel)
		})
	}

	t.Run("reads emit no audit record", func(t *testing.T) {
		for _, path := range []string{"/instructions", "/context_posts"} {
			e := setupChannelContextTest(t, model.ChannelTypeOpen)
			e.allowChannelContextPermissions(true, false)
			records := e.CaptureAuditRecords()

			resp := e.doChannelContextRequest(t, http.MethodGet, path, "")
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Empty(t, *records, "read-only routes must not emit audit records")
			e.Cleanup(t)
		}
	})
}
