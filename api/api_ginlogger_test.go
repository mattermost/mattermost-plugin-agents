// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
)

func TestGinLoggerLevels(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		err       error
		wantLevel string
	}{
		{name: "success with a failed best-effort step", status: http.StatusOK, err: errors.New("failed to deactivate bot"), wantLevel: "LogError"},
		{name: "server error", status: http.StatusInternalServerError, err: errors.New("db down"), wantLevel: "LogError"},
		{name: "forbidden", status: http.StatusForbidden, err: errors.New("must be a system admin"), wantLevel: "LogDebug"},
		{name: "unauthorized", status: http.StatusUnauthorized, err: errors.New("no session"), wantLevel: "LogDebug"},
		{name: "not found", status: http.StatusNotFound, err: errors.New("access policy not found"), wantLevel: "LogDebug"},
		{name: "client disconnect on success", status: http.StatusOK, err: fmt.Errorf("write tcp: %w", syscall.EPIPE), wantLevel: "LogDebug"},
		{name: "client disconnect on server error", status: http.StatusInternalServerError, err: context.Canceled, wantLevel: "LogDebug"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			for _, level := range []string{"LogError", "LogDebug"} {
				mockAPI.On(level, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything,
					mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
			}
			a := &API{pluginAPI: pluginapi.NewClient(mockAPI, nil)}

			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(a.ginlogger)
			router.GET("/agents/:agentid", func(c *gin.Context) {
				_ = c.Error(tt.err)
				c.Status(tt.status)
			})
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/agents/abc", nil))

			mockAPI.AssertNumberOfCalls(t, tt.wantLevel, 1)
			for _, other := range []string{"LogError", "LogDebug"} {
				if other != tt.wantLevel {
					mockAPI.AssertNotCalled(t, other, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything,
						mock.Anything, mock.Anything, mock.Anything, mock.Anything)
				}
			}
		})
	}
}
