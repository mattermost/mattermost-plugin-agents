// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

func TestAuditUpdateMCPUserPreferences(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	// A record-injection plant: user-supplied server names that do not match
	// any configured server must never reach the audit record verbatim.
	const plantedUnknownServer = "PLANTED-FREE-TEXT-NOT-A-SERVER"

	tests := []struct {
		name           string
		body           string
		kvSetErr       error
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name:           "successful save records the normalized server names and count",
			body:           `{"disabled_servers":["server-b","server-a","server-b"]}`,
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, []string{"server-a", "server-b"}, rec.EventData.Parameters["disabled_servers"])
				assert.Equal(t, 2, rec.EventData.Parameters["disabled_servers_count"])
			},
		},
		{
			name:           "unknown server names are counted but never recorded verbatim",
			body:           `{"disabled_servers":["` + plantedUnknownServer + `","server-a"]}`,
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, []string{"server-a"}, rec.EventData.Parameters["disabled_servers"])
				assert.Equal(t, 2, rec.EventData.Parameters["disabled_servers_count"])

				raw, err := json.Marshal(rec)
				require.NoError(t, err)
				assert.NotContains(t, string(raw), plantedUnknownServer,
					"free-text preference entries must not be injectable into the audit log")
			},
		},
		{
			name:           "malformed body records a 400 fail without preference parameters",
			body:           `{"disabled_servers":["server-a"`,
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.NotContains(t, rec.EventData.Parameters, "disabled_servers")
				assert.NotContains(t, rec.EventData.Parameters, "disabled_servers_count")
			},
		},
		{
			name:           "persistence failure records a 500 fail without claiming servers were disabled",
			body:           `{"disabled_servers":["server-a"]}`,
			kvSetErr:       errors.New("kv unavailable"),
			expectedStatus: http.StatusInternalServerError,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusInternalServerError, rec.Error.Code)
				assert.NotContains(t, rec.EventData.Parameters, "disabled_servers")
				assert.NotContains(t, rec.EventData.Parameters, "disabled_servers_count")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// The audited list is intersected with known servers; make the
			// legitimate test names known via the MCP config.
			e.config.mcpConfig = mcp.Config{Servers: []mcp.ServerConfig{
				{Name: "server-a", Enabled: true, BaseURL: "https://a.example"},
				{Name: "server-b", Enabled: true, BaseURL: "https://b.example"},
			}}

			mmClient := mmapimocks.NewMockClient(t)
			mmClient.On("KVSet", mock.AnythingOfType("string"), mock.Anything).Return(tt.kvSetErr).Maybe()
			e.api.mmClient = mmClient

			records := e.CaptureAuditRecords()

			request := httptest.NewRequest(http.MethodPut, "/mcp/user-preferences", strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventUpdateMCPUserPreferences, rec.EventName)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			assert.Equal(t, "/mcp/user-preferences", rec.Meta[model.AuditKeyAPIPath])
			tt.validateRecord(t, rec)
		})
	}
}
