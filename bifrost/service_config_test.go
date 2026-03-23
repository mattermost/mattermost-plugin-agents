// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

func TestResolveServiceConfig(t *testing.T) {
	t.Run("resolves dynamic provider from standard providers", func(t *testing.T) {
		resolvedConfig, err := resolveServiceConfig(llm.ServiceConfig{
			ID:   "service-1",
			Type: "vertex",
		}, 30*time.Second)
		require.NoError(t, err)
		require.Equal(t, schemas.Vertex, resolvedConfig.Provider)
		require.Len(t, resolvedConfig.Keys, 1)
		require.Equal(t, 1.0, resolvedConfig.Keys[0].Weight)
		require.NotNil(t, resolvedConfig.ProviderConfig)
	})

	t.Run("openaicompatible resolves to openai with normalized base url", func(t *testing.T) {
		resolvedConfig, err := resolveServiceConfig(llm.ServiceConfig{
			ID:     "service-2",
			Type:   llm.ServiceTypeOpenAICompatible,
			APIURL: "https://api.openai.com/v1/",
		}, 30*time.Second)
		require.NoError(t, err)
		require.Equal(t, schemas.OpenAI, resolvedConfig.Provider)
		require.Equal(t, "https://api.openai.com", resolvedConfig.ProviderConfig.NetworkConfig.BaseURL)
	})

	t.Run("advanced key json overlays provider specific config", func(t *testing.T) {
		resolvedConfig, err := resolveServiceConfig(llm.ServiceConfig{
			ID:   "service-3",
			Type: "vllm",
			BifrostKeyJSON: `{
				"vllm_key_config": {
					"url": {"val": "http://vllm:8000"},
					"model_name": "llama-3.1-8b"
				}
			}`,
		}, 30*time.Second)
		require.NoError(t, err)
		require.NotNil(t, resolvedConfig.Keys[0].VLLMKeyConfig)
		require.Equal(t, "http://vllm:8000", resolvedConfig.Keys[0].VLLMKeyConfig.URL.Val)
		require.Equal(t, "llama-3.1-8b", resolvedConfig.Keys[0].VLLMKeyConfig.ModelName)
	})

	t.Run("advanced provider config overlays network settings", func(t *testing.T) {
		resolvedConfig, err := resolveServiceConfig(llm.ServiceConfig{
			ID:   "service-4",
			Type: llm.ServiceTypeOpenAI,
			BifrostProviderConfigJSON: `{
				"network_config": {
					"base_url": "https://example.com/v1",
					"default_request_timeout_in_seconds": 120
				}
			}`,
		}, 30*time.Second)
		require.NoError(t, err)
		require.Equal(t, "https://example.com", resolvedConfig.ProviderConfig.NetworkConfig.BaseURL)
		require.Equal(t, 120, resolvedConfig.ProviderConfig.NetworkConfig.DefaultRequestTimeoutInSeconds)
	})
}
