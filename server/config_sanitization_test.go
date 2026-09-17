// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

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

// historicalCredentialSettingKeys lists the credential-bearing setting keys that
// the shipped manifest declared in earlier releases (v0.1.0 through v0.3.2). The
// server keeps a stored plugin setting under its original key even after a later
// manifest stops declaring that key, so an installation configured while one of
// those releases was active still holds a value under each of them.
func historicalCredentialSettingKeys() []string {
	return []string{
		"OpenAIAPIKey",
		"OpenAICompatibleKey",
		"AnthropicAPIKey",
		"AskSagePassword",
		"MattermostAISecret",
	}
}

// historicalPlainSettingKeys lists setting keys those same earlier releases
// declared that carry no credential, with a representative stored value for
// each. An operator can still read them out of config.json, so they stay.
func historicalPlainSettingKeys() map[string]any {
	return map[string]any{
		"OpenAIDefaultModel": "gpt-4",
		"llmgenerator":       "openai",
		"AllowedTeamIDs":     "team-1,team-2",
		"AskSageUsername":    "asksage-user",
		"MattermostAIUrl":    "https://ai.example.com",
		"EnableLLMTrace":     true,
	}
}

// settingKeyCasings returns the casings a stored setting key can carry: the one
// the manifest declared, the lowercased one the System Console writes, and an
// arbitrary one a hand-edited config.json can hold.
func settingKeyCasings() map[string]func(string) string {
	return map[string]func(string) string{
		"manifest":  func(key string) string { return key },
		"console":   strings.ToLower,
		"hand-held": strings.ToUpper,
	}
}

// TestStoredPluginSettingSanitization asserts that a value stored under a setting
// key the plugin no longer reads does not reach the sanitized server
// configuration, whatever casing it is stored under, while the setting the plugin
// does read and the remaining stored keys are left as they were.
func TestStoredPluginSettingSanitization(t *testing.T) {
	casings := settingKeyCasings()

	testCases := []struct {
		name          string
		casings       []string
		expectChanged bool
	}{
		{
			name:          "stored in the casing the manifest declared",
			casings:       []string{"manifest"},
			expectChanged: true,
		},
		{
			name:          "stored in the casing the System Console writes",
			casings:       []string{"console"},
			expectChanged: true,
		},
		{
			name:          "the same key stored under several casings at once",
			casings:       []string{"manifest", "console", "hand-held"},
			expectChanged: true,
		},
		{
			name:          "no such key stored",
			casings:       nil,
			expectChanged: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pluginManifest := readPluginManifest(t)
			serverCfg := serverConfigWithPluginConfig(t, pluginManifest.Id, credentialSentinelConfig())

			stored := serverCfg.PluginSettings.Plugins[pluginManifest.Id]
			plainSettings := historicalPlainSettingKeys()
			maps.Copy(stored, plainSettings)

			var sentinels []string
			for _, key := range historicalCredentialSettingKeys() {
				for _, casing := range testCase.casings {
					storedKey := casings[casing](key)
					sentinel := "sentinel-stored-" + storedKey
					stored[storedKey] = sentinel
					sentinels = append(sentinels, sentinel)
				}
			}

			before, err := json.Marshal(serverCfg.PluginSettings)
			require.NoError(t, err)
			for _, sentinel := range sentinels {
				require.Contains(t, string(before), sentinel,
					"fixture must store the sentinel in the plugin settings")
			}

			cleaned, changed := withoutObsoleteCredentialSettings(stored)
			assert.Equal(t, testCase.expectChanged, changed,
				"the cleanup reports whether the stored plugin settings need to be written back")
			if !testCase.expectChanged {
				assert.Equal(t, stored, cleaned,
					"with nothing to remove the stored plugin settings are returned unchanged")
			}

			require.Contains(t, cleaned, "config", "the setting the plugin reads is kept")
			for key, value := range plainSettings {
				assert.Equal(t, value, cleaned[key], "the value stored under %q is kept as it was", key)
			}
			untouched, err := json.Marshal(serverCfg.PluginSettings)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(untouched),
				"the cleanup works on a copy and leaves the settings it was given alone")

			serverCfg.PluginSettings.Plugins[pluginManifest.Id] = cleaned
			serverCfg.Sanitize([]*model.Manifest{pluginManifest}, nil)

			after, err := json.Marshal(serverCfg.PluginSettings)
			require.NoError(t, err)

			for _, sentinel := range sentinels {
				assert.NotContains(t, string(after), sentinel,
					"a value stored under a setting key the plugin no longer reads must not reach the sanitized configuration")
			}
			for _, sentinel := range credentialSentinels() {
				assert.NotContains(t, string(after), sentinel.value,
					"value at %s must not survive configuration sanitization", sentinel.jsonPath)
			}
			assert.Equal(t, model.FakeSetting, serverCfg.PluginSettings.Plugins[pluginManifest.Id]["config"],
				"the setting the plugin reads is still listed, redacted")
		})
	}
}

// TestRemoveObsoleteCredentialSettings asserts how the cleanup drives the plugin
// API: it reads the unsanitized configuration, writes back only when a stored key
// actually has to go, and treats a failed write as non-fatal.
func TestRemoveObsoleteCredentialSettings(t *testing.T) {
	const pluginID = "mattermost-ai"

	serverConfigWithStoredSettings := func(stored map[string]any) *model.Config {
		serverCfg := &model.Config{}
		serverCfg.SetDefaults()
		serverCfg.PluginSettings.Plugins = map[string]map[string]any{pluginID: stored}
		return serverCfg
	}

	t.Run("writes the remaining settings back when a stored key has to go", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		defer mockAPI.AssertExpectations(t)
		mockAPI.On("GetUnsanitizedConfig").Return(serverConfigWithStoredSettings(map[string]any{
			"config":          map[string]any{"defaultBotName": "agent"},
			"openaiapikey":    "sentinel-stored-console-key",
			"AnthropicAPIKey": "sentinel-stored-manifest-key",
			"AskSageUsername": "asksage-user",
		}))
		mockAPI.On("LogInfo", mock.Anything).Once()

		var saved map[string]any
		mockAPI.On("SavePluginConfig", mock.Anything).Run(func(args mock.Arguments) {
			saved = args.Get(0).(map[string]any)
		}).Return(nil).Once()

		removeObsoleteCredentialSettings(pluginapi.NewClient(mockAPI, nil), pluginID)

		require.NotNil(t, saved)
		assert.Equal(t, map[string]any{
			"config":          map[string]any{"defaultBotName": "agent"},
			"AskSageUsername": "asksage-user",
		}, saved)
	})

	t.Run("does not write when no such key is stored", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		defer mockAPI.AssertExpectations(t)
		mockAPI.On("GetUnsanitizedConfig").Return(serverConfigWithStoredSettings(map[string]any{
			"config":          map[string]any{"defaultBotName": "agent"},
			"AskSageUsername": "asksage-user",
		}))

		removeObsoleteCredentialSettings(pluginapi.NewClient(mockAPI, nil), pluginID)

		mockAPI.AssertNotCalled(t, "SavePluginConfig", mock.Anything)
	})

	t.Run("a configuration that cannot be written does not stop activation", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		defer mockAPI.AssertExpectations(t)
		mockAPI.On("GetUnsanitizedConfig").Return(serverConfigWithStoredSettings(map[string]any{
			"config":       map[string]any{"defaultBotName": "agent"},
			"openaiapikey": "sentinel-stored-console-key",
		}))
		mockAPI.On("SavePluginConfig", mock.Anything).Return(
			model.NewAppError("SaveConfig", "ent.cluster.save_config.error", nil, "", http.StatusForbidden)).Once()
		mockAPI.On("LogWarn", mock.Anything, "error", mock.Anything).Once()

		assert.NotPanics(t, func() {
			removeObsoleteCredentialSettings(pluginapi.NewClient(mockAPI, nil), pluginID)
		})
	})

	t.Run("no stored settings at all", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		defer mockAPI.AssertExpectations(t)
		serverCfg := &model.Config{}
		serverCfg.SetDefaults()
		serverCfg.PluginSettings.Plugins = map[string]map[string]any{}
		mockAPI.On("GetUnsanitizedConfig").Return(serverCfg)

		removeObsoleteCredentialSettings(pluginapi.NewClient(mockAPI, nil), pluginID)

		mockAPI.AssertNotCalled(t, "SavePluginConfig", mock.Anything)
	})
}

// TestManifestCustomSettingsAreMarkedSecret asserts that every custom-typed
// setting in the shipped manifest is marked secret. A custom setting is rendered
// by a component this plugin ships, so the manifest cannot describe what the
// stored value holds and sanitization has to assume it holds a credential.
func TestManifestCustomSettingsAreMarkedSecret(t *testing.T) {
	pluginManifest := readPluginManifest(t)
	require.NotNil(t, pluginManifest.SettingsSchema)

	assertCustomSettingsAreSecret := func(t *testing.T, location string, settings []*model.PluginSetting) {
		t.Helper()

		for _, setting := range settings {
			if setting.Type != "custom" {
				continue
			}
			assert.True(t, setting.Secret,
				"%s setting %q holds a value only this plugin can interpret", location, setting.Key)
		}
	}

	assertCustomSettingsAreSecret(t, "top-level", pluginManifest.SettingsSchema.Settings)
	for _, section := range pluginManifest.SettingsSchema.Sections {
		assertCustomSettingsAreSecret(t, "section "+section.Key, section.Settings)
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
