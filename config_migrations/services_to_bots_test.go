// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config_migrations

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestServicesToBotsMigration_Key(t *testing.T) {
	m := &ServicesToBotsMigration{}
	assert.Equal(t, MigrationKeyServicesToBots, m.Key())
}

func TestServicesToBotsMigration_Run(t *testing.T) {
	tests := []struct {
		name           string
		existingBots   []llm.BotConfig
		oldConfigJSON  string
		expectMigrated bool
		expectError    bool
		validateResult func(t *testing.T, result config.Config)
	}{
		{
			name: "Bots already exist - should skip",
			existingBots: []llm.BotConfig{
				{ID: "bot1", Name: "bot1"},
			},
			expectMigrated: false,
			expectError:    false,
		},
		{
			name:         "Single old service - should create service and bot with standard name",
			existingBots: []llm.BotConfig{},
			oldConfigJSON: `{
				"config": {
					"services": [
						{
							"name": "OpenAI GPT-4",
							"serviceName": "openai",
							"defaultModel": "gpt-4",
							"orgId": "org-123",
							"apiKey": "sk-test-key",
							"tokenLimit": 4000
						}
					]
				}
			}`,
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				// Should create one service
				require.Len(t, result.Services, 1)
				assert.Equal(t, "openai", result.Services[0].Type)
				assert.Equal(t, "OpenAI GPT-4", result.Services[0].Name)
				assert.Equal(t, "gpt-4", result.Services[0].DefaultModel)
				assert.Equal(t, "org-123", result.Services[0].OrgID)
				assert.Equal(t, "sk-test-key", result.Services[0].APIKey)
				assert.Equal(t, 4000, result.Services[0].InputTokenLimit)
				assert.NotEmpty(t, result.Services[0].ID)

				// Should create one bot
				require.Len(t, result.Bots, 1)
				assert.Equal(t, "ai1", result.Bots[0].Name)
				assert.Equal(t, "OpenAI GPT-4", result.Bots[0].DisplayName)
				assert.Equal(t, result.Services[0].ID, result.Bots[0].ServiceID)
			},
		},
		{
			name:         "Multiple old services - should create multiple services and bots",
			existingBots: []llm.BotConfig{},
			oldConfigJSON: `{
				"config": {
					"services": [
						{
							"name": "OpenAI GPT-4",
							"serviceName": "openai",
							"defaultModel": "gpt-4",
							"apiKey": "sk-openai-key",
							"tokenLimit": 4000
						},
						{
							"name": "Anthropic Claude",
							"serviceName": "anthropic",
							"defaultModel": "claude-3",
							"apiKey": "sk-anthropic-key",
							"tokenLimit": 8000
						}
					]
				}
			}`,
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				// Should create two services
				require.Len(t, result.Services, 2)

				// Check first service
				assert.Equal(t, "openai", result.Services[0].Type)
				assert.Equal(t, "OpenAI GPT-4", result.Services[0].Name)
				assert.Equal(t, "sk-openai-key", result.Services[0].APIKey)

				// Check second service
				assert.Equal(t, "anthropic", result.Services[1].Type)
				assert.Equal(t, "Anthropic Claude", result.Services[1].Name)
				assert.Equal(t, "sk-anthropic-key", result.Services[1].APIKey)

				// Should create two bots - first one does NOT get standard name (multiple bots)
				require.Len(t, result.Bots, 2)
				assert.Equal(t, "OpenAI GPT-4", result.Bots[0].DisplayName)
				assert.Equal(t, result.Services[0].ID, result.Bots[0].ServiceID)
				assert.Equal(t, "Anthropic Claude", result.Bots[1].DisplayName)
				assert.Equal(t, result.Services[1].ID, result.Bots[1].ServiceID)
			},
		},
		{
			name:         "Old service with URL - should migrate URL correctly",
			existingBots: []llm.BotConfig{},
			oldConfigJSON: `{
				"config": {
					"services": [
						{
							"name": "Custom LLM",
							"serviceName": "openaicompatible",
							"url": "https://custom-llm.example.com/v1",
							"apiKey": "custom-key",
							"defaultModel": "custom-model"
						}
					]
				}
			}`,
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				require.Len(t, result.Services, 1)
				assert.Equal(t, "openaicompatible", result.Services[0].Type)
				assert.Equal(t, "https://custom-llm.example.com/v1", result.Services[0].APIURL)
				assert.Equal(t, "custom-key", result.Services[0].APIKey)
				assert.Equal(t, "custom-model", result.Services[0].DefaultModel)

				require.Len(t, result.Bots, 1)
				assert.Equal(t, "ai1", result.Bots[0].Name)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock API
			mockAPI := &plugintest.API{}

			// Mock mutex lock/unlock operations
			mockAPI.On("KVSetWithOptions", mock.MatchedBy(func(key string) bool {
				return key == "mutex_migrate_services_to_bots"
			}), mock.Anything, mock.Anything).Return(true, nil)

			mockAPI.On("KVDelete", "mutex_migrate_services_to_bots").Return(nil)

			// Setup logging
			mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			mockAPI.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)

			// Mock LoadPluginConfiguration for cases where we need to load old config
			if tt.oldConfigJSON != "" {
				mockAPI.On("LoadPluginConfiguration", mock.AnythingOfType("*config_migrations.BotMigrationConfig")).Return(nil).Run(func(args mock.Arguments) {
					cfg := args.Get(0).(*BotMigrationConfig)
					// Unmarshal the test JSON into the config struct
					err := json.Unmarshal([]byte(tt.oldConfigJSON), cfg)
					require.NoError(t, err)
				})
			}

			pluginAPI := pluginapi.NewClient(mockAPI, nil)

			cfg := config.Config{
				Bots: tt.existingBots,
			}

			// Run migration
			migration := &ServicesToBotsMigration{}
			migrated, resultConfig, err := migration.Run(pluginAPI, cfg)

			// Check expectations
			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectMigrated, migrated)

			// Run custom validation if provided
			if tt.validateResult != nil {
				tt.validateResult(t, resultConfig)
			}
		})
	}
}
