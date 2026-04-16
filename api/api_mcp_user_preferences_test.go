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
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/stretchr/testify/require"
)

func TestHandlePutUserPreferencesRejectsMalformedJSONWithoutGinBindAbort(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	api := &API{}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/mcp/user-preferences", strings.NewReader(`{"disabled_servers":["server-a"`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("Mattermost-User-Id", testUserID)

	api.handlePutUserPreferences(ctx)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.True(t, ctx.IsAborted())
	require.Len(t, ctx.Errors, 1)
	require.Empty(t, ctx.Errors.ByType(gin.ErrorTypeBind))
	require.NotNil(t, ctx.Errors.Last())
	require.ErrorContains(t, ctx.Errors.Last(), "invalid request body")
}

func TestHandlePutUserPreferencesRequestEntityTooLarge(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	api := &API{}

	// Valid JSON array large enough to exceed MaxBytesReader (avoid slow per-rune loops).
	body := `{"disabled_servers":[` + strings.Repeat(`"x",`, 70000) + `"z"]}`
	require.Greater(t, len(body), mcp.UserPreferencesMaxRequestBodyBytes)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/mcp/user-preferences", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("Mattermost-User-Id", testUserID)

	api.handlePutUserPreferences(ctx)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	require.True(t, ctx.IsAborted())
}
