// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"encoding/json"
	"fmt"
)

// BifrostParameters are the parameters of a Bifrost embedding provider.
type BifrostParameters struct {
	Provider string `json:"provider"` // e.g., "openai", "azure", "cohere", "bedrock"
	APIKey   string `json:"apiKey"`
	APIURL   string `json:"apiURL,omitempty"`
	Model    string `json:"model"` // e.g., "text-embedding-3-small"
}

// OpenAIParameters are the parameters of an OpenAI or OpenAI-compatible embedding provider.
type OpenAIParameters struct {
	APIKey string `json:"apiKey"`
	APIURL string `json:"apiURL,omitempty"`
	Model  string `json:"embeddingModel"` // e.g., "text-embedding-3-small"
}

// Credential returns the API key the provider sends and the parts that decide
// where it goes: the provider type plus whatever its client reads to pick an
// upstream. It decodes the parameters into the same types the client does, so
// the credential rules cannot disagree with the client about what a parameter
// holds. A provider type with no client, or parameters the client could not
// decode, is an error: nothing may be bound to them.
func (u UpstreamConfig) Credential() (apiKey string, destination []string, err error) {
	switch u.Type {
	case ProviderTypeBifrost:
		var params BifrostParameters
		if err := json.Unmarshal(u.Parameters, &params); err != nil {
			return "", nil, fmt.Errorf("invalid %s embedding parameters: %w", u.Type, err)
		}
		return params.APIKey, []string{u.Type, params.Provider, params.APIURL}, nil
	case ProviderTypeOpenAI, ProviderTypeOpenAICompatible:
		var params OpenAIParameters
		if err := json.Unmarshal(u.Parameters, &params); err != nil {
			return "", nil, fmt.Errorf("invalid %s embedding parameters: %w", u.Type, err)
		}
		return params.APIKey, []string{u.Type, params.APIURL}, nil
	}
	return "", nil, fmt.Errorf("embedding provider type %q holds no credential", u.Type)
}
