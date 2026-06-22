// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateSeparateServicesFromBots(t *testing.T) {
	tests := []struct {
		name           string
		inputConfig    Config
		expectMigrated bool
		expectError    bool
		validateResult func(t *testing.T, result Config)
	}{
		{
			name: "Services already populated - should skip",
			inputConfig: Config{
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
			validateResult: func(t *testing.T, result Config) {
				assert.Len(t, result.Services, 1)
				assert.Len(t, result.Bots, 1)
				assert.Equal(t, "service1", result.Bots[0].ServiceID)
			},
		},
		{
			name:           "No bots exist - should skip",
			inputConfig:    Config{},
			expectMigrated: false,
			expectError:    false,
			validateResult: func(t *testing.T, result Config) {
				assert.Len(t, result.Services, 0)
				assert.Len(t, result.Bots, 0)
			},
		},
		{
			name: "Bots already have ServiceID - should skip",
			inputConfig: Config{
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
			validateResult: func(t *testing.T, result Config) {
				assert.Len(t, result.Services, 0)
				assert.Len(t, result.Bots, 1)
				assert.Equal(t, "service1", result.Bots[0].ServiceID)
			},
		},
		{
			name: "Bot without embedded service - should skip",
			inputConfig: Config{
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
			validateResult: func(t *testing.T, result Config) {
				assert.Len(t, result.Services, 0)
				assert.Len(t, result.Bots, 1)
			},
		},
		{
			name: "Single bot with embedded service - should extract and migrate",
			inputConfig: Config{
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
			validateResult: func(t *testing.T, result Config) {
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
			inputConfig: Config{
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
							OutputTokenLimit:        2000,
							UseResponsesAPI:         false,
						},
					},
				},
			},
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result Config) {
				require.Len(t, result.Services, 1)
				assert.Equal(t, llm.ServiceTypeOpenAI, result.Services[0].Type)
				assert.Equal(t, "key1", result.Services[0].APIKey)

				require.Len(t, result.Bots, 2)
				assert.Equal(t, result.Bots[0].ServiceID, result.Bots[1].ServiceID)
				assert.Nil(t, result.Bots[0].Service)
				assert.Nil(t, result.Bots[1].Service)
			},
		},
		{
			name: "Multiple bots with services differing only in name - should deduplicate",
			inputConfig: Config{
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
							OutputTokenLimit:        2000,
							UseResponsesAPI:         false,
						},
					},
					{
						ID:   "bot2",
						Name: "bot2",
						Service: &llm.ServiceConfig{
							Name:                    "Service B",
							Type:                    llm.ServiceTypeOpenAI,
							APIKey:                  "key1",
							OrgID:                   "org1",
							DefaultModel:            "gpt-4",
							APIURL:                  "https://api.openai.com",
							InputTokenLimit:         4000,
							StreamingTimeoutSeconds: 30,
							OutputTokenLimit:        2000,
							UseResponsesAPI:         false,
						},
					},
				},
			},
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result Config) {
				require.Len(t, result.Services, 1)
				assert.Equal(t, llm.ServiceTypeOpenAI, result.Services[0].Type)
				assert.Equal(t, "key1", result.Services[0].APIKey)

				require.Len(t, result.Bots, 2)
				assert.Equal(t, result.Bots[0].ServiceID, result.Bots[1].ServiceID)
				assert.Nil(t, result.Bots[0].Service)
				assert.Nil(t, result.Bots[1].Service)
			},
		},
		{
			name: "Multiple bots with different services - should create separate services",
			inputConfig: Config{
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
			validateResult: func(t *testing.T, result Config) {
				require.Len(t, result.Services, 2)

				require.Len(t, result.Bots, 2)
				assert.NotEqual(t, result.Bots[0].ServiceID, result.Bots[1].ServiceID)
				assert.Nil(t, result.Bots[0].Service)
				assert.Nil(t, result.Bots[1].Service)
			},
		},
		{
			name: "Mixed: some bots with ServiceID, some with embedded service",
			inputConfig: Config{
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
			validateResult: func(t *testing.T, result Config) {
				require.Len(t, result.Services, 2)

				require.Len(t, result.Bots, 2)
				assert.Equal(t, "existing-service", result.Bots[0].ServiceID)
				assert.NotEqual(t, "existing-service", result.Bots[1].ServiceID)
				assert.NotEmpty(t, result.Bots[1].ServiceID)
				assert.Nil(t, result.Bots[1].Service)
			},
		},
		{
			name: "Real-world config: many bots with identical embedded services - should deduplicate",
			inputConfig: Config{
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
							UseResponsesAPI:  false,
						},
					},
				},
			},
			expectMigrated: true,
			expectError:    false,
			validateResult: func(t *testing.T, result Config) {
				require.Len(t, result.Services, 2, "Expected 2 services: 1 deduplicated OpenAI + 1 Anthropic")

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

				require.Len(t, result.Bots, 5)

				for i := 0; i < 4; i++ {
					assert.Equal(t, openAIService.ID, result.Bots[i].ServiceID,
						"Bot %d (%s) should reference OpenAI service", i, result.Bots[i].Name)
					assert.Nil(t, result.Bots[i].Service, "Embedded service should be cleared for bot %d", i)
				}

				assert.Equal(t, anthropicService.ID, result.Bots[4].ServiceID)
				assert.Nil(t, result.Bots[4].Service)

				assert.Equal(t, "ai", result.Bots[0].Name)
				assert.Equal(t, "yoda-ai", result.Bots[1].Name)
				assert.Equal(t, "loki", result.Bots[2].Name)
				assert.Equal(t, "matter-ai", result.Bots[3].Name)
				assert.Equal(t, "claude", result.Bots[4].Name)

				assert.Contains(t, result.Bots[1].CustomInstructions, "wisdom")
				assert.Contains(t, result.Bots[2].CustomInstructions, "Loki")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultConfig, migrated, err := MigrateSeparateServicesFromBots(tt.inputConfig)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectMigrated, migrated)

			if tt.validateResult != nil {
				tt.validateResult(t, resultConfig)
			}
		})
	}
}

func TestFindIdenticalService(t *testing.T) {
	baseService := llm.ServiceConfig{
		ID:                      "base-id",
		Name:                    "Base Service",
		Type:                    llm.ServiceTypeOpenAI,
		APIKey:                  "key1",
		OrgID:                   "org1",
		DefaultModel:            "gpt-4",
		APIURL:                  "https://api.openai.com",
		InputTokenLimit:         4000,
		StreamingTimeoutSeconds: 30,
		OutputTokenLimit:        2000,
		UseResponsesAPI:         false,
	}

	serviceMap := map[string]llm.ServiceConfig{
		"base-id": baseService,
	}

	tests := []struct {
		name       string
		newService *llm.ServiceConfig
		expectedID string
		shouldFind bool
	}{
		{
			name: "Exact match found - all fields identical",
			newService: &llm.ServiceConfig{
				ID:                      "different-id",
				Name:                    "Base Service",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			expectedID: "base-id",
			shouldFind: true,
		},
		{
			name: "Match found - different name but otherwise identical",
			newService: &llm.ServiceConfig{
				ID:                      "different-id",
				Name:                    "Different Name",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			expectedID: "base-id",
			shouldFind: true,
		},
		{
			name: "No match - different Type",
			newService: &llm.ServiceConfig{
				ID:                      "different-id",
				Name:                    "Base Service",
				Type:                    llm.ServiceTypeAnthropic,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			expectedID: "",
			shouldFind: false,
		},
		{
			name: "No match - different APIKey",
			newService: &llm.ServiceConfig{
				ID:                      "different-id",
				Name:                    "Base Service",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "different-key",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			expectedID: "",
			shouldFind: false,
		},
		{
			name: "No match - different DefaultModel",
			newService: &llm.ServiceConfig{
				ID:                      "different-id",
				Name:                    "Base Service",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-3.5",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			expectedID: "",
			shouldFind: false,
		},
		{
			name: "No match - different InputTokenLimit",
			newService: &llm.ServiceConfig{
				ID:                      "different-id",
				Name:                    "Base Service",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         8000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			expectedID: "",
			shouldFind: false,
		},
		{
			name: "No match - different StreamingTimeoutSeconds",
			newService: &llm.ServiceConfig{
				ID:                      "different-id",
				Name:                    "Base Service",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 60,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			expectedID: "",
			shouldFind: false,
		},
		{
			name: "No match - different UseResponsesAPI",
			newService: &llm.ServiceConfig{
				ID:                      "different-id",
				Name:                    "Base Service",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         true,
			},
			expectedID: "",
			shouldFind: false,
		},
		{
			name: "No match with minimal fields",
			newService: &llm.ServiceConfig{
				Type:   llm.ServiceTypeOpenAI,
				APIKey: "key1",
			},
			expectedID: "",
			shouldFind: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findIdenticalService(serviceMap, tt.newService)

			if tt.shouldFind {
				assert.Equal(t, tt.expectedID, result)
			} else {
				assert.Empty(t, result)
			}
		})
	}
}

func TestServicesAreIdentical(t *testing.T) {
	baseService := llm.ServiceConfig{
		ID:                      "id1",
		Name:                    "Service A",
		Type:                    llm.ServiceTypeOpenAI,
		APIKey:                  "key1",
		OrgID:                   "org1",
		DefaultModel:            "gpt-4",
		APIURL:                  "https://api.openai.com",
		InputTokenLimit:         4000,
		StreamingTimeoutSeconds: 30,
		OutputTokenLimit:        2000,
		UseResponsesAPI:         false,
	}

	tests := []struct {
		name        string
		serviceA    llm.ServiceConfig
		serviceB    llm.ServiceConfig
		shouldMatch bool
	}{
		{
			name:        "Identical services",
			serviceA:    baseService,
			serviceB:    baseService,
			shouldMatch: true,
		},
		{
			name:     "Different ID but otherwise identical - should match",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id2",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			shouldMatch: true,
		},
		{
			name:     "Different Name but otherwise identical - should match",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service B",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			shouldMatch: true,
		},
		{
			name:     "Different Type",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeAnthropic,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			shouldMatch: false,
		},
		{
			name:     "Different APIKey",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "different-key",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			shouldMatch: false,
		},
		{
			name:     "Different OrgID",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org2",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			shouldMatch: false,
		},
		{
			name:     "Different DefaultModel",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-3.5",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			shouldMatch: false,
		},
		{
			name:     "Different APIURL",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://different.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			shouldMatch: false,
		},
		{
			name:     "Different InputTokenLimit",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         8000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			shouldMatch: false,
		},
		{
			name:     "Different StreamingTimeoutSeconds",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 60,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
			},
			shouldMatch: false,
		},
		{
			name:     "Different OutputTokenLimit",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        4000,
				UseResponsesAPI:         false,
			},
			shouldMatch: false,
		},
		{
			name:     "Different UseResponsesAPI",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeOpenAI,
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         true,
			},
			shouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := servicesAreIdentical(tt.serviceA, tt.serviceB)
			assert.Equal(t, tt.shouldMatch, result)
		})
	}
}

func TestMigrateServicesToBots(t *testing.T) {
	tests := []struct {
		name           string
		existingBots   []llm.BotConfig
		oldConfigJSON  string
		expectMigrated bool
		expectError    bool
		validateResult func(t *testing.T, result Config)
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
			name:           "No bots and no old services - should skip",
			existingBots:   []llm.BotConfig{},
			oldConfigJSON:  `{"config": {"services": []}}`,
			expectMigrated: false,
			expectError:    false,
			validateResult: func(t *testing.T, result Config) {
				assert.Empty(t, result.Services)
				assert.Empty(t, result.Bots)
			},
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
			validateResult: func(t *testing.T, result Config) {
				require.Len(t, result.Services, 1)
				assert.Equal(t, "openai", result.Services[0].Type)
				assert.Equal(t, "OpenAI GPT-4", result.Services[0].Name)
				assert.Equal(t, "gpt-4", result.Services[0].DefaultModel)
				assert.Equal(t, "org-123", result.Services[0].OrgID)
				assert.Equal(t, "sk-test-key", result.Services[0].APIKey)
				assert.Equal(t, 4000, result.Services[0].InputTokenLimit)
				assert.NotEmpty(t, result.Services[0].ID)

				require.Len(t, result.Bots, 1)
				assert.Equal(t, "ai1", result.Bots[0].Name)
				assert.Equal(t, "OpenAI GPT-4", result.Bots[0].DisplayName)
				assert.Equal(t, result.Services[0].ID, result.Bots[0].ServiceID)
				assert.True(t, result.Bots[0].MCPDynamicToolLoading)
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
			validateResult: func(t *testing.T, result Config) {
				require.Len(t, result.Services, 2)

				assert.Equal(t, "openai", result.Services[0].Type)
				assert.Equal(t, "OpenAI GPT-4", result.Services[0].Name)
				assert.Equal(t, "sk-openai-key", result.Services[0].APIKey)

				assert.Equal(t, "anthropic", result.Services[1].Type)
				assert.Equal(t, "Anthropic Claude", result.Services[1].Name)
				assert.Equal(t, "sk-anthropic-key", result.Services[1].APIKey)

				require.Len(t, result.Bots, 2)
				assert.Equal(t, "OpenAI GPT-4", result.Bots[0].DisplayName)
				assert.Equal(t, result.Services[0].ID, result.Bots[0].ServiceID)
				assert.True(t, result.Bots[0].MCPDynamicToolLoading)
				assert.Equal(t, "Anthropic Claude", result.Bots[1].DisplayName)
				assert.Equal(t, result.Services[1].ID, result.Bots[1].ServiceID)
				assert.True(t, result.Bots[1].MCPDynamicToolLoading)
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
			validateResult: func(t *testing.T, result Config) {
				require.Len(t, result.Services, 1)
				assert.Equal(t, "openaicompatible", result.Services[0].Type)
				assert.Equal(t, "https://custom-llm.example.com/v1", result.Services[0].APIURL)
				assert.Equal(t, "custom-key", result.Services[0].APIKey)
				assert.Equal(t, "custom-model", result.Services[0].DefaultModel)

				require.Len(t, result.Bots, 1)
				assert.Equal(t, "ai1", result.Bots[0].Name)
				assert.True(t, result.Bots[0].MCPDynamicToolLoading)
			},
		},
		{
			name:           "loadLegacyConfig returns error - should propagate",
			existingBots:   []llm.BotConfig{},
			oldConfigJSON:  "",
			expectMigrated: false,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Bots: tt.existingBots,
			}

			// Create loadLegacyConfig function from test JSON
			loadLegacyConfig := func() (LegacyServiceConfig, error) {
				if tt.oldConfigJSON == "" {
					return LegacyServiceConfig{}, fmt.Errorf("no legacy config available")
				}
				var legacy LegacyServiceConfig
				err := json.Unmarshal([]byte(tt.oldConfigJSON), &legacy)
				if err != nil {
					return LegacyServiceConfig{}, fmt.Errorf("failed to unmarshal test JSON: %w", err)
				}
				return legacy, nil
			}

			resultConfig, migrated, err := MigrateServicesToBots(cfg, loadLegacyConfig)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectMigrated, migrated)

			if tt.validateResult != nil {
				tt.validateResult(t, resultConfig)
			}
		})
	}
}

func TestRunAllLegacyMigrations(t *testing.T) {
	tests := []struct {
		name           string
		inputConfig    Config
		legacyJSON     string
		expectChanged  bool
		expectError    bool
		validateResult func(t *testing.T, result Config)
	}{
		{
			name:          "No migrations needed - empty config",
			inputConfig:   Config{},
			legacyJSON:    `{"config": {"services": []}}`,
			expectChanged: false,
			expectError:   false,
		},
		{
			name:        "Both migrations run sequentially",
			inputConfig: Config{},
			legacyJSON: `{
				"config": {
					"services": [
						{
							"name": "OpenAI",
							"serviceName": "openai",
							"defaultModel": "gpt-4",
							"apiKey": "key1",
							"tokenLimit": 4000
						}
					]
				}
			}`,
			expectChanged: true,
			expectError:   false,
			validateResult: func(t *testing.T, result Config) {
				// After both migrations: should have services and bots with ServiceID set
				require.Len(t, result.Services, 1)
				require.Len(t, result.Bots, 1)
				assert.NotEmpty(t, result.Bots[0].ServiceID)
				assert.Equal(t, result.Services[0].ID, result.Bots[0].ServiceID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadLegacyConfig := func() (LegacyServiceConfig, error) {
				var legacy LegacyServiceConfig
				err := json.Unmarshal([]byte(tt.legacyJSON), &legacy)
				return legacy, err
			}

			result, changed, err := RunAllLegacyMigrations(tt.inputConfig, loadLegacyConfig)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectChanged, changed)

			if tt.validateResult != nil {
				tt.validateResult(t, result)
			}
		})
	}
}
