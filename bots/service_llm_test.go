// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openAIService(id string) llm.ServiceConfig {
	return llm.ServiceConfig{
		ID:           id,
		Name:         id,
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "key",
		DefaultModel: "gpt-4o",
	}
}

func TestServiceCanServeCompletions(t *testing.T) {
	tests := []struct {
		name string
		svc  llm.ServiceConfig
		want bool
	}{
		{
			name: "openai with a default model can serve",
			svc:  openAIService("s1"),
			want: true,
		},
		{
			name: "load-test mock can serve",
			svc: llm.ServiceConfig{
				ID:           "mock",
				Type:         llm.ServiceTypeLoadTestMock,
				DefaultModel: "mock-model",
			},
			want: true,
		},
		{
			name: "anthropic with a default model can serve",
			svc: llm.ServiceConfig{
				ID:           "s2",
				Type:         llm.ServiceTypeAnthropic,
				APIKey:       "key",
				DefaultModel: "claude-sonnet-4-5",
			},
			want: true,
		},
		{
			name: "missing default model cannot serve",
			svc: llm.ServiceConfig{
				ID:     "s3",
				Type:   llm.ServiceTypeOpenAI,
				APIKey: "key",
			},
			want: false,
		},
		{
			name: "scale is valid but not constructible",
			svc: llm.ServiceConfig{
				ID:           "s4",
				Type:         llm.ServiceTypeScale,
				APIKey:       "key",
				APIURL:       "https://scale.example",
				DefaultModel: "scale-model",
			},
			want: false,
		},
		{
			name: "invalid service configuration cannot serve",
			svc: llm.ServiceConfig{
				ID:           "s5",
				Type:         llm.ServiceTypeOpenAI,
				DefaultModel: "gpt-4o",
			},
			want: false,
		},
		{
			name: "unknown structured output policy cannot serve",
			svc: llm.ServiceConfig{
				ID:                     "s6",
				Type:                   llm.ServiceTypeOpenAI,
				APIKey:                 "key",
				DefaultModel:           "gpt-4o",
				StructuredOutputPolicy: llm.StructuredOutputPolicy("sometimes"),
			},
			want: false,
		},
		{
			name: "known structured output policy can serve",
			svc: llm.ServiceConfig{
				ID:                     "s7",
				Type:                   llm.ServiceTypeOpenAI,
				APIKey:                 "key",
				DefaultModel:           "gpt-4o",
				StructuredOutputPolicy: llm.StructuredOutputPolicyNative,
			},
			want: true,
		},
		{
			name: "unknown service type cannot serve",
			svc: llm.ServiceConfig{
				ID:           "s8",
				Type:         "future-provider",
				DefaultModel: "model",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ServiceCanServeCompletions(tt.svc))
		})
	}
}

func TestResolveBridgeFallbacks(t *testing.T) {
	primaryWithFallback := func(id, fallbackID string) llm.ServiceConfig {
		svc := openAIService(id)
		svc.FallbackServiceID = fallbackID
		return svc
	}
	withModel := func(id, model string) llm.ServiceConfig {
		svc := openAIService(id)
		svc.DefaultModel = model
		return svc
	}

	tests := []struct {
		name       string
		services   []llm.ServiceConfig
		primary    llm.ServiceConfig
		wantIDs    []string
		wantErr    string
		wantModels []string
	}{
		{
			name:     "no fallback configured resolves to an empty chain",
			services: []llm.ServiceConfig{openAIService("a")},
			primary:  openAIService("a"),
			wantIDs:  nil,
		},
		{
			name: "resolves the full chain in order",
			services: []llm.ServiceConfig{
				primaryWithFallback("a", "b"),
				primaryWithFallback("b", "c"),
				openAIService("c"),
			},
			primary:    primaryWithFallback("a", "b"),
			wantIDs:    []string{"b", "c"},
			wantModels: []string{"gpt-4o", "gpt-4o"},
		},
		{
			name: "broken chain errors",
			services: []llm.ServiceConfig{
				primaryWithFallback("a", "missing"),
			},
			primary: primaryWithFallback("a", "missing"),
			wantErr: "does not exist",
		},
		{
			name: "cyclic chain errors",
			services: []llm.ServiceConfig{
				primaryWithFallback("a", "b"),
				primaryWithFallback("b", "a"),
			},
			primary: primaryWithFallback("a", "b"),
			wantErr: "cycle",
		},
		{
			name: "fallback with an unsupported type errors",
			services: []llm.ServiceConfig{
				primaryWithFallback("a", "scaled"),
				{
					ID:           "scaled",
					Type:         llm.ServiceTypeScale,
					APIKey:       "key",
					APIURL:       "https://scale.example",
					DefaultModel: "scale-model",
				},
			},
			primary: primaryWithFallback("a", "scaled"),
			wantErr: "unsupported type",
		},
		{
			name: "fallback without a default model errors",
			services: []llm.ServiceConfig{
				primaryWithFallback("a", "b"),
				{ID: "b", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
			},
			primary: primaryWithFallback("a", "b"),
			wantErr: "no default model",
		},
		{
			name: "duplicate service IDs resolve to the first entry",
			services: []llm.ServiceConfig{
				primaryWithFallback("a", "b"),
				withModel("b", "first-wins"),
				withModel("b", "second-loses"),
			},
			primary:    primaryWithFallback("a", "b"),
			wantIDs:    []string{"b"},
			wantModels: []string{"first-wins"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chain, err := ResolveBridgeFallbacks(tt.services, tt.primary)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)

			gotIDs := make([]string, len(chain))
			gotModels := make([]string, len(chain))
			for i, svc := range chain {
				gotIDs[i] = svc.ID
				gotModels[i] = svc.DefaultModel
			}
			if tt.wantIDs == nil {
				assert.Empty(t, gotIDs)
			} else {
				assert.Equal(t, tt.wantIDs, gotIDs)
			}
			if tt.wantModels != nil {
				assert.Equal(t, tt.wantModels, gotModels)
			}
		})
	}
}
