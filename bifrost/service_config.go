// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

// ResolvedServiceConfig is the normalized Bifrost configuration derived from a service config.
type ResolvedServiceConfig struct {
	Provider       schemas.ModelProvider
	Keys           []schemas.Key
	ProviderConfig *schemas.ProviderConfig
}

// ResolveTranscriptionServiceConfig resolves a service configuration into the
// provider, keys, and provider config required for transcription requests.
func ResolveTranscriptionServiceConfig(serviceConfig llm.ServiceConfig) (*ResolvedServiceConfig, error) {
	return resolveServiceConfig(serviceConfig, DefaultStreamingTimeout)
}

func resolveServiceConfig(serviceConfig llm.ServiceConfig, streamingTimeout time.Duration) (*ResolvedServiceConfig, error) {
	provider, err := llm.ModelProviderForServiceType(serviceConfig.Type)
	if err != nil {
		return nil, err
	}

	key := schemas.Key{
		Value:  schemas.EnvVar{Val: serviceConfig.APIKey},
		Weight: 1.0,
	}

	switch provider {
	case schemas.Azure:
		if serviceConfig.APIURL != "" {
			key.AzureKeyConfig = &schemas.AzureKeyConfig{
				Endpoint: schemas.EnvVar{Val: serviceConfig.APIURL},
			}
		}
	case schemas.Bedrock:
		if serviceConfig.AWSAccessKeyID != "" || serviceConfig.AWSSecretAccessKey != "" || serviceConfig.Region != "" {
			bedrockConfig := &schemas.BedrockKeyConfig{
				AccessKey: schemas.EnvVar{Val: serviceConfig.AWSAccessKeyID},
				SecretKey: schemas.EnvVar{Val: serviceConfig.AWSSecretAccessKey},
			}
			if serviceConfig.Region != "" {
				region := schemas.EnvVar{Val: serviceConfig.Region}
				bedrockConfig.Region = &region
			}
			key.BedrockKeyConfig = bedrockConfig
		}
	}

	if err := mergeAdvancedBifrostJSON(serviceConfig.BifrostKeyJSON, &key); err != nil {
		return nil, fmt.Errorf("invalid bifrostKeyJSON: %w", err)
	}

	providerConfig := &schemas.ProviderConfig{
		NetworkConfig:            schemas.DefaultNetworkConfig,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
	}

	if streamingTimeout > 0 {
		providerConfig.NetworkConfig.DefaultRequestTimeoutInSeconds = int(streamingTimeout.Seconds()) * 10
	} else {
		providerConfig.NetworkConfig.DefaultRequestTimeoutInSeconds = int(DefaultStreamingTimeout.Seconds()) * 10
	}

	apiURL := defaultServiceAPIURL(serviceConfig)
	if apiURL != "" {
		providerConfig.NetworkConfig.BaseURL = apiURL
	}

	if serviceConfig.OrgID != "" && provider == schemas.OpenAI {
		providerConfig.NetworkConfig.ExtraHeaders = map[string]string{
			"OpenAI-Organization": serviceConfig.OrgID,
		}
	}

	providerConfig.NetworkConfig.MaxRetries = 2
	providerConfig.NetworkConfig.RetryBackoffInitial = 1 * time.Second
	providerConfig.NetworkConfig.RetryBackoffMax = 10 * time.Second

	if err := mergeAdvancedBifrostJSON(serviceConfig.BifrostProviderConfigJSON, providerConfig); err != nil {
		return nil, fmt.Errorf("invalid bifrostProviderConfigJSON: %w", err)
	}

	providerConfig.NetworkConfig.BaseURL = normalizeOpenAIBaseURL(provider, providerConfig.NetworkConfig.BaseURL)
	providerConfig.CheckAndSetDefaults()

	return &ResolvedServiceConfig{
		Provider:       provider,
		Keys:           []schemas.Key{key},
		ProviderConfig: providerConfig,
	}, nil
}

func mergeAdvancedBifrostJSON(raw string, target any) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return err
	}

	return nil
}

func defaultServiceAPIURL(serviceConfig llm.ServiceConfig) string {
	if serviceConfig.APIURL != "" {
		return serviceConfig.APIURL
	}

	switch serviceConfig.Type {
	case string(schemas.Cohere):
		return "https://api.cohere.ai/compatibility/v1"
	case string(schemas.Mistral):
		return "https://api.mistral.ai/v1"
	default:
		return ""
	}
}

func normalizeListedModelID(modelID string, provider schemas.ModelProvider) string {
	parsedProvider, normalizedModelID := schemas.ParseModelString(modelID, provider)
	if parsedProvider == provider {
		return normalizedModelID
	}

	return modelID
}
