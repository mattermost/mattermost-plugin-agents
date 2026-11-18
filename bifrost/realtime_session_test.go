// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const testRealtimeAPIKey = "sk-realtime-test-key-0123456789"

type capturedMint struct {
	path   string
	auth   string
	org    string
	body   map[string]any
	called bool
}

func fakeRealtimeProvider(t *testing.T, status int, respBody string) (*httptest.Server, *capturedMint) {
	t.Helper()
	captured := &capturedMint{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.called = true
		captured.path = r.URL.Path
		captured.auth = r.Header.Get("Authorization")
		captured.org = r.Header.Get("OpenAI-Organization")
		raw, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.NoError(t, json.Unmarshal(raw, &captured.body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(server.Close)
	return server, captured
}

func inputAudio(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	session, ok := body["session"].(map[string]any)
	require.True(t, ok, "session must be an object")
	audio, ok := session["audio"].(map[string]any)
	require.True(t, ok, "session.audio must be an object")
	input, ok := audio["input"].(map[string]any)
	require.True(t, ok, "session.audio.input must be an object")
	return input
}

func TestCreateTranscriptionSession(t *testing.T) {
	tests := []struct {
		name              string
		model             string
		language          string
		apiURLSuffix      string
		wantTurnDetection any
		wantTurnMode      string
	}{
		{
			name:              "live model uses client turn detection",
			model:             "gpt-live-transcribe",
			wantTurnDetection: nil,
			wantTurnMode:      TurnDetectionNone,
		},
		{
			name:              "4o model uses server VAD",
			model:             "gpt-4o-transcribe",
			language:          "en",
			apiURLSuffix:      "/v1",
			wantTurnDetection: map[string]any{"type": "server_vad"},
			wantTurnMode:      TurnDetectionServerVAD,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expiresAt := time.Now().Add(time.Minute).Unix()
			server, captured := fakeRealtimeProvider(t, http.StatusOK,
				`{"value":"ek_sentinel_secret","expires_at":`+jsonInt(expiresAt)+`,"session":{"type":"transcription"}}`)

			minter, err := NewRealtimeSessionMinter(llm.ServiceConfig{
				ID:     "svc",
				Type:   llm.ServiceTypeOpenAI,
				APIKey: testRealtimeAPIKey,
				APIURL: server.URL + tt.apiURLSuffix,
				OrgID:  "org-123",
			})
			require.NoError(t, err)
			defer minter.Shutdown()

			session, err := minter.CreateTranscriptionSession(context.Background(), TranscriptionSessionRequest{
				Model:    tt.model,
				Language: tt.language,
			})
			require.NoError(t, err)

			assert.Equal(t, "/v1/realtime/client_secrets", captured.path)
			assert.Equal(t, "Bearer "+testRealtimeAPIKey, captured.auth)
			assert.Equal(t, "org-123", captured.org)

			sessionBody, ok := captured.body["session"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "transcription", sessionBody["type"])
			input := inputAudio(t, captured.body)
			transcription, ok := input["transcription"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tt.model, transcription["model"])
			if tt.language != "" {
				assert.Equal(t, tt.language, transcription["language"])
			} else {
				assert.NotContains(t, transcription, "language")
			}
			require.Contains(t, input, "turn_detection", "turn_detection must be sent explicitly, as null when off")
			assert.Equal(t, tt.wantTurnDetection, input["turn_detection"])
			assert.Equal(t, map[string]any{"anchor": "created_at", "seconds": float64(15)}, captured.body["expires_after"])

			assert.Equal(t, "ek_sentinel_secret", session.ClientSecret)
			assert.Equal(t, expiresAt*1000, session.ExpiresAt)
			assert.Equal(t, server.URL+"/v1/realtime/calls", session.CallsURL)
			assert.Equal(t, llm.ServiceTypeOpenAI, session.Provider)
			assert.Equal(t, tt.model, session.Model)
			assert.Equal(t, tt.wantTurnMode, session.TurnDetection)
		})
	}
}

func TestCreateTranscriptionSessionProviderErrorStaysOutOfSpans(t *testing.T) {
	const upstreamSentinel = "UPSTREAM-BODY-SENTINEL"

	tests := []struct {
		name        string
		serviceType string
		body        string
	}{
		{
			name:        "openai JSON error",
			serviceType: llm.ServiceTypeOpenAI,
			body:        `{"error":{"message":"upstream failure ` + upstreamSentinel + ` for ` + testRealtimeAPIKey + `","type":"server_error"}}`,
		},
		{
			// Bifrost copies a non-JSON Azure error body into Error.Message verbatim.
			name:        "azure raw error body",
			serviceType: llm.ServiceTypeAzure,
			body:        "upstream exploded " + upstreamSentinel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := setupTracerProvider(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			minter, err := NewRealtimeSessionMinter(llm.ServiceConfig{
				ID: "svc", Type: tt.serviceType, APIKey: testRealtimeAPIKey, APIURL: server.URL,
			})
			require.NoError(t, err)
			defer minter.Shutdown()

			session, err := minter.CreateTranscriptionSession(context.Background(), TranscriptionSessionRequest{Model: "gpt-live-transcribe"})
			require.Error(t, err)
			assert.Nil(t, session)
			assert.NotContains(t, err.Error(), testRealtimeAPIKey)

			spans := exporter.GetSpans()
			require.NotEmpty(t, spans)
			var found bool
			for _, span := range spans {
				if span.Name == "dictation.create_session" {
					found = true
					assert.Equal(t, codes.Error, span.Status.Code)
				}
				raw, marshalErr := json.Marshal(span)
				require.NoError(t, marshalErr)
				assert.NotContains(t, string(raw), upstreamSentinel)
				assert.NotContains(t, string(raw), testRealtimeAPIKey)
			}
			assert.True(t, found, "the mint span must be recorded")
		})
	}
}

func TestNewRealtimeSessionMinterRejectsUnusableServices(t *testing.T) {
	tests := []struct {
		name    string
		service llm.ServiceConfig
	}{
		{name: "anthropic", service: llm.ServiceConfig{ID: "svc", Type: llm.ServiceTypeAnthropic, APIKey: "k"}},
		{name: "openai compatible", service: llm.ServiceConfig{ID: "svc", Type: llm.ServiceTypeOpenAICompatible, APIKey: "k", APIURL: "http://localhost:1"}},
		{name: "gemini", service: llm.ServiceConfig{ID: "svc", Type: llm.ServiceTypeGemini, APIKey: "k"}},
		// Bifrost panics minting for an Azure key without an endpoint.
		{name: "azure without endpoint", service: llm.ServiceConfig{ID: "svc", Type: llm.ServiceTypeAzure, APIKey: "k"}},
		{name: "azure with blank endpoint", service: llm.ServiceConfig{ID: "svc", Type: llm.ServiceTypeAzure, APIKey: "k", APIURL: "  "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minter, err := NewRealtimeSessionMinter(tt.service)
			assert.Error(t, err)
			assert.Nil(t, minter)
		})
	}
}

func TestRealtimeCallsURL(t *testing.T) {
	tests := []struct {
		name     string
		provider schemas.ModelProvider
		apiURL   string
		want     string
		wantErr  bool
	}{
		{name: "openai default", provider: schemas.OpenAI, want: "https://api.openai.com/v1/realtime/calls"},
		{name: "openai custom base", provider: schemas.OpenAI, apiURL: "https://proxy.example.com/", want: "https://proxy.example.com/v1/realtime/calls"},
		{name: "azure endpoint", provider: schemas.Azure, apiURL: "https://res.openai.azure.com/", want: "https://res.openai.azure.com/openai/v1/realtime/calls"},
		{name: "azure without endpoint", provider: schemas.Azure, wantErr: true},
		{name: "unsupported provider", provider: schemas.Anthropic, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := realtimeCallsURL(tt.provider, tt.apiURL)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
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
		{"gpt-4o-transcribe", TurnDetectionServerVAD},
		{"gpt-4o-mini-transcribe", TurnDetectionServerVAD},
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
		want        schemas.ModelProvider
		wantErr     bool
	}{
		{llm.ServiceTypeOpenAI, TranscriptionServiceTypes, schemas.OpenAI, false},
		{llm.ServiceTypeOpenAICompatible, TranscriptionServiceTypes, schemas.OpenAI, false},
		{llm.ServiceTypeAzure, TranscriptionServiceTypes, schemas.Azure, false},
		{llm.ServiceTypeAnthropic, TranscriptionServiceTypes, "", true},
		{llm.ServiceTypeOpenAI, RealtimeSessionServiceTypes, schemas.OpenAI, false},
		{llm.ServiceTypeAzure, RealtimeSessionServiceTypes, schemas.Azure, false},
		{llm.ServiceTypeOpenAICompatible, RealtimeSessionServiceTypes, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.serviceType, func(t *testing.T) {
			got, err := ProviderForServiceType(tt.serviceType, tt.allowed)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
