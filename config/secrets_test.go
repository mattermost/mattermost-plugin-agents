// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsSecretPlaceholder(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "the placeholder itself", value: SecretPlaceholder, want: true},
		{name: "shorter run of asterisks", value: "**********", want: true},
		{name: "empty", value: "", want: false},
		{name: "credential", value: "sk-live-1234", want: false},
		{name: "credential containing asterisks", value: "sk-**-1234", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsSecretPlaceholder(tt.value))
		})
	}
}

func TestRedactSecrets(t *testing.T) {
	stored := Config{
		Services: []llm.ServiceConfig{{
			ID:                    "svc-1",
			APIKey:                "service-key",
			OrgID:                 "org-1",
			AWSAccessKeyID:        "AKIAEXAMPLE",
			AWSSecretAccessKey:    "aws-secret",
			VertexAuthCredentials: `{"type":"service_account"}`,
		}},
		Bots: []llm.BotConfig{{
			ID:      "bot-1",
			Service: &llm.ServiceConfig{ID: "svc-legacy", APIKey: "inline-key"},
		}},
		MCP: MCPConfig{Servers: []MCPServerConfig{{
			Name:         "Jira",
			BaseURL:      "https://mcp.example.com",
			ClientID:     "client-id",
			ClientSecret: "client-secret",
			Headers:      map[string]string{"Authorization": "Bearer header-token"},
		}}},
		WebSearch: WebSearchConfig{
			Google: WebSearchGoogleConfig{APIKey: "google-key", SearchEngineID: "engine-id"},
			Brave:  WebSearchBraveConfig{APIKey: ""},
		},
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			EmbeddingProvider: embeddings.UpstreamConfig{
				Parameters: json.RawMessage(`{"apiKey":"embedding-key","embeddingModel":"text-embedding-3-small"}`),
			},
		},
	}

	redacted := RedactSecrets(stored)

	tests := []struct {
		name string
		got  func(cfg Config) string
		want string
	}{
		{name: "service api key", got: func(cfg Config) string { return cfg.Services[0].APIKey }, want: SecretPlaceholder},
		{name: "service aws secret", got: func(cfg Config) string { return cfg.Services[0].AWSSecretAccessKey }, want: SecretPlaceholder},
		{name: "service vertex credentials", got: func(cfg Config) string { return cfg.Services[0].VertexAuthCredentials }, want: SecretPlaceholder},
		{name: "inline bot service api key", got: func(cfg Config) string { return cfg.Bots[0].Service.APIKey }, want: SecretPlaceholder},
		{name: "mcp client secret", got: func(cfg Config) string { return cfg.MCP.Servers[0].ClientSecret }, want: SecretPlaceholder},
		{name: "mcp header value", got: func(cfg Config) string { return cfg.MCP.Servers[0].Headers["Authorization"] }, want: SecretPlaceholder},
		{name: "google api key", got: func(cfg Config) string { return cfg.WebSearch.Google.APIKey }, want: SecretPlaceholder},
		{name: "unset brave api key stays empty", got: func(cfg Config) string { return cfg.WebSearch.Brave.APIKey }, want: ""},
		{name: "service org id", got: func(cfg Config) string { return cfg.Services[0].OrgID }, want: "org-1"},
		{name: "service aws access key id", got: func(cfg Config) string { return cfg.Services[0].AWSAccessKeyID }, want: "AKIAEXAMPLE"},
		{name: "mcp client id", got: func(cfg Config) string { return cfg.MCP.Servers[0].ClientID }, want: "client-id"},
		{name: "mcp base url", got: func(cfg Config) string { return cfg.MCP.Servers[0].BaseURL }, want: "https://mcp.example.com"},
		{name: "google search engine id", got: func(cfg Config) string { return cfg.WebSearch.Google.SearchEngineID }, want: "engine-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got(redacted))
		})
	}

	t.Run("embedding provider parameters", func(t *testing.T) {
		params := map[string]string{}
		require.NoError(t, json.Unmarshal(redacted.EmbeddingSearchConfig.EmbeddingProvider.Parameters, &params))
		assert.Equal(t, SecretPlaceholder, params["apiKey"])
		assert.Equal(t, "text-embedding-3-small", params["embeddingModel"])
	})

	t.Run("stored configuration is untouched", func(t *testing.T) {
		assert.Equal(t, "service-key", stored.Services[0].APIKey)
		assert.Equal(t, "inline-key", stored.Bots[0].Service.APIKey)
		assert.Equal(t, "Bearer header-token", stored.MCP.Servers[0].Headers["Authorization"])
	})
}

func TestRestoreSecrets(t *testing.T) {
	storedConfig := func() *Config {
		return &Config{
			Services: []llm.ServiceConfig{{
				ID:                    "svc-1",
				APIKey:                "service-key",
				AWSSecretAccessKey:    "aws-secret",
				VertexAuthCredentials: `{"type":"service_account"}`,
			}},
			Bots: []llm.BotConfig{{
				ID:      "bot-1",
				Service: &llm.ServiceConfig{ID: "svc-legacy", APIKey: "inline-key"},
			}},
			MCP: MCPConfig{Servers: []MCPServerConfig{{
				Name:         "Jira",
				ClientSecret: "client-secret",
				Headers: map[string]string{
					"Authorization": "Bearer header-token",
					"X-Tenant":      "tenant-1",
				},
			}}},
			WebSearch: WebSearchConfig{
				Google: WebSearchGoogleConfig{APIKey: "google-key"},
				Brave:  WebSearchBraveConfig{APIKey: "brave-key"},
			},
			EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
				EmbeddingProvider: embeddings.UpstreamConfig{
					Parameters: json.RawMessage(`{"apiKey":"embedding-key","embeddingModel":"text-embedding-3-small"}`),
				},
			},
		}
	}

	type credential struct {
		name string
		get  func(cfg Config) string
		set  func(cfg *Config, value string)
	}

	credentials := []credential{
		{
			name: "service api key",
			get:  func(cfg Config) string { return cfg.Services[0].APIKey },
			set:  func(cfg *Config, v string) { cfg.Services[0].APIKey = v },
		},
		{
			name: "service aws secret",
			get:  func(cfg Config) string { return cfg.Services[0].AWSSecretAccessKey },
			set:  func(cfg *Config, v string) { cfg.Services[0].AWSSecretAccessKey = v },
		},
		{
			name: "service vertex credentials",
			get:  func(cfg Config) string { return cfg.Services[0].VertexAuthCredentials },
			set:  func(cfg *Config, v string) { cfg.Services[0].VertexAuthCredentials = v },
		},
		{
			name: "inline bot service api key",
			get:  func(cfg Config) string { return cfg.Bots[0].Service.APIKey },
			set:  func(cfg *Config, v string) { cfg.Bots[0].Service.APIKey = v },
		},
		{
			name: "mcp client secret",
			get:  func(cfg Config) string { return cfg.MCP.Servers[0].ClientSecret },
			set:  func(cfg *Config, v string) { cfg.MCP.Servers[0].ClientSecret = v },
		},
		{
			name: "mcp header value",
			get:  func(cfg Config) string { return cfg.MCP.Servers[0].Headers["Authorization"] },
			set:  func(cfg *Config, v string) { cfg.MCP.Servers[0].Headers["Authorization"] = v },
		},
		{
			name: "google api key",
			get:  func(cfg Config) string { return cfg.WebSearch.Google.APIKey },
			set:  func(cfg *Config, v string) { cfg.WebSearch.Google.APIKey = v },
		},
		{
			name: "brave api key",
			get:  func(cfg Config) string { return cfg.WebSearch.Brave.APIKey },
			set:  func(cfg *Config, v string) { cfg.WebSearch.Brave.APIKey = v },
		},
	}

	tests := []struct {
		name     string
		incoming string
		want     string
	}{
		{name: "placeholder keeps the stored value", incoming: SecretPlaceholder, want: ""},
		{name: "explicit value replaces the stored value", incoming: "typed-value", want: "typed-value"},
		{name: "empty value clears the stored value", incoming: "", want: ""},
	}

	for _, tt := range tests {
		for _, cred := range credentials {
			t.Run(tt.name+"/"+cred.name, func(t *testing.T) {
				stored := storedConfig()
				incoming := *stored.Clone()
				cred.set(&incoming, tt.incoming)

				restored := RestoreSecrets(incoming, stored)

				want := tt.want
				if tt.incoming == SecretPlaceholder {
					want = cred.get(*stored)
				}
				assert.Equal(t, want, cred.get(restored))

				for _, other := range credentials {
					if other.name == cred.name {
						continue
					}
					assert.Equal(t, other.get(*stored), other.get(restored),
						"%s must keep its stored value", other.name)
				}
			})
		}
	}

	t.Run("embedding provider placeholder resolves and keeps other parameters", func(t *testing.T) {
		stored := storedConfig()
		incoming := *stored.Clone()
		incoming.EmbeddingSearchConfig.EmbeddingProvider.Parameters = json.RawMessage(
			`{"apiKey":"` + SecretPlaceholder + `","embeddingModel":"text-embedding-3-large"}`)

		restored := RestoreSecrets(incoming, stored)

		params := map[string]string{}
		require.NoError(t, json.Unmarshal(restored.EmbeddingSearchConfig.EmbeddingProvider.Parameters, &params))
		assert.Equal(t, "embedding-key", params["apiKey"])
		assert.Equal(t, "text-embedding-3-large", params["embeddingModel"])
	})

	t.Run("placeholder without a counterpart resolves to empty", func(t *testing.T) {
		incoming := Config{
			Services: []llm.ServiceConfig{{ID: "svc-new", APIKey: SecretPlaceholder}},
			MCP: MCPConfig{Servers: []MCPServerConfig{{
				Name:         "Confluence",
				ClientSecret: SecretPlaceholder,
				Headers:      map[string]string{"Authorization": SecretPlaceholder},
			}}},
			WebSearch: WebSearchConfig{Google: WebSearchGoogleConfig{APIKey: SecretPlaceholder}},
			EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
				EmbeddingProvider: embeddings.UpstreamConfig{
					Parameters: json.RawMessage(`{"apiKey":"` + SecretPlaceholder + `"}`),
				},
			},
		}

		restored := RestoreSecrets(incoming, nil)

		assert.Empty(t, restored.Services[0].APIKey)
		assert.Empty(t, restored.MCP.Servers[0].ClientSecret)
		assert.Empty(t, restored.MCP.Servers[0].Headers["Authorization"])
		assert.Empty(t, restored.WebSearch.Google.APIKey)

		params := map[string]string{}
		require.NoError(t, json.Unmarshal(restored.EmbeddingSearchConfig.EmbeddingProvider.Parameters, &params))
		assert.Empty(t, params["apiKey"])
	})

	t.Run("incoming configuration is untouched", func(t *testing.T) {
		stored := storedConfig()
		incoming := *stored.Clone()
		incoming.Services[0].APIKey = SecretPlaceholder

		RestoreSecrets(incoming, stored)

		assert.Equal(t, SecretPlaceholder, incoming.Services[0].APIKey)
	})
}
