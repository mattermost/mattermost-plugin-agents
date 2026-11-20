// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/net/dns/dnsmessage"

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
		wantCallID bool
	}{
		{name: "provider error", setup: func(f *llmtest.FakeRealtime) {
			f.CallStatus = http.StatusInternalServerError
			f.CallErrorMessage = upstreamSentinel + " " + testRealtimeAPIKey
		}, offer: "v=0\r\n"},
		{name: "no call ID", setup: func(f *llmtest.FakeRealtime) { f.OmitLocation = true }, offer: "v=0\r\n"},
		{name: "not an SDP answer", setup: func(f *llmtest.FakeRealtime) { f.AnswerBody = "<html>" + upstreamSentinel + "</html>" }, offer: "v=0\r\n", wantCallID: true},
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
			// The client never hangs up itself: a created call is reported so
			// the caller hangs it up with retries and keeps the slot on failure.
			assert.Empty(t, fake.Hangups())
			created, ok := errors.AsType[*CreatedCallError](err)
			assert.Equal(t, tt.wantCallID, ok)
			if ok {
				assert.Equal(t, fake.LastCallID(), created.CallID)
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
		{name: "response generated", expected: live, event: map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_1"}}},
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

// fakeDNSResolver answers A and AAAA queries for each host with its listed
// addresses, in order, so tests can resolve a name to several addresses.
func fakeDNSResolver(t *testing.T, hosts map[string][]net.IP) *net.Resolver {
	t.Helper()
	return &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go serveFakeDNS(server, hosts)
			return client, nil
		},
	}
}

// serveFakeDNS speaks DNS over a stream connection: each message is
// prefixed with its two-byte length.
func serveFakeDNS(conn net.Conn, hosts map[string][]net.IP) {
	defer conn.Close()
	for {
		var size [2]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return
		}
		msg := make([]byte, int(size[0])<<8|int(size[1]))
		if _, err := io.ReadFull(conn, msg); err != nil {
			return
		}
		var parser dnsmessage.Parser
		header, err := parser.Start(msg)
		if err != nil {
			return
		}
		question, err := parser.Question()
		if err != nil {
			return
		}
		builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: header.ID, Response: true, Authoritative: true})
		_ = builder.StartQuestions()
		_ = builder.Question(question)
		_ = builder.StartAnswers()
		name := strings.TrimSuffix(question.Name.String(), ".")
		for _, ip := range hosts[name] {
			rh := dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 60}
			if v4 := ip.To4(); v4 != nil && question.Type == dnsmessage.TypeA {
				_ = builder.AResource(rh, dnsmessage.AResource{A: [4]byte(v4)})
			} else if v4 == nil && question.Type == dnsmessage.TypeAAAA {
				_ = builder.AAAAResource(rh, dnsmessage.AAAAResource{AAAA: [16]byte(ip.To16())})
			}
		}
		reply, err := builder.Finish()
		if err != nil {
			return
		}
		if _, err = conn.Write(append([]byte{byte(len(reply) >> 8), byte(len(reply))}, reply...)); err != nil {
			return
		}
	}
}

func TestGuardedDialer(t *testing.T) {
	// The listener is on 127.0.0.2, and nothing listens on its port at
	// 127.0.0.1, so 127.0.0.1 is reachable but refuses the connection.
	listener, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skip("127.0.0.2 is not available:", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)

	hosts := map[string][]net.IP{
		"blocked-then-allowed.test": {net.ParseIP("169.254.169.254"), net.ParseIP("127.0.0.2")},
		"unreachable-first.test":    {net.ParseIP("127.0.0.1"), net.ParseIP("127.0.0.2")},
		"all-blocked.test":          {net.ParseIP("169.254.169.254"), net.ParseIP("0.0.0.0"), net.ParseIP("224.0.0.1")},
		"metadata.test":             {net.ParseIP("169.254.169.254")},
		"mapped-metadata.test":      {net.ParseIP("64:ff9b::a9fe:a9fe")},
		"link-local6.test":          {net.ParseIP("fe80::1")},
		"aws-imds6.test":            {net.ParseIP("fd00:ec2::254")},
		"alibaba-metadata.test":     {net.ParseIP("100.100.100.200")},
		"nat64-alibaba.test":        {net.ParseIP("64:ff9b::6464:64c8")},
		"6to4-alibaba.test":         {net.ParseIP("2002:6464:64c8::")},
	}
	tests := []struct {
		host        string
		wantConnect bool
	}{
		{host: "blocked-then-allowed.test", wantConnect: true},
		{host: "unreachable-first.test", wantConnect: true},
		{host: "all-blocked.test"},
		{host: "metadata.test"},
		{host: "mapped-metadata.test"},
		{host: "link-local6.test"},
		{host: "aws-imds6.test"},
		{host: "alibaba-metadata.test"},
		{host: "nat64-alibaba.test"},
		{host: "6to4-alibaba.test"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			dialer := newGuardedDialer()
			dialer.Resolver = fakeDNSResolver(t, hosts)
			dialer.Timeout = 2 * time.Second

			conn, err := dialer.DialContext(context.Background(), "tcp", net.JoinHostPort(tt.host, port))
			if !tt.wantConnect {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "blocked connection")
				return
			}
			require.NoError(t, err)
			defer conn.Close()
			host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
			assert.Equal(t, "127.0.0.2", host)
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

func TestSidebandKeepsAliveWithPings(t *testing.T) {
	fake := llmtest.NewFakeRealtime()
	defer fake.Close()
	c := newTestCallClient(t, fake)
	req := TranscriptionSessionRequest{Model: "gpt-live-transcribe"}
	call, err := c.CreateTranscriptionCall(context.Background(), "v=0\r\n", req)
	require.NoError(t, err)
	sb, err := c.Attach(context.Background(), call.CallID, req)
	require.NoError(t, err)
	require.True(t, fake.WaitAttached(call.CallID, 5*time.Second))
	assert.Equal(t, sidebandPingInterval, sb.pingInterval)
	sb.pingInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sb.Run(ctx) }()

	// A quiet session still pings, so an idle proxy doesn't drop it.
	require.Eventually(t, func() bool { return fake.Pings(call.CallID) >= 3 }, 5*time.Second, 10*time.Millisecond)
	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)
}

func TestSidebandEndsWhenPongsStop(t *testing.T) {
	fake := llmtest.NewFakeRealtime()
	defer fake.Close()
	fake.IgnorePings = true // a half-open connection: nothing comes back
	c := newTestCallClient(t, fake)
	req := TranscriptionSessionRequest{Model: "gpt-live-transcribe"}
	call, err := c.CreateTranscriptionCall(context.Background(), "v=0\r\n", req)
	require.NoError(t, err)
	sb, err := c.Attach(context.Background(), call.CallID, req)
	require.NoError(t, err)
	require.True(t, fake.WaitAttached(call.CallID, 5*time.Second))
	sb.pingInterval = 20 * time.Millisecond
	sb.pongGrace = 20 * time.Millisecond

	done := make(chan error, 1)
	go func() { done <- sb.Run(context.Background()) }()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, ErrSidebandClosed, "a dead sideband ends Run so the caller hangs up")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Run kept waiting on a dead sideband")
	}
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
