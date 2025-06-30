// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultipleServiceInstancesWithDifferentHeaders(t *testing.T) {
	// Example configuration with multiple OpenAI service instances,
	// each with different custom headers
	config := Config{
		Services: []llm.ServiceConfig{
			{
				Name:         "openai-production",
				Type:         "openai",
				APIKey:       "sk-prod-key",
				DefaultModel: "gpt-4",
				CustomHeaders: map[string]string{
					"X-Environment":    "production",
					"X-Cost-Center":    "marketing",
					"X-Request-Source": "mattermost-prod",
				},
			},
			{
				Name:         "openai-development",
				Type:         "openai",
				APIKey:       "sk-dev-key",
				DefaultModel: "gpt-3.5-turbo",
				CustomHeaders: map[string]string{
					"X-Environment":    "development",
					"X-Cost-Center":    "engineering",
					"X-Request-Source": "mattermost-dev",
					"X-Debug":          "true",
				},
			},
			{
				Name:         "openai-proxy",
				Type:         "openai_compatible",
				APIKey:       "sk-proxy-key",
				APIURL:       "https://proxy.company.com/v1",
				DefaultModel: "gpt-4",
				CustomHeaders: map[string]string{
					"X-Proxy-Auth":     "Bearer company-token",
					"X-Department":     "ai-ops",
					"X-Request-Source": "mattermost-proxy",
				},
			},
		},
		Bots: []llm.BotConfig{
			{
				ID:          "prod-bot",
				Name:        "production-assistant",
				DisplayName: "Production Assistant",
				Service: llm.ServiceConfig{
					Name:         "openai-production",
					Type:         "openai",
					APIKey:       "sk-prod-key",
					DefaultModel: "gpt-4",
					CustomHeaders: map[string]string{
						"X-Environment":    "production",
						"X-Cost-Center":    "marketing",
						"X-Request-Source": "mattermost-prod",
					},
				},
			},
			{
				ID:          "dev-bot",
				Name:        "development-assistant",
				DisplayName: "Development Assistant",
				Service: llm.ServiceConfig{
					Name:         "openai-development",
					Type:         "openai",
					APIKey:       "sk-dev-key",
					DefaultModel: "gpt-3.5-turbo",
					CustomHeaders: map[string]string{
						"X-Environment":    "development",
						"X-Cost-Center":    "engineering",
						"X-Request-Source": "mattermost-dev",
						"X-Debug":          "true",
					},
				},
			},
			{
				ID:          "proxy-bot",
				Name:        "proxy-assistant",
				DisplayName: "Proxy Assistant",
				Service: llm.ServiceConfig{
					Name:         "openai-proxy",
					Type:         "openai_compatible",
					APIKey:       "sk-proxy-key",
					APIURL:       "https://proxy.company.com/v1",
					DefaultModel: "gpt-4",
					CustomHeaders: map[string]string{
						"X-Proxy-Auth":     "Bearer company-token",
						"X-Department":     "ai-ops",
						"X-Request-Source": "mattermost-proxy",
					},
				},
			},
		},
	}

	// Verify each service has unique custom headers
	assert.NotEqual(t, config.Services[0].CustomHeaders, config.Services[1].CustomHeaders)
	assert.NotEqual(t, config.Services[1].CustomHeaders, config.Services[2].CustomHeaders)
	assert.NotEqual(t, config.Services[0].CustomHeaders, config.Services[2].CustomHeaders)

	// Verify production service headers
	prodHeaders := config.Services[0].CustomHeaders
	assert.Equal(t, "production", prodHeaders["X-Environment"])
	assert.Equal(t, "marketing", prodHeaders["X-Cost-Center"])
	assert.Equal(t, "mattermost-prod", prodHeaders["X-Request-Source"])
	_, hasDebug := prodHeaders["X-Debug"]
	assert.False(t, hasDebug, "Production service should not have debug header")

	// Verify development service headers
	devHeaders := config.Services[1].CustomHeaders
	assert.Equal(t, "development", devHeaders["X-Environment"])
	assert.Equal(t, "engineering", devHeaders["X-Cost-Center"])
	assert.Equal(t, "mattermost-dev", devHeaders["X-Request-Source"])
	assert.Equal(t, "true", devHeaders["X-Debug"])

	// Verify proxy service headers
	proxyHeaders := config.Services[2].CustomHeaders
	assert.Equal(t, "Bearer company-token", proxyHeaders["X-Proxy-Auth"])
	assert.Equal(t, "ai-ops", proxyHeaders["X-Department"])
	assert.Equal(t, "mattermost-proxy", proxyHeaders["X-Request-Source"])

	// Test OpenAI config transformation for each service
	prodOpenAIConfig := OpenAIConfigFromServiceConfig(config.Services[0])
	devOpenAIConfig := OpenAIConfigFromServiceConfig(config.Services[1])
	proxyOpenAIConfig := OpenAIConfigFromServiceConfig(config.Services[2])

	// Verify custom headers are preserved through transformation
	assert.Equal(t, config.Services[0].CustomHeaders, prodOpenAIConfig.CustomHeaders)
	assert.Equal(t, config.Services[1].CustomHeaders, devOpenAIConfig.CustomHeaders)
	assert.Equal(t, config.Services[2].CustomHeaders, proxyOpenAIConfig.CustomHeaders)

	// Verify other config fields are correct
	assert.Equal(t, "sk-prod-key", prodOpenAIConfig.APIKey)
	assert.Equal(t, "sk-dev-key", devOpenAIConfig.APIKey)
	assert.Equal(t, "sk-proxy-key", proxyOpenAIConfig.APIKey)
	assert.Equal(t, "https://proxy.company.com/v1", proxyOpenAIConfig.APIURL) // APIURL should be preserved
}

func TestServiceConfigJSONSerialization(t *testing.T) {
	// Test that custom headers serialize/deserialize correctly
	original := llm.ServiceConfig{
		Name:         "test-service",
		Type:         "openai",
		APIKey:       "sk-test",
		DefaultModel: "gpt-4",
		CustomHeaders: map[string]string{
			"X-Custom-Header-1": "value1",
			"X-Custom-Header-2": "value2",
			"Authorization":     "Bearer override-token",
		},
	}

	// Serialize to JSON
	jsonData, err := json.Marshal(original)
	require.NoError(t, err)

	// Deserialize from JSON
	var deserialized llm.ServiceConfig
	err = json.Unmarshal(jsonData, &deserialized)
	require.NoError(t, err)

	// Verify custom headers are preserved
	assert.Equal(t, original.CustomHeaders, deserialized.CustomHeaders)
	assert.Equal(t, "value1", deserialized.CustomHeaders["X-Custom-Header-1"])
	assert.Equal(t, "value2", deserialized.CustomHeaders["X-Custom-Header-2"])
	assert.Equal(t, "Bearer override-token", deserialized.CustomHeaders["Authorization"])
}
