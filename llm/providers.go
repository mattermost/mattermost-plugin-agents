// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "net/http"

// OpenAICompatibleProvider describes the configuration for an OpenAI-compatible
// provider that can be registered in the provider registry. Adding an entry to
// the registry is all that is needed to support a new provider — no changes to
// bots.go or api.go are required.
type OpenAICompatibleProvider struct {
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
	ServiceTypeCohere: {
		FixedAPIURL: "https://api.cohere.ai/compatibility/v1",
	},
	ServiceTypeMistral: {
		FixedAPIURL:          "https://api.mistral.ai/v1",
		DisableStreamOptions: true,
		UseMaxTokens:         true,
	},
	ServiceTypeScale: {
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
