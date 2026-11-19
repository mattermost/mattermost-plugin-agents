// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
)

const testRealtimeAPIKey = "sk-realtime-test-key-0123456789"

func newTestCallClient(t *testing.T, fake *llmtest.FakeRealtime) *RealtimeCallClient {
	t.Helper()
	c, err := NewRealtimeCallClient(llm.ServiceConfig{ID: "svc", Type: llm.ServiceTypeOpenAI, APIKey: testRealtimeAPIKey, APIURL: fake.URL() + "/v1"})
	require.NoError(t, err)
	return c
}

func postedInput(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var session map[string]any
	require.NoError(t, json.Unmarshal(raw, &session))
	assert.Equal(t, "transcription", session["type"])
	input, ok := session["audio"].(map[string]any)["input"].(map[string]any)
	require.True(t, ok)
	return input
}

func TestCreateTranscriptionCallSession(t *testing.T) {
	tests := []struct {
		model             string
		language          string
		wantLanguage      any // nil: absent
		wantLanguages     any
		wantTurnDetection any
		wantTurnMode      string
	}{
		{model: "gpt-live-transcribe", language: "en", wantLanguages: []any{"en"}, wantTurnMode: TurnDetectionNone},
		{model: "gpt-live-transcribe-2026-07-28", language: "fr", wantLanguages: []any{"fr"}, wantTurnMode: TurnDetectionNone},
		{model: "gpt-transcribe", language: "de", wantLanguages: []any{"de"}, wantTurnDetection: map[string]any{"type": "server_vad"}, wantTurnMode: TurnDetectionServerVAD},
		{model: "gpt-realtime-whisper", language: "en", wantLanguage: "en", wantTurnMode: TurnDetectionNone},
		{model: "gpt-4o-transcribe", language: "en", wantLanguage: "en", wantTurnDetection: map[string]any{"type": "server_vad"}, wantTurnMode: TurnDetectionServerVAD},
		{model: "gpt-live-transcribe", wantTurnMode: TurnDetectionNone},
	}
	for _, tt := range tests {
		t.Run(tt.model+"/"+tt.language, func(t *testing.T) {
			fake := llmtest.NewFakeRealtime()
			defer fake.Close()
			c := newTestCallClient(t, fake)

			call, err := c.CreateTranscriptionCall(context.Background(), "v=0\r\noffer\r\n", TranscriptionSessionRequest{Model: tt.model, Language: tt.language})
			require.NoError(t, err)
			assert.Equal(t, fake.LastCallID(), call.CallID)
			assert.Equal(t, llmtest.FakeAnswerSDP, call.AnswerSDP)
			assert.Equal(t, tt.wantTurnMode, call.TurnDetection)
			assert.Equal(t, []string{"v=0\r\noffer\r\n"}, fake.Offers())
			assert.Equal(t, []string{"Bearer " + testRealtimeAPIKey}, fake.AuthHeaders())

			input := postedInput(t, fake.Sessions()[0])
			transcription := input["transcription"].(map[string]any)
			assert.Equal(t, tt.model, transcription["model"])
			for field, want := range map[string]any{"language": tt.wantLanguage, "languages": tt.wantLanguages} {
				if want == nil {
					assert.NotContains(t, transcription, field)
				} else {
					assert.Equal(t, want, transcription[field], field)
				}
			}
			require.Contains(t, input, "turn_detection", "turn_detection is sent explicitly, as null when off")
			assert.Equal(t, tt.wantTurnDetection, input["turn_detection"])
		})
	}
}

func TestCreateTranscriptionCallFailsClosed(t *testing.T) {
	const upstreamSentinel = "UPSTREAM-BODY-SENTINEL"
	tests := []struct {
		name       string
		setup      func(f *llmtest.FakeRealtime)
		offer      string
		wantHangup bool
	}{
		{name: "provider error", setup: func(f *llmtest.FakeRealtime) {
			f.CallStatus = http.StatusInternalServerError
			f.CallErrorMessage = upstreamSentinel + " " + testRealtimeAPIKey
		}, offer: "v=0\r\n"},
		{name: "no call ID", setup: func(f *llmtest.FakeRealtime) { f.OmitLocation = true }, offer: "v=0\r\n"},
		{name: "not an SDP answer", setup: func(f *llmtest.FakeRealtime) { f.AnswerBody = "<html>" + upstreamSentinel + "</html>" }, offer: "v=0\r\n", wantHangup: true},
		{name: "empty offer", offer: ""},
		{name: "oversized offer", offer: string(make([]byte, MaxSDPBytes+1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := setupTracerProvider(t)
			fake := llmtest.NewFakeRealtime()
			defer fake.Close()
			if tt.setup != nil {
				tt.setup(fake)
			}
			c := newTestCallClient(t, fake)

			call, err := c.CreateTranscriptionCall(context.Background(), tt.offer, TranscriptionSessionRequest{Model: "gpt-live-transcribe"})
			require.Error(t, err)
			assert.Nil(t, call)
			assert.NotContains(t, err.Error(), testRealtimeAPIKey)
			if tt.wantHangup {
				assert.Equal(t, []string{fake.LastCallID()}, fake.Hangups(), "a created call is hung up on any later failure")
			} else {
				assert.Empty(t, fake.Hangups())
			}

			for _, span := range exporter.GetSpans() {
				if span.Name == "dictation.create_call" {
					assert.Equal(t, codes.Error, span.Status.Code)
				}
				raw, marshalErr := json.Marshal(span)
				require.NoError(t, marshalErr)
				assert.NotContains(t, string(raw), upstreamSentinel)
				assert.NotContains(t, string(raw), testRealtimeAPIKey)
			}
		})
	}
}

func TestParseRealtimeBaseURL(t *testing.T) {
	tests := []struct {
		raw     string
		wantErr bool
	}{
		{raw: "https://api.openai.com"},
		{raw: "https://proxy.example.com/openai/"},
		{raw: "http://localhost:8080"},
		{raw: "//api.openai.com", wantErr: true},
		{raw: "ftp://api.openai.com", wantErr: true},
		{raw: "ws://api.openai.com", wantErr: true},
		{raw: "file://host/path", wantErr: true},
		{raw: "javascript://api.openai.com/x", wantErr: true},
		{raw: "https://user:secret@api.openai.com", wantErr: true},
		{raw: "https://api.openai.com?key=secret", wantErr: true},
		{raw: "https://api.openai.com#frag", wantErr: true},
		{raw: "api.openai.com", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			_, err := ParseRealtimeBaseURL(tt.raw)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func TestNewRealtimeCallClientRejectsUnusableServices(t *testing.T) {
	tests := []struct {
		name    string
		service llm.ServiceConfig
	}{
		{name: "anthropic", service: llm.ServiceConfig{Type: llm.ServiceTypeAnthropic, APIKey: "k"}},
		{name: "azure", service: llm.ServiceConfig{Type: llm.ServiceTypeAzure, APIKey: "k", APIURL: "https://res.openai.azure.com"}},
		{name: "no key", service: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI}},
		{name: "protocol-relative URL", service: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, APIKey: "k", APIURL: "//api.openai.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewRealtimeCallClient(tt.service)
			assert.Error(t, err)
			assert.Nil(t, c)
		})
	}
}

func TestSessionEventMatches(t *testing.T) {
	prompt := "talk about cats"
	empty := ""
	live := TranscriptionSessionRequest{Model: "gpt-live-transcribe", Language: "en"}
	vad := TranscriptionSessionRequest{Model: "gpt-4o-transcribe"}
	tests := []struct {
		name     string
		expected TranscriptionSessionRequest
		event    any
		want     bool
	}{
		{name: "matching live session", expected: live, event: llmtest.SessionUpdated("gpt-live-transcribe", []string{"en"}, nil, nil), want: true},
		{name: "language reported as language", expected: live, event: map[string]any{"type": "session.updated", "session": map[string]any{"type": "transcription", "audio": map[string]any{"input": map[string]any{"transcription": map[string]any{"model": "gpt-live-transcribe", "language": "en"}}}}}, want: true},
		{name: "empty prompt", expected: live, event: llmtest.SessionUpdated("gpt-live-transcribe", []string{"en"}, &empty, nil), want: true},
		{name: "matching VAD session", expected: vad, event: llmtest.SessionUpdated("gpt-4o-transcribe", nil, nil, map[string]any{"type": "server_vad", "threshold": 0.5}), want: true},
		{name: "unrelated event", expected: live, event: map[string]any{"type": "conversation.item.input_audio_transcription.delta", "delta": "hi"}, want: true},
		{name: "model switched", expected: live, event: llmtest.SessionUpdated("gpt-4o-transcribe", []string{"en"}, nil, nil)},
		{name: "prompt added", expected: live, event: llmtest.SessionUpdated("gpt-live-transcribe", []string{"en"}, &prompt, nil)},
		{name: "language changed", expected: live, event: llmtest.SessionUpdated("gpt-live-transcribe", []string{"fr"}, nil, nil)},
		{name: "language removed", expected: live, event: llmtest.SessionUpdated("gpt-live-transcribe", nil, nil, nil)},
		{name: "turn detection turned on", expected: live, event: llmtest.SessionUpdated("gpt-live-transcribe", []string{"en"}, nil, map[string]any{"type": "server_vad"})},
		{name: "turn detection turned off", expected: vad, event: llmtest.SessionUpdated("gpt-4o-transcribe", nil, nil, nil)},
		{name: "keywords added", expected: live, event: map[string]any{"type": "session.updated", "session": map[string]any{"type": "transcription", "audio": map[string]any{"input": map[string]any{"transcription": map[string]any{"model": "gpt-live-transcribe", "languages": []string{"en"}, "keywords": []string{"x"}}}}}}},
		{name: "session type changed", expected: live, event: map[string]any{"type": "session.updated", "session": map[string]any{"type": "realtime", "model": "gpt-realtime"}}},
		{name: "unreadable event", expected: live, event: "not an object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.event)
			require.NoError(t, err)
			assert.Equal(t, tt.want, sessionEventMatches(raw, tt.expected))
		})
	}
}

func TestSidebandRunAndHangup(t *testing.T) {
	req := TranscriptionSessionRequest{Model: "gpt-live-transcribe", Language: "en"}
	tests := []struct {
		name    string
		act     func(f *llmtest.FakeRealtime, callID string, cancel context.CancelFunc)
		wantErr error
	}{
		{name: "session changed", act: func(f *llmtest.FakeRealtime, callID string, _ context.CancelFunc) {
			_ = f.SendEvent(callID, llmtest.SessionUpdated("gpt-live-transcribe", []string{"en"}, nil, nil))
			_ = f.SendEvent(callID, llmtest.SessionUpdated("gpt-4o-transcribe", nil, nil, nil))
		}, wantErr: ErrSessionChanged},
		{name: "call ended", act: func(f *llmtest.FakeRealtime, callID string, _ context.CancelFunc) { f.EndCall(callID) }, wantErr: ErrSidebandClosed},
		{name: "context ended", act: func(_ *llmtest.FakeRealtime, _ string, cancel context.CancelFunc) { cancel() }, wantErr: context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := llmtest.NewFakeRealtime()
			defer fake.Close()
			c := newTestCallClient(t, fake)
			call, err := c.CreateTranscriptionCall(context.Background(), "v=0\r\n", req)
			require.NoError(t, err)
			sb, err := c.Attach(context.Background(), call.CallID, req)
			require.NoError(t, err)
			require.True(t, fake.WaitAttached(call.CallID, 5*time.Second))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- sb.Run(ctx) }()
			tt.act(fake, call.CallID, cancel)
			select {
			case err := <-done:
				assert.True(t, errors.Is(err, tt.wantErr), "got %v", err)
			case <-time.After(5 * time.Second):
				require.FailNow(t, "sideband did not return")
			}

			require.NoError(t, c.Hangup(context.Background(), call.CallID))
			assert.Equal(t, []string{call.CallID}, fake.Hangups())
		})
	}
}

func TestAttachFailure(t *testing.T) {
	fake := llmtest.NewFakeRealtime()
	defer fake.Close()
	fake.AttachStatus = http.StatusForbidden
	c := newTestCallClient(t, fake)
	call, err := c.CreateTranscriptionCall(context.Background(), "v=0\r\n", TranscriptionSessionRequest{Model: "gpt-live-transcribe"})
	require.NoError(t, err)

	sb, err := c.Attach(context.Background(), call.CallID, TranscriptionSessionRequest{Model: "gpt-live-transcribe"})
	require.Error(t, err)
	assert.Nil(t, sb)
	assert.NotContains(t, err.Error(), testRealtimeAPIKey)
}

type fakeResolver map[string][]net.IP

func (r fakeResolver) LookupIP(_ context.Context, _, host string) ([]net.IP, error) {
	return r[host], nil
}

func TestGuardedDialContext(t *testing.T) {
	resolver := fakeResolver{
		"metadata.internal": {net.ParseIP("169.254.169.254")},
		"mixed.example":     {net.ParseIP("203.0.113.7"), net.ParseIP("169.254.169.254")},
		"mapped.example":    {net.ParseIP("::ffff:169.254.169.254")},
		"zero.example":      {net.ParseIP("0.0.0.0")},
		"v6zero.example":    {net.ParseIP("::")},
		"linklocal6":        {net.ParseIP("fe80::1")},
		"multicast.example": {net.ParseIP("224.0.0.1")},
		"public.example":    {net.ParseIP("203.0.113.7")},
		"private.example":   {net.ParseIP("10.1.2.3")},
		"loopback.example":  {net.ParseIP("127.0.0.1")},
	}
	tests := []struct {
		host        string
		wantBlocked bool
	}{
		{"metadata.internal", true},
		{"mixed.example", true},
		{"mapped.example", true},
		{"zero.example", true},
		{"v6zero.example", true},
		{"linklocal6", true},
		{"multicast.example", true},
		{"public.example", false},
		{"private.example", false},
		{"loopback.example", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			dialed := ""
			dial := guardedDialContext(resolver, func(_ context.Context, _, addr string) (net.Conn, error) {
				dialed = addr
				return nil, errors.New("not dialing in tests")
			})
			_, err := dial(context.Background(), "tcp", net.JoinHostPort(tt.host, "443"))
			require.Error(t, err)
			if tt.wantBlocked {
				assert.Empty(t, dialed, "a blocked address is never dialed")
				return
			}
			// The checked IP is dialed, not the name, so a second lookup
			// can't swap the address.
			assert.Equal(t, net.JoinHostPort(resolver[tt.host][0].String(), "443"), dialed)
		})
	}
}

func TestCallClientsShareOneTransport(t *testing.T) {
	a, err := NewRealtimeCallClient(llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, APIKey: "k"})
	require.NoError(t, err)
	b, err := NewRealtimeCallClient(llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, APIKey: "k2", APIURL: "https://proxy.example.com"})
	require.NoError(t, err)
	assert.Same(t, a.httpClient.Transport, b.httpClient.Transport, "per-call clients must not each hold idle connections")
	assert.Same(t, a.dialer, b.dialer)
}

func TestSidebandOversizedEvent(t *testing.T) {
	fake := llmtest.NewFakeRealtime()
	defer fake.Close()
	c := newTestCallClient(t, fake)
	req := TranscriptionSessionRequest{Model: "gpt-live-transcribe"}
	call, err := c.CreateTranscriptionCall(context.Background(), "v=0\r\n", req)
	require.NoError(t, err)
	sb, err := c.Attach(context.Background(), call.CallID, req)
	require.NoError(t, err)
	require.True(t, fake.WaitAttached(call.CallID, 5*time.Second))

	go func() {
		_ = fake.SendEvent(call.CallID, map[string]string{"type": "session.updated", "padding": strings.Repeat("x", realtimeEventMaxBytes+1)})
	}()
	assert.ErrorIs(t, sb.Run(context.Background()), ErrSessionChanged)
}

func TestHangupTreatsMissingCallAsEnded(t *testing.T) {
	tests := []struct {
		status        int
		wantErr       bool
		wantTransient bool
	}{
		{status: http.StatusOK},
		{status: http.StatusNotFound},
		{status: http.StatusServiceUnavailable, wantErr: true, wantTransient: true},
		{status: http.StatusTooManyRequests, wantErr: true, wantTransient: true},
		{status: http.StatusUnauthorized, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			fake := llmtest.NewFakeRealtime()
			defer fake.Close()
			fake.HangupStatus = tt.status
			c := newTestCallClient(t, fake)

			err := c.Hangup(context.Background(), "rtc_test_1")
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.wantTransient, IsTransientRealtimeError(err))
		})
	}
}

func TestTurnDetectionForModel(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"gpt-live-transcribe", TurnDetectionNone},
		{"gpt-live-transcribe-2026-10-01", TurnDetectionNone},
		{"gpt-realtime-whisper", TurnDetectionNone},
		{"gpt-4o-transcribe", TurnDetectionServerVAD},
		{"whisper-1", TurnDetectionServerVAD},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.want, TurnDetectionForModel(tt.model))
		})
	}
}

func TestProviderForServiceType(t *testing.T) {
	tests := []struct {
		serviceType string
		allowed     []string
		wantErr     bool
	}{
		{llm.ServiceTypeOpenAI, TranscriptionServiceTypes, false},
		{llm.ServiceTypeOpenAICompatible, TranscriptionServiceTypes, false},
		{llm.ServiceTypeAzure, TranscriptionServiceTypes, false},
		{llm.ServiceTypeAnthropic, TranscriptionServiceTypes, true},
		{llm.ServiceTypeOpenAI, RealtimeSessionServiceTypes, false},
		{llm.ServiceTypeAzure, RealtimeSessionServiceTypes, true},
	}
	for _, tt := range tests {
		t.Run(tt.serviceType, func(t *testing.T) {
			_, err := ProviderForServiceType(tt.serviceType, tt.allowed)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}
