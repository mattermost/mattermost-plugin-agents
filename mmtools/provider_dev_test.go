// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/config"
	sharedconfig "github.com/mattermost/mattermost-plugin-ai/config/shared"
	"github.com/mattermost/mattermost-plugin-ai/datasources"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMMToolProvider_DevToolsWithCommonDataSourcesOnly(t *testing.T) {
	t.Run("Dev tools available when common data sources is enabled", func(t *testing.T) {
		// Create common data sources config
		commonDataSourcesConfig := datasources.CreateDefaultConfig()
		commonDataSourcesConfig.FallbackDirectory = "../assets/fallback-data"
		commonDataSourcesConfig.Sources[0].Enabled = true

		// Create shared config with data sources
		sharedCfg := sharedconfig.Config{
			DataSources: commonDataSourcesConfig,
		}
		sharedConfigJSON, _ := json.Marshal(sharedCfg)

		// Create Dev config
		devConfigData := struct {
			Enabled bool `json:"enabled"`
		}{
			Enabled: true,
		}

		devConfigJSON, err := json.Marshal(devConfigData)
		require.NoError(t, err)

		testConfig := &config.Config{
			RoleConfigs: map[string]json.RawMessage{
				"dev":    devConfigJSON,
				"shared": sharedConfigJSON,
			},
		}

		configContainer := &config.Container{}
		configContainer.Update(testConfig)

		// Create provider
		provider := NewMMToolProvider(nil, nil, &http.Client{}, configContainer, nil)

		// Register Dev provider
		RegisterRoleProviderForTest(provider, configContainer, "dev")

		// Create Dev bot
		bot := bots.NewBot(
			llm.BotConfig{
				ID:          "devbotid",
				Name:        "devbot",
				DisplayName: "Dev Assistant",
			},
			llm.ServiceConfig{},
			nil,
			nil,
		)

		// Get tools
		tools := provider.GetTools(true, bot)

		// Verify Dev tools are present
		foundTools := make(map[string]bool)
		for _, tool := range tools {
			foundTools[tool.Name] = true
		}

		assert.True(t, foundTools["ExplainCodePattern"], "ExplainCodePattern should be available with common data sources")
		assert.True(t, foundTools["DebugIssue"], "DebugIssue should be available with common data sources")
		assert.True(t, foundTools["FindArchitecture"], "FindArchitecture should be available with common data sources")
		assert.True(t, foundTools["GetAPIExamples"], "GetAPIExamples should be available with common data sources")
		assert.True(t, foundTools["SummarizePRs"], "SummarizePRs should be available with common data sources")
	})

	t.Run("Dev tools NOT available when no data sources enabled", func(t *testing.T) {
		// Create Dev config
		devConfigData := struct {
			Enabled bool `json:"enabled"`
		}{
			Enabled: true,
		}

		devConfigJSON, err := json.Marshal(devConfigData)
		require.NoError(t, err)

		testConfig := &config.Config{
			RoleConfigs: map[string]json.RawMessage{
				"dev": devConfigJSON,
			},
		}

		configContainer := &config.Container{}
		configContainer.Update(testConfig)

		// Create provider
		provider := NewMMToolProvider(nil, nil, &http.Client{}, configContainer, nil)

		// Register Dev provider
		RegisterRoleProviderForTest(provider, configContainer, "dev")

		// Create Dev bot
		bot := bots.NewBot(
			llm.BotConfig{
				ID:          "devbotid",
				Name:        "devbot",
				DisplayName: "Dev Assistant",
			},
			llm.ServiceConfig{},
			nil,
			nil,
		)

		// Get tools
		tools := provider.GetTools(true, bot)

		// Verify Dev tools are NOT present
		for _, tool := range tools {
			assert.NotEqual(t, "ExplainCodePattern", tool.Name, "ExplainCodePattern should not be available without data sources")
			assert.NotEqual(t, "DebugIssue", tool.Name, "DebugIssue should not be available without data sources")
			assert.NotEqual(t, "FindArchitecture", tool.Name, "FindArchitecture should not be available without data sources")
			assert.NotEqual(t, "GetAPIExamples", tool.Name, "GetAPIExamples should not be available without data sources")
			assert.NotEqual(t, "SummarizePRs", tool.Name, "SummarizePRs should not be available without data sources")
		}
	})

	t.Run("Dev tools NOT available when Dev config disabled", func(t *testing.T) {
		// Create common data sources config
		commonDataSourcesConfig := datasources.CreateDefaultConfig()
		commonDataSourcesConfig.FallbackDirectory = "../assets/fallback-data"
		commonDataSourcesConfig.Sources[0].Enabled = true

		// Create shared config with data sources
		sharedCfg := sharedconfig.Config{
			DataSources: commonDataSourcesConfig,
		}
		sharedConfigJSON, _ := json.Marshal(sharedCfg)

		// Create Dev config with enabled=false
		devConfigData := struct {
			Enabled bool `json:"enabled"`
		}{
			Enabled: false, // Disabled
		}

		devConfigJSON, err := json.Marshal(devConfigData)
		require.NoError(t, err)

		testConfig := &config.Config{
			RoleConfigs: map[string]json.RawMessage{
				"dev":    devConfigJSON,
				"shared": sharedConfigJSON,
			},
		}

		configContainer := &config.Container{}
		configContainer.Update(testConfig)

		// Create provider
		provider := NewMMToolProvider(nil, nil, &http.Client{}, configContainer, nil)

		// Register Dev provider
		RegisterRoleProviderForTest(provider, configContainer, "dev")

		// Create Dev bot
		bot := bots.NewBot(
			llm.BotConfig{
				ID:          "devbotid",
				Name:        "devbot",
				DisplayName: "Dev Assistant",
			},
			llm.ServiceConfig{},
			nil,
			nil,
		)

		// Get tools
		tools := provider.GetTools(true, bot)

		// Verify Dev tools are NOT present
		for _, tool := range tools {
			assert.NotEqual(t, "ExplainCodePattern", tool.Name, "ExplainCodePattern should not be available when Dev config is disabled")
			assert.NotEqual(t, "DebugIssue", tool.Name, "DebugIssue should not be available when Dev config is disabled")
			assert.NotEqual(t, "FindArchitecture", tool.Name, "FindArchitecture should not be available when Dev config is disabled")
			assert.NotEqual(t, "GetAPIExamples", tool.Name, "GetAPIExamples should not be available when Dev config is disabled")
			assert.NotEqual(t, "SummarizePRs", tool.Name, "SummarizePRs should not be available when Dev config is disabled")
		}
	})
}
