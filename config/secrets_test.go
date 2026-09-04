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

func TestRedactSecrets(t *testing.T) {
	stored := Config{
		Services: []llm.ServiceConfig{{
			ID:                    "service-1",
			Name:                  "provider",
			APIKey:                "service-api-key",
			AWSAccessKeyID:        "access-key-id",
			AWSSecretAccessKey:    "service-aws-secret",
			VertexAuthCredentials: "service-vertex-credentials",
		}},
		Bots: []llm.BotConfig{{
			ID: "bot-1",
			Service: &llm.ServiceConfig{
				ID:                    "embedded-service",
				APIKey:                "embedded-api-key",
				AWSSecretAccessKey:    "embedded-aws-secret",
				VertexAuthCredentials: "embedded-vertex-credentials",
			},
		}},
		WebSearch: WebSearchConfig{
			Google: WebSearchGoogleConfig{APIKey: "google-api-key", SearchEngineID: "engine-1"},
			Brave:  WebSearchBraveConfig{APIKey: "brave-api-key"},
		},
		MCP: MCPConfig{Servers: []MCPServerConfig{{
			Name:                  "server-1",
			BaseURL:               "https://mcp.example.com",
			ClientID:              "client-id",
			ClientSecret:          "client-secret",
			Headers:               map[string]string{"Authorization": "header-secret", "Empty": ""},
			ServiceAccountHeaders: map[string]string{"X-Service-Key": "service-header-secret"},
		}}},
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			EmbeddingProvider: embeddings.UpstreamConfig{
				Parameters: json.RawMessage(`{
					"apiKey":"embedding-api-key",
					"awsSecretAccessKey":"embedding-aws-secret",
					"vertexAuthCredentials":"embedding-vertex-credentials",
					"embeddingModel":"model-1",
					"options":{"dimensions":1536}
				}`),
			},
		},
		DefaultBotName: "bot-1",
	}

	redacted := RedactSecrets(stored)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "service api key", got: redacted.Services[0].APIKey, want: FakeSetting},
		{name: "service aws secret", got: redacted.Services[0].AWSSecretAccessKey, want: FakeSetting},
		{name: "service vertex credentials", got: redacted.Services[0].VertexAuthCredentials, want: FakeSetting},
		{name: "embedded service api key", got: redacted.Bots[0].Service.APIKey, want: FakeSetting},
		{name: "embedded service aws secret", got: redacted.Bots[0].Service.AWSSecretAccessKey, want: FakeSetting},
		{name: "embedded service vertex credentials", got: redacted.Bots[0].Service.VertexAuthCredentials, want: FakeSetting},
		{name: "google api key", got: redacted.WebSearch.Google.APIKey, want: FakeSetting},
		{name: "brave api key", got: redacted.WebSearch.Brave.APIKey, want: FakeSetting},
		{name: "mcp client secret", got: redacted.MCP.Servers[0].ClientSecret, want: FakeSetting},
		{name: "mcp header", got: redacted.MCP.Servers[0].Headers["Authorization"], want: FakeSetting},
		{name: "empty mcp header", got: redacted.MCP.Servers[0].Headers["Empty"], want: ""},
		{name: "mcp service account header", got: redacted.MCP.Servers[0].ServiceAccountHeaders["X-Service-Key"], want: FakeSetting},
		{name: "aws access key id is unchanged", got: redacted.Services[0].AWSAccessKeyID, want: "access-key-id"},
		{name: "mcp client id is unchanged", got: redacted.MCP.Servers[0].ClientID, want: "client-id"},
		{name: "non-secret field is unchanged", got: redacted.DefaultBotName, want: "bot-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}

	var params map[string]any
	require.NoError(t, json.Unmarshal(redacted.EmbeddingSearchConfig.EmbeddingProvider.Parameters, &params))
	assert.Equal(t, FakeSetting, params["apiKey"])
	assert.Equal(t, FakeSetting, params["awsSecretAccessKey"])
	assert.Equal(t, FakeSetting, params["vertexAuthCredentials"])
	assert.Equal(t, "model-1", params["embeddingModel"])
	assert.Equal(t, map[string]any{"dimensions": float64(1536)}, params["options"])

	t.Run("does not mutate or alias input", func(t *testing.T) {
		redacted.Services[0].Name = "changed"
		redacted.Bots[0].Service.ID = "changed"
		redacted.MCP.Servers[0].Headers["Authorization"] = "changed"
		redacted.MCP.Servers[0].ServiceAccountHeaders["X-Service-Key"] = "changed"

		assert.Equal(t, "provider", stored.Services[0].Name)
		assert.Equal(t, "embedded-service", stored.Bots[0].Service.ID)
		assert.Equal(t, "header-secret", stored.MCP.Servers[0].Headers["Authorization"])
		assert.Equal(t, "service-header-secret", stored.MCP.Servers[0].ServiceAccountHeaders["X-Service-Key"])
		assert.Contains(t, string(stored.EmbeddingSearchConfig.EmbeddingProvider.Parameters), "embedding-api-key")
	})
}

func TestRedactSecretsInvalidEmbeddingParameters(t *testing.T) {
	cfg := Config{
		DefaultBotName: "bot-1",
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			EmbeddingProvider: embeddings.UpstreamConfig{
				Parameters: json.RawMessage(`{"apiKey":"stored-value`),
			},
		},
	}

	redacted := RedactSecrets(cfg)
	assert.Equal(t, "bot-1", redacted.DefaultBotName)
	assert.JSONEq(t, "null", string(redacted.EmbeddingSearchConfig.EmbeddingProvider.Parameters))
}

func TestEmbeddingNonStringCredentials(t *testing.T) {
	parameters := json.RawMessage(`{
		"apiKey":{"privateObject":"value"},
		"awsSecretAccessKey":{"privateObject":"value"},
		"vertexAuthCredentials":{"privateObject":"value"},
		"embeddingModel":"model-visible"
	}`)
	stored := Config{
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			EmbeddingProvider: embeddings.UpstreamConfig{Parameters: parameters},
		},
	}

	redacted := RedactSecrets(stored)
	var params map[string]any
	require.NoError(t, json.Unmarshal(redacted.EmbeddingSearchConfig.EmbeddingProvider.Parameters, &params))
	for _, field := range embeddingCredentialFields {
		assert.Equal(t, FakeSetting, params[field])
	}
	assert.Equal(t, "model-visible", params["embeddingModel"])
	assert.NotContains(t, string(redacted.EmbeddingSearchConfig.EmbeddingProvider.Parameters), "privateObject")

	restored := RestoreSecrets(redacted, stored)
	assert.JSONEq(t, string(parameters), string(restored.EmbeddingSearchConfig.EmbeddingProvider.Parameters))
	assert.JSONEq(t, string(parameters), string(stored.EmbeddingSearchConfig.EmbeddingProvider.Parameters))

	t.Run("explicit empty strings remain empty", func(t *testing.T) {
		emptyParameters := json.RawMessage(`{"apiKey":"","awsSecretAccessKey":"","vertexAuthCredentials":""}`)
		emptyStored := Config{
			EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
				EmbeddingProvider: embeddings.UpstreamConfig{Parameters: emptyParameters},
			},
		}

		assert.JSONEq(t, string(emptyParameters), string(RedactSecrets(emptyStored).EmbeddingSearchConfig.EmbeddingProvider.Parameters))
	})

	t.Run("incoming non-placeholder remains incoming", func(t *testing.T) {
		incomingParameters := json.RawMessage(`{"apiKey":{"incoming":"value"}}`)
		incoming := Config{
			EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
				EmbeddingProvider: embeddings.UpstreamConfig{Parameters: incomingParameters},
			},
		}

		restoredIncoming := RestoreSecrets(incoming, stored)
		assert.JSONEq(t, string(incomingParameters), string(restoredIncoming.EmbeddingSearchConfig.EmbeddingProvider.Parameters))
	})
}

func TestRestoreSecrets(t *testing.T) {
	storedConfig := func() Config {
		return Config{
			Services: []llm.ServiceConfig{{
				ID:                    "service-1",
				APIKey:                "stored-api-key",
				AWSSecretAccessKey:    "stored-aws-secret",
				VertexAuthCredentials: "stored-vertex-credentials",
			}},
			Bots: []llm.BotConfig{{
				ID: "bot-1",
				Service: &llm.ServiceConfig{
					APIKey:                "stored-embedded-api-key",
					AWSSecretAccessKey:    "stored-embedded-aws-secret",
					VertexAuthCredentials: "stored-embedded-vertex-credentials",
				},
			}},
			WebSearch: WebSearchConfig{
				Google: WebSearchGoogleConfig{APIKey: "stored-google-key"},
				Brave:  WebSearchBraveConfig{APIKey: "stored-brave-key"},
			},
			MCP: MCPConfig{Servers: []MCPServerConfig{{
				Name:                  "server-1",
				ClientSecret:          "stored-client-secret",
				Headers:               map[string]string{"Authorization": "stored-header", "Removed": "removed-header"},
				ServiceAccountHeaders: map[string]string{"X-Service-Key": "stored-service-header", "Removed-Service": "removed-service-header"},
			}}},
		}
	}

	t.Run("placeholders restore the secret inventory", func(t *testing.T) {
		stored := storedConfig()
		incoming := *stored.Clone()
		incoming.Services[0].APIKey = FakeSetting
		incoming.Services[0].AWSSecretAccessKey = FakeSetting
		incoming.Services[0].VertexAuthCredentials = FakeSetting
		incoming.Bots[0].Service.APIKey = FakeSetting
		incoming.Bots[0].Service.AWSSecretAccessKey = FakeSetting
		incoming.Bots[0].Service.VertexAuthCredentials = FakeSetting
		incoming.WebSearch.Google.APIKey = FakeSetting
		incoming.WebSearch.Brave.APIKey = FakeSetting
		incoming.MCP.Servers[0].ClientSecret = FakeSetting
		incoming.MCP.Servers[0].Headers["Authorization"] = FakeSetting
		incoming.MCP.Servers[0].ServiceAccountHeaders["X-Service-Key"] = FakeSetting

		restored := RestoreSecrets(incoming, stored)

		assert.Equal(t, stored.Services[0].APIKey, restored.Services[0].APIKey)
		assert.Equal(t, stored.Services[0].AWSSecretAccessKey, restored.Services[0].AWSSecretAccessKey)
		assert.Equal(t, stored.Services[0].VertexAuthCredentials, restored.Services[0].VertexAuthCredentials)
		assert.Equal(t, stored.Bots[0].Service.APIKey, restored.Bots[0].Service.APIKey)
		assert.Equal(t, stored.Bots[0].Service.AWSSecretAccessKey, restored.Bots[0].Service.AWSSecretAccessKey)
		assert.Equal(t, stored.Bots[0].Service.VertexAuthCredentials, restored.Bots[0].Service.VertexAuthCredentials)
		assert.Equal(t, stored.WebSearch.Google.APIKey, restored.WebSearch.Google.APIKey)
		assert.Equal(t, stored.WebSearch.Brave.APIKey, restored.WebSearch.Brave.APIKey)
		assert.Equal(t, stored.MCP.Servers[0].ClientSecret, restored.MCP.Servers[0].ClientSecret)
		assert.Equal(t, stored.MCP.Servers[0].Headers["Authorization"], restored.MCP.Servers[0].Headers["Authorization"])
		assert.Equal(t, stored.MCP.Servers[0].ServiceAccountHeaders["X-Service-Key"], restored.MCP.Servers[0].ServiceAccountHeaders["X-Service-Key"])
	})

	cases := []struct {
		name     string
		incoming string
		want     string
	}{
		{name: "placeholder restores", incoming: FakeSetting, want: "stored-api-key"},
		{name: "empty clears", incoming: "", want: ""},
		{name: "new value replaces", incoming: "replacement", want: "replacement"},
		{name: "other asterisks replace", incoming: "********", want: "********"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := storedConfig()
			incoming := *stored.Clone()
			incoming.Services[0].APIKey = tc.incoming

			restored := RestoreSecrets(incoming, stored)

			assert.Equal(t, tc.want, restored.Services[0].APIKey)
			assert.Equal(t, tc.incoming, incoming.Services[0].APIKey, "incoming must remain unchanged")
		})
	}

	t.Run("new objects cannot inherit stored values", func(t *testing.T) {
		stored := storedConfig()
		incoming := Config{
			Services: []llm.ServiceConfig{{ID: "service-new", APIKey: FakeSetting}},
			Bots:     []llm.BotConfig{{ID: "bot-new", Service: &llm.ServiceConfig{APIKey: FakeSetting}}},
			MCP: MCPConfig{Servers: []MCPServerConfig{{
				Name:                  "server-new",
				ClientSecret:          FakeSetting,
				Headers:               map[string]string{"Authorization": FakeSetting},
				ServiceAccountHeaders: map[string]string{"X-Service-Key": FakeSetting},
			}}},
		}

		restored := RestoreSecrets(incoming, stored)

		assert.Empty(t, restored.Services[0].APIKey)
		assert.Empty(t, restored.Bots[0].Service.APIKey)
		assert.Empty(t, restored.MCP.Servers[0].ClientSecret)
		assert.Empty(t, restored.MCP.Servers[0].Headers["Authorization"])
		assert.Empty(t, restored.MCP.Servers[0].ServiceAccountHeaders["X-Service-Key"])
	})

	t.Run("header maps restore by key", func(t *testing.T) {
		stored := storedConfig()
		incoming := *stored.Clone()
		incoming.MCP.Servers[0].Headers = map[string]string{
			"Authorization": FakeSetting,
			"New":           "new-header",
		}
		incoming.MCP.Servers[0].ServiceAccountHeaders = map[string]string{
			"X-Service-Key": FakeSetting,
			"New-Service":   "new-service-header",
		}

		restored := RestoreSecrets(incoming, stored)

		assert.Equal(t, map[string]string{
			"Authorization": "stored-header",
			"New":           "new-header",
		}, restored.MCP.Servers[0].Headers)
		assert.NotContains(t, restored.MCP.Servers[0].Headers, "Removed")
		assert.Equal(t, map[string]string{
			"X-Service-Key": "stored-service-header",
			"New-Service":   "new-service-header",
		}, restored.MCP.Servers[0].ServiceAccountHeaders)
		assert.NotContains(t, restored.MCP.Servers[0].ServiceAccountHeaders, "Removed-Service")
	})

	t.Run("mcp server match follows endpoint", func(t *testing.T) {
		stored := storedConfig()
		stored.MCP.Servers[0].BaseURL = "https://mcp.example.com"

		placeholderServer := func(name, baseURL string) MCPServerConfig {
			return MCPServerConfig{
				Name:                  name,
				BaseURL:               baseURL,
				ClientSecret:          FakeSetting,
				Headers:               map[string]string{"Authorization": FakeSetting},
				ServiceAccountHeaders: map[string]string{"X-Service-Key": FakeSetting},
			}
		}

		tests := []struct {
			name   string
			server MCPServerConfig
			secret string
			header string
			svcHdr string
		}{
			{name: "same server", server: placeholderServer("server-1", "https://mcp.example.com"), secret: "stored-client-secret", header: "stored-header", svcHdr: "stored-service-header"},
			{name: "renamed server at equivalent endpoint", server: placeholderServer("server-renamed", " HTTPS://MCP.EXAMPLE.COM:443/ "), secret: "stored-client-secret", header: "stored-header", svcHdr: "stored-service-header"},
			{name: "same name at new endpoint", server: placeholderServer("server-1", "https://other.example.com")},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				incoming := Config{MCP: MCPConfig{Servers: []MCPServerConfig{tt.server}}}
				restored := RestoreSecrets(incoming, stored)
				assert.Equal(t, tt.secret, restored.MCP.Servers[0].ClientSecret)
				assert.Equal(t, tt.header, restored.MCP.Servers[0].Headers["Authorization"])
				assert.Equal(t, tt.svcHdr, restored.MCP.Servers[0].ServiceAccountHeaders["X-Service-Key"])
			})
		}

		t.Run("same name takes priority over ambiguous canonical fallback", func(t *testing.T) {
			stored.MCP.Servers = append(stored.MCP.Servers, MCPServerConfig{
				Name:         "server-2",
				BaseURL:      "https://MCP.EXAMPLE.COM:443/",
				ClientSecret: "other-client-secret",
			})

			incoming := Config{MCP: MCPConfig{Servers: []MCPServerConfig{{
				Name:         "server-1",
				BaseURL:      "https://MCP.EXAMPLE.COM:443/",
				ClientSecret: FakeSetting,
			}}}}
			restored := RestoreSecrets(incoming, stored)
			assert.Equal(t, "stored-client-secret", restored.MCP.Servers[0].ClientSecret)

			incoming.MCP.Servers[0].Name = "server-renamed"
			restored = RestoreSecrets(incoming, stored)
			assert.Empty(t, restored.MCP.Servers[0].ClientSecret)
		})
	})
}

func TestRestoreEmbeddingSecrets(t *testing.T) {
	stored := Config{
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			EmbeddingProvider: embeddings.UpstreamConfig{
				Parameters: json.RawMessage(`{
					"apiKey":"stored-api-key",
					"awsSecretAccessKey":"stored-aws-secret",
					"vertexAuthCredentials":"stored-vertex-credentials",
					"embeddingModel":"model-1"
				}`),
			},
		},
	}

	tests := []struct {
		name        string
		incoming    string
		want        string
		emptyStored bool
	}{
		{
			name: "mixed restore clear replace and keep options",
			incoming: `{
				"apiKey":"********************************",
				"awsSecretAccessKey":"",
				"vertexAuthCredentials":"replacement-vertex",
				"embeddingModel":"model-2",
				"options":{"dimensions":3072},
				"unknown":"kept"
			}`,
			want: `{
				"apiKey":"stored-api-key",
				"awsSecretAccessKey":"",
				"vertexAuthCredentials":"replacement-vertex",
				"embeddingModel":"model-2",
				"options":{"dimensions":3072},
				"unknown":"kept"
			}`,
		},
		{
			name:        "new config placeholder becomes empty",
			incoming:    `{"apiKey":"********************************","embeddingModel":"model-2"}`,
			want:        `{"apiKey":"","embeddingModel":"model-2"}`,
			emptyStored: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storedForCase := stored
			if tt.emptyStored {
				storedForCase = Config{}
			}
			incoming := Config{
				EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
					EmbeddingProvider: embeddings.UpstreamConfig{Parameters: json.RawMessage(tt.incoming)},
				},
			}

			restored := RestoreSecrets(incoming, storedForCase)

			assert.JSONEq(t, tt.want, string(restored.EmbeddingSearchConfig.EmbeddingProvider.Parameters))
			assert.JSONEq(t, tt.incoming, string(incoming.EmbeddingSearchConfig.EmbeddingProvider.Parameters))
		})
	}
}
