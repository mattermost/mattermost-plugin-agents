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

// BifrostTranscriber implements transcription using the Bifrost gateway.
type BifrostTranscriber struct {
	client   *bifrostcore.Bifrost
	provider schemas.ModelProvider
	model    string
}

// TranscriptionConfig holds configuration for creating a BifrostTranscriber.
type TranscriptionConfig struct {
	Provider schemas.ModelProvider
	APIKey   string
	APIURL   string
	Model    string // e.g., "whisper-1"
}

// NewTranscriber creates a new BifrostTranscriber.
func NewTranscriber(cfg TranscriptionConfig) (*BifrostTranscriber, error) {
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

	return &BifrostTranscriber{
		client:   client,
		provider: cfg.Provider,
		model:    model,
	}, nil
}

// Transcribe converts audio to text using Bifrost.
func (t *BifrostTranscriber) Transcribe(file io.Reader) (*subtitles.Subtitles, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Read the file into bytes for the request
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read audio file: %w", err)
	}

	req := &schemas.BifrostRequest{
		Provider: t.provider,
		Model:    t.model,
		Input: schemas.RequestInput{
			TranscriptionInput: &schemas.TranscriptionInput{
				File:           data,
				ResponseFormat: Ptr("vtt"), // Use VTT format for timed transcription
			},
		},
	}

	resp, bifrostErr := t.client.TranscriptionRequest(ctx, req)
	if bifrostErr != nil {
		return nil, fmt.Errorf("bifrost transcription error: %s", bifrostErr.Error.Message)
	}

	if resp == nil || resp.Transcribe == nil || resp.Transcribe.Text == "" {
		return nil, fmt.Errorf("no transcription data returned")
	}

	// Parse the VTT response
	timedTranscript, err := subtitles.NewSubtitlesFromVTT(strings.NewReader(resp.Transcribe.Text))
	if err != nil {
		return nil, fmt.Errorf("unable to parse transcription: %w", err)
	}

	return timedTranscript, nil
}

// Shutdown gracefully shuts down the Bifrost client.
func (t *BifrostTranscriber) Shutdown() {
	if t.client != nil {
		t.client.Shutdown()
	}
}
