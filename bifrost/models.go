// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"

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
		displayName := m.ID
		if m.Name != nil && *m.Name != "" {
			displayName = *m.Name
		}
		models = append(models, llm.ModelInfo{
			ID:          m.ID,
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

	return FetchModels(FetchModelsConfig{
		Provider: provider,
		APIKey:   apiKey,
		APIURL:   apiURL,
		OrgID:    orgID,
	})
}
