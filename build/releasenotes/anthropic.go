// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultAnthropicURL = "https://api.anthropic.com"
	defaultModel        = "claude-sonnet-5-5"
	anthropicVersion    = "2023-06-01"
)

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type outputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type messagesRequest struct {
	Model        string        `json:"model"`
	MaxTokens    int           `json:"max_tokens"`
	System       string        `json:"system,omitempty"`
	Messages     []message     `json:"messages"`
	OutputConfig *outputConfig `json:"output_config,omitempty"`
}

type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
}

type errorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// llmClient is a minimal Anthropic Messages API client. It sends no sampling
// parameters (newer models reject non-default values) and leaves thinking at
// the model default.
type llmClient struct {
	BaseURL string
	APIKey  string
	Model   string
	// Effort is optional; when set it is sent as output_config.effort.
	Effort  string
	HTTP    *http.Client
	Backoff time.Duration
}

func newLLMClient(apiKey, model string) *llmClient {
	base := strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
	if base == "" {
		base = defaultAnthropicURL
	}
	return &llmClient{
		BaseURL: base,
		APIKey:  apiKey,
		Model:   model,
		Effort:  strings.TrimSpace(os.Getenv("RELEASE_NOTES_EFFORT")),
		// Responses are not streamed, so allow time for long generations.
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
		Backoff: 5 * time.Second,
	}
}

// complete sends one conversation and returns the concatenated text blocks.
// Transient failures (429, 5xx, network) are retried a bounded number of times.
func (c *llmClient) complete(ctx context.Context, system string, msgs []message, maxTokens int) (string, error) {
	reqBody := messagesRequest{Model: c.Model, MaxTokens: maxTokens, System: system, Messages: msgs}
	if c.Effort != "" {
		reqBody.OutputConfig = &outputConfig{Effort: c.Effort}
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := sleepCtx(ctx, c.Backoff*time.Duration(attempt-1)); err != nil {
				return "", err
			}
		}
		text, retry, err := c.completeOnce(ctx, payload)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return "", lastErr
}

func (c *llmClient) completeOnce(ctx context.Context, payload []byte) (text string, retry bool, err error) {
	url := strings.TrimRight(c.BaseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		return "", true, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", true, fmt.Errorf("reading anthropic response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var e errorResponse
		_ = json.Unmarshal(body, &e)
		detail := resp.Status
		if e.Error.Type != "" {
			detail += " (" + e.Error.Type + ")"
		}
		if e.Error.Message != "" {
			detail += ": " + truncateRunes(e.Error.Message, 300)
		}
		transient := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return "", transient, fmt.Errorf("anthropic API error: %s", detail)
	}

	var parsed messagesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", false, fmt.Errorf("decoding anthropic response: %w", err)
	}
	switch parsed.StopReason {
	case "max_tokens":
		return "", false, errors.New("anthropic response was cut off at max_tokens")
	case "refusal":
		return "", false, errors.New("anthropic declined the request (stop_reason refusal)")
	}
	var sb strings.Builder
	for _, b := range parsed.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	if strings.TrimSpace(sb.String()) == "" {
		return "", false, errors.New("anthropic response had no text")
	}
	return sb.String(), false, nil
}
