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
		want  bool
	}{
		{
			name:  "openai with a structured-output model family is capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o",
			want:  true,
		},
		{
			name:  "openai dated snapshot of a supported family is capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o-2024-11-20",
			want:  true,
		},
		{
			name:  "openai reasoning model is capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "o3-mini",
			want:  true,
		},
		{
			name:  "openai o1 snapshot with structured outputs is capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "o1-2024-12-17",
			want:  true,
		},
		{
			name:  "openai gpt-4o snapshot predating structured outputs is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o-2024-05-13",
			want:  false,
		},
		{
			name:  "openai o1-mini has no structured outputs",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "o1-mini",
			want:  false,
		},
		{
			name:  "openai o1-preview predates structured outputs",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "o1-preview-2024-09-12",
			want:  false,
		},
		{
			name:  "openai chatgpt-serving alias is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "chatgpt-4o-latest",
			want:  false,
		},
		{
			name:  "openai audio variant of a supported family is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o-audio-preview",
			want:  false,
		},
		{
			name:  "openai dated audio snapshot is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o-audio-preview-2024-12-17",
			want:  false,
		},
		{
			name:  "openai mini audio variant is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o-mini-audio-preview",
			want:  false,
		},
		{
			name:  "openai realtime variant is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o-realtime-preview",
			want:  false,
		},
		{
			name:  "openai search variant is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-4o-search-preview",
			want:  false,
		},
		{
			name:  "openai legacy chat model is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "gpt-3.5-turbo",
			want:  false,
		},
		{
			name:  "openai with an empty model is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			model: "",
			want:  false,
		},
		{
			name:  "openai compatible is not capable even for an openai model name",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAICompatible, APIURL: "http://localhost:11434/v1"},
			model: "gpt-4o",
			want:  false,
		},
		{
			name:  "openai compatible on the responses API is still not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAICompatible, APIURL: "http://localhost:8000/v1", UseResponsesAPI: true},
			model: "gpt-4o",
			want:  false,
		},
		{
			name:  "openai compatible with an unknown model is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeOpenAICompatible, APIURL: "http://localhost:11434/v1"},
			model: "llama3.1:70b",
			want:  false,
		},
		{
			name:  "azure is not capable because deployment names carry no model identity",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeAzure, APIKey: "key", APIURL: "https://x.openai.azure.com", UseResponsesAPI: true},
			model: "my-gpt4o-deployment",
			want:  false,
		},
		{
			name:  "anthropic is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeAnthropic, APIKey: "key"},
			model: "claude-sonnet-4-5",
			want:  false,
		},
		{
			name:  "gemini model is capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeGemini, APIKey: "key"},
			model: "gemini-2.5-pro",
			want:  true,
		},
		{
			name:  "gemini 1.5 model is capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeGemini, APIKey: "key"},
			model: "gemini-1.5-flash",
			want:  true,
		},
		{
			name:  "gemini 1.0 generation predates response schemas",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeGemini, APIKey: "key"},
			model: "gemini-1.0-pro",
			want:  false,
		},
		{
			name:  "legacy gemini-pro alias is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeGemini, APIKey: "key"},
			model: "gemini-pro",
			want:  false,
		},
		{
			name:  "legacy gemini-pro-vision alias is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeGemini, APIKey: "key"},
			model: "gemini-pro-vision",
			want:  false,
		},
		{
			name:  "gemini embedding model is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeGemini, APIKey: "key"},
			model: "gemini-embedding-001",
			want:  false,
		},
		{
			name:  "gemma on the gemini endpoint is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeGemini, APIKey: "key"},
			model: "gemma-3-27b-it",
			want:  false,
		},
		{
			name:  "vertex is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeVertex, VertexProjectID: "p", Region: "us-east1"},
			model: "gemini-2.5-pro",
			want:  false,
		},
		{
			name:  "bedrock is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeBedrock, Region: "us-east-1"},
			model: "anthropic.claude-sonnet-4-5-v1:0",
			want:  false,
		},
		{
			name:  "cohere is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeCohere, APIKey: "key"},
			model: "command-r-plus",
			want:  false,
		},
		{
			name:  "mistral is not capable",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeMistral, APIKey: "key"},
			model: "mistral-large-latest",
			want:  false,
		},
		{
			name:  "scale has no native path",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeScale, APIKey: "key", APIURL: "https://scale.example"},
			model: "gpt-4o",
			want:  false,
		},
		{
			name:  "load-test mock has no native path",
			svc:   llm.ServiceConfig{ID: "s", Type: llm.ServiceTypeLoadTestMock},
			model: "mock",
			want:  false,
		},
		{
			name:  "unrecognized service type has no native path",
			svc:   llm.ServiceConfig{ID: "s", Type: "future-provider"},
			model: "gpt-4o",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ResolveStructuredOutputCapability(tt.svc, tt.model))
		})
	}
}
