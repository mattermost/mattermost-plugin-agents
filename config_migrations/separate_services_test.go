// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config_migrations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSeparateServicesFromBotsMigration_Key(t *testing.T) {
	m := &SeparateServicesFromBotsMigration{}
	assert.Equal(t, MigrationKeySeparateServicesFromBots, m.Key())
}

func TestSeparateServicesFromBotsMigration_Run(t *testing.T) {
	tests := []struct {
		name           string
		inputConfig    config.Config
		expectMigrated bool
		expectError    bool
		validateResult func(t *testing.T, result config.Config)
	}{
		{
			name: "Services already populated - should skip",
			inputConfig: config.Config{
				Services: []llm.ServiceConfig{
					{
						ID:     "service1",
						Type:   llm.ServiceTypeOpenAI,
						APIKey: "key1",
					},
				},
				Bots: []llm.BotConfig{
					{
						ID:        "bot1",
						Name:      "bot1",
						ServiceID: "service1",
					},
				},
			},
			expectMigrated: false,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				// Config should remain unchanged
				assert.Len(t, result.Services, 1)
				assert.Len(t, result.Bots, 1)
				assert.Equal(t, "service1", result.Bots[0].ServiceID)
			},
		},
		{
			name:           "No bots exist - should skip",
			inputConfig:    config.Config{},
			expectMigrated: false,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				assert.Len(t, result.Services, 0)
				assert.Len(t, result.Bots, 0)
			},
		},
		{
			name: "Bots already have ServiceID - should skip",
			inputConfig: config.Config{
				Bots: []llm.BotConfig{
					{
						ID:        "bot1",
						Name:      "bot1",
						ServiceID: "service1",
					},
				},
			},
			expectMigrated: false,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				assert.Len(t, result.Services, 0)
				assert.Len(t, result.Bots, 1)
				assert.Equal(t, "service1", result.Bots[0].ServiceID)
			},
		},
		{
			name: "Bot without embedded service - should skip",
			inputConfig: config.Config{
				Bots: []llm.BotConfig{
					{
						ID:      "bot1",
						Name:    "bot1",
						Service: nil,
					},
				},
			},
			expectMigrated: false,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				assert.Len(t, result.Services, 0)
				assert.Len(t, result.Bots, 1)
			},
		},
		{
			name: "Single bot with embedded service - should extract and migrate",
			inputConfig: config.Config{
				Bots: []llm.BotConfig{
					{
						ID:   "bot1",
						Name: "bot1",
						Service: &llm.ServiceConfig{
							Type:         llm.ServiceTypeOpenAI,
							APIKey:       "key1",
							DefaultModel: "gpt-4",
						},
					},
				},
			},
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				require.Len(t, result.Services, 1)
				assert.Equal(t, llm.ServiceTypeOpenAI, result.Services[0].Type)
				assert.Equal(t, "key1", result.Services[0].APIKey)
				assert.Equal(t, "gpt-4", result.Services[0].DefaultModel)

				require.Len(t, result.Bots, 1)
				assert.Equal(t, result.Services[0].ID, result.Bots[0].ServiceID)
				assert.Nil(t, result.Bots[0].Service, "Embedded service field should be cleared after migration")
			},
		},
		{
			name: "Multiple bots with identical service - should deduplicate",
			inputConfig: config.Config{
				Bots: []llm.BotConfig{
					{
						ID:   "bot1",
						Name: "bot1",
						Service: &llm.ServiceConfig{
							Name:                    "Service A",
							Type:                    llm.ServiceTypeOpenAI,
							APIKey:                  "key1",
							OrgID:                   "org1",
							DefaultModel:            "gpt-4",
							APIURL:                  "https://api.openai.com",
							InputTokenLimit:         4000,
							StreamingTimeoutSeconds: 30,
							SendUserID:              true,
							OutputTokenLimit:        2000,
							UseResponsesAPI:         false,
						},
					},
					{
						ID:   "bot2",
						Name: "bot2",
						Service: &llm.ServiceConfig{
							Name:                    "Service A",
							Type:                    llm.ServiceTypeOpenAI,
							APIKey:                  "key1",
							OrgID:                   "org1",
							DefaultModel:            "gpt-4",
							APIURL:                  "https://api.openai.com",
							InputTokenLimit:         4000,
							StreamingTimeoutSeconds: 30,
							SendUserID:              true,
							OutputTokenLimit:        2000,
							UseResponsesAPI:         false,
						},
					},
				},
			},
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				// Should create only one service
				require.Len(t, result.Services, 1)
				assert.Equal(t, llm.ServiceTypeOpenAI, result.Services[0].Type)
				assert.Equal(t, "key1", result.Services[0].APIKey)

				// Both bots should reference the same service
				require.Len(t, result.Bots, 2)
				assert.Equal(t, result.Bots[0].ServiceID, result.Bots[1].ServiceID)
				assert.Nil(t, result.Bots[0].Service)
				assert.Nil(t, result.Bots[1].Service)
			},
		},
		{
			name: "Multiple bots with services differing only in name - should deduplicate",
			inputConfig: config.Config{
				Bots: []llm.BotConfig{
					{
						ID:   "bot1",
						Name: "bot1",
						Service: &llm.ServiceConfig{
							Name:                    "Service A",
							Type:                    llm.ServiceTypeOpenAI,
							APIKey:                  "key1",
							OrgID:                   "org1",
							DefaultModel:            "gpt-4",
							APIURL:                  "https://api.openai.com",
							InputTokenLimit:         4000,
							StreamingTimeoutSeconds: 30,
							SendUserID:              true,
							OutputTokenLimit:        2000,
							UseResponsesAPI:         false,
						},
					},
					{
						ID:   "bot2",
						Name: "bot2",
						Service: &llm.ServiceConfig{
							Name:                    "Service B", // Different name!
							Type:                    llm.ServiceTypeOpenAI,
							APIKey:                  "key1",
							OrgID:                   "org1",
							DefaultModel:            "gpt-4",
							APIURL:                  "https://api.openai.com",
							InputTokenLimit:         4000,
							StreamingTimeoutSeconds: 30,
							SendUserID:              true,
							OutputTokenLimit:        2000,
							UseResponsesAPI:         false,
						},
					},
				},
			},
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				// Should create only one service (name difference should be ignored)
				require.Len(t, result.Services, 1)
				assert.Equal(t, llm.ServiceTypeOpenAI, result.Services[0].Type)
				assert.Equal(t, "key1", result.Services[0].APIKey)

				// Both bots should reference the same service
				require.Len(t, result.Bots, 2)
				assert.Equal(t, result.Bots[0].ServiceID, result.Bots[1].ServiceID)
				assert.Nil(t, result.Bots[0].Service)
				assert.Nil(t, result.Bots[1].Service)
			},
		},
		{
			name: "Multiple bots with different services - should create separate services",
			inputConfig: config.Config{
				Bots: []llm.BotConfig{
					{
						ID:   "bot1",
						Name: "bot1",
						Service: &llm.ServiceConfig{
							Type:         llm.ServiceTypeOpenAI,
							APIKey:       "key1",
							DefaultModel: "gpt-4",
						},
					},
					{
						ID:   "bot2",
						Name: "bot2",
						Service: &llm.ServiceConfig{
							Type:         llm.ServiceTypeAnthropic,
							APIKey:       "key2",
							DefaultModel: "claude-3",
						},
					},
				},
			},
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				// Should create two services
				require.Len(t, result.Services, 2)

				// Bots should reference different services
				require.Len(t, result.Bots, 2)
				assert.NotEqual(t, result.Bots[0].ServiceID, result.Bots[1].ServiceID)
				assert.Nil(t, result.Bots[0].Service)
				assert.Nil(t, result.Bots[1].Service)
			},
		},
		{
			name: "Mixed: some bots with ServiceID, some with embedded service",
			inputConfig: config.Config{
				Services: []llm.ServiceConfig{
					{
						ID:     "existing-service",
						Type:   llm.ServiceTypeOpenAI,
						APIKey: "key-existing",
					},
				},
				Bots: []llm.BotConfig{
					{
						ID:        "bot1",
						Name:      "bot1",
						ServiceID: "existing-service",
					},
					{
						ID:   "bot2",
						Name: "bot2",
						Service: &llm.ServiceConfig{
							Type:   llm.ServiceTypeAnthropic,
							APIKey: "key2",
						},
					},
				},
			},
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				// Should have original service plus new extracted service
				require.Len(t, result.Services, 2)

				require.Len(t, result.Bots, 2)
				// Bot1 should still reference existing service
				assert.Equal(t, "existing-service", result.Bots[0].ServiceID)
				// Bot2 should reference new service
				assert.NotEqual(t, "existing-service", result.Bots[1].ServiceID)
				assert.NotEmpty(t, result.Bots[1].ServiceID)
				assert.Nil(t, result.Bots[1].Service)
			},
		},
		{
			name: "Real-world config: many bots with identical embedded services - should deduplicate",
			inputConfig: config.Config{
				Bots: []llm.BotConfig{
					{
						ID:          "OpenAI",
						Name:        "ai",
						DisplayName: "OpenAI",
						Service: &llm.ServiceConfig{
							Type:             llm.ServiceTypeOpenAI,
							APIKey:           "test-key",
							DefaultModel:     "gpt-4o",
							InputTokenLimit:  32768,
							OutputTokenLimit: 0,
							SendUserID:       false,
							UseResponsesAPI:  false,
						},
					},
					{
						ID:                 "8ji6s8wyutu",
						Name:               "yoda-ai",
						DisplayName:        "YodaAI",
						CustomInstructions: "Respond with wisdom and a calm, nurturing tone...",
						Service: &llm.ServiceConfig{
							Type:             llm.ServiceTypeOpenAI,
							APIKey:           "test-key",
							DefaultModel:     "gpt-4o",
							InputTokenLimit:  32768,
							OutputTokenLimit: 0,
							SendUserID:       false,
							UseResponsesAPI:  false,
						},
					},
					{
						ID:                 "li5ivf2ay4",
						Name:               "loki",
						DisplayName:        "Loki",
						CustomInstructions: "You are Loki. Respond in a cunning manner...",
						Service: &llm.ServiceConfig{
							Type:             llm.ServiceTypeOpenAI,
							APIKey:           "test-key",
							DefaultModel:     "gpt-4o",
							InputTokenLimit:  32768,
							OutputTokenLimit: 0,
							SendUserID:       false,
							UseResponsesAPI:  false,
						},
					},
					{
						ID:                 "matter-ai",
						Name:               "matter-ai",
						DisplayName:        "MatterAI",
						CustomInstructions: "You are a Mattermost LLM...",
						Service: &llm.ServiceConfig{
							Type:             llm.ServiceTypeOpenAI,
							APIKey:           "test-key",
							DefaultModel:     "gpt-4o",
							InputTokenLimit:  32768,
							OutputTokenLimit: 0,
							SendUserID:       false,
							UseResponsesAPI:  false,
						},
					},
					{
						ID:          "anthropic-bot",
						Name:        "claude",
						DisplayName: "Claude",
						Service: &llm.ServiceConfig{
							Type:             llm.ServiceTypeAnthropic,
							APIKey:           "anthropic-key",
							DefaultModel:     "claude-3-5-sonnet-20241022",
							InputTokenLimit:  100000,
							OutputTokenLimit: 8192,
							SendUserID:       false,
							UseResponsesAPI:  false,
						},
					},
				},
			},
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				// Should create only 2 services (OpenAI and Anthropic), deduplicating the 4 identical OpenAI services
				require.Len(t, result.Services, 2, "Expected 2 services: 1 deduplicated OpenAI + 1 Anthropic")

				// Find the OpenAI and Anthropic services
				var openAIService, anthropicService *llm.ServiceConfig
				for i := range result.Services {
					switch result.Services[i].Type {
					case llm.ServiceTypeOpenAI:
						openAIService = &result.Services[i]
					case llm.ServiceTypeAnthropic:
						anthropicService = &result.Services[i]
					}
				}

				require.NotNil(t, openAIService, "OpenAI service should exist")
				require.NotNil(t, anthropicService, "Anthropic service should exist")

				assert.Equal(t, "test-key", openAIService.APIKey)
				assert.Equal(t, "gpt-4o", openAIService.DefaultModel)
				assert.Equal(t, 32768, openAIService.InputTokenLimit)

				assert.Equal(t, "anthropic-key", anthropicService.APIKey)
				assert.Equal(t, "claude-3-5-sonnet-20241022", anthropicService.DefaultModel)
				assert.Equal(t, 100000, anthropicService.InputTokenLimit)

				// All 5 bots should be migrated
				require.Len(t, result.Bots, 5)

				// First 4 bots should reference the same OpenAI service
				for i := 0; i < 4; i++ {
					assert.Equal(t, openAIService.ID, result.Bots[i].ServiceID,
						"Bot %d (%s) should reference OpenAI service", i, result.Bots[i].Name)
					assert.Nil(t, result.Bots[i].Service, "Embedded service should be cleared for bot %d", i)
				}

				// Last bot should reference the Anthropic service
				assert.Equal(t, anthropicService.ID, result.Bots[4].ServiceID)
				assert.Nil(t, result.Bots[4].Service)

				// Verify bot names are preserved
				assert.Equal(t, "ai", result.Bots[0].Name)
				assert.Equal(t, "yoda-ai", result.Bots[1].Name)
				assert.Equal(t, "loki", result.Bots[2].Name)
				assert.Equal(t, "matter-ai", result.Bots[3].Name)
				assert.Equal(t, "claude", result.Bots[4].Name)

				// Verify custom instructions are preserved
				assert.Contains(t, result.Bots[1].CustomInstructions, "wisdom")
				assert.Contains(t, result.Bots[2].CustomInstructions, "Loki")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock API
			mockAPI := &plugintest.API{}

			// Mock mutex lock/unlock operations
			mockAPI.On("KVSetWithOptions", mock.MatchedBy(func(key string) bool {
				return key == "mutex_migrate_separate_services_from_bots"
			}), mock.Anything, mock.Anything).Return(true, nil)

			mockAPI.On("KVDelete", "mutex_migrate_separate_services_from_bots").Return(nil)

			// Setup logging - accept variadic arguments for structured logging
			mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			mockAPI.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)

			pluginAPI := pluginapi.NewClient(mockAPI, nil)

			// Run migration
			migration := &SeparateServicesFromBotsMigration{}
			migrated, resultConfig, err := migration.Run(pluginAPI, tt.inputConfig)

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
