// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package gemini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"time"

	"google.golang.org/genai"
)

type Config struct {
	APIKey              string        `json:"apiKey"`
	DefaultModel        string        `json:"defaultModel"`
	ImageGenerationModel string       `json:"imageGenerationModel"` // e.g., "gemini-2.5-flash-image" or "gemini-3-pro-image-preview"
	StreamingTimeout    time.Duration `json:"streamingTimeout"`
}

type Gemini struct {
	client *genai.Client
	config Config
}

// New creates a new Gemini client
func New(config Config) (*Gemini, error) {
	if config.APIKey == "" {
		return nil, errors.New("API key is required")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  config.APIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	// Set default image generation model if not specified
	if config.ImageGenerationModel == "" {
		config.ImageGenerationModel = "gemini-2.5-flash-image"
	}

	return &Gemini{
		client: client,
		config: config,
	}, nil
}

// GenerateImage generates an image from a text prompt using Gemini
func (g *Gemini) GenerateImage(prompt string) (image.Image, error) {
	ctx := context.Background()
	if g.config.StreamingTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.config.StreamingTimeout)
		defer cancel()
	}

	// Generate content with the image generation model
	result, err := g.client.Models.GenerateContent(
		ctx,
		g.config.ImageGenerationModel,
		genai.Text(prompt),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate image: %w", err)
	}

	// Extract image data from the response
	// Gemini returns images as raw bytes in InlineData
	if result == nil || len(result.Candidates) == 0 {
		return nil, errors.New("no image data returned from Gemini")
	}

	candidate := result.Candidates[0]
	if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
		return nil, errors.New("no content parts in response")
	}

	// Look for inline data (image) in the parts
	var imgBytes []byte
	for _, part := range candidate.Content.Parts {
		if part.InlineData != nil {
			// InlineData.Data contains raw image bytes (not base64-encoded)
			imgBytes = part.InlineData.Data
			break
		}
	}

	if imgBytes == nil {
		return nil, errors.New("no image data found in response")
	}

	// Decode PNG image
	r := bytes.NewReader(imgBytes)
	imgData, err := png.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decode PNG image: %w", err)
	}

	return imgData, nil
}

// Close closes the Gemini client (no-op for compatibility)
func (g *Gemini) Close() error {
	// The genai.Client doesn't require explicit closing
	return nil
}
