// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
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

	settings := providerSettingsFromService(provider, serviceConfig)
	if settings.StreamingTimeout <= 0 {
		settings.StreamingTimeout = DefaultStreamingTimeout
	}

	// Use bot's model if specified, otherwise use service's default model
	if botConfig.Model != "" {
		settings.DefaultModel = botConfig.Model
	}

	cfg := Config{
		ProviderSettings: settings,
		InputTokenLimit:  serviceConfig.InputTokenLimit,
		OutputTokenLimit: serviceConfig.OutputTokenLimit,
		UseResponsesAPI:  llm.ServiceUsesResponsesAPI(serviceConfig),

		// Bot-specific configuration
		EnabledNativeTools: filterNativeToolsForServiceType(serviceConfig.Type, botConfig.EnabledNativeTools),
		ReasoningEnabled:   botConfig.ReasoningEnabled,
		ReasoningEffort:    botConfig.ReasoningEffort,
		ThinkingBudget:     botConfig.ThinkingBudget,
	}

	for _, fbSvc := range fallbackServices {
		fbEntry, fbErr := serviceConfigToFallbackEntry(fbSvc)
		if fbErr != nil {
			// Fail bot setup rather than silently dropping the fallback: an
			// admin must not believe a fallback is in place when it isn't.
			return nil, fmt.Errorf("fallback service %q cannot be used: %w", fbSvc.ID, fbErr)
		}
		cfg.Fallbacks = append(cfg.Fallbacks, fbEntry)
	}

	return New(cfg)
}

// providerSettingsFromService maps a ServiceConfig's provider connection fields
// onto ProviderSettings. It does not fill in per-provider URL defaults; bifrost
// has its own and they drift.
func providerSettingsFromService(provider schemas.ModelProvider, svc llm.ServiceConfig) ProviderSettings {
	return ProviderSettings{
		Provider:              provider,
		APIKey:                svc.APIKey,
		APIURL:                normalizeOpenAIBaseURL(provider, svc.APIURL),
		OrgID:                 svc.OrgID,
		Region:                svc.Region,
		AWSAccessKeyID:        svc.AWSAccessKeyID,
		AWSSecretAccessKey:    svc.AWSSecretAccessKey,
		VertexProjectID:       svc.VertexProjectID,
		VertexProjectNumber:   svc.VertexProjectNumber,
		VertexAuthCredentials: svc.VertexAuthCredentials,
		DefaultModel:          svc.DefaultModel,
		StreamingTimeout:      time.Duration(svc.StreamingTimeoutSeconds) * time.Second,
	}
}

// serviceConfigToFallbackEntry converts a ServiceConfig into a FallbackEntry
// for registration with the Bifrost client.
func serviceConfigToFallbackEntry(svc llm.ServiceConfig) (FallbackEntry, error) {
	provider, err := MapServiceTypeToProvider(svc.Type)
	if err != nil {
		return FallbackEntry{}, err
	}

	// Unlike the primary path, fallbacks pin explicit base URLs for Cohere and
	// Mistral when none is configured.
	if svc.APIURL == "" {
		switch svc.Type {
		case llm.ServiceTypeCohere:
			svc.APIURL = "https://api.cohere.ai/compatibility/v1"
		case llm.ServiceTypeMistral:
			svc.APIURL = "https://api.mistral.ai/v1"
		}
	}

	return FallbackEntry{
		ProviderSettings: providerSettingsFromService(provider, svc),
		ID:               svc.ID,
		// Credential-based providers like Bedrock authenticate without an API
		// key, so an empty key does not make them keyless.
		IsKeyLess: svc.APIKey == "" && provider != schemas.Bedrock,
		// An OpenAI-base fallback that does not itself use the Responses API
		// (e.g. a local Ollama/vLLM server) must be registered chat-only so
		// Bifrost downgrades Responses-API requests instead of failing on
		// /v1/responses. Other base providers handle the Responses API natively.
		ChatOnly: provider == schemas.OpenAI && !llm.ServiceUsesResponsesAPI(svc),
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
