// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockPluginAPI exposes the plugintest API the test MMBots was built with.
func mockPluginAPI(b *MMBots) *plugintest.API {
	return b.ensureBotsClusterMutex.(*plugintest.API)
}

// jsonSchemaOption requests structured output for a small object schema.
func jsonSchemaOption() llm.LanguageModelOption {
	schema := llm.NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()
	return func(cfg *llm.LanguageModelConfig) {
		cfg.JSONOutputFormat = schema
	}
}

// promptFallbackApplied reports whether the built model rewrote the request
// into prompt instructions instead of sending the schema natively. The
// load-test mock counts tokens across the request's posts, so the injected
// system instruction is directly observable.
func promptFallbackApplied(t *testing.T, model llm.LanguageModel) bool {
	t.Helper()

	request := llm.CompletionRequest{Posts: []llm.Post{
		{Role: llm.PostRoleUser, Message: "Summarize the channel."},
	}}

	plain, err := model.CountTokens(context.Background(), request)
	require.NoError(t, err)
	withSchema, err := model.CountTokens(context.Background(), request, jsonSchemaOption())
	require.NoError(t, err)

	return withSchema > plain
}

func mockServiceWithPolicy(id string, policy llm.StructuredOutputPolicy) llm.ServiceConfig {
	return llm.ServiceConfig{
		ID:                     id,
		Name:                   id + " service",
		Type:                   llm.ServiceTypeLoadTestMock,
		DefaultModel:           "mock-model",
		StructuredOutputPolicy: policy,
	}
}

func geminiService(id, model string, policy llm.StructuredOutputPolicy) llm.ServiceConfig {
	return llm.ServiceConfig{
		ID:                     id,
		Name:                   id + " service",
		Type:                   llm.ServiceTypeGemini,
		APIKey:                 "key",
		DefaultModel:           model,
		StructuredOutputPolicy: policy,
	}
}

func TestBuildLLMStructuredOutputPolicy(t *testing.T) {
	tests := []struct {
		name               string
		service            llm.ServiceConfig
		botConfig          *llm.BotConfig
		fallbacks          []llm.ServiceConfig
		wantPromptFallback bool
	}{
		{
			name:               "native policy sends the schema to the provider",
			service:            mockServiceWithPolicy("mock", llm.StructuredOutputPolicyNative),
			wantPromptFallback: false,
		},
		{
			name:               "prompt fallback policy rewrites the request",
			service:            mockServiceWithPolicy("mock", llm.StructuredOutputPolicyPromptFallback),
			wantPromptFallback: true,
		},
		{
			name:               "auto policy on a provider without native support rewrites the request",
			service:            mockServiceWithPolicy("mock", llm.StructuredOutputPolicyAuto),
			wantPromptFallback: true,
		},
		{
			name:               "empty policy behaves like auto",
			service:            mockServiceWithPolicy("mock", ""),
			wantPromptFallback: true,
		},
		{
			name:               "invalid policy value falls back to prompt instructions",
			service:            mockServiceWithPolicy("mock", llm.StructuredOutputPolicy("sometimes")),
			wantPromptFallback: true,
		},
		{
			name:    "deprecated agent structured output flag is ignored",
			service: mockServiceWithPolicy("mock", llm.StructuredOutputPolicyAuto),
			botConfig: &llm.BotConfig{
				Name:                    "agent",
				StructuredOutputEnabled: true,
			},
			wantPromptFallback: true,
		},
		{
			name:    "deprecated agent flag cannot disable a native service policy",
			service: mockServiceWithPolicy("mock", llm.StructuredOutputPolicyNative),
			botConfig: &llm.BotConfig{
				Name:                    "agent",
				StructuredOutputEnabled: false,
			},
			wantPromptFallback: false,
		},
		{
			name:    "native primary with a capable auto fallback stays native",
			service: mockServiceWithPolicy("mock", llm.StructuredOutputPolicyNative),
			fallbacks: []llm.ServiceConfig{
				geminiService("gemini", "gemini-2.5-pro", llm.StructuredOutputPolicyAuto),
			},
			wantPromptFallback: false,
		},
		{
			name:    "native primary with an unknown auto fallback rewrites the request",
			service: mockServiceWithPolicy("mock", llm.StructuredOutputPolicyNative),
			fallbacks: []llm.ServiceConfig{
				geminiService("gemma", "gemma-3-27b-it", llm.StructuredOutputPolicyAuto),
			},
			wantPromptFallback: true,
		},
		{
			name:    "native primary with a prompt-fallback fallback rewrites the request",
			service: mockServiceWithPolicy("mock", llm.StructuredOutputPolicyNative),
			fallbacks: []llm.ServiceConfig{
				geminiService("gemini", "gemini-2.5-pro", llm.StructuredOutputPolicyPromptFallback),
			},
			wantPromptFallback: true,
		},
		{
			name:    "every attempt native keeps the schema",
			service: mockServiceWithPolicy("mock", llm.StructuredOutputPolicyNative),
			fallbacks: []llm.ServiceConfig{
				geminiService("gemini", "gemini-2.5-pro", llm.StructuredOutputPolicyNative),
				geminiService("gemini-2", "gemini-2.0-flash", llm.StructuredOutputPolicyNative),
			},
			wantPromptFallback: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mmBots := newTestMMBots(t, &mockConfig{})
			mockAPI := mockPluginAPI(mmBots)
			mockAPI.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			model, shutdown, err := mmBots.buildLLM(tt.service, tt.botConfig, tt.fallbacks, serviceTokenUsageIdentity(tt.service))
			require.NoError(t, err)
			require.NotNil(t, model)
			require.NotNil(t, shutdown)
			shutdown()

			assert.Equal(t, tt.wantPromptFallback, promptFallbackApplied(t, model))
		})
	}
}

func TestBuildLLMServiceCallKeepsServiceDefaults(t *testing.T) {
	mmBots := newTestMMBots(t, &mockConfig{})
	mockAPI := mockPluginAPI(mmBots)
	mockAPI.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	service := mockServiceWithPolicy("mock", llm.StructuredOutputPolicyNative)
	service.LoadTestMockConfig = buildTinyLoadTestProfile(t, nil)

	model, shutdown, err := mmBots.buildLLM(service, nil, nil, serviceTokenUsageIdentity(service))
	require.NoError(t, err)
	defer shutdown()

	// The chain is usable end to end without an agent behind it.
	assert.Equal(t, 100000, model.InputTokenLimit())
	response, err := model.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, response)

	primary, fallbacks := structuredOutputTargets(service, nil, nil)
	assert.Equal(t, "mock-model", primary.Model, "a service call runs the service default model")
	assert.Empty(t, fallbacks)
}

func TestStructuredOutputTargets(t *testing.T) {
	primaryService := geminiService("primary", "gemini-2.5-pro", llm.StructuredOutputPolicyAuto)
	fallbackService := geminiService("backup", "gemini-2.0-flash", llm.StructuredOutputPolicyAuto)

	tests := []struct {
		name              string
		service           llm.ServiceConfig
		botConfig         *llm.BotConfig
		fallbacks         []llm.ServiceConfig
		wantPrimaryModel  string
		wantFallbackIDs   []string
		wantFallbackModel []string
	}{
		{
			name:             "service call uses the service default model",
			service:          primaryService,
			wantPrimaryModel: "gemini-2.5-pro",
		},
		{
			name:             "agent without an override uses the service default model",
			service:          primaryService,
			botConfig:        &llm.BotConfig{Name: "agent"},
			wantPrimaryModel: "gemini-2.5-pro",
		},
		{
			name:             "agent model override wins for the primary",
			service:          primaryService,
			botConfig:        &llm.BotConfig{Name: "agent", Model: "gemma-3-27b-it"},
			wantPrimaryModel: "gemma-3-27b-it",
		},
		{
			name:              "fallbacks keep their own default models",
			service:           primaryService,
			botConfig:         &llm.BotConfig{Name: "agent", Model: "gemini-3-pro"},
			fallbacks:         []llm.ServiceConfig{fallbackService},
			wantPrimaryModel:  "gemini-3-pro",
			wantFallbackIDs:   []string{"backup"},
			wantFallbackModel: []string{"gemini-2.0-flash"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary, fallbacks := structuredOutputTargets(tt.service, tt.botConfig, tt.fallbacks)

			assert.Equal(t, tt.service.ID, primary.Service.ID)
			assert.Equal(t, tt.wantPrimaryModel, primary.Model)

			require.Len(t, fallbacks, len(tt.wantFallbackIDs))
			for i, wantID := range tt.wantFallbackIDs {
				assert.Equal(t, wantID, fallbacks[i].Service.ID)
				assert.Equal(t, tt.wantFallbackModel[i], fallbacks[i].Model)
			}
		})
	}
}

// TestAgentModelOverrideParticipatesInCapabilityResolution pins that the model
// an agent actually runs — not the service default — decides whether a native
// schema is sent, using the real resolver.
func TestAgentModelOverrideParticipatesInCapabilityResolution(t *testing.T) {
	service := geminiService("primary", "gemini-2.5-pro", llm.StructuredOutputPolicyAuto)

	tests := []struct {
		name               string
		botConfig          *llm.BotConfig
		wantPromptFallback bool
	}{
		{
			name:               "capable service default model sends the schema",
			botConfig:          &llm.BotConfig{Name: "agent"},
			wantPromptFallback: false,
		},
		{
			name:               "override to a model without native support rewrites the request",
			botConfig:          &llm.BotConfig{Name: "agent", Model: "gemma-3-27b-it"},
			wantPromptFallback: true,
		},
		{
			name:               "override to another capable model still sends the schema",
			botConfig:          &llm.BotConfig{Name: "agent", Model: "gemini-2.0-flash"},
			wantPromptFallback: false,
		},
		{
			name:               "deprecated structured output flag does not change the decision",
			botConfig:          &llm.BotConfig{Name: "agent", Model: "gemma-3-27b-it", StructuredOutputEnabled: true},
			wantPromptFallback: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary, fallbacks := structuredOutputTargets(service, tt.botConfig, nil)
			recorder := &recordingLanguageModel{}
			wrapper := llm.NewStructuredOutputFallbackWrapper(recorder, primary, fallbacks, bifrost.ResolveStructuredOutputCapability)

			_, err := wrapper.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
				Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "Summarize the channel."}},
			}, jsonSchemaOption())
			require.NoError(t, err)

			if tt.wantPromptFallback {
				assert.Nil(t, recorder.config.JSONOutputFormat, "schema must not reach the provider")
				assert.Len(t, recorder.posts, 2, "a system instruction must be injected")
			} else {
				assert.NotNil(t, recorder.config.JSONOutputFormat, "schema must reach the provider")
				assert.Len(t, recorder.posts, 1)
			}
		})
	}
}

func TestTokenUsageIdentities(t *testing.T) {
	service := llm.ServiceConfig{
		ID:           "svc-1",
		Name:         "Primary OpenAI",
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "key",
		DefaultModel: "gpt-4o",
	}

	tests := []struct {
		name      string
		identity  llm.TokenUsageIdentity
		wantModel string
		wantBot   string
	}{
		{
			name:      "agent without a model override reports the service default model",
			identity:  agentTokenUsageIdentity(service, llm.BotConfig{Name: "agent"}),
			wantModel: "gpt-4o",
			wantBot:   "agent",
		},
		{
			name:      "agent with a model override reports the effective model",
			identity:  agentTokenUsageIdentity(service, llm.BotConfig{Name: "agent", Model: "gpt-4.1"}),
			wantModel: "gpt-4.1",
			wantBot:   "agent",
		},
		{
			name: "agent identity survives EnsureBots folding the override into the service",
			identity: func() llm.TokenUsageIdentity {
				folded := service
				folded.DefaultModel = "gpt-4.1"
				return agentTokenUsageIdentity(folded, llm.BotConfig{Name: "agent", Model: "gpt-4.1"})
			}(),
			wantModel: "gpt-4.1",
			wantBot:   "agent",
		},
		{
			name:      "service identity has no agent username",
			identity:  serviceTokenUsageIdentity(service),
			wantModel: "gpt-4o",
			wantBot:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantBot, tt.identity.BotUsername)
			assert.Equal(t, tt.wantModel, tt.identity.DefaultModel)
			assert.Equal(t, "svc-1", tt.identity.ServiceID)
			assert.Equal(t, "Primary OpenAI", tt.identity.ServiceName)
			assert.Equal(t, llm.ServiceTypeOpenAI, tt.identity.ServiceType)
		})
	}
}

// recordingLanguageModel captures the request and options it was called with.
type recordingLanguageModel struct {
	posts  []llm.Post
	config llm.LanguageModelConfig
}

func (m *recordingLanguageModel) capture(request llm.CompletionRequest, opts []llm.LanguageModelOption) {
	m.posts = request.Posts
	var cfg llm.LanguageModelConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	m.config = cfg
}

func (m *recordingLanguageModel) ChatCompletion(_ context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	m.capture(request, opts)
	return &llm.TextStreamResult{Stream: make(chan llm.TextStreamEvent)}, nil
}

func (m *recordingLanguageModel) ChatCompletionNoStream(_ context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	m.capture(request, opts)
	return "{}", nil
}

func (m *recordingLanguageModel) CountTokens(_ context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (int, error) {
	m.capture(request, opts)
	return 0, nil
}

func (m *recordingLanguageModel) InputTokenLimit() int  { return 4096 }
func (m *recordingLanguageModel) OutputTokenLimit() int { return 4096 }
