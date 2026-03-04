// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

// MapServiceTypeToProvider maps our service type strings to Bifrost provider constants.
func MapServiceTypeToProvider(serviceType string) (schemas.ModelProvider, error) {
	switch serviceType {
	case llm.ServiceTypeOpenAI:
		return schemas.OpenAI, nil
	case llm.ServiceTypeOpenAICompatible:
		return schemas.OpenAI, nil // Uses OpenAI with custom base URL
	case llm.ServiceTypeAzure:
		return schemas.Azure, nil
	case llm.ServiceTypeAnthropic:
		return schemas.Anthropic, nil
	case llm.ServiceTypeBedrock:
		return schemas.Bedrock, nil
	case llm.ServiceTypeCohere:
		return schemas.Cohere, nil
	case llm.ServiceTypeMistral:
		return schemas.Mistral, nil
	default:
		return "", fmt.Errorf("unsupported service type: %s", serviceType)
	}
}

// NewFromServiceConfig creates a LLM instance from ServiceConfig and BotConfig.
func NewFromServiceConfig(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig) (*LLM, error) {
	provider, err := MapServiceTypeToProvider(serviceConfig.Type)
	if err != nil {
		return nil, err
	}

	// Calculate streaming timeout
	streamingTimeout := DefaultStreamingTimeout
	if serviceConfig.StreamingTimeoutSeconds > 0 {
		streamingTimeout = time.Duration(serviceConfig.StreamingTimeoutSeconds) * time.Second
	}

	// Determine the API URL
	apiURL := serviceConfig.APIURL

	// For OpenAI Compatible services like Cohere and Mistral, set the appropriate base URL
	switch serviceConfig.Type {
	case llm.ServiceTypeCohere:
		if apiURL == "" {
			apiURL = "https://api.cohere.ai/compatibility/v1"
		}
	case llm.ServiceTypeMistral:
		if apiURL == "" {
			apiURL = "https://api.mistral.ai/v1"
		}
	}

	apiURL = normalizeOpenAIBaseURL(provider, apiURL)

	cfg := Config{
		Provider:           provider,
		APIKey:             serviceConfig.APIKey,
		APIURL:             apiURL,
		OrgID:              serviceConfig.OrgID,
		Region:             serviceConfig.Region,
		AWSAccessKeyID:     serviceConfig.AWSAccessKeyID,
		AWSSecretAccessKey: serviceConfig.AWSSecretAccessKey,
		DefaultModel:       serviceConfig.DefaultModel,
		InputTokenLimit:    serviceConfig.InputTokenLimit,
		OutputTokenLimit:   serviceConfig.OutputTokenLimit,
		StreamingTimeout:   streamingTimeout,
		SendUserID:         serviceConfig.SendUserID,
		UseResponsesAPI:    serviceConfig.UseResponsesAPI,

		// Bot-specific configuration
		EnabledNativeTools: botConfig.EnabledNativeTools,
		ReasoningEnabled:   botConfig.ReasoningEnabled,
		ReasoningEffort:    botConfig.ReasoningEffort,
		ThinkingBudget:     botConfig.ThinkingBudget,
	}

	// Use bot's model if specified, otherwise use service's default model
	if botConfig.Model != "" {
		cfg.DefaultModel = botConfig.Model
	}

	// Strip provider prefix from model name if present.
	// Bifrost ListModels returns IDs like "anthropic/claude-sonnet-4-20250514" but
	// chat/streaming requests expect just "claude-sonnet-4-20250514" since the
	// provider is already specified separately in the request.
	cfg.DefaultModel = stripProviderPrefix(cfg.DefaultModel)

	return New(cfg)
}

// stripProviderPrefix removes a leading "provider/" prefix from a model name.
// Bifrost's ListModels endpoint returns model IDs like "anthropic/claude-sonnet-4-20250514",
// but provider APIs expect just "claude-sonnet-4-20250514" since the provider is specified
// separately. This function handles configs saved with the prefix.
func stripProviderPrefix(model string) string {
	if idx := strings.Index(model, "/"); idx >= 0 {
		prefix := model[:idx]
		if _, err := MapServiceTypeToProvider(prefix); err == nil {
			return model[idx+1:]
		}
	}
	return model
}

// normalizeOpenAIBaseURL strips a trailing /v1 suffix from API URLs for OpenAI-type providers.
// Bifrost constructs full request paths starting with /v1/ (e.g., /v1/chat/completions,
// /v1/responses), so the base URL must not include a /v1 suffix. This maintains backward
// compatibility with URLs like "https://api.openai.com/v1" which were handled correctly
// by the previous OpenAI Go SDK.
func normalizeOpenAIBaseURL(provider schemas.ModelProvider, apiURL string) string {
	if provider == schemas.OpenAI && apiURL != "" {
		apiURL = strings.TrimRight(apiURL, "/")
		apiURL = strings.TrimSuffix(apiURL, "/v1")
	}
	return apiURL
}

// IsSupported returns true if the service type is supported by Bifrost.
func IsSupported(serviceType string) bool {
	switch serviceType {
	case llm.ServiceTypeOpenAI,
		llm.ServiceTypeOpenAICompatible,
		llm.ServiceTypeAzure,
		llm.ServiceTypeAnthropic,
		llm.ServiceTypeBedrock,
		llm.ServiceTypeCohere,
		llm.ServiceTypeMistral:
		return true
	default:
		return false
	}
}
