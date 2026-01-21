// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"fmt"
	"net/http"

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

// FetchModels retrieves the list of available models from a provider.
// Note: Model listing is not supported in Bifrost v1.1.38 - this returns an error.
func FetchModels(cfg FetchModelsConfig, httpClient *http.Client) ([]llm.ModelInfo, error) {
	// Bifrost v1.1.38 doesn't support listing models
	// Return an empty list for now - this feature requires a newer version of Bifrost
	return []llm.ModelInfo{}, nil
}

// FetchModelsForServiceType fetches models for a given service type string.
func FetchModelsForServiceType(serviceType, apiKey, apiURL, orgID string, httpClient *http.Client) ([]llm.ModelInfo, error) {
	provider, err := MapServiceTypeToProvider(serviceType)
	if err != nil {
		return nil, fmt.Errorf("model fetching not supported for service type: %s", serviceType)
	}

	return FetchModels(FetchModelsConfig{
		Provider: provider,
		APIKey:   apiKey,
		APIURL:   apiURL,
		OrgID:    orgID,
	}, httpClient)
}
