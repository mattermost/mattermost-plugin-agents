// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func decodeLicenseError(t *testing.T, resp *http.Response) licenseErrorResponse {
	t.Helper()
	var body licenseErrorResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return body
}

func requireLicenseDenied(t *testing.T, resp *http.Response, cap enterprise.Capability) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	body := decodeLicenseError(t, resp)
	require.NotEmpty(t, body.Error)
	require.Equal(t, enterprise.RequiredLevel(cap).Key(), body.LicenseRequired)
}

func requireNotLicenseDenied(t *testing.T, resp *http.Response) {
	t.Helper()
	require.NotEqual(t, http.StatusForbidden, resp.StatusCode)
}

type capabilityRouteTest struct {
	name     string
	method   string
	path     string
	body     string
	cap      enterprise.Capability
	minLevel enterprise.Level
	setup    func(e *TestEnvironment)
}

func setupPostRoute(e *TestEnvironment) {
	e.setupTestBot(llm.BotConfig{Name: "permtest", DisplayName: "Permission Bot"})
	e.mockAPI.On("GetPost", "postid").Return(&model.Post{
		Id:        "postid",
		UserId:    testBotUserID,
		ChannelId: "channelid",
	}, nil)
	e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
		Id:     "channelid",
		Type:   model.ChannelTypeOpen,
		TeamId: "teamid",
	}, nil)
	e.mockAPI.On("HasPermissionToChannel", testUserID, "channelid", model.PermissionReadChannel).Return(true)
}

func setupChannelRoute(e *TestEnvironment) {
	e.setupTestBot(llm.BotConfig{Name: "permtest", DisplayName: "Permission Bot"})
	e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
		Id:     "channelid",
		Type:   model.ChannelTypeOpen,
		TeamId: "teamid",
	}, nil)
	e.mockAPI.On("HasPermissionToChannel", testUserID, "channelid", model.PermissionReadChannel).Return(true)
}

func setupAdminRoute(e *TestEnvironment) {
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true)
}

func setupSearchRoute(e *TestEnvironment) {
	e.setupTestBot(llm.BotConfig{Name: "permtest", DisplayName: "Permission Bot"})
}

func capabilityRouteTests() []capabilityRouteTest {
	return []capabilityRouteTest{
		{
			name:     "thread analysis",
			method:   http.MethodPost,
			path:     "/post/postid/analyze?botUsername=permtest",
			body:     `{`,
			cap:      enterprise.CapThreadSummarization,
			minLevel: enterprise.LevelProfessional,
			setup:    setupPostRoute,
		},
		{
			name:     "transcribe file",
			method:   http.MethodPost,
			path:     "/post/postid/transcribe/file/fileid?botUsername=permtest",
			body:     `{`,
			cap:      enterprise.CapMeetings,
			minLevel: enterprise.LevelEnterprise,
			setup:    setupPostRoute,
		},
		{
			name:     "summarize transcription",
			method:   http.MethodPost,
			path:     "/post/postid/summarize_transcription?botUsername=permtest",
			body:     `{`,
			cap:      enterprise.CapMeetings,
			minLevel: enterprise.LevelEnterprise,
			setup:    setupPostRoute,
		},
		{
			name:     "postback summary",
			method:   http.MethodPost,
			path:     "/post/postid/postback_summary?botUsername=permtest",
			body:     `{`,
			cap:      enterprise.CapMeetings,
			minLevel: enterprise.LevelEnterprise,
			setup:    setupPostRoute,
		},
		{
			name:     "channel analyze",
			method:   http.MethodPost,
			path:     "/channel/channelid/analyze?botUsername=permtest",
			body:     `{`,
			cap:      enterprise.CapChannelSummarization,
			minLevel: enterprise.LevelProfessional,
			setup:    setupChannelRoute,
		},
		{
			name:     "channel interval",
			method:   http.MethodPost,
			path:     "/channel/channelid/interval?botUsername=permtest",
			body:     `{`,
			cap:      enterprise.CapChannelSummarization,
			minLevel: enterprise.LevelProfessional,
			setup:    setupChannelRoute,
		},
		{
			name:     "search query",
			method:   http.MethodPost,
			path:     "/search?botUsername=permtest",
			body:     `{"query":"hello"}`,
			cap:      enterprise.CapSemanticSearch,
			minLevel: enterprise.LevelEnterprise,
			setup:    setupSearchRoute,
		},
		{
			name:     "search run",
			method:   http.MethodPost,
			path:     "/search/run?botUsername=permtest",
			body:     `{"query":"hello"}`,
			cap:      enterprise.CapSemanticSearch,
			minLevel: enterprise.LevelEnterprise,
			setup:    setupSearchRoute,
		},
		{
			name:     "search raw",
			method:   http.MethodPost,
			path:     "/search/raw",
			body:     `{"query":"hello"}`,
			cap:      enterprise.CapSemanticSearch,
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name:     "admin reindex",
			method:   http.MethodPost,
			path:     "/admin/reindex",
			body:     `{}`,
			cap:      enterprise.CapSemanticSearch,
			minLevel: enterprise.LevelEnterprise,
			setup:    setupAdminRoute,
		},
		{
			name:     "admin reindex catchup",
			method:   http.MethodPost,
			path:     "/admin/reindex/catchup",
			body:     ``,
			cap:      enterprise.CapSemanticSearch,
			minLevel: enterprise.LevelEnterprise,
			setup:    setupAdminRoute,
		},
		{
			name:     "admin rebuild vector index",
			method:   http.MethodPost,
			path:     "/admin/reindex/rebuild-vector-index",
			body:     ``,
			cap:      enterprise.CapSemanticSearch,
			minLevel: enterprise.LevelEnterprise,
			setup:    setupAdminRoute,
		},
	}
}

func TestCapabilityRoutesByLevel(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, route := range capabilityRouteTests() {
		t.Run(route.name, func(t *testing.T) {
			for _, level := range enterprisetest.AllLevels {
				t.Run(level.String(), func(t *testing.T) {
					e := SetupTestEnvironment(t)
					defer e.Cleanup(t)

					e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
					e.OverrideLicense(enterprisetest.LicenseFor(level))
					e.mockAPI.On("LogError", mock.Anything).Maybe()
					if route.setup != nil {
						route.setup(e)
					}

					var reader io.Reader
					if route.body != "" {
						reader = strings.NewReader(route.body)
					}
					req := httptest.NewRequest(route.method, route.path, reader)
					req.Header.Add("Mattermost-User-ID", testUserID)
					recorder := httptest.NewRecorder()
					e.api.ServeHTTP(&plugin.Context{}, recorder, req)
					resp := recorder.Result()

					if level < route.minLevel {
						requireLicenseDenied(t, resp, route.cap)
						return
					}
					requireNotLicenseDenied(t, resp)
				})
			}
		})
	}
}

func TestCapabilityRoutesFailClosed(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, route := range capabilityRouteTests() {
		t.Run(route.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.licenseChecker = nil
			e.mockAPI.On("LogError", mock.Anything).Maybe()
			if route.setup != nil {
				route.setup(e)
			}

			var reader io.Reader
			if route.body != "" {
				reader = strings.NewReader(route.body)
			}
			req := httptest.NewRequest(route.method, route.path, reader)
			req.Header.Add("Mattermost-User-ID", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			requireLicenseDenied(t, recorder.Result(), route.cap)
		})
	}
}

func TestAdminReindexReadAndCancelUngated(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "status", method: http.MethodGet, path: "/admin/reindex/status"},
		{name: "health-check", method: http.MethodGet, path: "/admin/reindex/health-check"},
		{name: "cancel", method: http.MethodPost, path: "/admin/reindex/cancel"},
	}

	for _, tc := range tests {
		for _, level := range enterprisetest.AllLevels {
			t.Run(tc.name+"/"+level.String(), func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
				e.OverrideLicense(enterprisetest.LicenseFor(level))
				e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(true)
				e.mockAPI.On("LogError", mock.Anything).Maybe()

				req := httptest.NewRequest(tc.method, tc.path, nil)
				req.Header.Add("Mattermost-User-ID", testUserID)
				recorder := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, recorder, req)

				requireNotLicenseDenied(t, recorder.Result())
			})
		}
	}
}
