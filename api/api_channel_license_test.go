// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestChannelAnalysisLicenseMiddleware(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name     string
		endpoint string
		body     string
		licensed bool
	}{
		{
			name:     "analyze endpoint is blocked when unlicensed",
			endpoint: "/channel/channelid/analyze?botUsername=permtest",
			body:     `{`,
			licensed: false,
		},
		{
			name:     "analyze endpoint proceeds past license check when licensed",
			endpoint: "/channel/channelid/analyze?botUsername=permtest",
			body:     `{`,
			licensed: true,
		},
		{
			name:     "interval endpoint is blocked when unlicensed",
			endpoint: "/channel/channelid/interval?botUsername=permtest",
			body:     `{`,
			licensed: false,
		},
		{
			name:     "interval endpoint proceeds past license check when licensed",
			endpoint: "/channel/channelid/interval?botUsername=permtest",
			body:     `{`,
			licensed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.setupTestBot(llm.BotConfig{
				Name:        "permtest",
				DisplayName: "Permission Bot",
			})

			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
			if test.licensed {
				e.mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
			} else {
				e.mockAPI.On("GetLicense").Return(&model.License{}).Maybe()
			}

			e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
				Id:     "channelid",
				Type:   model.ChannelTypeOpen,
				TeamId: "teamid",
			}, nil)
			e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			request := httptest.NewRequest(http.MethodPost, test.endpoint, strings.NewReader(test.body))
			request.Header.Add("Mattermost-User-ID", "userid")

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)
			resp := recorder.Result()

			if test.licensed {
				require.NotEqual(t, http.StatusForbidden, resp.StatusCode)
				return
			}

			require.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}
}
