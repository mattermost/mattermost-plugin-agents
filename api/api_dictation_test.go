// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/dictation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const (
	dictationServiceID      = "dict1234567890123456789012"
	dictationAPIKey         = "sk-dictation-test-key-0123456789"
	dictationSecretSentinel = "ek_SENTINEL_CLIENT_SECRET"
	upstreamBodySentinel    = "UPSTREAM_BODY_SENTINEL"
	urlCredentialSentinel   = "URLSECRETSENTINEL"
)

type fakeRealtimeProvider struct {
	status   atomic.Int32
	requests atomic.Int32
	lastBody atomic.Value // map[string]any
}

func newFakeRealtimeProvider(t *testing.T) (*fakeRealtimeProvider, *httptest.Server) {
	t.Helper()
	p := &fakeRealtimeProvider{}
	p.status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		assert.Equal(t, "/v1/realtime/client_secrets", r.URL.Path)
		assert.Equal(t, "Bearer "+dictationAPIKey, r.Header.Get("Authorization"))
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		p.lastBody.Store(body)

		w.Header().Set("Content-Type", "application/json")
		status := int(p.status.Load())
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = w.Write([]byte(`{"error":{"message":"` + upstreamBodySentinel + `","type":"server_error"}}`))
			return
		}
		expiresAt := time.Now().Add(time.Minute).Unix()
		_, _ = w.Write([]byte(`{"value":"` + dictationSecretSentinel + `","expires_at":` + strconv.FormatInt(expiresAt, 10) + `}`))
	}))
	t.Cleanup(server.Close)
	return p, server
}

func setupDictationEnv(t *testing.T, level enterprise.Level) (*TestEnvironment, *fakeRealtimeProvider) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.OverrideLicense(enterprisetest.LicenseFor(level))

	provider, server := newFakeRealtimeProvider(t)
	e.config.services = []llm.ServiceConfig{
		{ID: "anth123456789012345678901", Name: "Agents", Type: llm.ServiceTypeAnthropic, APIKey: "anthropic-key"},
		{ID: dictationServiceID, Name: "Dictation", Type: llm.ServiceTypeOpenAI, APIKey: dictationAPIKey, APIURL: server.URL},
	}
	e.config.dictation = config.DictationConfig{Enabled: true, ServiceID: dictationServiceID, Model: "gpt-live-transcribe"}
	return e, provider
}

func (e *TestEnvironment) doDictationRequest(method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Mattermost-User-Id", testUserID)
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{SessionId: "sessionid"}, recorder, req)
	return recorder
}

// denyDictationService makes the access checker deny the dictation service.
func (e *TestEnvironment) denyDictationService() {
	checker := accesscontrol.New(perIDDecisionClient{denied: map[string]bool{dictationServiceID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)
	e.api.SetDictationService(dictation.New(e.config, enterprise.NewLicenseChecker(e.client), checker, &e.client.Log))
}

func TestCreateDictationSessionOutcomes(t *testing.T) {
	tests := []struct {
		name          string
		level         enterprise.Level
		setup         func(e *TestEnvironment, p *fakeRealtimeProvider)
		wantStatus    int
		wantProviders int32
	}{
		{
			name:       "disabled",
			level:      enterprise.LevelEnterprise,
			setup:      func(e *TestEnvironment, _ *fakeRealtimeProvider) { e.config.dictation.Enabled = false },
			wantStatus: http.StatusNotFound,
		},
		{name: "unlicensed", level: enterprise.LevelUnlicensed, wantStatus: http.StatusForbidden},
		{name: "professional", level: enterprise.LevelProfessional, wantStatus: http.StatusForbidden},
		{name: "enterprise", level: enterprise.LevelEnterprise, wantStatus: http.StatusOK, wantProviders: 1},
		{name: "enterprise advanced", level: enterprise.LevelEnterpriseAdvanced, wantStatus: http.StatusOK, wantProviders: 1},
		{
			name:  "service deleted",
			level: enterprise.LevelEnterprise,
			setup: func(e *TestEnvironment, _ *fakeRealtimeProvider) {
				e.config.services = e.config.services[:1]
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:  "service is anthropic",
			level: enterprise.LevelEnterprise,
			setup: func(e *TestEnvironment, _ *fakeRealtimeProvider) {
				e.config.dictation.ServiceID = e.config.services[0].ID
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:  "service without an API key",
			level: enterprise.LevelEnterprise,
			setup: func(e *TestEnvironment, _ *fakeRealtimeProvider) {
				e.config.services[1].APIKey = ""
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:  "service API URL carries credentials",
			level: enterprise.LevelEnterprise,
			setup: func(e *TestEnvironment, _ *fakeRealtimeProvider) {
				e.config.services[1].APIURL = strings.Replace(e.config.services[1].APIURL, "http://", "http://user:"+urlCredentialSentinel+"@", 1)
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "service access denied",
			level:      enterprise.LevelEnterprise,
			setup:      func(e *TestEnvironment, _ *fakeRealtimeProvider) { e.denyDictationService() },
			wantStatus: http.StatusForbidden,
		},
		{
			name:          "provider failure",
			level:         enterprise.LevelEnterprise,
			setup:         func(_ *TestEnvironment, p *fakeRealtimeProvider) { p.status.Store(http.StatusInternalServerError) },
			wantStatus:    http.StatusBadGateway,
			wantProviders: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, provider := setupDictationEnv(t, tt.level)
			defer e.Cleanup(t)
			if tt.setup != nil {
				tt.setup(e, provider)
			}

			recorder := e.doDictationRequest(http.MethodPost, "/dictation/session")

			require.Equal(t, tt.wantStatus, recorder.Code, recorder.Body.String())
			assert.Equal(t, tt.wantProviders, provider.requests.Load())
			assert.NotContains(t, recorder.Body.String(), upstreamBodySentinel)
			assert.NotContains(t, recorder.Body.String(), dictationAPIKey)
			assert.NotContains(t, recorder.Body.String(), urlCredentialSentinel)
			if tt.wantStatus == http.StatusForbidden && tt.level < enterprise.LevelEnterprise {
				requireLicenseDenied(t, recorder.Result(), enterprise.CapDictation)
			}
			if tt.wantStatus == http.StatusOK {
				assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			} else {
				assert.NotContains(t, recorder.Body.String(), dictationSecretSentinel)
			}
		})
	}
}

func TestCreateDictationSessionResponse(t *testing.T) {
	tests := []struct {
		model             string
		wantTurnDetection string
		wantSent          any
	}{
		{model: "gpt-live-transcribe", wantTurnDetection: "none", wantSent: nil},
		{model: "gpt-4o-transcribe", wantTurnDetection: "server_vad", wantSent: map[string]any{"type": "server_vad"}},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			e, provider := setupDictationEnv(t, enterprise.LevelEnterprise)
			defer e.Cleanup(t)
			e.config.dictation.Model = tt.model

			recorder := e.doDictationRequest(http.MethodPost, "/dictation/session")
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

			var body map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			assert.ElementsMatch(t,
				[]string{"clientSecret", "expiresAt", "callsURL", "provider", "model", "turnDetection"},
				keysOf(body))
			assert.Equal(t, dictationSecretSentinel, body["clientSecret"])
			assert.Greater(t, body["expiresAt"], float64(time.Now().UnixMilli()))
			assert.Equal(t, e.config.services[1].APIURL+"/v1/realtime/calls", body["callsURL"])
			assert.Equal(t, llm.ServiceTypeOpenAI, body["provider"])
			assert.Equal(t, tt.model, body["model"])
			assert.Equal(t, tt.wantTurnDetection, body["turnDetection"])

			sent, ok := provider.lastBody.Load().(map[string]any)
			require.True(t, ok)
			session, ok := sent["session"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "transcription", session["type"])
			input := session["audio"].(map[string]any)["input"].(map[string]any)
			assert.Equal(t, tt.model, input["transcription"].(map[string]any)["model"])
			require.Contains(t, input, "turn_detection")
			assert.Equal(t, tt.wantSent, input["turn_detection"])
		})
	}
}

func TestCreateDictationSessionProviderErrorStaysServerSide(t *testing.T) {
	previous := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})

	e, provider := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	provider.status.Store(http.StatusInternalServerError)
	records := e.CaptureAuditRecords()

	recorder := e.doDictationRequest(http.MethodPost, "/dictation/session")
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), upstreamBodySentinel)

	spans := exporter.GetSpans()
	require.NotEmpty(t, spans)
	for _, span := range spans {
		raw, err := json.Marshal(span)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), upstreamBodySentinel, "span %q", span.Name)
	}

	require.Len(t, *records, 1)
	raw, err := json.Marshal((*records)[0])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), upstreamBodySentinel)
}

func TestCreateDictationSessionRateLimited(t *testing.T) {
	e, provider := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)

	statuses := make([]int, 0, 3)
	for range 3 {
		statuses = append(statuses, e.doDictationRequest(http.MethodPost, "/dictation/session").Code)
	}

	assert.Equal(t, []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests}, statuses)
	assert.Equal(t, int32(2), provider.requests.Load(), "a rate-limited request never reaches the provider")
}

func TestCreateDictationSessionAudit(t *testing.T) {
	e, _ := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	e.config.dictation.Language = "xx-LANGUAGE-SENTINEL"
	e.config.dictation.Model = "MODEL-SENTINEL-transcribe"
	records := e.CaptureAuditRecords()

	recorder := e.doDictationRequest(http.MethodPost, "/dictation/session")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), dictationSecretSentinel)

	require.Len(t, *records, 1)
	rec := (*records)[0]
	assert.Equal(t, AuditEventCreateDictationSession, rec.EventName)
	assert.Equal(t, model.AuditStatusSuccess, rec.Status)
	assert.Equal(t, testUserID, rec.Actor.UserId)
	assert.Equal(t, dictationServiceID, rec.EventData.Parameters[audit.KeyServiceID])

	raw, err := json.Marshal(rec)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), dictationSecretSentinel)
	assert.NotContains(t, string(raw), dictationAPIKey)
	assert.NotContains(t, string(raw), "LANGUAGE-SENTINEL")
	assert.NotContains(t, string(raw), "MODEL-SENTINEL", "audit records hold identifiers, not config values")
}

func TestGetDictationAvailability(t *testing.T) {
	tests := []struct {
		name          string
		level         enterprise.Level
		setup         func(e *TestEnvironment)
		wantEnabled   bool
		wantAvailable bool
		wantReason    string
	}{
		{name: "available", level: enterprise.LevelEnterprise, wantEnabled: true, wantAvailable: true},
		{
			name:       "disabled",
			level:      enterprise.LevelEnterprise,
			setup:      func(e *TestEnvironment) { e.config.dictation.Enabled = false },
			wantReason: dictation.ReasonDisabled,
		},
		{name: "professional", level: enterprise.LevelProfessional, wantEnabled: true, wantReason: dictation.ReasonLicense},
		{
			name:        "misconfigured",
			level:       enterprise.LevelEnterprise,
			setup:       func(e *TestEnvironment) { e.config.dictation.ServiceID = "gone" },
			wantEnabled: true,
			wantReason:  dictation.ReasonMisconfigured,
		},
		{
			name:        "access denied",
			level:       enterprise.LevelEnterprise,
			setup:       func(e *TestEnvironment) { e.denyDictationService() },
			wantEnabled: true,
			wantReason:  dictation.ReasonAccessDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, provider := setupDictationEnv(t, tt.level)
			defer e.Cleanup(t)
			if tt.setup != nil {
				tt.setup(e)
			}

			recorder := e.doDictationRequest(http.MethodGet, "/dictation/availability")
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

			var body map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			assert.Equal(t, tt.wantEnabled, body["enabled"])
			assert.Equal(t, tt.wantAvailable, body["available"])
			assert.Equal(t, "webrtc", body["transport"])
			if tt.wantReason == "" {
				assert.NotContains(t, body, "reason")
				assert.Equal(t, llm.ServiceTypeOpenAI, body["provider"])
				assert.Equal(t, "gpt-live-transcribe", body["model"])
			} else {
				assert.Equal(t, tt.wantReason, body["reason"])
			}
			assert.Zero(t, provider.requests.Load(), "availability never mints")
		})
	}
}

func TestDictationAfterLicenseDowngrade(t *testing.T) {
	e, _ := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)

	require.Equal(t, http.StatusOK, e.doDictationRequest(http.MethodPost, "/dictation/session").Code)

	e.OverrideLicense(enterprisetest.LicenseFor(enterprise.LevelProfessional))
	e.api.dictationService.Reconcile()

	recorder := e.doDictationRequest(http.MethodGet, "/dictation/availability")
	require.Equal(t, http.StatusOK, recorder.Code)
	var avail dictation.Availability
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &avail))
	assert.False(t, avail.Available)
	assert.Equal(t, dictation.ReasonLicense, avail.Reason)

	requireLicenseDenied(t, e.doDictationRequest(http.MethodPost, "/dictation/session").Result(), enterprise.CapDictation)
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
