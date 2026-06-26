// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// FetchModelsConfig holds configuration for fetching models.
type FetchModelsConfig struct {
	Provider schemas.ModelProvider
	APIKey   string
	APIURL   string
	OrgID    string

	// Region applies to providers that require a region to list models
	// (Vertex AI, Bedrock).
	Region string

	// Vertex AI credentials. Empty AuthCredentials signals ADC / attached IAM.
	VertexProjectID       string
	VertexProjectNumber   string
	VertexAuthCredentials string
}

// FetchModels retrieves the list of available models from a provider using Bifrost.
func FetchModels(cfg FetchModelsConfig) ([]llm.ModelInfo, error) {
	account := &providerAccount{
		ProviderSettings: ProviderSettings{
			Provider:              cfg.Provider,
			APIKey:                cfg.APIKey,
			APIURL:                cfg.APIURL,
			OrgID:                 cfg.OrgID,
			Region:                cfg.Region,
			VertexProjectID:       cfg.VertexProjectID,
			VertexProjectNumber:   cfg.VertexProjectNumber,
			VertexAuthCredentials: cfg.VertexAuthCredentials,
		},
	}

	client, err := newBifrostClient(account, cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Bifrost client for model listing: %w", err)
	}
	defer client.Shutdown()

	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

	req := &schemas.BifrostListModelsRequest{
		Provider: cfg.Provider,
	}

	resp, bifrostErr := client.ListAllModels(bifrostCtx, req)
	if bifrostErr != nil {
		return nil, llm.SanitizeProviderError(fmt.Errorf("bifrost list models error: %s", bifrostErrorString(bifrostErr)), cfg.APIKey)
	}

	if resp == nil {
		return []llm.ModelInfo{}, nil
	}

	return convertBifrostModels(resp.Data), nil
}

func convertBifrostModels(in []schemas.Model) []llm.ModelInfo {
	out := make([]llm.ModelInfo, 0, len(in))
	for _, m := range in {
		modelID := m.ID
		if idx := strings.Index(modelID, "/"); idx >= 0 {
			modelID = modelID[idx+1:]
		}
		displayName := modelID
		if m.Name != nil && *m.Name != "" {
			displayName = *m.Name
		}
		// Cohere, Mistral, and Groq (via the OpenAI client) publish only
		// ContextLength; fall back to it for the input cap.
		inputLimit := m.MaxInputTokens
		if inputLimit == nil {
			inputLimit = m.ContextLength
		}
		out = append(out, llm.ModelInfo{
			ID:               modelID,
			DisplayName:      displayName,
			InputTokenLimit:  inputLimit,
			OutputTokenLimit: m.MaxOutputTokens,
			ContextLength:    m.ContextLength,
		})
	}
	return out
}

// FetchModelsForServiceType fetches models for a given service type string.
// This variant is kept for services that only require API-key style credentials
// (OpenAI, Anthropic, Azure, OpenAI-compatible, Gemini, Cohere, Mistral). Use
// FetchModelsForService for Vertex AI and other providers that need structured
// credentials beyond a single API key.
func FetchModelsForServiceType(serviceType, apiKey, apiURL, orgID string) ([]llm.ModelInfo, error) {
	return FetchModelsForService(llm.ServiceConfig{
		Type:   serviceType,
		APIKey: apiKey,
		APIURL: apiURL,
		OrgID:  orgID,
	})
}

// FetchModelsForService fetches models for a given service configuration. This
// handles provider-specific credentials (for example, Vertex AI's project ID,
// region, and service-account JSON) that cannot be expressed as a single API
// key.
func FetchModelsForService(svc llm.ServiceConfig) ([]llm.ModelInfo, error) {
	provider, err := MapServiceTypeToProvider(svc.Type)
	if err != nil {
		return nil, fmt.Errorf("model fetching not supported for service type: %s", svc.Type)
	}

	return FetchModels(FetchModelsConfig{
		Provider:              provider,
		APIKey:                svc.APIKey,
		APIURL:                normalizeOpenAIBaseURL(provider, svc.APIURL),
		OrgID:                 svc.OrgID,
		Region:                svc.Region,
		VertexProjectID:       svc.VertexProjectID,
		VertexProjectNumber:   svc.VertexProjectNumber,
		VertexAuthCredentials: svc.VertexAuthCredentials,
	})
}
