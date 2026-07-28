// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestChangedTopLevelConfigKeys(t *testing.T) {
	tests := []struct {
		name     string
		prev     *config.Config
		next     config.Config
		expected []string
	}{
		{
			name:     "identical configs report nothing",
			prev:     &config.Config{DefaultBotName: "ai"},
			next:     config.Config{DefaultBotName: "ai"},
			expected: []string{},
		},
		{
			name: "only differing sections are reported, sorted",
			prev: &config.Config{DefaultBotName: "ai"},
			next: config.Config{
				DefaultBotName: "other",
				Services:       []llm.ServiceConfig{{ID: "svc1", Type: "openai", APIKey: "sk-secret"}},
			},
			expected: []string{"defaultBotName", "services"},
		},
		{
			name: "nil previous config reports every key of the new config",
			prev: nil,
			next: config.Config{},
			expected: func() []string {
				raw, err := json.Marshal(config.Config{})
				require.NoError(t, err)
				var m map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(raw, &m))
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				return keys
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := changedTopLevelConfigKeys(tt.prev, tt.next)
			assert.ElementsMatch(t, tt.expected, got)
		})
	}
}

// setupAuditConfigEnv wires the full-router test environment with a working
// config store/updater/notifier so PUT /admin/config exercises the real
// middleware stack end to end.
func setupAuditConfigEnv(t *testing.T) (*TestEnvironment, *testConfigStore) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	store := &testConfigStore{cfg: &config.Config{}}
	e.api.configStore = store
	e.api.configUpdater = &testConfigUpdater{}
	e.api.clusterNotifier = &testClusterNotifier{}
	return e, store
}

func TestAuditMiddlewareSaveConfig(t *testing.T) {
	const plantedSecret = "sk-super-secret-api-key-value" //nolint:gosec // fake credential planted to prove it never reaches the audit record

	requestBody := `{"defaultBotName":"ai","services":[{"id":"svc1","name":"OpenAI","type":"openai","apiKey":"` + plantedSecret + `"}]}`

	tests := []struct {
		name           string
		userID         string
		isAdmin        bool
		body           string
		saveErr        error
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord)
	}{
		{
			name:           "success records actor, changed keys, and no secrets",
			userID:         "userid",
			isAdmin:        true,
			body:           requestBody,
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, "userid", rec.Actor.UserId)
				assert.Equal(t, "/admin/config", rec.Meta[model.AuditKeyAPIPath])
				assert.Equal(t, []string{"defaultBotName", "mcp", "services"}, rec.EventData.Parameters["changed_keys"])

				raw, err := json.Marshal(rec)
				require.NoError(t, err)
				assert.NotContains(t, string(raw), plantedSecret, "audit record must never carry config values")
			},
		},
		{
			name:           "permission denial records a fail with 403",
			userID:         "userid",
			isAdmin:        false,
			body:           requestBody,
			expectedStatus: http.StatusForbidden,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, "userid", rec.Actor.UserId)
				assert.Equal(t, http.StatusForbidden, rec.Error.Code)
			},
		},
		{
			name:           "unauthenticated request records a fail with 401 and empty actor",
			userID:         "",
			body:           requestBody,
			expectedStatus: http.StatusUnauthorized,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Empty(t, rec.Actor.UserId)
				assert.Equal(t, http.StatusUnauthorized, rec.Error.Code)
			},
		},
		{
			name:           "store failure records a fail with 500 and a description",
			userID:         "userid",
			isAdmin:        true,
			body:           requestBody,
			saveErr:        errors.New("kv exploded"),
			expectedStatus: http.StatusInternalServerError,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusInternalServerError, rec.Error.Code)
				assert.NotEmpty(t, rec.Error.Description)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, store := setupAuditConfigEnv(t)
			defer e.Cleanup(t)
			store.saveErr = tt.saveErr

			if tt.userID != "" {
				e.mockAPI.On("HasPermissionTo", tt.userID, model.PermissionManageSystem).Return(tt.isAdmin)
			}
			records := e.CaptureAuditRecords()

			req := httptest.NewRequest(http.MethodPut, "/admin/config", strings.NewReader(tt.body))
			if tt.userID != "" {
				req.Header.Set("Mattermost-User-Id", tt.userID)
			}
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{SessionId: "sessionid", IPAddress: "127.0.0.1"}, recorder, req)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventSaveConfig, rec.EventName)
			assert.Equal(t, "sessionid", rec.Actor.SessionId)
			assert.Equal(t, "127.0.0.1", rec.Actor.IpAddress)
			tt.validateRecord(t, rec)
		})
	}
}

func TestAuditMiddlewareUnauditedRouteEmitsNothing(t *testing.T) {
	e, _ := setupAuditConfigEnv(t)
	defer e.Cleanup(t)

	e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(true)
	records := e.CaptureAuditRecords()

	req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	req.Header.Set("Mattermost-User-Id", "userid")
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, req)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	assert.Empty(t, *records, "read-only routes must not emit audit records")
}

func TestAuditMiddlewareEmitsFailRecordOnPanic(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	records := e.CaptureAuditRecords()

	panicky := func(c *gin.Context) { panic("boom") }
	e.api.auditEvents[handlerFuncName(panicky)] = "panicEvent"

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(e.api.auditMiddleware(&plugin.Context{}))
	router.POST("/panic", panicky)

	req := httptest.NewRequest(http.MethodPost, "/panic", nil)
	req.Header.Set("Mattermost-User-Id", "userid")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusInternalServerError, recorder.Result().StatusCode)
	require.Len(t, *records, 1, "a panicking handler must still produce an audit record")
	rec := (*records)[0]
	assert.Equal(t, "panicEvent", rec.EventName)
	assert.Equal(t, model.AuditStatusFail, rec.Status)
	assert.Equal(t, http.StatusInternalServerError, rec.Error.Code)
	assert.NotContains(t, rec.Error.Description, "boom", "panic values must not leak into the record")
}

func TestAuditMiddlewareRecordsTraceID(t *testing.T) {
	// The middleware picks the trace ID off the ambient otelgin span; a real
	// tracer provider is needed for the span context to carry one.
	prevProvider := otel.GetTracerProvider()
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prevProvider)
		_ = tp.Shutdown(context.Background())
	})

	e, _ := setupAuditConfigEnv(t)
	defer e.Cleanup(t)

	e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(true)
	records := e.CaptureAuditRecords()

	req := httptest.NewRequest(http.MethodPut, "/admin/config", strings.NewReader(`{}`))
	req.Header.Set("Mattermost-User-Id", "userid")
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, req)

	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
	require.Len(t, *records, 1)
	traceID, ok := (*records)[0].Meta[audit.MetaTraceID].(string)
	require.True(t, ok, "trace_id meta must be present")
	assert.Len(t, traceID, 32)
	assert.NotEqual(t, strings.Repeat("0", 32), traceID)
}
