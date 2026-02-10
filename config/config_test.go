// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Sanitize(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		check  func(t *testing.T, c *Config)
	}{
		{
			name: "sanitizes service API keys",
			config: Config{
				Services: []llm.ServiceConfig{
					{ID: "svc1", APIKey: "sk-secret-key", AWSAccessKeyID: "AKIAIOSFODNN7", AWSSecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCY"},
					{ID: "svc2", APIKey: "sk-another-key"},
					{ID: "svc3"},
				},
			},
			check: func(t *testing.T, c *Config) {
				assert.Equal(t, redactedValue, c.Services[0].APIKey)
				assert.Equal(t, redactedValue, c.Services[0].AWSAccessKeyID)
				assert.Equal(t, redactedValue, c.Services[0].AWSSecretAccessKey)
				assert.Equal(t, redactedValue, c.Services[1].APIKey)
				assert.Empty(t, c.Services[2].APIKey)
			},
		},
		{
			name: "sanitizes web search Google API key",
			config: Config{
				WebSearch: WebSearchConfig{
					Google: WebSearchGoogleConfig{
						APIKey:         "google-api-key-123",
						SearchEngineID: "search-engine-id",
					},
				},
			},
			check: func(t *testing.T, c *Config) {
				assert.Equal(t, redactedValue, c.WebSearch.Google.APIKey)
				assert.Equal(t, "search-engine-id", c.WebSearch.Google.SearchEngineID)
			},
		},
		{
			name: "sanitizes web search Brave API key",
			config: Config{
				WebSearch: WebSearchConfig{
					Brave: WebSearchBraveConfig{
						APIKey: "brave-api-key-456",
						APIURL: "https://api.brave.com",
					},
				},
			},
			check: func(t *testing.T, c *Config) {
				assert.Equal(t, redactedValue, c.WebSearch.Brave.APIKey)
				assert.Equal(t, "https://api.brave.com", c.WebSearch.Brave.APIURL)
			},
		},
		{
			name: "sanitizes embedding provider parameters",
			config: Config{
				EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
					EmbeddingProvider: embeddings.UpstreamConfig{
						Type:       "openai",
						Parameters: json.RawMessage(`{"apiKey":"sk-embed-secret","embeddingModel":"text-embedding-3-small"}`),
					},
				},
			},
			check: func(t *testing.T, c *Config) {
				assert.Nil(t, c.EmbeddingSearchConfig.EmbeddingProvider.Parameters)
				assert.Equal(t, "openai", c.EmbeddingSearchConfig.EmbeddingProvider.Type)
			},
		},
		{
			name: "sanitizes MCP server headers",
			config: Config{
				MCP: mcp.Config{
					Servers: []mcp.ServerConfig{
						{
							Name: "server1",
							Headers: map[string]string{
								"Authorization": "Bearer secret-token",
								"X-Api-Key":     "another-secret",
							},
						},
						{
							Name: "server2",
						},
					},
				},
			},
			check: func(t *testing.T, c *Config) {
				assert.Equal(t, redactedValue, c.MCP.Servers[0].Headers["Authorization"])
				assert.Equal(t, redactedValue, c.MCP.Servers[0].Headers["X-Api-Key"])
				assert.Empty(t, c.MCP.Servers[1].Headers)
			},
		},
		{
			name: "sanitizes deprecated bot inline service config",
			config: Config{
				Bots: []llm.BotConfig{
					{
						Name:        "bot1",
						DisplayName: "Bot 1",
						ServiceID:   "svc1",
						Service: &llm.ServiceConfig{
							APIKey:             "inline-secret",
							AWSAccessKeyID:     "AKIA-INLINE",
							AWSSecretAccessKey: "inline-aws-secret",
						},
					},
					{
						Name:        "bot2",
						DisplayName: "Bot 2",
						ServiceID:   "svc2",
					},
				},
			},
			check: func(t *testing.T, c *Config) {
				require.NotNil(t, c.Bots[0].Service)
				assert.Equal(t, redactedValue, c.Bots[0].Service.APIKey)
				assert.Equal(t, redactedValue, c.Bots[0].Service.AWSAccessKeyID)
				assert.Equal(t, redactedValue, c.Bots[0].Service.AWSSecretAccessKey)
				assert.Nil(t, c.Bots[1].Service)
			},
		},
		{
			name: "preserves non-sensitive fields",
			config: Config{
				DefaultBotName:   "copilot",
				EnableLLMTrace:   true,
				AllowUnsafeLinks: false,
				Services: []llm.ServiceConfig{
					{ID: "svc1", Name: "OpenAI", Type: "openai", APIKey: "secret", DefaultModel: "gpt-4"},
				},
				WebSearch: WebSearchConfig{
					Enabled:  true,
					Provider: "google",
					Google: WebSearchGoogleConfig{
						APIKey:         "google-key",
						SearchEngineID: "engine-id",
						ResultLimit:    10,
					},
				},
			},
			check: func(t *testing.T, c *Config) {
				assert.Equal(t, "copilot", c.DefaultBotName)
				assert.True(t, c.EnableLLMTrace)
				assert.Equal(t, "svc1", c.Services[0].ID)
				assert.Equal(t, "OpenAI", c.Services[0].Name)
				assert.Equal(t, "openai", c.Services[0].Type)
				assert.Equal(t, "gpt-4", c.Services[0].DefaultModel)
				assert.True(t, c.WebSearch.Enabled)
				assert.Equal(t, "google", c.WebSearch.Provider)
				assert.Equal(t, "engine-id", c.WebSearch.Google.SearchEngineID)
				assert.Equal(t, 10, c.WebSearch.Google.ResultLimit)
			},
		},
		{
			name:   "handles empty config",
			config: Config{},
			check: func(t *testing.T, c *Config) {
				assert.Empty(t, c.Services)
				assert.Empty(t, c.WebSearch.Google.APIKey)
				assert.Empty(t, c.WebSearch.Brave.APIKey)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.config
			cfg.Sanitize()
			tt.check(t, &cfg)
		})
	}
}

func TestConfig_Sanitize_DoesNotModifyOriginal(t *testing.T) {
	original := Config{
		Services: []llm.ServiceConfig{
			{ID: "svc1", APIKey: "original-secret"},
		},
		WebSearch: WebSearchConfig{
			Google: WebSearchGoogleConfig{APIKey: "google-secret"},
		},
	}

	cloned := original.Clone()
	cloned.Sanitize()

	assert.Equal(t, "original-secret", original.Services[0].APIKey)
	assert.Equal(t, "google-secret", original.WebSearch.Google.APIKey)
	assert.Equal(t, redactedValue, cloned.Services[0].APIKey)
	assert.Equal(t, redactedValue, cloned.WebSearch.Google.APIKey)
}
