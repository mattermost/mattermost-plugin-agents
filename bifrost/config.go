// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// MapServiceTypeToProvider maps our service type strings to Bifrost provider constants.
func MapServiceTypeToProvider(serviceType string) (schemas.ModelProvider, error) {
	return llm.ModelProviderForServiceType(serviceType)
}

func supportsNativeTools(serviceType string) bool {
	switch serviceType {
	case llm.ServiceTypeOpenAI,
		llm.ServiceTypeOpenAICompatible,
		llm.ServiceTypeAzure,
		llm.ServiceTypeAnthropic:
		return true
	default:
		return false
	}
}

func filterNativeToolsForServiceType(serviceType string, tools []string) []string {
	if len(tools) == 0 {
		return tools
	}

	filtered := make([]string, 0, len(tools))
	if !supportsNativeTools(serviceType) {
		return filtered
	}

	filtered = append(filtered, tools...)
	return filtered
}

// NewFromServiceConfig creates a LLM instance from ServiceConfig and BotConfig.
func NewFromServiceConfig(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig) (*LLM, error) {
	// Calculate streaming timeout
	streamingTimeout := DefaultStreamingTimeout
	if serviceConfig.StreamingTimeoutSeconds > 0 {
		streamingTimeout = time.Duration(serviceConfig.StreamingTimeoutSeconds) * time.Second
	}

	resolvedConfig, err := resolveServiceConfig(serviceConfig, streamingTimeout)
	if err != nil {
		return nil, err
	}

	enabledNativeTools := filterNativeToolsForServiceType(serviceConfig.Type, botConfig.EnabledNativeTools)

	cfg := Config{
		Provider:         resolvedConfig.Provider,
		Keys:             resolvedConfig.Keys,
		ProviderConfig:   resolvedConfig.ProviderConfig,
		DefaultModel:     serviceConfig.DefaultModel,
		InputTokenLimit:  serviceConfig.InputTokenLimit,
		OutputTokenLimit: serviceConfig.OutputTokenLimit,
		StreamingTimeout: streamingTimeout,
		SendUserID:       serviceConfig.SendUserID,
		UseResponsesAPI:  llm.ServiceUsesResponsesAPI(serviceConfig),

		// Bot-specific configuration
		EnabledNativeTools: enabledNativeTools,
		ReasoningEnabled:   botConfig.ReasoningEnabled,
		ReasoningEffort:    botConfig.ReasoningEffort,
		ThinkingBudget:     botConfig.ThinkingBudget,
	}

	// Use bot's model if specified, otherwise use service's default model
	if botConfig.Model != "" {
		cfg.DefaultModel = botConfig.Model
	}

	return New(cfg)
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
	return llm.IsSupportedServiceType(serviceType)
}
