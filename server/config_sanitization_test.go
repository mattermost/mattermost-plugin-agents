// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// pluginManifestPath is the shipped manifest, read from disk so these tests pin
// the artifact that is actually published rather than a copy of it.
const pluginManifestPath = "../plugin.json"

// credentialSentinel pairs a human-readable JSON path with the unique value
// planted at that path by credentialSentinelConfig.
type credentialSentinel struct {
	jsonPath string
	value    string
}

// credentialSentinels lists every configuration value that can hold a credential,
// each with the sentinel planted at it. Keep in sync with credentialSentinelConfig.
func credentialSentinels() []credentialSentinel {
	return []credentialSentinel{
		{"config.services[0].apiKey", "sentinel-service-api-key"},
		{"config.services[0].awsAccessKeyID", "sentinel-service-aws-access-key-id"},
		{"config.services[0].awsSecretAccessKey", "sentinel-service-aws-secret-access-key"},
		{"config.services[0].vertexAuthCredentials", "sentinel-service-vertex-auth-credentials"},
		{"config.services[1].apiKey", "sentinel-second-service-api-key"},
		{"config.bots[0].service.apiKey", "sentinel-bot-service-api-key"},
		{"config.bots[0].service.awsAccessKeyID", "sentinel-bot-service-aws-access-key-id"},
		{"config.bots[0].service.awsSecretAccessKey", "sentinel-bot-service-aws-secret-access-key"},
		{"config.bots[0].service.vertexAuthCredentials", "sentinel-bot-service-vertex-auth-credentials"},
		{"config.mcp.servers[0].clientSecret", "sentinel-mcp-client-secret"},
		{"config.mcp.servers[0].headers", "sentinel-mcp-header-value"},
		{"config.mcp.servers[0].serviceAccountHeaders", "sentinel-mcp-service-account-header-value"},
		{"config.mcp.servers[1].serviceAccountHeaders", "sentinel-second-mcp-service-account-header-value"},
		{"config.webSearch.google.apiKey", "sentinel-websearch-google-api-key"},
		{"config.webSearch.brave.apiKey", "sentinel-websearch-brave-api-key"},
		{"config.embeddingSearchConfig.embeddingProvider.parameters.apiKey", "sentinel-embedding-provider-api-key"},
	}
}

// credentialSentinelConfig returns a fully-populated plugin configuration in which
// every credential-bearing field carries the sentinel listed in credentialSentinels.
// Non-credential fields are populated too so the same fixture drives the
// diagnostics assertions.
func credentialSentinelConfig() *config.Config {
	return &config.Config{
		Services: []llm.ServiceConfig{
			{
				ID:                    "service-bedrock",
				Name:                  "Bedrock",
				Type:                  llm.ServiceTypeBedrock,
				APIKey:                "sentinel-service-api-key",
				Region:                "us-east-1",
				AWSAccessKeyID:        "sentinel-service-aws-access-key-id",
				AWSSecretAccessKey:    "sentinel-service-aws-secret-access-key",
				VertexProjectID:       "vertex-project",
				VertexAuthCredentials: "sentinel-service-vertex-auth-credentials",
			},
			{
				ID:     "service-openai",
				Name:   "OpenAI",
				Type:   llm.ServiceTypeOpenAI,
				APIKey: "sentinel-second-service-api-key",
			},
		},
		Bots: []llm.BotConfig{
			{
				ID:          "bot-1",
				Name:        "agent",
				DisplayName: "Agent",
				ServiceID:   "service-openai",
				// Service is the deprecated inline service kept for migration; it
				// carries the same credential fields as a top-level service.
				Service: &llm.ServiceConfig{
					ID:                    "inline-service",
					Type:                  llm.ServiceTypeOpenAI,
					APIKey:                "sentinel-bot-service-api-key",
					AWSAccessKeyID:        "sentinel-bot-service-aws-access-key-id",
					AWSSecretAccessKey:    "sentinel-bot-service-aws-secret-access-key",
					VertexAuthCredentials: "sentinel-bot-service-vertex-auth-credentials",
				},
			},
		},
		DefaultBotName:                  "agent",
		EnableCallSummary:               true,
		EnableTokenUsageLogging:         true,
		EnableChannelMentionToolCalling: true,
		AllowNativeWebSearchInChannels:  true,
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			Type:       embeddings.SearchTypeComposite,
			Dimensions: 1536,
			VectorStore: embeddings.UpstreamConfig{
				Type:       embeddings.VectorStoreTypePGVector,
				Parameters: json.RawMessage(`{"dimensions":1536}`),
			},
			EmbeddingProvider: embeddings.UpstreamConfig{
				Type:       embeddings.ProviderTypeOpenAI,
				Parameters: json.RawMessage(`{"embeddingModel":"text-embedding-3-small","apiKey":"sentinel-embedding-provider-api-key"}`),
			},
		},
		MCP: config.MCPConfig{
			Enabled: true,
			Servers: []config.MCPServerConfig{
				{
					ID:                    "mcp-1",
					Name:                  "jira",
					Enabled:               true,
					BaseURL:               "https://mcp.example.com",
					Headers:               map[string]string{"X-Api-Key": "sentinel-mcp-header-value"},
					ServiceAccountHeaders: map[string]string{"Authorization": "sentinel-mcp-service-account-header-value"},
					ClientID:              "mcp-client",
					ClientSecret:          "sentinel-mcp-client-secret",
				},
				{
					ID:                    "mcp-2",
					Name:                  "github",
					Enabled:               false,
					BaseURL:               "https://mcp2.example.com",
					ServiceAccountHeaders: map[string]string{"Authorization": "sentinel-second-mcp-service-account-header-value"},
				},
			},
		},
		WebSearch: config.WebSearchConfig{
			Enabled:  true,
			Provider: "google",
			Google: config.WebSearchGoogleConfig{
				APIKey:         "sentinel-websearch-google-api-key",
				SearchEngineID: "engine-id",
			},
			Brave: config.WebSearchBraveConfig{
				APIKey: "sentinel-websearch-brave-api-key",
			},
		},
		TelemetryOutput: "logs",
	}
}

// readPluginManifest parses the shipped plugin.json into a manifest.
func readPluginManifest(t *testing.T) *model.Manifest {
	t.Helper()

	data, err := os.ReadFile(pluginManifestPath)
	require.NoError(t, err)

	var parsed model.Manifest
	require.NoError(t, json.Unmarshal(data, &parsed))

	return &parsed
}

// serverConfigWithPluginConfig reproduces how the server stores the plugin's
// configuration: the whole blob lives under PluginSettings.Plugins[<plugin id>]
// keyed by the lowercased manifest setting key.
func serverConfigWithPluginConfig(t *testing.T, pluginID string, cfg *config.Config) *model.Config {
	t.Helper()

	encoded, err := json.Marshal(configuration{Config: *cfg})
	require.NoError(t, err)

	var stored map[string]any
	require.NoError(t, json.Unmarshal(encoded, &stored))
	require.Contains(t, stored, "config", "the plugin configuration is stored under the lowercased manifest setting key")

	serverCfg := &model.Config{}
	serverCfg.SetDefaults()
	serverCfg.PluginSettings.Plugins = map[string]map[string]any{
		pluginID: stored,
	}

	return serverCfg
}

// TestPluginConfigSanitization asserts that the plugin configuration stored in the
// server configuration is redacted by the server's own configuration sanitization,
// which is driven entirely by the shipped manifest.
func TestPluginConfigSanitization(t *testing.T) {
	pluginManifest := readPluginManifest(t)
	serverCfg := serverConfigWithPluginConfig(t, pluginManifest.Id, credentialSentinelConfig())

	before, err := json.Marshal(serverCfg.PluginSettings)
	require.NoError(t, err)

	serverCfg.Sanitize([]*model.Manifest{pluginManifest}, nil)

	after, err := json.Marshal(serverCfg.PluginSettings)
	require.NoError(t, err)

	for _, sentinel := range credentialSentinels() {
		t.Run(sentinel.jsonPath, func(t *testing.T) {
			require.True(t, strings.Contains(string(before), sentinel.value),
				"fixture must place the sentinel at %s in the stored configuration", sentinel.jsonPath)
			assert.False(t, strings.Contains(string(after), sentinel.value),
				"value at %s must not survive configuration sanitization", sentinel.jsonPath)
		})
	}
}

// TestPluginManifestAndDiagnostics asserts that the plugin stays installable and
// keeps contributing its non-secret operational data: the manifest remains valid
// and still declares the setting the System Console UI is registered against, the
// plugin entry survives sanitization, and the diagnostics file keeps its fields.
func TestPluginManifestAndDiagnostics(t *testing.T) {
	t.Run("manifest remains valid and declares the Config setting", func(t *testing.T) {
		pluginManifest := readPluginManifest(t)

		require.NoError(t, pluginManifest.IsValid())
		assert.Equal(t, "mattermost-ai", pluginManifest.Id)

		require.NotNil(t, pluginManifest.SettingsSchema)
		var found bool
		for _, setting := range pluginManifest.SettingsSchema.Settings {
			if setting.Key == "Config" {
				found = true
				assert.Equal(t, "custom", setting.Type,
					"the System Console registers a custom component for this setting")
			}
		}
		assert.True(t, found, "the manifest must declare the Config setting")
	})

	t.Run("plugin entry remains listed after sanitization", func(t *testing.T) {
		pluginManifest := readPluginManifest(t)
		serverCfg := serverConfigWithPluginConfig(t, pluginManifest.Id, credentialSentinelConfig())

		serverCfg.Sanitize([]*model.Manifest{pluginManifest}, nil)

		require.Contains(t, serverCfg.PluginSettings.Plugins, pluginManifest.Id,
			"an operator must still be able to see the plugin is installed and configured")
		assert.Contains(t, serverCfg.PluginSettings.Plugins[pluginManifest.Id], "config")
	})

	t.Run("diagnostics retain non-secret fields", func(t *testing.T) {
		store := stubAgentCounter{count: 4}

		packet, err := buildSupportPacket(store, credentialSentinelConfig(), "1.0.0-test")
		require.NoError(t, err)
		require.NotNil(t, packet)

		require.NotNil(t, packet.TotalAgents)
		assert.Equal(t, 4, *packet.TotalAgents)
		assert.Equal(t, "1.0.0-test", packet.Version)
		assert.Equal(t, 2, packet.TotalLLMServices)
		assert.Equal(t, []string{llm.ServiceTypeBedrock, llm.ServiceTypeOpenAI}, packet.LLMServiceTypes)
		assert.True(t, packet.MCPEnabled)
		assert.Equal(t, 2, packet.TotalMCPServers)
		assert.Equal(t, 1, packet.EnabledMCPServers)
		assert.True(t, packet.EnableCallSummary)
		assert.True(t, packet.EnableTokenUsageLogging)
		assert.True(t, packet.EnableChannelMentionToolCalling)
		assert.True(t, packet.AllowNativeWebSearchInChannels)
		assert.True(t, packet.WebSearchEnabled)
		assert.True(t, packet.EmbeddingSearchEnabled)
		assert.True(t, packet.TelemetryEnabled)

		body, marshalErr := yaml.Marshal(packet)
		require.NoError(t, marshalErr)
		for _, sentinel := range credentialSentinels() {
			assert.False(t, strings.Contains(string(body), sentinel.value),
				"the diagnostics file carries only non-secret fields (%s)", sentinel.jsonPath)
		}
	})
}
