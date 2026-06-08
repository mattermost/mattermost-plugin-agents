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
// fallbackServices is an ordered slice of fallback services resolved from the
// primary service's fallback chain (see llm.ResolveFallbackChain). Each fallback
// service's DefaultModel is used as the fallback model.
func NewFromServiceConfig(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig, fallbackServices []llm.ServiceConfig) (*LLM, error) {
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

	// Build fallback entries from the resolved fallback service chain.
	for _, fbSvc := range fallbackServices {
		fbEntry, fbErr := serviceConfigToFallbackEntry(fbSvc)
		if fbErr != nil {
			// Skip unsupported fallback services rather than failing the whole bot.
			continue
		}
		cfg.Fallbacks = append(cfg.Fallbacks, fbEntry)
	}

	return New(cfg)
}

// serviceConfigToFallbackEntry converts a ServiceConfig into a FallbackEntry
// for registration with the Bifrost client.
func serviceConfigToFallbackEntry(svc llm.ServiceConfig) (FallbackEntry, error) {
	provider, err := MapServiceTypeToProvider(svc.Type)
	if err != nil {
		return FallbackEntry{}, err
	}

	apiURL := svc.APIURL
	switch svc.Type {
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

	return FallbackEntry{
		ID:                    svc.ID,
		Provider:              provider,
		Model:                 svc.DefaultModel,
		APIKey:                svc.APIKey,
		APIURL:                apiURL,
		OrgID:                 svc.OrgID,
		Region:                svc.Region,
		AWSAccessKeyID:        svc.AWSAccessKeyID,
		AWSSecretAccessKey:    svc.AWSSecretAccessKey,
		VertexProjectID:       svc.VertexProjectID,
		VertexProjectNumber:   svc.VertexProjectNumber,
		VertexAuthCredentials: svc.VertexAuthCredentials,
		// A fallback registered as a custom provider is keyless when it has no
		// API key (e.g. a local Ollama server). Credential-based providers like
		// Bedrock authenticate without an API key, so they are never keyless.
		IsKeyLess: svc.APIKey == "" && provider != schemas.Bedrock,
		// An OpenAI-base fallback that does not itself use the Responses API
		// (e.g. a local Ollama/vLLM server) is chat-only: registering it as a
		// custom provider with chat-only AllowedRequests lets Bifrost downgrade a
		// Responses-API request to chat completions instead of failing on
		// /v1/responses. Other base providers handle the Responses API natively.
		ChatOnly:                provider == schemas.OpenAI && !llm.ServiceUsesResponsesAPI(svc),
		StreamingTimeoutSeconds: svc.StreamingTimeoutSeconds,
	}, nil
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
