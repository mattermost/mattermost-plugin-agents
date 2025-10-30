// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/openai"
)

func TestOpenAIConfigFromServiceConfig(t *testing.T) {
	tests := []struct {
		name          string
		serviceConfig llm.ServiceConfig
		botConfig     llm.BotConfig
		validate      func(t *testing.T, config openai.Config)
	}{
		{
			name: "basic configuration with reasoning enabled",
			serviceConfig: llm.ServiceConfig{
				ID:                      "service1",
				APIKey:                  "test-api-key",
				OrgID:                   "test-org",
				DefaultModel:            "gpt-4o",
				InputTokenLimit:         128000,
				OutputTokenLimit:        4096,
				StreamingTimeoutSeconds: 60,
				SendUserID:              true,
				UseResponsesAPI:         true,
			},
			botConfig: llm.BotConfig{
				ID:                 "bot1",
				Name:               "testbot",
				DisplayName:        "Test Bot",
				ServiceID:          "service1",
				ReasoningEnabled:   true,
				ReasoningEffort:    "medium",
				EnabledNativeTools: []string{"web_search"},
			},
			validate: func(t *testing.T, config openai.Config) {
				assert.Equal(t, "test-api-key", config.APIKey)
				assert.Equal(t, "test-org", config.OrgID)
				assert.Equal(t, "gpt-4o", config.DefaultModel)
				assert.Equal(t, 128000, config.InputTokenLimit)
				assert.Equal(t, 4096, config.OutputTokenLimit)
				assert.Equal(t, 60*time.Second, config.StreamingTimeout)
				assert.True(t, config.SendUserID)
				assert.True(t, config.UseResponsesAPI)
				assert.Equal(t, []string{"web_search"}, config.EnabledNativeTools)
				assert.True(t, config.BotConfig.ReasoningEnabled)
				assert.Equal(t, "medium", config.BotConfig.ReasoningEffort)
			},
		},
		{
			name: "configuration with reasoning disabled",
			serviceConfig: llm.ServiceConfig{
				ID:               "service2",
				APIKey:           "test-api-key-2",
				DefaultModel:     "gpt-4",
				InputTokenLimit:  8192,
				OutputTokenLimit: 2048,
				UseResponsesAPI:  true,
			},
			botConfig: llm.BotConfig{
				ID:               "bot2",
				Name:             "testbot2",
				DisplayName:      "Test Bot 2",
				ServiceID:        "service2",
				ReasoningEnabled: false,
			},
			validate: func(t *testing.T, config openai.Config) {
				assert.Equal(t, "test-api-key-2", config.APIKey)
				assert.Equal(t, "gpt-4", config.DefaultModel)
				assert.False(t, config.BotConfig.ReasoningEnabled)
				assert.Equal(t, 30*time.Second, config.StreamingTimeout) // default
			},
		},
		{
			name: "configuration with custom reasoning effort",
			serviceConfig: llm.ServiceConfig{
				ID:               "service3",
				APIKey:           "test-api-key-3",
				DefaultModel:     "o1-preview",
				InputTokenLimit:  128000,
				OutputTokenLimit: 4096,
				UseResponsesAPI:  true,
			},
			botConfig: llm.BotConfig{
				ID:               "bot3",
				Name:             "testbot3",
				DisplayName:      "Test Bot 3",
				ServiceID:        "service3",
				ReasoningEnabled: true,
				ReasoningEffort:  "high",
			},
			validate: func(t *testing.T, config openai.Config) {
				assert.True(t, config.BotConfig.ReasoningEnabled)
				assert.Equal(t, "high", config.BotConfig.ReasoningEffort)
			},
		},
		{
			name: "configuration with custom streaming timeout",
			serviceConfig: llm.ServiceConfig{
				ID:                      "service4",
				APIKey:                  "test-api-key-4",
				DefaultModel:            "gpt-4-turbo",
				InputTokenLimit:         128000,
				OutputTokenLimit:        4096,
				StreamingTimeoutSeconds: 120,
				UseResponsesAPI:         false,
			},
			botConfig: llm.BotConfig{
				ID:          "bot4",
				Name:        "testbot4",
				DisplayName: "Test Bot 4",
				ServiceID:   "service4",
			},
			validate: func(t *testing.T, config openai.Config) {
				assert.Equal(t, 120*time.Second, config.StreamingTimeout)
				assert.False(t, config.UseResponsesAPI)
			},
		},
		{
			name: "configuration with multiple native tools",
			serviceConfig: llm.ServiceConfig{
				ID:               "service5",
				APIKey:           "test-api-key-5",
				DefaultModel:     "gpt-4o",
				InputTokenLimit:  128000,
				OutputTokenLimit: 4096,
				UseResponsesAPI:  true,
			},
			botConfig: llm.BotConfig{
				ID:                 "bot5",
				Name:               "testbot5",
				DisplayName:        "Test Bot 5",
				ServiceID:          "service5",
				EnabledNativeTools: []string{"web_search", "file_search", "code_interpreter"},
			},
			validate: func(t *testing.T, config openai.Config) {
				assert.Equal(t, []string{"web_search", "file_search", "code_interpreter"}, config.EnabledNativeTools)
			},
		},
		{
			name: "configuration with API URL",
			serviceConfig: llm.ServiceConfig{
				ID:               "service6",
				APIKey:           "test-api-key-6",
				APIURL:           "https://custom-api.example.com",
				DefaultModel:     "custom-model",
				InputTokenLimit:  32000,
				OutputTokenLimit: 2000,
			},
			botConfig: llm.BotConfig{
				ID:          "bot6",
				Name:        "testbot6",
				DisplayName: "Test Bot 6",
				ServiceID:   "service6",
			},
			validate: func(t *testing.T, config openai.Config) {
				assert.Equal(t, "https://custom-api.example.com", config.APIURL)
				assert.Equal(t, "custom-model", config.DefaultModel)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := OpenAIConfigFromServiceConfig(tt.serviceConfig, tt.botConfig)
			tt.validate(t, config)
		})
	}
}
