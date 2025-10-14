// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

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

func TestMigrateSeparateServicesFromBots(t *testing.T) {
	tests := []struct {
		name           string
		inputConfig    config.Config
		migrationDone  bool
		expectMigrated bool
		expectError    bool
		validateResult func(t *testing.T, result config.Config)
	}{
		{
			name: "Migration already done - should skip",
			inputConfig: config.Config{
				Bots: []llm.BotConfig{
					{
						ID:   "bot1",
						Name: "bot1",
						Service: &llm.ServiceConfig{
							Type:   llm.ServiceTypeOpenAI,
							APIKey: "key1",
						},
					},
				},
			},
			migrationDone:  true,
			expectMigrated: false,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				// Config should remain unchanged
				assert.Len(t, result.Services, 0)
				assert.Len(t, result.Bots, 1)
				assert.NotNil(t, result.Bots[0].Service)
			},
		},
		{
			name: "Services already populated - should skip and mark as done",
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
			migrationDone:  false,
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
			name:           "No bots exist - should skip and mark as done",
			inputConfig:    config.Config{},
			migrationDone:  false,
			expectMigrated: false,
			expectError:    false,
			validateResult: func(t *testing.T, result config.Config) {
				assert.Len(t, result.Services, 0)
				assert.Len(t, result.Bots, 0)
			},
		},
		{
			name: "Bots already have ServiceID - should skip and mark as done",
			inputConfig: config.Config{
				Bots: []llm.BotConfig{
					{
						ID:        "bot1",
						Name:      "bot1",
						ServiceID: "service1",
					},
				},
			},
			migrationDone:  false,
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
			migrationDone:  false,
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
			migrationDone:  false,
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
							EnabledNativeTools:      []string{"tool1", "tool2"},
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
							EnabledNativeTools:      []string{"tool1", "tool2"},
						},
					},
				},
			},
			migrationDone:  false,
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
							EnabledNativeTools:      []string{"tool1", "tool2"},
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
							EnabledNativeTools:      []string{"tool1", "tool2"},
						},
					},
				},
			},
			migrationDone:  false,
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
			migrationDone:  false,
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
			migrationDone:  false,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock API
			mockAPI := &plugintest.API{}

			// Setup KV store for migration flag
			// KVGet returns ([]byte, error) - need to return JSON-marshaled bool
			var migrationBytes []byte
			if tt.migrationDone {
				migrationBytes = []byte("true")
			} else {
				migrationBytes = []byte("false")
			}
			mockAPI.On("KVGet", "migrate_separate_services_from_bots_done").Return(migrationBytes, nil)

			// Mock mutex lock/unlock operations (cluster.Mutex uses KVSetWithOptions for locking)
			mockAPI.On("KVSetWithOptions", mock.MatchedBy(func(key string) bool {
				return key == "mutex_migrate_separate_services_from_bots" || key == "migrate_separate_services_from_bots_done"
			}), mock.Anything, mock.Anything).Return(true, nil)

			mockAPI.On("KVDelete", "mutex_migrate_separate_services_from_bots").Return(nil)

			// Setup logging - accept variadic arguments for structured logging
			mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			mockAPI.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)

			pluginAPI := pluginapi.NewClient(mockAPI, nil)

			// Run migration
			migrated, resultConfig, err := migrateSeparateServicesFromBots(pluginAPI, tt.inputConfig)

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

func TestFindExistingServiceID(t *testing.T) {
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
		SendUserID:              true,
		OutputTokenLimit:        2000,
		UseResponsesAPI:         false,
		EnabledNativeTools:      []string{"tool1", "tool2"},
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
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
			},
			expectedID: "base-id",
			shouldFind: true,
		},
		{
			name: "Match found - different name but otherwise identical",
			newService: &llm.ServiceConfig{
				ID:                      "different-id",
				Name:                    "Different Name", // Name should be ignored
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
				EnabledNativeTools:      []string{"tool1", "tool2"},
			},
			expectedID: "base-id",
			shouldFind: true,
		},
		{
			name: "No match - different Type",
			newService: &llm.ServiceConfig{
				ID:                      "different-id",
				Name:                    "Base Service",
				Type:                    llm.ServiceTypeAnthropic, // Different
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				APIKey:                  "different-key", // Different
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				DefaultModel:            "gpt-3.5", // Different
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				InputTokenLimit:         8000, // Different
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				StreamingTimeoutSeconds: 60, // Different
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         true, // Different
				EnabledNativeTools:      []string{"tool1", "tool2"},
			},
			expectedID: "",
			shouldFind: false,
		},
		{
			name: "Match found - different EnabledNativeTools",
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
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool3"}, // Different
			},
			expectedID: "base-id",
			shouldFind: true,
		},
		{
			name: "Match found with empty OrgID and minimal fields",
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
		SendUserID:              true,
		OutputTokenLimit:        2000,
		UseResponsesAPI:         false,
		EnabledNativeTools:      []string{"tool1", "tool2"},
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
				ID:                      "id2", // Different ID
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
				EnabledNativeTools:      []string{"tool1", "tool2"},
			},
			shouldMatch: true,
		},
		{
			name:     "Different Name but otherwise identical - should match",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service B", // Different Name
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
				EnabledNativeTools:      []string{"tool1", "tool2"},
			},
			shouldMatch: true,
		},
		{
			name:     "Different Type",
			serviceA: baseService,
			serviceB: llm.ServiceConfig{
				ID:                      "id1",
				Name:                    "Service A",
				Type:                    llm.ServiceTypeAnthropic, // Different
				APIKey:                  "key1",
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				APIKey:                  "different-key", // Different
				OrgID:                   "org1",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				OrgID:                   "org2", // Different
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				DefaultModel:            "gpt-3.5", // Different
				APIURL:                  "https://api.openai.com",
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				APIURL:                  "https://different.com", // Different
				InputTokenLimit:         4000,
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				InputTokenLimit:         8000, // Different
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				StreamingTimeoutSeconds: 60, // Different
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
			},
			shouldMatch: false,
		},
		{
			name:     "Different SendUserID",
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
				SendUserID:              false, // Different
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				SendUserID:              true,
				OutputTokenLimit:        4000, // Different
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool2"},
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
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         true, // Different
				EnabledNativeTools:      []string{"tool1", "tool2"},
			},
			shouldMatch: false,
		},
		{
			name:     "Different EnabledNativeTools length",
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
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1"}, // Different length
			},
			shouldMatch: true,
		},
		{
			name:     "Different EnabledNativeTools content",
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
				SendUserID:              true,
				OutputTokenLimit:        2000,
				UseResponsesAPI:         false,
				EnabledNativeTools:      []string{"tool1", "tool3"}, // Different content
			},
			shouldMatch: true,
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
		migrationDone  bool
		expectMigrated bool
		expectError    bool
		validateResult func(t *testing.T, result config.Config)
	}{
		{
			name:           "Migration already done - should skip",
			existingBots:   []llm.BotConfig{},
			migrationDone:  true,
			expectMigrated: false,
			expectError:    false,
		},
		{
			name: "Bots already exist - should skip and mark as done",
			existingBots: []llm.BotConfig{
				{ID: "bot1", Name: "bot1"},
			},
			migrationDone:  false,
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
			migrationDone:  false,
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
			migrationDone:  false,
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
			migrationDone:  false,
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

			// Setup KV store for migration flag
			// KVGet returns ([]byte, error) - need to return JSON-marshaled bool
			var migrationBytes []byte
			if tt.migrationDone {
				migrationBytes = []byte("true")
			} else {
				migrationBytes = []byte("false")
			}
			mockAPI.On("KVGet", "migrate_services_to_bots_done").Return(migrationBytes, nil)

			// Mock mutex lock/unlock operations (cluster.Mutex uses KVSetWithOptions for locking)
			mockAPI.On("KVSetWithOptions", mock.MatchedBy(func(key string) bool {
				return key == "mutex_migrate_services_to_bots" || key == "migrate_services_to_bots_done"
			}), mock.Anything, mock.Anything).Return(true, nil)

			mockAPI.On("KVDelete", "mutex_migrate_services_to_bots").Return(nil)

			// Setup logging
			mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			mockAPI.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)

			// Mock LoadPluginConfiguration for cases where we need to load old config
			if tt.oldConfigJSON != "" {
				mockAPI.On("LoadPluginConfiguration", mock.AnythingOfType("*main.BotMigrationConfig")).Return(nil).Run(func(args mock.Arguments) {
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
			migrated, resultConfig, err := migrateServicesToBots(pluginAPI, cfg)

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
