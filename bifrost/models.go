// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// FetchModelsConfig holds configuration for fetching models.
type FetchModelsConfig struct {
	Provider       schemas.ModelProvider
	Keys           []schemas.Key
	ProviderConfig *schemas.ProviderConfig
}

// FetchModels retrieves the list of available models from a provider using Bifrost.
func FetchModels(cfg FetchModelsConfig) ([]llm.ModelInfo, error) {
	account := &providerAccount{
		provider:       cfg.Provider,
		keys:           cfg.Keys,
		providerConfig: cfg.ProviderConfig,
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
		modelID := normalizeListedModelID(m.ID, cfg.Provider)
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

// FetchModelsForService fetches models for a given service configuration.
func FetchModelsForService(serviceConfig llm.ServiceConfig) ([]llm.ModelInfo, error) {
	resolvedConfig, err := resolveServiceConfig(serviceConfig, DefaultStreamingTimeout)
	if err != nil {
		return nil, fmt.Errorf("model fetching not supported for service type: %s", serviceConfig.Type)
	}

	return FetchModels(FetchModelsConfig{
		Provider:       resolvedConfig.Provider,
		Keys:           resolvedConfig.Keys,
		ProviderConfig: resolvedConfig.ProviderConfig,
	})
}

// FetchModelsForServiceType fetches models for a given service type string.
func FetchModelsForServiceType(serviceType, apiKey, apiURL, orgID string) ([]llm.ModelInfo, error) {
	return FetchModelsForService(llm.ServiceConfig{
		ID:     "fetch-models",
		Type:   serviceType,
		APIKey: apiKey,
		APIURL: apiURL,
		OrgID:  orgID,
	})
}
