// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"strings"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

// FetchModelsConfig holds configuration for fetching models.
type FetchModelsConfig struct {
	Provider schemas.ModelProvider
	APIKey   string
	APIURL   string
	OrgID    string
}

// FetchModels retrieves the list of available models from a provider using Bifrost.
func FetchModels(cfg FetchModelsConfig) ([]llm.ModelInfo, error) {
	account := &providerAccount{
		provider: cfg.Provider,
		apiKey:   cfg.APIKey,
		apiURL:   cfg.APIURL,
		orgID:    cfg.OrgID,
	}

	bifrostConfig := schemas.BifrostConfig{
		Account: account,
	}

	client, err := bifrostcore.Init(context.Background(), bifrostConfig)
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
		return nil, fmt.Errorf("bifrost list models error: %s", bifrostErr.Error.Message)
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
func FetchModelsForServiceType(serviceType, apiKey, apiURL, orgID string) ([]llm.ModelInfo, error) {
	provider, err := MapServiceTypeToProvider(serviceType)
	if err != nil {
		return nil, fmt.Errorf("model fetching not supported for service type: %s", serviceType)
	}

	apiURL = normalizeFetchModelsAPIURL(serviceType, provider, apiURL)

	return FetchModels(FetchModelsConfig{
		Provider: provider,
		APIKey:   apiKey,
		APIURL:   apiURL,
		OrgID:    orgID,
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
