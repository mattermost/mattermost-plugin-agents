// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/llm"
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
		provider:              cfg.Provider,
		apiKey:                cfg.APIKey,
		apiURL:                cfg.APIURL,
		orgID:                 cfg.OrgID,
		region:                cfg.Region,
		vertexProjectID:       cfg.VertexProjectID,
		vertexProjectNumber:   cfg.VertexProjectNumber,
		vertexAuthCredentials: cfg.VertexAuthCredentials,
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
		return nil, llm.SanitizeProviderError(fmt.Errorf("bifrost list models error: %s", bifrostErr.Error.Message), cfg.APIKey)
	}

	if resp == nil {
		return []llm.ModelInfo{}, nil
	}

	models := make([]llm.ModelInfo, 0, len(resp.Data))
	for _, m := range resp.Data {
		// Bifrost ListModels returns IDs with a provider prefix (e.g. "anthropic/claude-sonnet-4-20250514").
		// Strip the prefix so the saved config uses plain model names that the provider APIs expect.
		modelID := m.ID
		if idx := strings.Index(modelID, "/"); idx >= 0 {
			modelID = modelID[idx+1:]
		}
		displayName := modelID
		if m.Name != nil && *m.Name != "" {
			displayName = *m.Name
		}
		models = append(models, llm.ModelInfo{
			ID:          modelID,
			DisplayName: displayName,
		})
	}

	return models, nil
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

	apiURL := normalizeFetchModelsAPIURL(svc.Type, provider, svc.APIURL)

	return FetchModels(FetchModelsConfig{
		Provider:              provider,
		APIKey:                svc.APIKey,
		APIURL:                apiURL,
		OrgID:                 svc.OrgID,
		Region:                svc.Region,
		VertexProjectID:       svc.VertexProjectID,
		VertexProjectNumber:   svc.VertexProjectNumber,
		VertexAuthCredentials: svc.VertexAuthCredentials,
	})
}

func normalizeFetchModelsAPIURL(serviceType string, provider schemas.ModelProvider, apiURL string) string {
	switch serviceType {
	case llm.ServiceTypeCohere:
		if apiURL == "" {
			apiURL = "https://api.cohere.ai/compatibility/v1"
		}
	case llm.ServiceTypeMistral:
		if apiURL == "" {
			apiURL = "https://api.mistral.ai/v1"
		}
	}

	return normalizeOpenAIBaseURL(provider, apiURL)
}
