// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package interpluginclient provides a client for interacting with the Mattermost AI plugin
// from other Mattermost plugins.
package interpluginclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
)

const (
	// DefaultTimeout is the default timeout for all requests to the AI plugin
	DefaultTimeout = 10 * time.Minute

	aiPluginID = "mattermost-ai"
)

type PluginAPI interface {
	PluginHTTP(*http.Request) *http.Response
}

// Client allows calling the AI plugin functions from other plugins
type Client struct {
	httpClient http.Client
}

type pluginAPIRoundTripper struct {
	api PluginAPI
}

func (p *pluginAPIRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp := p.api.PluginHTTP(req)
	if resp == nil {
		return nil, errors.Errorf("Failed to make interplugin request")
	}

	return resp, nil
}

// CompletionRequest represents the data needed for an interplugin completion request
type SimpleCompletionRequest struct {
	// SystemPrompt is the text system prompt to send to the AI model
	SystemPrompt string `json:"systemPrompt"`

	// UserPrompt is the text user prompt to send to the AI model
	UserPrompt string `json:"userPrompt"`

	// BotUsername specifies which AI bot to use (optional, uses default bot if empty)
	BotUsername string `json:"botUsername,omitempty"`

	// RequesterUserID is the user ID of the user requesting the completion
	RequesterUserID string `json:"requesterUserID"`

	// Parameters allows customizing the completion behavior
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

// CompletionResponse represents the response from an interplugin completion request
type SimpleCompletionResponse struct {
	Response string `json:"response"`
}

// CompletionWithContext sends a prompt to the AI plugin with context and returns the generated response
func (c *Client) SimpleCompletionWithContext(ctx context.Context, req SimpleCompletionRequest) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	apiURL := fmt.Sprintf("/%s/inter-plugin/v1/simple_completion", aiPluginID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var completionResp SimpleCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completionResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return completionResp.Response, nil
}

// Completion sends a prompt to the AI plugin and returns the generated response (with default timeout)
func (c *Client) SimpleCompletion(req SimpleCompletionRequest) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	return c.SimpleCompletionWithContext(ctx, req)
}

// NewClientFromPlugin creates a new Client using the plugin's API client
func NewClient(p *plugin.MattermostPlugin) *Client {
	client := &Client{}
	client.httpClient.Transport = &pluginAPIRoundTripper{p.API}

	return client
}
