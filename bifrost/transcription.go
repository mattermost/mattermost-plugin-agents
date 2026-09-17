// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/subtitles"
)

// Transcriber implements transcription using the Bifrost gateway.
type Transcriber struct {
	client   *bifrostcore.Bifrost
	provider schemas.ModelProvider
	apiKey   string // used only to redact configured secrets from provider error surfaces
	model    string
	logger   ErrorLogger
}

// TranscriptionConfig holds configuration for creating a Transcriber.
type TranscriptionConfig struct {
	Provider schemas.ModelProvider
	APIKey   string
	APIURL   string
	Model    string // e.g., "whisper-1"

	// Logger receives provider error bodies too unstructured to be carried in
	// the returned error. Optional.
	Logger ErrorLogger
}

// NewTranscriber creates a new Transcriber.
func NewTranscriber(cfg TranscriptionConfig) (*Transcriber, error) {
	account := &providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: cfg.Provider,
			APIKey:   cfg.APIKey,
			APIURL:   cfg.APIURL,
		},
	}

	client, err := newBifrostClient(account, cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Bifrost client for transcription: %w", err)
	}

	model := cfg.Model
	if model == "" {
		model = "whisper-1"
	}

	return &Transcriber{
		client:   client,
		provider: cfg.Provider,
		apiKey:   cfg.APIKey,
		model:    model,
		logger:   cfg.Logger,
	}, nil
}

// Transcribe converts audio to text using Bifrost.
func (t *Transcriber) Transcribe(file io.Reader) (*subtitles.Subtitles, error) {
	bifrostCtx, cancel := schemas.NewBifrostContextWithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Read the file into bytes for the request
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read audio file: %w", err)
	}

	req := &schemas.BifrostTranscriptionRequest{
		Provider: t.provider,
		Model:    t.model,
		Input: &schemas.TranscriptionInput{
			File: data,
		},
		Params: &schemas.TranscriptionParameters{
			ResponseFormat: new("vtt"), // Use VTT format for timed transcription
		},
	}

	resp, bifrostErr := t.client.TranscriptionRequest(bifrostCtx, req)
	if bifrostErr != nil {
		return nil, providerError(t.logger, []string{t.apiKey}, "bifrost transcription error", bifrostErr)
	}

	if resp == nil || resp.Text == "" {
		return nil, fmt.Errorf("no transcription data returned")
	}

	// Parse the VTT response
	timedTranscript, err := subtitles.NewSubtitlesFromVTT(strings.NewReader(resp.Text))
	if err != nil {
		return nil, fmt.Errorf("unable to parse transcription: %w", err)
	}

	return timedTranscript, nil
}

// Shutdown gracefully shuts down the Bifrost client.
func (t *Transcriber) Shutdown() {
	if t.client != nil {
		t.client.Shutdown()
	}
}
