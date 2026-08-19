// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestResolveStructuredOutputCapability(t *testing.T) {
	tests := []struct {
		name  string
		svc   llm.ServiceConfig
		model string
		want  llm.StructuredOutputCapability
	}{
		{
			name:  "openai with a structured-output model family is supported",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o",
			want:  llm.StructuredOutputCapabilitySupported,
		},
		{
			name:  "openai dated snapshot of a supported family is supported",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o-2024-11-20",
			want:  llm.StructuredOutputCapabilitySupported,
		},
		{
			name:  "openai reasoning model is supported",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "o3-mini",
			want:  llm.StructuredOutputCapabilitySupported,
		},
		{
			name:  "openai gpt-4o snapshot predating structured outputs is unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o-2024-05-13",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "openai legacy chat model is unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-3.5-turbo",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "openai with an empty model is unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "openai compatible is unknown even for an openai model name",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAICompatible, APIURL: "http://localhost:11434/v1"},
			model: "gpt-4o",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "openai compatible on the responses API is still unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAICompatible, APIURL: "http://localhost:8000/v1", UseResponsesAPI: true},
			model: "gpt-4o",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "openai compatible with an unknown model is unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAICompatible, APIURL: "http://localhost:11434/v1"},
			model: "llama3.1:70b",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "azure is unknown because deployment names carry no model identity",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeAzure, APIKey: "key", APIURL: "https://x.openai.azure.com", UseResponsesAPI: true},
			model: "my-gpt4o-deployment",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "anthropic is unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeAnthropic, APIKey: "key"},
			model: "claude-sonnet-4-5",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "gemini model is supported",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeGemini, APIKey: "key"},
			model: "gemini-2.5-pro",
			want:  llm.StructuredOutputCapabilitySupported,
		},
		{
			name:  "gemma on the gemini endpoint is unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeGemini, APIKey: "key"},
			model: "gemma-3-27b-it",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "vertex is unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeVertex, VertexProjectID: "p", Region: "us-east1"},
			model: "gemini-2.5-pro",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "bedrock is unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeBedrock, Region: "us-east-1"},
			model: "anthropic.claude-sonnet-4-5-v1:0",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "cohere is unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeCohere, APIKey: "key"},
			model: "command-r-plus",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "mistral is unknown",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeMistral, APIKey: "key"},
			model: "mistral-large-latest",
			want:  llm.StructuredOutputCapabilityUnknown,
		},
		{
			name:  "scale is unsupported",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeScale, APIKey: "key", APIURL: "https://scale.example"},
			model: "gpt-4o",
			want:  llm.StructuredOutputCapabilityUnsupported,
		},
		{
			name:  "load-test mock is unsupported",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeLoadTestMock},
			model: "mock",
			want:  llm.StructuredOutputCapabilityUnsupported,
		},
		{
			name:  "unrecognized service type is unsupported",
			svc:   llm.ServiceConfig{ID: "s", Type: "future-provider"},
			model: "gpt-4o",
			want:  llm.StructuredOutputCapabilityUnsupported,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ResolveStructuredOutputCapability(tt.svc, tt.model))
		})
	}
}

// TestResolveStructuredOutputCapabilityNeverSupportedForUnknownOpenAICompatibleModels
// pins the conservative default: an operator-run endpoint must never silently
// start receiving native JSON schemas because a model name looked familiar.
func TestResolveStructuredOutputCapabilityNeverSupportedForUnknownOpenAICompatibleModels(t *testing.T) {
	models := []string{"", "llama3.1", "qwen2.5-72b-instruct", "gpt-4o", "gpt-5", "o3", "custom/model:latest"}

	for _, model := range models {
		t.Run("model="+model, func(t *testing.T) {
			svc := llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAICompatible, APIURL: "http://localhost:11434/v1"}
			assert.Equal(t, llm.StructuredOutputCapabilityUnknown, ResolveStructuredOutputCapability(svc, model))
		})
	}
}

func TestResolveStructuredOutputCapabilityCoversEveryServiceType(t *testing.T) {
	// Every service type the plugin knows must get an explicit answer, so a new
	// type cannot fall through to a permissive default.
	serviceTypes := []string{
		llm.ServiceTypeOpenAI,
		llm.ServiceTypeOpenAICompatible,
		llm.ServiceTypeAzure,
		llm.ServiceTypeAnthropic,
		llm.ServiceTypeCohere,
		llm.ServiceTypeBedrock,
		llm.ServiceTypeMistral,
		llm.ServiceTypeScale,
		llm.ServiceTypeGemini,
		llm.ServiceTypeVertex,
		llm.ServiceTypeLoadTestMock,
	}

	valid := map[llm.StructuredOutputCapability]bool{
		llm.StructuredOutputCapabilitySupported:   true,
		llm.StructuredOutputCapabilityUnknown:     true,
		llm.StructuredOutputCapabilityUnsupported: true,
	}

	for _, serviceType := range serviceTypes {
		t.Run(serviceType, func(t *testing.T) {
			got := ResolveStructuredOutputCapability(llm.ServiceConfig{ID: "s", Type: serviceType}, "some-model")
			assert.True(t, valid[got], "unexpected capability %q", got)
		})
	}
}
