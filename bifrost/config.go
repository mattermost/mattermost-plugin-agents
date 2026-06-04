// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/llm"
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
	case llm.ServiceTypeGemini:
		return schemas.Gemini, nil
	case llm.ServiceTypeVertex:
		return schemas.Vertex, nil
	default:
		return "", fmt.Errorf("unsupported service type: %s", serviceType)
	}
}

// SupportsNativeTools reports whether the given service type can use provider
// native tools (currently, web search). This gates both request-time filtering
// and the effective-behavior checks used by built-in Mattermost tools so that
// built-in fallbacks do not get suppressed when native tools would be stripped.
func SupportsNativeTools(serviceType string) bool {
	provider, err := MapServiceTypeToProvider(serviceType)
	if err != nil {
		return false
	}
	return supportsNativeToolsProvider(provider)
}

func supportsNativeTools(serviceType string) bool {
	return SupportsNativeTools(serviceType)
}

func supportsNativeToolsProvider(provider schemas.ModelProvider) bool {
	switch provider {
	case schemas.OpenAI, schemas.Azure, schemas.Anthropic, schemas.Gemini, schemas.Vertex:
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
	provider, err := MapServiceTypeToProvider(serviceConfig.Type)
	if err != nil {
		return nil, err
	}

	// Calculate streaming timeout
	streamingTimeout := DefaultStreamingTimeout
	if serviceConfig.StreamingTimeoutSeconds > 0 {
		streamingTimeout = time.Duration(serviceConfig.StreamingTimeoutSeconds) * time.Second
	}

	// Don't fill in per-provider defaults here; bifrost has its own and they drift.
	apiURL := normalizeOpenAIBaseURL(provider, serviceConfig.APIURL)
	enabledNativeTools := filterNativeToolsForServiceType(serviceConfig.Type, botConfig.EnabledNativeTools)

	cfg := Config{
		Provider:              provider,
		APIKey:                serviceConfig.APIKey,
		APIURL:                apiURL,
		OrgID:                 serviceConfig.OrgID,
		Region:                serviceConfig.Region,
		AWSAccessKeyID:        serviceConfig.AWSAccessKeyID,
		AWSSecretAccessKey:    serviceConfig.AWSSecretAccessKey,
		VertexProjectID:       serviceConfig.VertexProjectID,
		VertexProjectNumber:   serviceConfig.VertexProjectNumber,
		VertexAuthCredentials: serviceConfig.VertexAuthCredentials,
		DefaultModel:          serviceConfig.DefaultModel,
		InputTokenLimit:       serviceConfig.InputTokenLimit,
		OutputTokenLimit:      serviceConfig.OutputTokenLimit,
		StreamingTimeout:      streamingTimeout,
		UseResponsesAPI:       llm.ServiceUsesResponsesAPI(serviceConfig),

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
	switch serviceType {
	case llm.ServiceTypeOpenAI,
		llm.ServiceTypeOpenAICompatible,
		llm.ServiceTypeAzure,
		llm.ServiceTypeAnthropic,
		llm.ServiceTypeBedrock,
		llm.ServiceTypeCohere,
		llm.ServiceTypeMistral,
		llm.ServiceTypeGemini,
		llm.ServiceTypeVertex:
		return true
	default:
		return false
	}
}
