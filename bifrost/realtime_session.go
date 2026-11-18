// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

const (
	// realtimeSessionTTL only bounds how long the browser has to connect
	// (measured at about 1 s); a connected session outlives it, up to the
	// provider's 60 minute maximum. Within the TTL one key can open any
	// number of sessions, so it is kept short.
	realtimeSessionTTL     = 15 * time.Second
	realtimeMintTimeout    = 15 * time.Second
	defaultOpenAIBaseURL   = "https://api.openai.com"
	openAIRealtimeCallPath = "/v1/realtime/calls"
	azureRealtimeCallPath  = "/openai/v1/realtime/calls"
)

// Turn detection modes reported to the browser. With TurnDetectionNone the
// client commits each audio segment itself.
const (
	TurnDetectionNone      = "none"
	TurnDetectionServerVAD = "server_vad"
)

// clientTurnDetectionModels need turn_detection: null, matched by prefix so
// dated snapshots of the same model behave the same.
var clientTurnDetectionModels = []string{"gpt-live-transcribe"}

// TurnDetectionForModel returns the turn detection mode a transcription
// session uses for model.
func TurnDetectionForModel(model string) string {
	for _, prefix := range clientTurnDetectionModels {
		if strings.HasPrefix(model, prefix) {
			return TurnDetectionNone
		}
	}
	return TurnDetectionServerVAD
}

// TranscriptionSessionRequest configures a transcription-only realtime session.
type TranscriptionSessionRequest struct {
	// Model is an OpenAI model name or an Azure deployment name.
	Model string
	// Language is an optional ISO-639-1 hint.
	Language string
}

// TranscriptionSession is a minted browser session. ClientSecret is a
// credential: never log it or put it in spans, audit records or errors.
type TranscriptionSession struct {
	ClientSecret  string `json:"clientSecret"`
	ExpiresAt     int64  `json:"expiresAt"` // Unix milliseconds
	CallsURL      string `json:"callsURL"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	TurnDetection string `json:"turnDetection"`
}

// RealtimeSessionMinter mints ephemeral realtime client secrets for one
// service with the service's own credentials and network settings.
type RealtimeSessionMinter struct {
	client      *bifrostcore.Bifrost
	account     *providerAccount
	provider    schemas.ModelProvider
	serviceType string
	callsURL    string
}

// NewRealtimeSessionMinter builds a minter for service, which must be one of
// RealtimeSessionServiceTypes.
func NewRealtimeSessionMinter(service llm.ServiceConfig) (*RealtimeSessionMinter, error) {
	provider, err := ProviderForServiceType(service.Type, RealtimeSessionServiceTypes)
	if err != nil {
		return nil, err
	}

	settings := providerSettingsFromService(provider, service)
	callsURL, err := realtimeCallsURL(provider, settings.APIURL)
	if err != nil {
		return nil, err
	}

	account := &providerAccount{ProviderSettings: settings}
	client, err := newBifrostClient(account, service.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Bifrost client for realtime sessions: %w", err)
	}

	return &RealtimeSessionMinter{
		client:      client,
		account:     account,
		provider:    provider,
		serviceType: service.Type,
		callsURL:    callsURL,
	}, nil
}

// realtimeCallsURL is sent to browsers, so a base URL carrying credentials,
// a query or a fragment is refused rather than passed on.
func realtimeCallsURL(provider schemas.ModelProvider, apiURL string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
			return "", errors.New("the service API URL must hold only a scheme, host and path for realtime sessions")
		}
	}
	switch provider {
	case schemas.OpenAI:
		if base == "" {
			base = defaultOpenAIBaseURL
		}
		return base + openAIRealtimeCallPath, nil
	case schemas.Azure:
		// Bifrost dereferences a nil Azure endpoint config and panics, so an
		// Azure service without an endpoint must never reach it.
		if base == "" {
			return "", errors.New("an Azure service needs an endpoint URL for realtime sessions")
		}
		return base + azureRealtimeCallPath, nil
	default:
		return "", fmt.Errorf("provider %s does not support realtime sessions", provider)
	}
}

// Shutdown releases the Bifrost client.
func (m *RealtimeSessionMinter) Shutdown() {
	m.client.Shutdown()
}

type realtimeClientSecretRequest struct {
	ExpiresAfter realtimeExpiresAfter         `json:"expires_after"`
	Session      realtimeTranscriptionSession `json:"session"`
}

type realtimeExpiresAfter struct {
	Anchor  string `json:"anchor"`
	Seconds int    `json:"seconds"`
}

type realtimeTranscriptionSession struct {
	Type  string        `json:"type"`
	Audio realtimeAudio `json:"audio"`
}

type realtimeAudio struct {
	Input realtimeAudioInput `json:"input"`
}

type realtimeAudioInput struct {
	Transcription  realtimeTranscription  `json:"transcription"`
	NoiseReduction realtimeNoiseReduction `json:"noise_reduction"`
	// TurnDetection is sent as null when the client segments audio itself.
	TurnDetection *realtimeTurnDetection `json:"turn_detection"`
}

type realtimeTranscription struct {
	Model    string `json:"model"`
	Language string `json:"language,omitempty"`
}

type realtimeNoiseReduction struct {
	Type string `json:"type"`
}

type realtimeTurnDetection struct {
	Type string `json:"type"`
}

type realtimeClientSecretResponse struct {
	Value     string `json:"value"`
	ExpiresAt int64  `json:"expires_at"`
}

func buildTranscriptionSessionBody(req TranscriptionSessionRequest) ([]byte, string, error) {
	turnDetection := TurnDetectionForModel(req.Model)
	input := realtimeAudioInput{
		Transcription:  realtimeTranscription(req),
		NoiseReduction: realtimeNoiseReduction{Type: "near_field"},
	}
	if turnDetection == TurnDetectionServerVAD {
		input.TurnDetection = &realtimeTurnDetection{Type: TurnDetectionServerVAD}
	}
	body, err := json.Marshal(realtimeClientSecretRequest{
		ExpiresAfter: realtimeExpiresAfter{Anchor: "created_at", Seconds: int(realtimeSessionTTL.Seconds())},
		Session: realtimeTranscriptionSession{
			Type:  "transcription",
			Audio: realtimeAudio{Input: input},
		},
	})
	return body, turnDetection, err
}

// CreateTranscriptionSession mints a transcription-only session. The whole
// session configuration is built here; nothing comes from the browser. The
// provider locks the session type, but a modified client holding the key can
// still switch to another, possibly pricier, transcription model (and change
// VAD, language and prompt) with session.update, so Model is what was
// configured, not necessarily what is billed.
func (m *RealtimeSessionMinter) CreateTranscriptionSession(ctx context.Context, req TranscriptionSessionRequest) (*TranscriptionSession, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "dictation.create_session", trace.WithAttributes(
		telemetry.LLMProvider.String(m.serviceType),
		telemetry.LLMModel.String(req.Model),
	))
	defer span.End()

	fail := func(err error) (*TranscriptionSession, error) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if strings.TrimSpace(req.Model) == "" {
		return fail(errors.New("a transcription model is required"))
	}

	sessionProvider, ok := m.client.GetProviderByKey(m.provider).(schemas.RealtimeSessionProvider)
	if !ok {
		return fail(fmt.Errorf("provider %s does not support realtime sessions", m.provider))
	}

	bifrostCtx, cancel := schemas.NewBifrostContextWithTimeout(ctx, realtimeMintTimeout)
	defer cancel()
	trackAttemptErrors(bifrostCtx)

	keys, err := m.account.GetKeysForProvider(bifrostCtx, m.provider)
	if err != nil || len(keys) == 0 {
		return fail(errors.New("no credentials configured for the realtime session provider"))
	}

	body, turnDetection, err := buildTranscriptionSessionBody(req)
	if err != nil {
		return fail(fmt.Errorf("failed to encode realtime session request: %w", err))
	}

	resp, bifrostErr := sessionProvider.CreateRealtimeClientSecret(bifrostCtx, keys[0], body)
	if bifrostErr != nil {
		// Error.Message can carry the raw upstream body (verbatim for Azure),
		// so the span gets only the structured fields; the returned error is
		// for the server log.
		span.SetAttributes(describeBifrostError(upstreamError(bifrostCtx, bifrostErr)).attributes()...)
		span.SetStatus(codes.Error, "realtime session provider error")
		return nil, providerError(bifrostCtx, nil, "realtime session error", bifrostErr, m.account.APIKey)
	}
	if resp == nil {
		return fail(errors.New("realtime session provider returned no response"))
	}

	var secret realtimeClientSecretResponse
	if err := json.Unmarshal(resp.Body, &secret); err != nil {
		return fail(errors.New("realtime session provider returned an unreadable response"))
	}
	if secret.Value == "" {
		return fail(errors.New("realtime session provider returned no client secret"))
	}

	// An upstream expiry later than the requested TTL must not widen the
	// window in which the key can open sessions.
	expiresAt := time.Now().Add(realtimeSessionTTL)
	if upstream := time.Unix(secret.ExpiresAt, 0); secret.ExpiresAt > 0 && upstream.Before(expiresAt) {
		expiresAt = upstream
	}

	return &TranscriptionSession{
		ClientSecret:  secret.Value,
		ExpiresAt:     expiresAt.UnixMilli(),
		CallsURL:      m.callsURL,
		Provider:      m.serviceType,
		Model:         req.Model,
		TurnDetection: turnDetection,
	}, nil
}
