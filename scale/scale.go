// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scale

import (
	"net/http"

	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/openai"
)

const defaultScaleModel = "openai/gpt-4o"

// placeholderAPIKey is a sentinel value passed to the OpenAI SDK which requires
// a non-empty API key. Actual authentication is handled by the RoundTripper
// using Scale's custom x-api-key header.
const placeholderAPIKey = "scale-auth"

// New creates an OpenAI-compatible client configured for Scale AI (including ScaleGov).
// It wraps the provided HTTP client's transport with a RoundTripper that replaces
// the standard Authorization header with Scale's x-api-key and x-selected-account-id headers.
func New(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig, httpClient *http.Client) *openai.OpenAI {
	// Set default model if not specified
	if serviceConfig.DefaultModel == "" {
		serviceConfig.DefaultModel = defaultScaleModel
	}

	// Build the OpenAI-compatible config with Scale-appropriate options:
	// disableStreamOptions=true (LiteLLM may not support stream_options)
	// useMaxTokens=false (use max_completion_tokens)
	cfg := config.OpenAIConfigFromServiceConfigWithOptions(serviceConfig, botConfig, true, false)

	cfg.APIKey = placeholderAPIKey

	// Wrap the existing HTTP client's transport with Scale's custom auth
	var baseTransport http.RoundTripper
	if httpClient != nil {
		baseTransport = httpClient.Transport
	}

	scaleClient := &http.Client{
		Transport: &RoundTripper{
			Base:      baseTransport,
			APIKey:    serviceConfig.APIKey,
			AccountID: serviceConfig.OrgID,
		},
	}
	if httpClient != nil {
		scaleClient.Timeout = httpClient.Timeout
		scaleClient.CheckRedirect = httpClient.CheckRedirect
		scaleClient.Jar = httpClient.Jar
	}

	return openai.NewCompatible(cfg, scaleClient)
}
