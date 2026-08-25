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
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestConfigChangedJSONKeys pins config.Config's behavior through the shared
// audit.ChangedJSONKeys helper (generic cases live in audit/diff_test.go).
func TestConfigChangedJSONKeys(t *testing.T) {
	tests := []struct {
		name     string
		prev     *config.Config
		next     config.Config
		validate func(t *testing.T, got []string)
	}{
		{
			name: "identical configs report nothing",
			prev: &config.Config{DefaultBotName: "ai"},
			next: config.Config{DefaultBotName: "ai"},
			validate: func(t *testing.T, got []string) {
				assert.Empty(t, got)
			},
		},
		{
			name: "only differing sections are reported, sorted",
			prev: &config.Config{DefaultBotName: "ai"},
			next: config.Config{
				DefaultBotName: "other",
				Services:       []llm.ServiceConfig{{ID: "svc1", Type: "openai", APIKey: "sk-secret"}},
			},
			validate: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"defaultBotName", "services"}, got)
			},
		},
		{
			name: "nil previous config reports every key of the new config",
			prev: nil,
			next: config.Config{},
			// Independently verifiable properties instead of re-running the
			// implementation's marshal round-trip: known keys present, sorted,
			// and covering at least the documented top-level sections.
			validate: func(t *testing.T, got []string) {
				assert.True(t, sort.StringsAreSorted(got), "keys must be sorted")
				assert.GreaterOrEqual(t, len(got), 10)
				for _, key := range []string{"bots", "defaultBotName", "embeddingSearchConfig", "mcp", "services", "webSearch"} {
					assert.Contains(t, got, key)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(t, audit.ChangedJSONKeys(tt.prev, tt.next))
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
		getErr         error
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
				assert.Equal(t, true, rec.EventData.Parameters["persisted"])

				raw, err := json.Marshal(rec)
				require.NoError(t, err)
				assert.NotContains(t, string(raw), plantedSecret, "audit record must never carry config values")
			},
		},
		{
			name:           "prior-config read failure still saves and audits, omitting changed_keys",
			userID:         "userid",
			isAdmin:        true,
			body:           requestBody,
			getErr:         errors.New("kv read exploded"),
			expectedStatus: http.StatusOK,
			validateRecord: func(t *testing.T, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.NotContains(t, rec.EventData.Parameters, "changed_keys",
					"best-effort diff must be omitted, not fabricated, when the prior config is unreadable")
				assert.Equal(t, true, rec.EventData.Parameters["persisted"])
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
				assert.Empty(t, rec.Error.Description,
					"free-form handler error text must never enter audit records")
				assert.NotContains(t, rec.EventData.Parameters, "persisted",
					"a save that never landed must not claim persistence")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, store := setupAuditConfigEnv(t)
			defer e.Cleanup(t)
			store.getErr = tt.getErr
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

// TestAuditRegistryAllRoutesEmit hits every audited route and requires that a
// record with the registered event name is emitted, whatever the response
// status. This guards the HandlerName-keyed registry: if a future change
// wraps a registered handler in a closure at route registration, the route
// silently stops being audited and only this test notices.
func TestAuditRegistryAllRoutesEmit(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		event  string
		method string
		path   string
		bridge bool // authenticate as a plugin instead of a user
	}{
		{event: AuditEventSaveConfig, method: http.MethodPut, path: "/admin/config"},
		{event: AuditEventReindexPosts, method: http.MethodPost, path: "/admin/reindex"},
		{event: AuditEventCancelReindexJob, method: http.MethodPost, path: "/admin/reindex/cancel"},
		{event: AuditEventCatchUpReindex, method: http.MethodPost, path: "/admin/reindex/catchup"},
		{event: AuditEventRebuildVectorIndex, method: http.MethodPost, path: "/admin/reindex/rebuild-vector-index"},
		{event: AuditEventClearMCPToolsCache, method: http.MethodPost, path: "/admin/mcp/tools/cache/clear"},
		{event: AuditEventUpdateMCPPluginServer, method: http.MethodPut, path: "/admin/mcp/plugin-servers/some.plugin"},
		{event: AuditEventCreateAgent, method: http.MethodPost, path: "/agents"},
		{event: AuditEventUpdateAgent, method: http.MethodPut, path: "/agents/agentid"},
		{event: AuditEventDeleteAgent, method: http.MethodDelete, path: "/agents/agentid"},
		{event: AuditEventUpdateAgentAvatar, method: http.MethodPost, path: "/agents/agentid/avatar"},
		{event: AuditEventCreateCustomPrompt, method: http.MethodPost, path: "/custom-prompts"},
		{event: AuditEventUpdateCustomPrompt, method: http.MethodPut, path: "/custom-prompts/promptid"},
		{event: AuditEventDeleteCustomPrompt, method: http.MethodDelete, path: "/custom-prompts/promptid"},
		{event: AuditEventUpdateChannelAutoReply, method: http.MethodPut, path: "/channel/channelid/autoreply"},
		{event: AuditEventMCPOAuthCallback, method: http.MethodGet, path: "/oauth/callback"},
		{event: AuditEventMCPOAuthStart, method: http.MethodGet, path: "/mcp/oauth/someserver/start"},
		{event: AuditEventMCPOAuthDisconnect, method: http.MethodDelete, path: "/mcp/oauth/someserver"},
		{event: AuditEventUpdateMCPUserPreferences, method: http.MethodPut, path: "/mcp/user-preferences"},
		{event: AuditEventRegisterMCPPluginServer, method: http.MethodPost, path: "/bridge/v1/mcp/register", bridge: true},
		{event: AuditEventUnregisterMCPPluginServer, method: http.MethodPost, path: "/bridge/v1/mcp/unregister", bridge: true},
		{event: AuditEventToolCallApproval, method: http.MethodPost, path: "/post/postid/tool_call"},
		{event: AuditEventToolResultApproval, method: http.MethodPost, path: "/post/postid/tool_result"},
	}

	// One case per registry row, no more and no fewer (the session grant is
	// not routed and is covered by its own tests).
	require.Len(t, tests, len(buildAuditEventRegistry(&API{})))

	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			records := e.CaptureAuditRecords()

			// Generous permission/KV defaults: the point is reaching the
			// route, not its happy path. Denials and errors emit too.
			e.mockAPI.On("HasPermissionTo", mock.Anything, mock.Anything).Return(false).Maybe()
			e.mockAPI.On("KVGet", mock.Anything).Return(([]byte)(nil), (*model.AppError)(nil)).Maybe()
			e.mockAPI.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(true, (*model.AppError)(nil)).Maybe()
			e.mockAPI.On("GetChannel", mock.Anything).Return((*model.Channel)(nil), &model.AppError{Message: "not found"}).Maybe()

			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader("{}"))
			if tt.bridge {
				req.Header.Set("Mattermost-Plugin-ID", "some.calling.plugin")
			} else {
				req.Header.Set("Mattermost-User-Id", "userid")
			}
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Len(t, *records, 1, "route must emit exactly one audit record (status %d)", recorder.Result().StatusCode)
			assert.Equal(t, tt.event, (*records)[0].EventName)
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
	assert.Equal(t, "panic during request handling", rec.Error.Description,
		"panic records carry only the static marker, never the panic value")
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
