// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/llm"
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
		{llm.ServiceTypeBedrock, false},
		{llm.ServiceTypeCohere, false},
		{llm.ServiceTypeMistral, false},
		{llm.ServiceTypeScale, false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.serviceType, func(t *testing.T) {
			assert.Equal(t, tt.want, supportsNativeTools(tt.serviceType))
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
			llmInstance, err := NewFromServiceConfig(service, bot)
			require.NoError(t, err)
			assert.Equal(t, tt.wantUseResponsesAPI, llmInstance.useResponsesAPI)
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
		{"Bedrock drops native tools", llm.ServiceTypeBedrock, false},
		{"Cohere drops native tools", llm.ServiceTypeCohere, false},
		{"Mistral drops native tools", llm.ServiceTypeMistral, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := llm.ServiceConfig{
				ID:     "test",
				Type:   tt.serviceType,
				APIKey: "key",
				APIURL: "http://localhost",
				Region: "us-east-1",
			}
			bot := llm.BotConfig{
				EnabledNativeTools: []string{"web_search"},
			}
			llmInstance, err := NewFromServiceConfig(service, bot)
			require.NoError(t, err)
			if tt.wantTools {
				assert.Equal(t, []string{"web_search"}, llmInstance.enabledNativeTools)
			} else {
				assert.Equal(t, []string{}, llmInstance.enabledNativeTools)
			}
		})
	}
}
