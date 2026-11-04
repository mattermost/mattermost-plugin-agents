// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
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

// TestClearMigratedPluginSettings asserts that the plugin's entry in the server
// configuration is emptied whenever it still holds anything, and left alone
// otherwise.
func TestClearMigratedPluginSettings(t *testing.T) {
	pluginManifest := readPluginManifest(t)
	migratedEntry := serverConfigWithPluginConfig(t, pluginManifest.Id, credentialSentinelConfig()).
		PluginSettings.Plugins[pluginManifest.Id]
	migratedEntry["OpenAIAPIKey"] = "sentinel-legacy-openai-api-key"
	migratedEntry["asksagepassword"] = "sentinel-legacy-asksage-password"

	testCases := []struct {
		name        string
		stored      map[string]any
		saveErr     *model.AppError
		expectSave  bool
		expectLevel string
	}{
		{
			name:        "migrated entry with legacy keys is emptied",
			stored:      migratedEntry,
			expectSave:  true,
			expectLevel: "LogInfo",
		},
		{
			name:        "a configuration that cannot be written does not stop activation",
			stored:      map[string]any{"config": map[string]any{"defaultBotName": "agent"}},
			saveErr:     model.NewAppError("SaveConfig", "ent.cluster.save_config.error", nil, "", http.StatusForbidden),
			expectSave:  true,
			expectLevel: "LogWarn",
		},
		{
			name:   "already empty entry is not rewritten",
			stored: map[string]any{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)
			mockAPI.On("GetPluginConfig").Return(testCase.stored)

			var saved map[string]any
			if testCase.expectSave {
				mockAPI.On("SavePluginConfig", mock.Anything).Run(func(args mock.Arguments) {
					saved = args.Get(0).(map[string]any)
				}).Return(testCase.saveErr).Once()
			}
			mockAPI.On("LogInfo", mock.Anything).Maybe()
			mockAPI.On("LogWarn", mock.Anything, "error", mock.Anything).Maybe()

			assert.NotPanics(t, func() {
				clearMigratedPluginSettings(pluginapi.NewClient(mockAPI, nil))
			})

			if !testCase.expectSave {
				mockAPI.AssertNotCalled(t, "SavePluginConfig", mock.Anything)
				return
			}
			assert.Empty(t, saved, "nothing from the migrated entry is written back")

			var logged []string
			for _, call := range mockAPI.Calls {
				if strings.HasPrefix(call.Method, "Log") {
					logged = append(logged, call.Method)
				}
			}
			assert.Equal(t, []string{testCase.expectLevel}, logged)
		})
	}
}
