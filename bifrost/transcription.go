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

	"github.com/mattermost/mattermost-plugin-ai/subtitles"
)

// Transcriber implements transcription using the Bifrost gateway.
type Transcriber struct {
	client   *bifrostcore.Bifrost
	provider schemas.ModelProvider
	model    string
}

// TranscriptionConfig holds configuration for creating a Transcriber.
type TranscriptionConfig struct {
	Provider schemas.ModelProvider
	APIKey   string
	APIURL   string
	Model    string // e.g., "whisper-1"
}

// NewTranscriber creates a new Transcriber.
func NewTranscriber(cfg TranscriptionConfig) (*Transcriber, error) {
	account := &providerAccount{
		provider: cfg.Provider,
		apiKey:   cfg.APIKey,
		apiURL:   cfg.APIURL,
	}

	bifrostConfig := schemas.BifrostConfig{
		Account: account,
	}

	client, err := bifrostcore.Init(context.Background(), bifrostConfig)
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
		model:    model,
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
			ResponseFormat: Ptr("vtt"), // Use VTT format for timed transcription
		},
	}

	resp, bifrostErr := t.client.TranscriptionRequest(bifrostCtx, req)
	if bifrostErr != nil {
		return nil, fmt.Errorf("bifrost transcription error: %s", bifrostErr.Error.Message)
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
