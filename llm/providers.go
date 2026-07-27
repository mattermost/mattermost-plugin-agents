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
}

// openAICompatibleProviders is the registry of known OpenAI-compatible providers.
var openAICompatibleProviders = map[string]OpenAICompatibleProvider{
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
	// OpenCode Go is the managed subscription tier of the OpenCode coding
	// agent. It exposes an OpenAI-compatible chat-completions endpoint at
	// https://opencode.ai/zen/go/v1 using standard Bearer auth, so the
	// default transport is sufficient — no CreateTransport hook is needed.
	// The 16 model IDs the gateway actually serves (Grok 4.5, GLM-5.2,
	// GLM-5.1, Kimi K3, Kimi K2.7 Code, Kimi K2.6, MiMo-V2.5-Pro, MiMo-V2.5,
	// Qwen3.7 Max, Qwen3.7 Plus, Qwen3.6 Plus, MiniMax M2.7, MiniMax M3,
	// DeepSeek V4 Pro, DeepSeek V4 Flash, Hy3) are lowercase-hyphenated —
	// e.g. "kimi-k3" — never "gpt-4o" or other upstream-native names.
	ServiceTypeOpenCodeGo: {
		DefaultModel: "kimi-k3",
	},
}

// GetOpenAICompatibleProvider returns the provider configuration for the given
// service type, if it is registered.
func GetOpenAICompatibleProvider(serviceType string) (OpenAICompatibleProvider, bool) {
	p, ok := openAICompatibleProviders[serviceType]
	return p, ok
}
