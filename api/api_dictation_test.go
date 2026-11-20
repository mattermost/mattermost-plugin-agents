// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
)

const (
	dictationServiceID    = "dict1234567890123456789012"
	dictationAPIKey       = "sk-dictation-test-key-0123456789"
	upstreamBodySentinel  = "UPSTREAM_BODY_SENTINEL"
	transcriptSentinel    = "TRANSCRIPT_CONTENT_SENTINEL"
	urlCredentialSentinel = "URLSECRETSENTINEL"
	testOffer             = "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\ns=-\r\n"
)

// pluginUserGetter looks users up through the plugin API, as the server's
// wiring does.
type pluginUserGetter struct{ client *pluginapi.Client }

func (g pluginUserGetter) GetUser(id string) (*model.User, error) { return g.client.User.Get(id) }

// memoryLeaseStore has the KV store's compare-and-set semantics. Sharing one
// between two dictation services stands in for two HA nodes.
type memoryLeaseStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newMemoryLeaseStore() *memoryLeaseStore {
	return &memoryLeaseStore{values: map[string][]byte{}}
}

func (m *memoryLeaseStore) Get(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.values[key], nil
}

func (m *memoryLeaseStore) CompareAndSet(key string, old, value []byte, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.values[key]
	if (old == nil && exists) || (old != nil && !bytes.Equal(current, old)) {
		return false, nil
	}
	m.values[key] = value
	return true, nil
}

type sessionEnd struct{ userID, sessionID, reason string }

type dictationEnv struct {
	*TestEnvironment
	t      *testing.T
	fake   *llmtest.FakeRealtime
	leases *memoryLeaseStore
	ended  chan sessionEnd
}

// newDictationService builds a service the way the server does, with this
// environment's config, license, users, channels and shared lease store.
func (e *dictationEnv) newDictationService(access dictation.AccessChecker, opts ...dictation.Option) *dictation.Service {
	opts = append([]dictation.Option{
		dictation.WithSessionEndNotifier(func(userID, sessionID, reason string) {
			e.ended <- sessionEnd{userID, sessionID, reason}
		}),
		dictation.WithChannelDirectory(dictation.NewPluginChannelDirectory(e.client)),
	}, opts...)
	svc := dictation.New(e.config, enterprise.NewLicenseChecker(e.client), access, pluginUserGetter{e.client}, e.leases, &e.client.Log, opts...)
	e.t.Cleanup(svc.Shutdown)
	return svc
}

func setupDictationEnv(t *testing.T, level enterprise.Level, opts ...dictation.Option) *dictationEnv {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.OverrideLicense(enterprisetest.LicenseFor(level))
	e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Roles: model.SystemUserRoleId}, nil).Maybe()

	fake := llmtest.NewFakeRealtime()
	t.Cleanup(fake.Close)
	e.config.services = []llm.ServiceConfig{
		{ID: "anth123456789012345678901", Name: "Agents", Type: llm.ServiceTypeAnthropic, APIKey: "anthropic-key"},
		{ID: dictationServiceID, Name: "Dictation", Type: llm.ServiceTypeOpenAI, APIKey: dictationAPIKey, APIURL: fake.URL()},
	}
	e.config.dictation = config.DictationConfig{Enabled: true, ServiceID: dictationServiceID, Model: "gpt-live-transcribe", Language: "en"}

	de := &dictationEnv{TestEnvironment: e, t: t, fake: fake, leases: newMemoryLeaseStore(), ended: make(chan sessionEnd, 8)}
	e.api.SetDictationService(de.newDictationService(e.api.accessChecker, opts...))
	return de
}

func (e *dictationEnv) postSession(body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/dictation/session", strings.NewReader(body))
	req.Header.Set("Mattermost-User-Id", testUserID)
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{SessionId: "sessionid"}, recorder, req)
	return recorder
}

func offerBody(sdp string) string {
	b, _ := json.Marshal(map[string]string{"sdp": sdp})
	return string(b)
}

func (e *dictationEnv) startSession(t *testing.T) (sessionID, callID string) {
	t.Helper()
	recorder := e.postSession(offerBody(testOffer))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	callID = e.fake.LastCallID()
	require.True(t, e.fake.WaitAttached(callID, 5*time.Second))
	return body["sessionId"].(string), callID
}

func (e *dictationEnv) waitEnd(t *testing.T) sessionEnd {
	t.Helper()
	select {
	case end := <-e.ended:
		return end
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the server did not end the session")
		return sessionEnd{}
	}
}

// denyingAccess makes the access checker deny the dictation service.
func denyingAccess() *accesscontrol.Checker {
	return accesscontrol.New(perIDDecisionClient{denied: map[string]bool{dictationServiceID: true}}, nil, accesscontrol.NoMCPServerIDs, nil)
}

func TestCreateDictationSessionOutcomes(t *testing.T) {
	tests := []struct {
		name         string
		level        enterprise.Level
		body         string
		setup        func(e *dictationEnv)
		wantStatus   int
		wantCalls    int
		wantHangups  int
		wantLicenseE bool
		after        func(t *testing.T, e *dictationEnv)
	}{
		{name: "enterprise", level: enterprise.LevelEnterprise, wantStatus: http.StatusCreated, wantCalls: 1},
		{name: "enterprise advanced", level: enterprise.LevelEnterpriseAdvanced, wantStatus: http.StatusCreated, wantCalls: 1},
		{name: "unlicensed", level: enterprise.LevelUnlicensed, wantStatus: http.StatusForbidden, wantLicenseE: true},
		{name: "professional", level: enterprise.LevelProfessional, wantStatus: http.StatusForbidden, wantLicenseE: true},
		{
			name:       "disabled",
			level:      enterprise.LevelEnterprise,
			setup:      func(e *dictationEnv) { e.config.dictation.Enabled = false },
			wantStatus: http.StatusNotFound,
		},
		{name: "empty offer", level: enterprise.LevelEnterprise, body: offerBody("  "), wantStatus: http.StatusBadRequest},
		{name: "invalid JSON", level: enterprise.LevelEnterprise, body: "{", wantStatus: http.StatusBadRequest},
		{name: "oversized offer", level: enterprise.LevelEnterprise, body: offerBody("v=0\r\n" + strings.Repeat("a", 70*1024)), wantStatus: http.StatusRequestEntityTooLarge},
		{
			name:  "guest account",
			level: enterprise.LevelEnterprise,
			setup: func(e *dictationEnv) {
				e.mockAPI.ExpectedCalls = withoutCall(e.mockAPI.ExpectedCalls, "GetUser", testUserID)
				e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Roles: model.SystemGuestRoleId}, nil)
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:  "bot account",
			level: enterprise.LevelEnterprise,
			setup: func(e *dictationEnv) {
				e.mockAPI.ExpectedCalls = withoutCall(e.mockAPI.ExpectedCalls, "GetUser", testUserID)
				e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Roles: model.SystemUserRoleId, IsBot: true}, nil)
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:  "user lookup fails",
			level: enterprise.LevelEnterprise,
			setup: func(e *dictationEnv) {
				e.mockAPI.ExpectedCalls = withoutCall(e.mockAPI.ExpectedCalls, "GetUser", testUserID)
				e.mockAPI.On("GetUser", testUserID).Return(nil, model.NewAppError("GetUser", "app.user.get.app_error", nil, "", http.StatusInternalServerError))
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "service access denied",
			level:      enterprise.LevelEnterprise,
			setup:      func(e *dictationEnv) { e.api.SetDictationService(e.newDictationService(denyingAccess())) },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "service deleted",
			level:      enterprise.LevelEnterprise,
			setup:      func(e *dictationEnv) { e.config.services = e.config.services[:1] },
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "service is anthropic",
			level:      enterprise.LevelEnterprise,
			setup:      func(e *dictationEnv) { e.config.dictation.ServiceID = e.config.services[0].ID },
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:  "API URL carries credentials",
			level: enterprise.LevelEnterprise,
			setup: func(e *dictationEnv) {
				e.config.services[1].APIURL = strings.Replace(e.config.services[1].APIURL, "http://", "http://user:"+urlCredentialSentinel+"@", 1)
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:  "protocol-relative API URL",
			level: enterprise.LevelEnterprise,
			setup: func(e *dictationEnv) {
				e.config.services[1].APIURL = strings.TrimPrefix(e.config.services[1].APIURL, "http:")
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:  "API URL on the unspecified address",
			level: enterprise.LevelEnterprise,
			setup: func(e *dictationEnv) {
				e.config.services[1].APIURL = strings.Replace(e.fake.URL(), "127.0.0.1", "0.0.0.0", 1)
			},
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "API URL on the cloud metadata address",
			level:      enterprise.LevelEnterprise,
			setup:      func(e *dictationEnv) { e.config.services[1].APIURL = "http://169.254.169.254" },
			wantStatus: http.StatusBadGateway,
		},
		{
			name:  "provider failure",
			level: enterprise.LevelEnterprise,
			setup: func(e *dictationEnv) {
				e.fake.CallStatus = http.StatusInternalServerError
				e.fake.CallErrorMessage = upstreamBodySentinel
			},
			wantStatus: http.StatusBadGateway,
			wantCalls:  1,
		},
		{
			name:        "invalid SDP answer and the call is hung up",
			level:       enterprise.LevelEnterprise,
			setup:       func(e *dictationEnv) { e.fake.AnswerBody = "<html>" + upstreamBodySentinel + "</html>" },
			wantStatus:  http.StatusBadGateway,
			wantCalls:   1,
			wantHangups: 1,
		},
		{
			name:  "invalid SDP answer and an unconfirmed hangup keeps the slot",
			level: enterprise.LevelEnterprise,
			setup: func(e *dictationEnv) {
				e.fake.AnswerBody = "<html>" + upstreamBodySentinel + "</html>"
				e.fake.HangupStatus = http.StatusInternalServerError
			},
			wantStatus:  http.StatusBadGateway,
			wantCalls:   1,
			wantHangups: 0,
			after: func(t *testing.T, e *dictationEnv) {
				e.fake.AnswerBody = ""
				e.fake.HangupStatus = 0
				e.startSession(t)
				assert.Equal(t, http.StatusTooManyRequests, e.postSession(offerBody(testOffer)).Code, "a created call that may still run keeps its slot")
			},
		},
		{
			name:       "API URL on the Alibaba metadata address",
			level:      enterprise.LevelEnterprise,
			setup:      func(e *dictationEnv) { e.config.services[1].APIURL = "http://100.100.100.200" },
			wantStatus: http.StatusBadGateway,
		},
		{
			name:        "sideband attach fails and the call is hung up",
			level:       enterprise.LevelEnterprise,
			setup:       func(e *dictationEnv) { e.fake.AttachStatus = http.StatusForbidden },
			wantStatus:  http.StatusBadGateway,
			wantCalls:   1,
			wantHangups: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupDictationEnv(t, tt.level)
			defer e.Cleanup(t)
			if tt.setup != nil {
				tt.setup(e)
			}
			body := tt.body
			if body == "" {
				body = offerBody(testOffer)
			}

			recorder := e.postSession(body)

			require.Equal(t, tt.wantStatus, recorder.Code, recorder.Body.String())
			assert.Equal(t, tt.wantCalls, e.fake.CallCount())
			assert.Len(t, e.fake.Hangups(), tt.wantHangups)
			if !tt.wantLicenseE {
				assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			}
			for _, secret := range []string{dictationAPIKey, upstreamBodySentinel, urlCredentialSentinel} {
				assert.NotContains(t, recorder.Body.String(), secret)
			}
			if tt.wantLicenseE {
				requireLicenseDenied(t, recorder.Result(), enterprise.CapDictation)
			}
			if tt.after != nil {
				tt.after(t, e)
			}
		})
	}
}

func withoutCall(calls []*mock.Call, method string, arg any) []*mock.Call {
	kept := calls[:0]
	for _, c := range calls {
		if c.Method == method && len(c.Arguments) > 0 && c.Arguments[0] == arg {
			continue
		}
		kept = append(kept, c)
	}
	return kept
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
			e := setupDictationEnv(t, enterprise.LevelEnterprise)
			defer e.Cleanup(t)
			e.config.dictation.Model = tt.model

			recorder := e.postSession(offerBody(testOffer))
			require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

			var body map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			assert.ElementsMatch(t, []string{"sdp", "sessionId", "provider", "model", "turnDetection", "maxDurationSeconds"}, keysOf(body))
			assert.Equal(t, llmtest.FakeAnswerSDP, body["sdp"])
			assert.True(t, model.IsValidId(body["sessionId"].(string)))
			assert.NotEqual(t, e.fake.LastCallID(), body["sessionId"], "the provider call ID stays on the server")
			assert.Equal(t, llm.ServiceTypeOpenAI, body["provider"])
			assert.Equal(t, tt.model, body["model"])
			assert.Equal(t, tt.wantTurnDetection, body["turnDetection"])
			assert.Equal(t, float64(600), body["maxDurationSeconds"])

			assert.Equal(t, []string{testOffer}, e.fake.Offers())
			assert.Equal(t, []string{"Bearer " + dictationAPIKey}, e.fake.AuthHeaders())
			var session map[string]any
			require.NoError(t, json.Unmarshal(e.fake.Sessions()[0], &session))
			assert.Equal(t, "transcription", session["type"])
			input := session["audio"].(map[string]any)["input"].(map[string]any)
			assert.Equal(t, tt.model, input["transcription"].(map[string]any)["model"])
			require.Contains(t, input, "turn_detection")
			assert.Equal(t, tt.wantSent, input["turn_detection"])
			assert.True(t, e.fake.WaitAttached(e.fake.LastCallID(), 5*time.Second), "the sideband is attached before the answer is returned")
		})
	}
}

func TestDictationSessionClosedOnPolicyChange(t *testing.T) {
	prompt := "pretend to be a pirate"
	tests := []struct {
		name  string
		event any
	}{
		{name: "model switched", event: llmtest.SessionUpdated("gpt-4o-transcribe", []string{"en"}, nil, nil)},
		{name: "prompt added", event: llmtest.SessionUpdated("gpt-live-transcribe", []string{"en"}, &prompt, nil)},
		{name: "language changed", event: llmtest.SessionUpdated("gpt-live-transcribe", []string{"fr"}, nil, nil)},
		{name: "turn detection turned on", event: llmtest.SessionUpdated("gpt-live-transcribe", []string{"en"}, nil, map[string]any{"type": "server_vad"})},
		{name: "session type changed", event: map[string]any{"type": "session.updated", "session": map[string]any{"type": "realtime", "model": "gpt-realtime"}}},
		{name: "response generated", event: map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_1"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupDictationEnv(t, enterprise.LevelEnterprise)
			defer e.Cleanup(t)
			sessionID, callID := e.startSession(t)

			// A session.updated that keeps the configuration is fine.
			require.NoError(t, e.fake.SendEvent(callID, llmtest.SessionUpdated("gpt-live-transcribe", []string{"en"}, nil, nil)))
			require.NoError(t, e.fake.SendEvent(callID, tt.event))

			assert.Equal(t, sessionEnd{testUserID, sessionID, dictation.EndReasonPolicy}, e.waitEnd(t))
			assert.Equal(t, []string{callID}, e.fake.Hangups())
		})
	}
}

func TestDictationSessionDurationCap(t *testing.T) {
	e := setupDictationEnv(t, enterprise.LevelEnterprise, dictation.WithMaxSessionDuration(200*time.Millisecond))
	defer e.Cleanup(t)
	sessionID, callID := e.startSession(t)

	assert.Equal(t, sessionEnd{testUserID, sessionID, dictation.EndReasonDurationLimit}, e.waitEnd(t))
	assert.Equal(t, []string{callID}, e.fake.Hangups())
}

func TestDictationSessionsHungUpOnShutdown(t *testing.T) {
	e := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	svc := e.newDictationService(e.api.accessChecker)
	e.api.SetDictationService(svc)
	sessionID, callID := e.startSession(t)

	svc.Shutdown()

	assert.Equal(t, sessionEnd{testUserID, sessionID, dictation.EndReasonShutdown}, e.waitEnd(t))
	assert.Equal(t, []string{callID}, e.fake.Hangups())
}

func TestDictationConcurrentSessionCapAcrossNodes(t *testing.T) {
	e := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	nodeA := e.newDictationService(e.api.accessChecker)
	nodeB := e.newDictationService(e.api.accessChecker)

	e.api.SetDictationService(nodeA)
	_, firstCall := e.startSession(t)
	e.api.SetDictationService(nodeB)
	e.startSession(t)

	recorder := e.postSession(offerBody(testOffer))
	require.Equal(t, http.StatusTooManyRequests, recorder.Code, "the cap holds across nodes")
	assert.Equal(t, 2, e.fake.CallCount(), "a capped request never reaches the provider")

	// The browser closing its connection ends the call and frees the slot,
	// whichever node the next request lands on.
	e.fake.EndCall(firstCall)
	require.Eventually(t, func() bool {
		return e.postSession(offerBody(testOffer)).Code == http.StatusCreated
	}, 5*time.Second, 50*time.Millisecond)
}

func TestDictationSessionHungUpWhenSidebandDrops(t *testing.T) {
	e := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	_, callID := e.startSession(t)

	// A dropped sideband doesn't prove the call ended: the server hangs it
	// up before freeing the slot.
	e.fake.EndCall(callID)
	require.Eventually(t, func() bool { return slices.Contains(e.fake.Hangups(), callID) }, 5*time.Second, 20*time.Millisecond)
	e.startSession(t)
	e.startSession(t)
}

func TestDictationSessionOversizedEventClosesSession(t *testing.T) {
	e := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	sessionID, callID := e.startSession(t)

	// An event too large to read can't be checked: it ends the call as a
	// policy violation instead of silently dropping the sideband.
	huge := llmtest.SessionUpdated("gpt-live-transcribe", []string{"en"}, nil, nil)
	huge["padding"] = strings.Repeat("x", 2<<20)
	// The server stops reading partway, so the write itself may fail.
	_ = e.fake.SendEvent(callID, huge)

	assert.Equal(t, sessionEnd{testUserID, sessionID, dictation.EndReasonPolicy}, e.waitEnd(t))
	assert.Contains(t, e.fake.Hangups(), callID)
}

func TestDictationHangupFailures(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(f *llmtest.FakeRealtime)
		wantAttempts int
		wantFreed    bool
	}{
		{name: "transient failures are retried", setup: func(f *llmtest.FakeRealtime) { f.HangupFailures = 2 }, wantAttempts: 3, wantFreed: true},
		{name: "a call already gone counts as ended", setup: func(f *llmtest.FakeRealtime) { f.HangupStatus = http.StatusNotFound }, wantAttempts: 1, wantFreed: true},
		{name: "a hangup that keeps failing keeps the slot", setup: func(f *llmtest.FakeRealtime) { f.HangupStatus = http.StatusInternalServerError }, wantAttempts: 3},
		{name: "a rejected hangup is not retried and keeps the slot", setup: func(f *llmtest.FakeRealtime) { f.HangupStatus = http.StatusUnauthorized }, wantAttempts: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupDictationEnv(t, enterprise.LevelEnterprise)
			defer e.Cleanup(t)
			_, callID := e.startSession(t)
			tt.setup(e.fake)

			require.NoError(t, e.fake.SendEvent(callID, llmtest.SessionUpdated("gpt-4o-transcribe", nil, nil, nil)))
			e.waitEnd(t)

			assert.Len(t, e.fake.HangupAttempts(), tt.wantAttempts)
			e.startSession(t)
			status := e.postSession(offerBody(testOffer)).Code
			if tt.wantFreed {
				assert.Equal(t, http.StatusCreated, status)
			} else {
				assert.Equal(t, http.StatusTooManyRequests, status, "a call that may still run keeps its slot")
			}
		})
	}
}

func TestDictationShutdownWaitsForStartsInProgress(t *testing.T) {
	e := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	svc := e.newDictationService(e.api.accessChecker)
	e.api.SetDictationService(svc)
	e.fake.CallDelay = 300 * time.Millisecond

	result := make(chan int, 1)
	go func() { result <- e.postSession(offerBody(testOffer)).Code }()
	require.Eventually(t, func() bool { return e.fake.CallCount() == 1 }, 5*time.Second, 5*time.Millisecond)

	svc.Shutdown()

	// Shutdown returned only after the start finished and hung up its call.
	assert.Equal(t, []string{e.fake.LastCallID()}, e.fake.Hangups())
	assert.Equal(t, http.StatusServiceUnavailable, <-result)
}

func TestCreateDictationSessionClientGone(t *testing.T) {
	tests := []struct {
		name      string
		cancelAt  func(e *dictationEnv) // waits for the moment to disconnect
		wantCalls int
	}{
		{name: "before the request starts", wantCalls: 0},
		{
			name: "while the call is being created",
			cancelAt: func(e *dictationEnv) {
				e.fake.CallDelay = 200 * time.Millisecond
			},
			wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupDictationEnv(t, enterprise.LevelEnterprise)
			defer e.Cleanup(t)

			ctx, cancel := context.WithCancel(context.Background())
			if tt.cancelAt == nil {
				cancel()
			} else {
				tt.cancelAt(e)
				go func() {
					for e.fake.CallCount() == 0 {
						time.Sleep(5 * time.Millisecond)
					}
					cancel()
				}()
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/dictation/session", strings.NewReader(offerBody(testOffer)))
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			cancel()

			assert.NotContains(t, recorder.Body.String(), "v=0", "no answer is returned")
			require.Equal(t, tt.wantCalls, e.fake.CallCount())
			if tt.wantCalls > 0 {
				assert.Contains(t, e.fake.Hangups(), e.fake.LastCallID(), "the abandoned call is hung up")
			}
			e.fake.CallDelay = 0
			// Its slot is free: two more sessions fit.
			e.startSession(t)
			e.startSession(t)
		})
	}
}

func TestEndDictationSessionCoalescesAndStopsLocalWatcher(t *testing.T) {
	e := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	// A gateway that answers the hangup but leaves the sideband open.
	e.fake.KeepSidebandOnHangup = true
	e.fake.HangupDelay = 100 * time.Millisecond
	sessionID, callID := e.startSession(t)

	const concurrent = 5
	statuses := make(chan int, concurrent)
	for range concurrent {
		go func() {
			req := httptest.NewRequest(http.MethodDelete, "/dictation/session/"+sessionID, nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			statuses <- recorder.Code
		}()
	}
	for range concurrent {
		assert.Equal(t, http.StatusNoContent, <-statuses)
	}
	assert.Len(t, e.fake.HangupAttempts(), 1, "concurrent ends share one hangup")
	require.Eventually(t, func() bool { return e.fake.SidebandClosed(callID) }, 5*time.Second, 10*time.Millisecond, "this node's watcher stops without waiting for the provider")
	select {
	case end := <-e.ended:
		assert.Failf(t, "a user-ended session sends no end notification", "%+v", end)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEndDictationSessionLimitsAndErrors(t *testing.T) {
	t.Run("hangup failure is a 502 and keeps the slot", func(t *testing.T) {
		e := setupDictationEnv(t, enterprise.LevelEnterprise)
		defer e.Cleanup(t)
		sessionID, _ := e.startSession(t)
		e.fake.HangupStatus = http.StatusInternalServerError

		req := httptest.NewRequest(http.MethodDelete, "/dictation/session/"+sessionID, nil)
		req.Header.Set("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		assert.Equal(t, http.StatusBadGateway, recorder.Code)

		e.fake.HangupStatus = 0
		e.startSession(t)
		assert.Equal(t, http.StatusTooManyRequests, e.postSession(offerBody(testOffer)).Code, "the unconfirmed call keeps its slot")
	})

	t.Run("ends are rate limited per user", func(t *testing.T) {
		e := setupDictationEnv(t, enterprise.LevelEnterprise)
		defer e.Cleanup(t)
		last := 0
		for range 31 {
			req := httptest.NewRequest(http.MethodDelete, "/dictation/session/"+model.NewId(), nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			last = recorder.Code
		}
		assert.Equal(t, http.StatusTooManyRequests, last)
	})
}

func TestEndDictationSession(t *testing.T) {
	const otherUserID = "othe12345678901234567890ab"
	tests := []struct {
		name        string
		userID      string
		sessionID   func(own string) string
		repeat      bool
		wantStatus  int
		wantHangup  bool
		wantFreeing bool
	}{
		{name: "own session", userID: testUserID, sessionID: func(own string) string { return own }, wantStatus: http.StatusNoContent, wantHangup: true, wantFreeing: true},
		{name: "own session twice", userID: testUserID, sessionID: func(own string) string { return own }, repeat: true, wantStatus: http.StatusNoContent, wantHangup: true, wantFreeing: true},
		{name: "another user's session", userID: otherUserID, sessionID: func(own string) string { return own }, wantStatus: http.StatusNotFound},
		{name: "unknown session", userID: testUserID, sessionID: func(string) string { return model.NewId() }, wantStatus: http.StatusNotFound},
		{name: "malformed session ID", userID: testUserID, sessionID: func(string) string { return "not-an-id" }, wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupDictationEnv(t, enterprise.LevelEnterprise)
			defer e.Cleanup(t)
			e.mockAPI.On("GetUser", otherUserID).Return(&model.User{Id: otherUserID, Roles: model.SystemUserRoleId}, nil).Maybe()
			// The session is watched by node A; the end request lands on node B.
			nodeA := e.newDictationService(e.api.accessChecker)
			e.api.SetDictationService(nodeA)
			own, callID := e.startSession(t)
			e.startSession(t)
			e.api.SetDictationService(e.newDictationService(e.api.accessChecker))
			records := e.CaptureAuditRecords()

			deleteSession := func() int {
				req := httptest.NewRequest(http.MethodDelete, "/dictation/session/"+tt.sessionID(own), nil)
				req.Header.Set("Mattermost-User-Id", tt.userID)
				recorder := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, recorder, req)
				return recorder.Code
			}
			status := deleteSession()
			if tt.repeat {
				status = deleteSession()
			}

			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantHangup, slices.Contains(e.fake.Hangups(), callID))
			if tt.wantFreeing {
				e.startSession(t)
			} else {
				assert.Equal(t, http.StatusTooManyRequests, e.postSession(offerBody(testOffer)).Code, "the slot stays taken")
			}

			require.NotEmpty(t, *records)
			rec := (*records)[0]
			assert.Equal(t, AuditEventEndDictationSession, rec.EventName)
			if tt.wantStatus == http.StatusNoContent {
				assert.Equal(t, own, rec.EventData.Parameters[audit.KeyDictationSessionID])
				assert.Equal(t, dictationServiceID, rec.EventData.Parameters[audit.KeyServiceID])
			}
		})
	}
}

func TestCreateDictationSessionAuditAndLogsCarryNoContent(t *testing.T) {
	previous := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})

	e := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	e.config.dictation.Model = "gpt-live-transcribe"
	e.config.dictation.Language = "en"
	records := e.CaptureAuditRecords()

	_, callID := e.startSession(t)
	require.NoError(t, e.fake.SendEvent(callID, map[string]any{
		"type":    "conversation.item.input_audio_transcription.delta",
		"item_id": "item_1",
		"delta":   transcriptSentinel,
	}))
	require.NoError(t, e.fake.SendEvent(callID, map[string]any{
		"type":       "conversation.item.input_audio_transcription.completed",
		"item_id":    "item_1",
		"transcript": transcriptSentinel,
	}))
	prompt := transcriptSentinel
	require.NoError(t, e.fake.SendEvent(callID, llmtest.SessionUpdated("gpt-live-transcribe", []string{"en"}, &prompt, nil)))
	e.waitEnd(t)

	require.Len(t, *records, 1)
	rec := (*records)[0]
	assert.Equal(t, AuditEventCreateDictationSession, rec.EventName)
	assert.Equal(t, model.AuditStatusSuccess, rec.Status)
	assert.Equal(t, testUserID, rec.Actor.UserId)
	assert.Equal(t, dictationServiceID, rec.EventData.Parameters[audit.KeyServiceID])
	assert.True(t, model.IsValidId(fmt.Sprint(rec.EventData.Parameters[audit.KeyDictationSessionID])), "the session ID lets auditors pair a start with its end")

	raw, err := json.Marshal(rec)
	require.NoError(t, err)
	for _, content := range []string{transcriptSentinel, testOffer, llmtest.FakeAnswerSDP, dictationAPIKey, callID, "gpt-live-transcribe"} {
		assert.NotContains(t, string(raw), content)
	}

	for _, call := range e.mockAPI.Calls {
		assert.NotContains(t, fmt.Sprint(call.Arguments...), transcriptSentinel, "logged by %s", call.Method)
	}
	for _, span := range exporter.GetSpans() {
		spanJSON, err := json.Marshal(span)
		require.NoError(t, err)
		assert.NotContains(t, string(spanJSON), transcriptSentinel, "span %q", span.Name)
		assert.NotContains(t, string(spanJSON), dictationAPIKey, "span %q", span.Name)
	}
}

func TestCreateDictationSessionAuditUsesResolvedService(t *testing.T) {
	e := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	records := e.CaptureAuditRecords()
	// Disabled: the request never resolves a service, so none is recorded.
	e.config.dictation.Enabled = false

	require.Equal(t, http.StatusNotFound, e.postSession(offerBody(testOffer)).Code)
	require.Len(t, *records, 1)
	assert.NotContains(t, (*records)[0].EventData.Parameters, audit.KeyServiceID)
}

func TestGetDictationAvailability(t *testing.T) {
	tests := []struct {
		name          string
		level         enterprise.Level
		setup         func(e *dictationEnv)
		wantEnabled   bool
		wantAvailable bool
		wantReason    string
	}{
		{name: "available", level: enterprise.LevelEnterprise, wantEnabled: true, wantAvailable: true},
		{
			name:       "disabled",
			level:      enterprise.LevelEnterprise,
			setup:      func(e *dictationEnv) { e.config.dictation.Enabled = false },
			wantReason: dictation.ReasonDisabled,
		},
		{name: "professional", level: enterprise.LevelProfessional, wantEnabled: true, wantReason: dictation.ReasonLicense},
		{
			name:        "misconfigured",
			level:       enterprise.LevelEnterprise,
			setup:       func(e *dictationEnv) { e.config.dictation.ServiceID = "gone" },
			wantEnabled: true,
			wantReason:  dictation.ReasonUnavailable,
		},
		{
			name:        "access denied",
			level:       enterprise.LevelEnterprise,
			setup:       func(e *dictationEnv) { e.api.SetDictationService(e.newDictationService(denyingAccess())) },
			wantEnabled: true,
			wantReason:  dictation.ReasonUnavailable,
		},
		{
			name:  "guest",
			level: enterprise.LevelEnterprise,
			setup: func(e *dictationEnv) {
				e.mockAPI.ExpectedCalls = withoutCall(e.mockAPI.ExpectedCalls, "GetUser", testUserID)
				e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Roles: model.SystemGuestRoleId}, nil)
			},
			wantEnabled: true,
			wantReason:  dictation.ReasonUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupDictationEnv(t, tt.level)
			defer e.Cleanup(t)
			if tt.setup != nil {
				tt.setup(e)
			}

			req := httptest.NewRequest(http.MethodGet, "/dictation/availability", nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

			var body map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			assert.Equal(t, tt.wantEnabled, body["enabled"])
			assert.Equal(t, tt.wantAvailable, body["available"])
			assert.Equal(t, "webrtc", body["transport"])
			if tt.wantReason == "" {
				assert.NotContains(t, body, "reason")
				assert.Equal(t, "gpt-live-transcribe", body["model"])
			} else {
				assert.Equal(t, tt.wantReason, body["reason"])
				assert.Empty(t, body["model"])
			}
			assert.Zero(t, e.fake.CallCount(), "availability never creates a call")
		})
	}
}

func TestDictationAfterLicenseDowngrade(t *testing.T) {
	e := setupDictationEnv(t, enterprise.LevelEnterprise)
	defer e.Cleanup(t)
	e.startSession(t)

	e.OverrideLicense(enterprisetest.LicenseFor(enterprise.LevelProfessional))
	e.api.dictationService.Reconcile()

	req := httptest.NewRequest(http.MethodGet, "/dictation/availability", nil)
	req.Header.Set("Mattermost-User-Id", testUserID)
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, req)
	var avail dictation.Availability
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &avail))
	assert.Equal(t, dictation.ReasonLicense, avail.Reason)

	requireLicenseDenied(t, e.postSession(offerBody(testOffer)).Result(), enterprise.CapDictation)
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
