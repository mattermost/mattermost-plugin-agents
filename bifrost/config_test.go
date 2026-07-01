// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestSupportsNativeTools(t *testing.T) {
	tests := []struct {
		serviceType string
		want        bool
	}{
		{llm.ServiceTypeOpenAI, true},
		{llm.ServiceTypeOpenAICompatible, true},
		{llm.ServiceTypeAzure, true},
		{llm.ServiceTypeAnthropic, true},
		{llm.ServiceTypeGemini, true},
		{llm.ServiceTypeVertex, true},
		{llm.ServiceTypeBedrock, false},
		{llm.ServiceTypeCohere, false},
		{llm.ServiceTypeMistral, false},
		{llm.ServiceTypeScale, false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.serviceType, func(t *testing.T) {
			assert.Equal(t, tt.want, supportsNativeTools(tt.serviceType))
			assert.Equal(t, tt.want, SupportsNativeTools(tt.serviceType))
		})
	}
}

func TestFilterNativeToolsForServiceType(t *testing.T) {
	tools := []string{"web_search"}

	tests := []struct {
		name        string
		serviceType string
		tools       []string
		want        []string
	}{
		{"OpenAI keeps tools", llm.ServiceTypeOpenAI, tools, tools},
		{"Anthropic keeps tools", llm.ServiceTypeAnthropic, tools, tools},
		{"Gemini keeps tools", llm.ServiceTypeGemini, tools, tools},
		{"Vertex keeps tools", llm.ServiceTypeVertex, tools, tools},
		{"Bedrock drops tools", llm.ServiceTypeBedrock, tools, []string{}},
		{"Cohere drops tools", llm.ServiceTypeCohere, tools, []string{}},
		{"Mistral drops tools", llm.ServiceTypeMistral, tools, []string{}},
		{"nil tools stay nil", llm.ServiceTypeOpenAI, nil, nil},
		{"empty tools stay empty", llm.ServiceTypeOpenAI, []string{}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterNativeToolsForServiceType(tt.serviceType, tt.tools)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewFromServiceConfigOpenAIForcesResponsesAPI(t *testing.T) {
	tests := []struct {
		name                string
		serviceType         string
		useResponsesAPI     bool
		wantUseResponsesAPI bool
	}{
		{"OpenAI direct always uses Responses API", llm.ServiceTypeOpenAI, false, true},
		{"OpenAI direct with flag true", llm.ServiceTypeOpenAI, true, true},
		{"OpenAI Compatible respects flag false", llm.ServiceTypeOpenAICompatible, false, false},
		{"OpenAI Compatible respects flag true", llm.ServiceTypeOpenAICompatible, true, true},
		{"Anthropic respects flag false", llm.ServiceTypeAnthropic, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := llm.ServiceConfig{
				ID:              "test",
				Type:            tt.serviceType,
				APIKey:          "key",
				APIURL:          "http://localhost",
				Region:          "us-east-1",
				UseResponsesAPI: tt.useResponsesAPI,
			}
			bot := llm.BotConfig{
				EnabledNativeTools: []string{"web_search"},
			}
			llmInstance, err := NewFromServiceConfig(service, bot, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantUseResponsesAPI, llmInstance.useResponsesAPI)
		})
	}
}

// TestNewFromServiceConfigPropagatesInputTokenLimit pins the contract that a
// manually-set "Input token limit" in the system console flows through to
// the running LLM, so the context indicator can compute utilization. A user
// configured 250000 for an OpenAI bot and the context endpoint returned no
// input_token_limit; this catches that regression at the boundary.
func TestNewFromServiceConfigPropagatesInputTokenLimit(t *testing.T) {
	tests := []struct {
		name            string
		inputTokenLimit int
	}{
		{"manual 250000", 250000},
		{"zero passes through unchanged", 0},
		{"small value", 4096},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := llm.ServiceConfig{
				ID:              "test",
				Type:            llm.ServiceTypeOpenAI,
				APIKey:          "key",
				APIURL:          "http://localhost",
				InputTokenLimit: tt.inputTokenLimit,
			}
			llmInstance, err := NewFromServiceConfig(service, llm.BotConfig{}, nil)
			require.NoError(t, err)
			defer llmInstance.client.Shutdown()

			assert.Equal(t, tt.inputTokenLimit, llmInstance.InputTokenLimit(),
				"the manually-configured token limit must survive the trip through bifrost.Config "+
					"so the /context endpoint can render a utilization ring")
		})
	}
}

func TestNewFromServiceConfigFiltersNativeTools(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		wantTools   bool
	}{
		{"OpenAI keeps native tools", llm.ServiceTypeOpenAI, true},
		{"Anthropic keeps native tools", llm.ServiceTypeAnthropic, true},
		{"Gemini keeps native tools", llm.ServiceTypeGemini, true},
		{"Vertex keeps native tools", llm.ServiceTypeVertex, true},
		{"Bedrock drops native tools", llm.ServiceTypeBedrock, false},
		{"Cohere drops native tools", llm.ServiceTypeCohere, false},
		{"Mistral drops native tools", llm.ServiceTypeMistral, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := llm.ServiceConfig{
				ID:              "test",
				Type:            tt.serviceType,
				APIKey:          "key",
				APIURL:          "http://localhost",
				Region:          "us-east-1",
				VertexProjectID: "my-project",
			}
			bot := llm.BotConfig{
				EnabledNativeTools: []string{"web_search"},
			}
			llmInstance, err := NewFromServiceConfig(service, bot, nil)
			require.NoError(t, err)
			if tt.wantTools {
				assert.Equal(t, []string{"web_search"}, llmInstance.enabledNativeTools)
			} else {
				assert.Equal(t, []string{}, llmInstance.enabledNativeTools)
			}
		})
	}
}
