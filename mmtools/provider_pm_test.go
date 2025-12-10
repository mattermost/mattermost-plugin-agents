// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/config"
	pmconfig "github.com/mattermost/mattermost-plugin-ai/config/pm"
	sharedconfig "github.com/mattermost/mattermost-plugin-ai/config/shared"
	"github.com/mattermost/mattermost-plugin-ai/datasources"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMMToolProvider_PMToolsWithCommonDataSourcesOnly(t *testing.T) {
	t.Run("PM tools available when only common data sources is enabled", func(t *testing.T) {
		// Create common data sources config
		commonDataSourcesConfig := datasources.CreateDefaultConfig()
		commonDataSourcesConfig.FallbackDirectory = "../assets/fallback-data"
		commonDataSourcesConfig.Sources[0].Enabled = true

		// Create shared config with data sources
		sharedCfg := sharedconfig.Config{
			DataSources: commonDataSourcesConfig,
		}
		sharedConfigJSON, _ := json.Marshal(sharedCfg)

		// Create PM config without MockMode
		pmConfigData := pmconfig.Config{
			Enabled: true,
			MockMode: &pmconfig.MockModeConfig{
				Enabled:   false, // No mock mode
				Directory: "",
			},
		}

		pmConfigJSON, err := json.Marshal(pmConfigData)
		require.NoError(t, err)

		testConfig := &config.Config{
			RoleConfigs: map[string]json.RawMessage{
				"pm":     pmConfigJSON,
				"shared": sharedConfigJSON,
			},
		}

		configContainer := &config.Container{}
		configContainer.Update(testConfig)

		// Create provider
		provider := NewMMToolProvider(nil, nil, &http.Client{}, configContainer, nil)

		// Register PM provider
		RegisterRoleProviderForTest(provider, configContainer, "pm")

		// Create PM bot
		bot := bots.NewBot(
			llm.BotConfig{
				ID:          "pmbotid",
				Name:        "pmbot",
				DisplayName: "PM Assistant",
			},
			llm.ServiceConfig{},
			nil,
			nil,
		)

		// Get tools
		tools := provider.GetTools(true, bot)

		// Verify PM tools are present
		pmToolsFound := map[string]bool{
			"CompileMarketResearch":     false,
			"AnalyzeFeatureGaps":        false,
			"AnalyzeStrategicAlignment": false,
		}

		for _, tool := range tools {
			if _, exists := pmToolsFound[tool.Name]; exists {
				pmToolsFound[tool.Name] = true
			}
		}

		assert.True(t, pmToolsFound["CompileMarketResearch"], "CompileMarketResearch tool should be available")
		assert.True(t, pmToolsFound["AnalyzeFeatureGaps"], "AnalyzeFeatureGaps tool should be available")
		assert.True(t, pmToolsFound["AnalyzeStrategicAlignment"], "AnalyzeStrategicAlignment tool should be available")
	})

	t.Run("PM tools NOT available when no data sources enabled", func(t *testing.T) {
		// Create PM config with everything disabled
		pmConfigData := pmconfig.Config{
			Enabled: true,
			MockMode: &pmconfig.MockModeConfig{
				Enabled:   false,
				Directory: "",
			},
		}

		pmConfigJSON, err := json.Marshal(pmConfigData)
		require.NoError(t, err)

		testConfig := &config.Config{
			RoleConfigs: map[string]json.RawMessage{
				"pm": pmConfigJSON,
			},
		}

		configContainer := &config.Container{}
		configContainer.Update(testConfig)

		// Create provider
		provider := NewMMToolProvider(nil, nil, &http.Client{}, configContainer, nil)

		// Register PM provider
		RegisterRoleProviderForTest(provider, configContainer, "pm")

		// Create PM bot
		bot := bots.NewBot(
			llm.BotConfig{
				ID:          "pmbotid",
				Name:        "pmbot",
				DisplayName: "PM Assistant",
			},
			llm.ServiceConfig{},
			nil,
			nil,
		)

		// Get tools
		tools := provider.GetTools(true, bot)

		// Verify PM tools are NOT present
		for _, tool := range tools {
			assert.NotEqual(t, "CompileMarketResearch", tool.Name, "CompileMarketResearch should not be available without data sources")
			assert.NotEqual(t, "AnalyzeFeatureGaps", tool.Name, "AnalyzeFeatureGaps should not be available without data sources")
			assert.NotEqual(t, "AnalyzeStrategicAlignment", tool.Name, "AnalyzeStrategicAlignment should not be available without data sources")
		}
	})

	t.Run("PM tools available with mock mode enabled", func(t *testing.T) {
		// Create PM config with mock mode enabled
		pmConfigData := pmconfig.Config{
			Enabled: true,
			MockMode: &pmconfig.MockModeConfig{
				Enabled:   true, // Mock mode enabled
				Directory: "../assets/fallback-data",
			},
		}

		pmConfigJSON, err := json.Marshal(pmConfigData)
		require.NoError(t, err)

		testConfig := &config.Config{
			RoleConfigs: map[string]json.RawMessage{
				"pm": pmConfigJSON,
			},
		}

		configContainer := &config.Container{}
		configContainer.Update(testConfig)

		// Create provider
		provider := NewMMToolProvider(nil, nil, &http.Client{}, configContainer, nil)

		// Register PM provider
		RegisterRoleProviderForTest(provider, configContainer, "pm")

		// Create PM bot
		bot := bots.NewBot(
			llm.BotConfig{
				ID:          "pmbotid",
				Name:        "pmbot",
				DisplayName: "PM Assistant",
			},
			llm.ServiceConfig{},
			nil,
			nil,
		)

		// Get tools
		tools := provider.GetTools(true, bot)

		// Verify PM tools are present
		pmToolsFound := map[string]bool{
			"CompileMarketResearch":     false,
			"AnalyzeFeatureGaps":        false,
			"AnalyzeStrategicAlignment": false,
		}

		for _, tool := range tools {
			if _, exists := pmToolsFound[tool.Name]; exists {
				pmToolsFound[tool.Name] = true
			}
		}

		assert.True(t, pmToolsFound["CompileMarketResearch"], "CompileMarketResearch tool should be available with mock mode")
		assert.True(t, pmToolsFound["AnalyzeFeatureGaps"], "AnalyzeFeatureGaps tool should be available with mock mode")
		assert.True(t, pmToolsFound["AnalyzeStrategicAlignment"], "AnalyzeStrategicAlignment tool should be available with mock mode")
	})
}
