// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
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
		{llm.ServiceTypeNorth, false},
		{llm.ServiceTypeMistral, false},
		{llm.ServiceTypeScale, false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.serviceType, func(t *testing.T) {
			assert.Equal(t, tt.want, SupportsNativeTools(tt.serviceType))
		})
	}
}

// OpenAI runs a sandbox but its container files are not retrievable.
func TestSupportsProviderFileDownload(t *testing.T) {
	tests := []struct {
		serviceType string
		want        bool
	}{
		{llm.ServiceTypeAnthropic, true},
		{llm.ServiceTypeOpenAI, false},
		{llm.ServiceTypeOpenAICompatible, false},
		{llm.ServiceTypeAzure, false},
		{llm.ServiceTypeGemini, false},
		{llm.ServiceTypeVertex, false},
		{llm.ServiceTypeBedrock, false},
		{llm.ServiceTypeCohere, false},
		{llm.ServiceTypeNorth, false},
		{llm.ServiceTypeMistral, false},
		{llm.ServiceTypeLoadTestMock, false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.serviceType, func(t *testing.T) {
			assert.Equal(t, tt.want, SupportsProviderFileDownload(tt.serviceType))
		})
	}
}

func TestFilterNativeToolsForServiceType(t *testing.T) {
	allTools := []string{
		llm.NativeToolWebSearch,
		llm.NativeToolWebFetch,
		llm.NativeToolFileSearch,
		llm.NativeToolCodeInterpreter,
	}

	tests := []struct {
		name        string
		serviceType string
		tools       []string
		want        []string
	}{
		{
			// file_search is dropped even for OpenAI: the plugin cannot
			// configure the vector_store_ids the tool requires, and sending
			// the bare tool would 400 every completion.
			name:        "OpenAI keeps its Responses API tools, drops web_fetch and file_search",
			serviceType: llm.ServiceTypeOpenAI,
			tools:       allTools,
			want:        []string{llm.NativeToolWebSearch, llm.NativeToolCodeInterpreter},
		},
		{
			name:        "Azure keeps its Responses API tools, drops web_fetch and file_search",
			serviceType: llm.ServiceTypeAzure,
			tools:       allTools,
			want:        []string{llm.NativeToolWebSearch, llm.NativeToolCodeInterpreter},
		},
		{
			name:        "OpenAI-compatible keeps its Responses API tools, drops web_fetch and file_search",
			serviceType: llm.ServiceTypeOpenAICompatible,
			tools:       allTools,
			want:        []string{llm.NativeToolWebSearch, llm.NativeToolCodeInterpreter},
		},
		{
			name:        "Anthropic keeps its server tools, drops file_search",
			serviceType: llm.ServiceTypeAnthropic,
			tools:       allTools,
			want:        []string{llm.NativeToolWebSearch, llm.NativeToolWebFetch, llm.NativeToolCodeInterpreter},
		},
		{
			name:        "Gemini keeps only web_search",
			serviceType: llm.ServiceTypeGemini,
			tools:       allTools,
			want:        []string{llm.NativeToolWebSearch},
		},
		{
			name:        "Vertex keeps only web_search",
			serviceType: llm.ServiceTypeVertex,
			tools:       allTools,
			want:        []string{llm.NativeToolWebSearch},
		},
		{
			name:        "North drops all native tools",
			serviceType: llm.ServiceTypeNorth,
			tools:       allTools,
			want:        []string{},
		},
		{
			name:        "unknown tool ids are dropped",
			serviceType: llm.ServiceTypeAnthropic,
			tools:       []string{"totally_made_up", llm.NativeToolWebSearch},
			want:        []string{llm.NativeToolWebSearch},
		},
		{"Bedrock drops tools", llm.ServiceTypeBedrock, []string{llm.NativeToolWebSearch}, []string{}},
		{"Cohere drops tools", llm.ServiceTypeCohere, []string{llm.NativeToolWebSearch}, []string{}},
		{"North drops tools", llm.ServiceTypeNorth, []string{llm.NativeToolWebSearch}, []string{}},
		{"Mistral drops tools", llm.ServiceTypeMistral, []string{llm.NativeToolWebSearch}, []string{}},
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
		{"North always uses Responses API", llm.ServiceTypeNorth, false, true},
		{"North with flag true", llm.ServiceTypeNorth, true, true},
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
		{"North drops native tools", llm.ServiceTypeNorth, false},
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

func TestMapServiceTypeToProviderAndIsSupported(t *testing.T) {
	tests := []struct {
		serviceType   string
		wantProvider  schemas.ModelProvider
		wantSupported bool
		wantMapError  bool
	}{
		{llm.ServiceTypeOpenAI, schemas.OpenAI, true, false},
		{llm.ServiceTypeOpenAICompatible, schemas.OpenAI, true, false},
		{llm.ServiceTypeNorth, schemas.OpenAI, true, false},
		{llm.ServiceTypeAnthropic, schemas.Anthropic, true, false},
		{llm.ServiceTypeScale, "", false, true},
		{"unknown", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.serviceType, func(t *testing.T) {
			assert.Equal(t, tt.wantSupported, IsSupported(tt.serviceType))
			got, err := MapServiceTypeToProvider(tt.serviceType)
			if tt.wantMapError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantProvider, got)
		})
	}
}

func TestNormalizeNorthBaseURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty stays empty", input: "", want: ""},
		{name: "instance root", input: "http://host", want: "http://host/api"},
		{name: "instance root trailing slash", input: "http://host/", want: "http://host/api"},
		{name: "already /api", input: "http://host/api", want: "http://host/api"},
		{name: "/api trailing slash", input: "http://host/api/", want: "http://host/api"},
		{name: "/api/v1", input: "http://host/api/v1", want: "http://host/api"},
		{name: "/api/v1 trailing slash", input: "http://host/api/v1/", want: "http://host/api"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeNorthBaseURL(tt.input))
		})
	}
}

func TestProviderSettingsDisableStore(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		want        bool
	}{
		{"north disables store", llm.ServiceTypeNorth, true},
		{"openai leaves store enabled", llm.ServiceTypeOpenAI, false},
		{"openai compatible leaves store enabled", llm.ServiceTypeOpenAICompatible, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := llm.ServiceConfig{
				ID:     "test",
				Type:   tt.serviceType,
				APIKey: "key",
				APIURL: "http://localhost",
			}
			provider, err := MapServiceTypeToProvider(svc.Type)
			require.NoError(t, err)
			settings := providerSettingsFromService(provider, svc)
			assert.Equal(t, tt.want, settings.DisableStore)

			acc := &providerAccount{ProviderSettings: settings}
			cfg, err := acc.GetConfigForProvider(acc.registeredName())
			require.NoError(t, err)
			if tt.want {
				require.NotNil(t, cfg.OpenAIConfig)
				assert.True(t, cfg.OpenAIConfig.DisableStore)
			} else {
				assert.Nil(t, cfg.OpenAIConfig)
			}
		})
	}
}

func TestNewFromServiceConfigNorthFallbackCompatibility(t *testing.T) {
	northFallback := llm.ServiceConfig{
		ID:           "svc-north",
		Type:         llm.ServiceTypeNorth,
		APIKey:       "north-key",
		APIURL:       "http://localhost",
		DefaultModel: "command-a",
	}
	openaiFallback := llm.ServiceConfig{
		ID:           "svc-openai",
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "openai-key",
		DefaultModel: "gpt-4o",
	}

	tests := []struct {
		name          string
		primary       llm.ServiceConfig
		fallback      llm.ServiceConfig
		bot           llm.BotConfig
		wantErrSubstr string
	}{
		{
			name: "chat-path primary rejects north fallback",
			primary: llm.ServiceConfig{
				ID:           "svc-anthropic",
				Type:         llm.ServiceTypeAnthropic,
				APIKey:       "anthropic-key",
				DefaultModel: "claude-sonnet-4-20250514",
			},
			fallback:      northFallback,
			bot:           llm.BotConfig{ID: "bot-1", ServiceID: "svc-anthropic"},
			wantErrSubstr: "requires a primary service that uses the Responses API",
		},
		{
			name: "openaicompatible chat path rejects north fallback",
			primary: llm.ServiceConfig{
				ID:           "svc-compat",
				Type:         llm.ServiceTypeOpenAICompatible,
				APIKey:       "compat-key",
				APIURL:       "http://localhost",
				DefaultModel: "llama3",
			},
			fallback:      northFallback,
			bot:           llm.BotConfig{ID: "bot-1", ServiceID: "svc-compat"},
			wantErrSubstr: "requires a primary service that uses the Responses API",
		},
		{
			name: "openai responses primary with north fallback is ok",
			primary: llm.ServiceConfig{
				ID:           "svc-openai",
				Type:         llm.ServiceTypeOpenAI,
				APIKey:       "openai-key",
				DefaultModel: "gpt-4o",
			},
			fallback: northFallback,
			bot:      llm.BotConfig{ID: "bot-1", ServiceID: "svc-openai"},
		},
		{
			name: "openai primary with native tools rejects north fallback",
			primary: llm.ServiceConfig{
				ID:           "svc-openai",
				Type:         llm.ServiceTypeOpenAI,
				APIKey:       "openai-key",
				DefaultModel: "gpt-4o",
			},
			fallback: northFallback,
			bot: llm.BotConfig{
				ID:                 "bot-1",
				ServiceID:          "svc-openai",
				EnabledNativeTools: []string{llm.NativeToolWebSearch},
			},
			wantErrSubstr: "does not support provider-native tools",
		},
		{
			name: "north primary with openai fallback is ok",
			primary: llm.ServiceConfig{
				ID:           "svc-north",
				Type:         llm.ServiceTypeNorth,
				APIKey:       "north-key",
				APIURL:       "http://localhost",
				DefaultModel: "command-a",
			},
			fallback: openaiFallback,
			bot:      llm.BotConfig{ID: "bot-1", ServiceID: "svc-north"},
		},
		{
			name: "openaicompatible with responses api accepts north fallback",
			primary: llm.ServiceConfig{
				ID:              "svc-compat",
				Type:            llm.ServiceTypeOpenAICompatible,
				APIKey:          "compat-key",
				APIURL:          "http://localhost",
				DefaultModel:    "gpt-oss",
				UseResponsesAPI: true,
			},
			fallback: northFallback,
			bot:      llm.BotConfig{ID: "bot-1", ServiceID: "svc-compat"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llmInstance, err := NewFromServiceConfig(tt.primary, tt.bot, []llm.ServiceConfig{tt.fallback})
			if tt.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSubstr)
				assert.Contains(t, err.Error(), tt.fallback.ID)
				return
			}
			require.NoError(t, err)
			defer llmInstance.Shutdown()
			require.Len(t, llmInstance.fallbacks, 1)
			assert.Equal(t, tt.fallback.ID, llmInstance.fallbacks[0].serviceID)
		})
	}
}
