// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config_migrations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/assert"
)

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
		SendUserID:              true,
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
				SendUserID:              true,
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
			result := FindIdenticalService(serviceMap, tt.newService)

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
			},
			shouldMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ServicesAreIdentical(tt.serviceA, tt.serviceB)
			assert.Equal(t, tt.shouldMatch, result)
		})
	}
}
