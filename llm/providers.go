// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "net/http"

// OpenAICompatibleProvider describes the configuration for an OpenAI-compatible
// provider that can be registered in the provider registry. Adding an entry to
// the registry is all that is needed to support a new provider — no changes to
// bots.go or api.go are required.
type OpenAICompatibleProvider struct {
	// KnownModels is a static list of models shown in the UI when
	// dynamic fetching from the provider's /models endpoint isn't available.
	KnownModels []ModelInfo

	// DefaultModel used when none is configured.
	DefaultModel string

	// CreateTransport returns a custom RoundTripper for non-standard auth.
	// If nil, the default HTTP client is used (standard Bearer token auth).
	CreateTransport func(cfg ServiceConfig, base http.RoundTripper) http.RoundTripper

	// DisableStreamOptions disables the stream_options parameter.
	DisableStreamOptions bool

	// UseMaxTokens uses max_tokens instead of max_completion_tokens.
	UseMaxTokens bool

	// FixedAPIURL overrides the service config's APIURL
	// (for providers like Cohere/Mistral with fixed endpoints).
	FixedAPIURL string
}

// openAICompatibleProviders is the registry of known OpenAI-compatible providers.
var openAICompatibleProviders = map[string]OpenAICompatibleProvider{
	ServiceTypeScale: {
		KnownModels: []ModelInfo{
			{ID: "openai/gpt-4o", DisplayName: "openai/gpt-4o"},
			{ID: "bedrock/anthropic.claude-sonnet-4-5-20250929-v1:0", DisplayName: "bedrock/anthropic.claude-sonnet-4-5-20250929-v1:0"},
			{ID: "bedrock/anthropic.claude-3-7-sonnet-20250219-v1:0", DisplayName: "bedrock/anthropic.claude-3-7-sonnet-20250219-v1:0"},
			{ID: "model_zoo/gpt-oss-120b", DisplayName: "model_zoo/gpt-oss-120b"},
			{ID: "model_zoo/llama-3-3-70b-instruct", DisplayName: "model_zoo/llama-3-3-70b-instruct"},
			{ID: "model_zoo/llama-3-1-8b-instruct", DisplayName: "model_zoo/llama-3-1-8b-instruct"},
			{ID: "model_zoo/defense-llama-3-8b-instruct", DisplayName: "model_zoo/defense-llama-3-8b-instruct"},
			{ID: "bedrock/amazon.nova-pro-v1:0", DisplayName: "bedrock/amazon.nova-pro-v1:0"},
			{ID: "bedrock/amazon.nova-lite-v1:0", DisplayName: "bedrock/amazon.nova-lite-v1:0"},
		},
		DefaultModel:         "openai/gpt-4o",
		DisableStreamOptions: true,
		CreateTransport: func(cfg ServiceConfig, base http.RoundTripper) http.RoundTripper {
			headers := map[string]string{"x-api-key": cfg.APIKey}
			if cfg.OrgID != "" {
				headers["x-selected-account-id"] = cfg.OrgID
			}
			return &CustomAuthTransport{
				Base:          base,
				RemoveHeaders: []string{"Authorization"},
				SetHeaders:    headers,
			}
		},
	},
}

// GetOpenAICompatibleProvider returns the provider configuration for the given
// service type, if it is registered.
func GetOpenAICompatibleProvider(serviceType string) (OpenAICompatibleProvider, bool) {
	p, ok := openAICompatibleProviders[serviceType]
	return p, ok
}
