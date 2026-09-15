// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const (
	northModelsPageLimit = 100
	northModelsMaxPages  = 20
	northModelsTimeout   = 30 * time.Second
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

// FetchModelsForService fetches models for a given service configuration. This
// handles provider-specific credentials (for example, Vertex AI's project ID,
// region, and service-account JSON) that cannot be expressed as a single API
// key. The admin handler builds a ServiceConfig from raw type/key/url fields
// and calls this, so North listing is served from the same entry point.
func FetchModelsForService(svc llm.ServiceConfig) ([]llm.ModelInfo, error) {
	if svc.Type == llm.ServiceTypeNorth {
		return fetchNorthModels(context.Background(), svc.APIURL, svc.APIKey)
	}

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

type northModelsResponse struct {
	Data      []northModel `json:"data"`
	NextToken string       `json:"next_token"`
}

type northModel struct {
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	ContextLength *int   `json:"context_length"`
	MaxTokens     *int   `json:"max_tokens"`
}

// fetchNorthModels lists models from GET {normalizedBase}/v2/models. It does
// not use Bifrost: that path hits /v1/models, which returns unusable labels.
func fetchNorthModels(ctx context.Context, apiURL, apiKey string) ([]llm.ModelInfo, error) {
	base := normalizeNorthBaseURL(apiURL)
	if base == "" {
		return nil, fmt.Errorf("api URL is required")
	}

	ctx, cancel := context.WithTimeout(ctx, northModelsTimeout)
	defer cancel()

	var models []llm.ModelInfo
	nextToken := ""
	for page := 0; page < northModelsMaxPages; page++ {
		endpoint := strings.TrimRight(base, "/") + "/v2/models"
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("invalid north models URL: %w", err)
		}
		q := u.Query()
		q.Set("limit", fmt.Sprintf("%d", northModelsPageLimit))
		if nextToken != "" {
			q.Set("next_token", nextToken)
		}
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("north models request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Accept", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llm.SanitizeProviderError(fmt.Errorf("north models request failed: %w", err), apiKey)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, llm.SanitizeProviderError(fmt.Errorf("reading north models response: %w", readErr), apiKey)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, llm.SanitizeProviderError(fmt.Errorf("north models request failed with status %d", resp.StatusCode), apiKey)
		}

		var parsed northModelsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, llm.SanitizeProviderError(fmt.Errorf("decoding north models response: %w", err), apiKey)
		}

		for _, m := range parsed.Data {
			if m.Name == "" {
				continue
			}
			displayName := m.DisplayName
			if displayName == "" {
				displayName = m.Name
			}
			models = append(models, llm.ModelInfo{
				ID:               m.Name,
				DisplayName:      displayName,
				InputTokenLimit:  m.ContextLength,
				OutputTokenLimit: m.MaxTokens,
				ContextLength:    m.ContextLength,
			})
		}

		if parsed.NextToken == "" {
			break
		}
		nextToken = parsed.NextToken
	}

	if models == nil {
		models = []llm.ModelInfo{}
	}
	return models, nil
}
