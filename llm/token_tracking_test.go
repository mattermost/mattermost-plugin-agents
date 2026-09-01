// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockLanguageModel is a mock implementation of the LanguageModel interface
type MockLanguageModel struct {
	mock.Mock
}

func (m *MockLanguageModel) ChatCompletion(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	args := m.Called(ctx, request, opts)
	return args.Get(0).(*TextStreamResult), args.Error(1)
}

func (m *MockLanguageModel) ChatCompletionNoStream(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	args := m.Called(ctx, request, opts)
	return args.String(0), args.Error(1)
}

func (m *MockLanguageModel) CountTokens(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (int, error) {
	args := m.Called(ctx, request, opts)
	return args.Int(0), args.Error(1)
}

func (m *MockLanguageModel) InputTokenLimit() int {
	args := m.Called()
	return args.Int(0)
}

func (m *MockLanguageModel) OutputTokenLimit() int {
	args := m.Called()
	return args.Int(0)
}

type observedTokenUsage struct {
	botName      string
	teamID       string
	userID       string
	inputTokens  int
	outputTokens int
}

type observedMetrics struct {
	mu    sync.Mutex
	calls []observedTokenUsage
}

func (m *observedMetrics) ObserveTokenUsage(botName, teamID, userID string, inputTokens, outputTokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, observedTokenUsage{
		botName:      botName,
		teamID:       teamID,
		userID:       userID,
		inputTokens:  inputTokens,
		outputTokens: outputTokens,
	})
}

func (m *observedMetrics) Calls() []observedTokenUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]observedTokenUsage, len(m.calls))
	copy(result, m.calls)
	return result
}

type pluginLogEntry struct {
	message string
	fields  map[string]any
}

type observedPluginLogger struct {
	mu      sync.Mutex
	entries []pluginLogEntry
}

func (l *observedPluginLogger) Info(message string, keyValuePairs ...any) {
	fields := map[string]any{}
	for i := 0; i+1 < len(keyValuePairs); i += 2 {
		key, ok := keyValuePairs[i].(string)
		if !ok {
			continue
		}
		fields[key] = keyValuePairs[i+1]
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, pluginLogEntry{message: message, fields: fields})
}

func (l *observedPluginLogger) Entries() []pluginLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]pluginLogEntry, len(l.entries))
	copy(result, l.entries)
	return result
}

func makeStream(events ...TextStreamEvent) *TextStreamResult {
	stream := make(chan TextStreamEvent, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return &TextStreamResult{Stream: stream}
}

func makeTestTokenUsageSinks(loggingEnabled bool, pluginLogger TokenUsagePluginLogger, tokenLogger *mlog.Logger) *TokenUsageSinks {
	sinks := NewTokenUsageSinks(pluginLogger)
	sinks.SetLoggingEnabled(loggingEnabled)
	sinks.SetPluginEnabled(pluginLogger != nil)
	sinks.SetFileEnabled(tokenLogger != nil)
	sinks.SetFileLogger(tokenLogger)
	return sinks
}

func TestTokenTrackingWrapper_ChatCompletion_TableDriven(t *testing.T) {
	agentIdentity := TokenUsageIdentity{
		BotUsername:  "fallback-bot",
		ServiceID:    "svc-1",
		ServiceName:  "Primary OpenAI",
		DefaultModel: "identity-model",
		ServiceType:  "openai",
	}

	tests := []struct {
		name               string
		identity           TokenUsageIdentity
		request            CompletionRequest
		opts               []LanguageModelOption
		stream             *TextStreamResult
		expectedEventTypes []EventType
		expectedMetrics    []observedTokenUsage
		expectedLogFields  map[string]any
	}{
		{
			name:     "aggregates usage and emits rich dimensions",
			identity: agentIdentity,
			request: CompletionRequest{
				Context: &Context{
					RequestingUser: &model.User{Id: "user-123"},
					Team:           &model.Team{Id: "team-456"},
					Channel:        &model.Channel{Id: "channel-789", Type: model.ChannelTypeOpen},
					BotName:        "Test Bot",
					BotUsername:    "testbot",
					BotUserID:      "bot-user-id",
					BotModel:       "context-model",
					BotServiceType: "openai",
				},
				Operation:        OperationConversation,
				OperationSubType: SubTypeStreaming,
			},
			opts: []LanguageModelOption{
				WithModel("override-model"),
			},
			stream: makeStream(
				TextStreamEvent{Type: EventTypeText, Value: "hello"},
				TextStreamEvent{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 10, OutputTokens: 5}},
				TextStreamEvent{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 2, OutputTokens: 3}},
				TextStreamEvent{Type: EventTypeEnd, Value: nil},
			),
			expectedEventTypes: []EventType{EventTypeText, EventTypeEnd},
			expectedMetrics: []observedTokenUsage{
				{
					botName:      "testbot",
					teamID:       "team-456",
					userID:       "user-123",
					inputTokens:  12,
					outputTokens: 8,
				},
			},
			expectedLogFields: map[string]any{
				"event":             TokenUsageLogEvent,
				"schema_version":    TokenUsageLogSchemaVersion,
				"user_id":           "user-123",
				"team_id":           "team-456",
				"channel_id":        "channel-789",
				"channel_type":      "open",
				"agent_name":        "Test Bot",
				"agent_username":    "testbot",
				"bot_username":      "testbot",
				"agent_user_id":     "bot-user-id",
				"model":             "override-model",
				"service_type":      "openai",
				"service_id":        "svc-1",
				"service_name":      "Primary OpenAI",
				"operation":         OperationConversation,
				"operation_subtype": SubTypeStreaming,
				"input_tokens":      int64(12),
				"output_tokens":     int64(8),
				"total_tokens":      int64(20),
			},
		},
		{
			name:     "uses unknown defaults for nil context with stream subtype default",
			identity: TokenUsageIdentity{BotUsername: "fallback-bot"},
			request: CompletionRequest{
				Operation: "",
			},
			stream: makeStream(
				TextStreamEvent{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 1, OutputTokens: 2}},
				TextStreamEvent{Type: EventTypeEnd, Value: nil},
			),
			expectedEventTypes: []EventType{EventTypeEnd},
			expectedMetrics: []observedTokenUsage{
				{
					botName:      "fallback-bot",
					teamID:       TokenUsageUnknown,
					userID:       TokenUsageUnknown,
					inputTokens:  1,
					outputTokens: 2,
				},
			},
			expectedLogFields: map[string]any{
				"event":             TokenUsageLogEvent,
				"schema_version":    TokenUsageLogSchemaVersion,
				"user_id":           TokenUsageUnknown,
				"team_id":           TokenUsageUnknown,
				"channel_id":        TokenUsageUnknown,
				"channel_type":      TokenUsageUnknown,
				"agent_name":        "fallback-bot",
				"agent_username":    "fallback-bot",
				"bot_username":      "fallback-bot",
				"agent_user_id":     TokenUsageUnknown,
				"model":             TokenUsageUnknown,
				"service_type":      TokenUsageUnknown,
				"service_id":        TokenUsageUnknown,
				"service_name":      TokenUsageUnknown,
				"operation":         TokenUsageUnknown,
				"operation_subtype": SubTypeStreaming,
				"input_tokens":      int64(1),
				"output_tokens":     int64(2),
				"total_tokens":      int64(3),
			},
		},
		{
			name:     "identity supplies model and service type when the context has none",
			identity: agentIdentity,
			request: CompletionRequest{
				Context:   &Context{BotUsername: "agentbot"},
				Operation: OperationConversation,
			},
			stream: makeStream(
				TextStreamEvent{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 1, OutputTokens: 1}},
				TextStreamEvent{Type: EventTypeEnd, Value: nil},
			),
			expectedEventTypes: []EventType{EventTypeEnd},
			expectedMetrics: []observedTokenUsage{
				{
					botName:      "agentbot",
					teamID:       TokenUsageUnknown,
					userID:       TokenUsageUnknown,
					inputTokens:  1,
					outputTokens: 1,
				},
			},
			expectedLogFields: map[string]any{
				"agent_name":     "agentbot",
				"agent_username": "agentbot",
				"model":          "identity-model",
				"service_type":   "openai",
				"service_id":     "svc-1",
				"service_name":   "Primary OpenAI",
			},
		},
		{
			name: "service-only identity blanks every agent dimension",
			identity: TokenUsageIdentity{
				ServiceID:    "svc-bridge",
				ServiceName:  "Bridge Service",
				DefaultModel: "claude-sonnet-4-5",
				ServiceType:  "anthropic",
			},
			request: CompletionRequest{
				Context: &Context{
					RequestingUser: &model.User{Id: "user-9"},
				},
				Operation: OperationBridgeService,
			},
			stream: makeStream(
				TextStreamEvent{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 7, OutputTokens: 8}},
				TextStreamEvent{Type: EventTypeEnd, Value: nil},
			),
			expectedEventTypes: []EventType{EventTypeEnd},
			expectedMetrics: []observedTokenUsage{
				{
					botName:      "",
					teamID:       TokenUsageUnknown,
					userID:       "user-9",
					inputTokens:  7,
					outputTokens: 8,
				},
			},
			expectedLogFields: map[string]any{
				"agent_name":     "",
				"agent_username": "",
				"bot_username":   "",
				"agent_user_id":  "",
				"user_id":        "user-9",
				"model":          "claude-sonnet-4-5",
				"service_type":   "anthropic",
				"service_id":     "svc-bridge",
				"service_name":   "Bridge Service",
				"operation":      OperationBridgeService,
			},
		},
		{
			name: "known service with a blank name logs a blank service name",
			identity: TokenUsageIdentity{
				ServiceID:    "svc-unnamed",
				DefaultModel: "gpt-4o",
				ServiceType:  "openai",
			},
			request: CompletionRequest{Operation: OperationBridgeService},
			stream: makeStream(
				TextStreamEvent{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 1, OutputTokens: 1}},
				TextStreamEvent{Type: EventTypeEnd, Value: nil},
			),
			expectedEventTypes: []EventType{EventTypeEnd},
			expectedMetrics: []observedTokenUsage{
				{
					botName:      "",
					teamID:       TokenUsageUnknown,
					userID:       TokenUsageUnknown,
					inputTokens:  1,
					outputTokens: 1,
				},
			},
			expectedLogFields: map[string]any{
				"service_id":   "svc-unnamed",
				"service_name": "",
			},
		},
		{
			name:     "maps DM channels to dm team dimension",
			identity: TokenUsageIdentity{BotUsername: "fallback-bot"},
			request: CompletionRequest{
				Context: &Context{
					RequestingUser: &model.User{Id: "dm-user"},
					Channel:        &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect},
					BotUsername:    "dm-bot",
					BotModel:       "claude",
				},
				Operation:        OperationThreadAnalysis,
				OperationSubType: "action_items",
			},
			stream: makeStream(
				TextStreamEvent{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 3, OutputTokens: 4}},
				TextStreamEvent{Type: EventTypeEnd, Value: nil},
			),
			expectedEventTypes: []EventType{EventTypeEnd},
			expectedMetrics: []observedTokenUsage{
				{
					botName:      "dm-bot",
					teamID:       "dm",
					userID:       "dm-user",
					inputTokens:  3,
					outputTokens: 4,
				},
			},
			expectedLogFields: map[string]any{
				"team_id":      "dm",
				"channel_type": "direct",
				"operation":    OperationThreadAnalysis,
			},
		},
		{
			name:     "ignores invalid usage payloads",
			identity: TokenUsageIdentity{BotUsername: "fallback-bot"},
			request:  CompletionRequest{},
			stream: makeStream(
				TextStreamEvent{Type: EventTypeUsage, Value: "invalid-usage"},
				TextStreamEvent{Type: EventTypeEnd, Value: nil},
			),
			expectedEventTypes: []EventType{EventTypeEnd},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockLLM := &MockLanguageModel{}
			metrics := &observedMetrics{}
			pluginLogger := &observedPluginLogger{}
			sinks := makeTestTokenUsageSinks(true, pluginLogger, nil)
			wrapper := NewTokenUsageLoggingWrapper(mockLLM, tc.identity, sinks, metrics)

			mockLLM.On("ChatCompletion", mock.Anything, mock.Anything, mock.Anything).Return(tc.stream, nil).Once()

			result, err := wrapper.ChatCompletion(context.Background(), tc.request, tc.opts...)
			require.NoError(t, err)
			require.NotNil(t, result)

			eventTypes := []EventType{}
			for event := range result.Stream {
				eventTypes = append(eventTypes, event.Type)
			}
			assert.Equal(t, tc.expectedEventTypes, eventTypes)

			observedCalls := metrics.Calls()
			if tc.expectedMetrics == nil {
				assert.Empty(t, observedCalls)
			} else {
				assert.Equal(t, tc.expectedMetrics, observedCalls)
			}

			if tc.expectedLogFields == nil {
				assert.Empty(t, pluginLogger.Entries())
			} else {
				entries := pluginLogger.Entries()
				require.Len(t, entries, 1)
				for key, expectedValue := range tc.expectedLogFields {
					assert.Equalf(t, expectedValue, entries[0].fields[key], "field %s", key)
				}
			}

			mockLLM.AssertExpectations(t)
		})
	}
}

func TestBuildTokenUsageLogKeyValuePairs(t *testing.T) {
	dimensions := tokenUsageDimensions{
		userID:           "user-1",
		teamID:           "team-1",
		channelID:        "channel-1",
		channelType:      "open",
		botName:          "Agent Bot",
		botUsername:      "agent",
		botUserID:        "bot-user-1",
		model:            "claude-sonnet-4-5",
		serviceType:      "anthropic",
		serviceID:        "svc-1",
		serviceName:      "Primary Anthropic",
		operation:        OperationConversation,
		operationSubType: SubTypeStreaming,
	}

	tests := []struct {
		name  string
		usage TokenUsage
		want  map[string]any
	}{
		{
			name:  "rich usage fields populated",
			usage: TokenUsage{InputTokens: 1000, OutputTokens: 300, CachedReadTokens: 800, CachedWriteTokens: 100, ReasoningTokens: 64, Cost: 0.0123},
			want: map[string]any{
				"input_tokens":        int64(1000),
				"output_tokens":       int64(300),
				"total_tokens":        int64(1300),
				"cached_read_tokens":  int64(800),
				"cached_write_tokens": int64(100),
				"reasoning_tokens":    int64(64),
				"cost":                0.0123,
			},
		},
		{
			name:  "zero usage emits zeros for every numeric field",
			usage: TokenUsage{},
			want: map[string]any{
				"input_tokens":        int64(0),
				"output_tokens":       int64(0),
				"total_tokens":        int64(0),
				"cached_read_tokens":  int64(0),
				"cached_write_tokens": int64(0),
				"reasoning_tokens":    int64(0),
				"cost":                float64(0),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := buildTokenUsageLogKeyValuePairs(dimensions, tt.usage)
			keyed := map[string]any{}
			for i := 0; i+1 < len(fields); i += 2 {
				keyed[fields[i].(string)] = fields[i+1]
			}
			for key, want := range tt.want {
				assert.Equal(t, want, keyed[key], "field %s", key)
			}
			// Dimensions and meta keys must always be present.
			assert.Equal(t, TokenUsageLogEvent, keyed["event"])
			assert.Equal(t, TokenUsageLogSchemaVersion, keyed["schema_version"])
			assert.Equal(t, "user-1", keyed["user_id"])
			assert.Equal(t, "claude-sonnet-4-5", keyed["model"])
			assert.Equal(t, "svc-1", keyed["service_id"])
			assert.Equal(t, "Primary Anthropic", keyed["service_name"])
		})
	}
}

func TestExtractTokenUsageDimensions(t *testing.T) {
	agentContext := &Context{
		BotName:        "Context Agent",
		BotUsername:    "contextbot",
		BotUserID:      "context-bot-id",
		BotModel:       "context-model",
		BotServiceType: "anthropic",
	}

	tests := []struct {
		name        string
		identity    TokenUsageIdentity
		request     CompletionRequest
		optionModel string
		want        tokenUsageDimensions
	}{
		{
			name: "context wins over identity for model and service type",
			identity: TokenUsageIdentity{
				BotUsername:  "identitybot",
				ServiceID:    "svc-1",
				ServiceName:  "Primary",
				DefaultModel: "identity-model",
				ServiceType:  "openai",
			},
			request: CompletionRequest{Context: agentContext},
			want: tokenUsageDimensions{
				botName:     "Context Agent",
				botUsername: "contextbot",
				botUserID:   "context-bot-id",
				model:       "context-model",
				serviceType: "anthropic",
				serviceID:   "svc-1",
				serviceName: "Primary",
			},
		},
		{
			name: "per-call model overrides both context and identity",
			identity: TokenUsageIdentity{
				BotUsername:  "identitybot",
				ServiceID:    "svc-1",
				DefaultModel: "identity-model",
				ServiceType:  "openai",
			},
			request:     CompletionRequest{Context: agentContext},
			optionModel: "option-model",
			want: tokenUsageDimensions{
				botName:     "Context Agent",
				botUsername: "contextbot",
				botUserID:   "context-bot-id",
				model:       "option-model",
				serviceType: "anthropic",
				serviceID:   "svc-1",
				serviceName: "",
			},
		},
		{
			name: "identity fills model and service type when the context is empty",
			identity: TokenUsageIdentity{
				BotUsername:  "identitybot",
				ServiceID:    "svc-2",
				ServiceName:  "Secondary",
				DefaultModel: "identity-model",
				ServiceType:  "gemini",
			},
			request: CompletionRequest{},
			want: tokenUsageDimensions{
				botName:     "identitybot",
				botUsername: "identitybot",
				botUserID:   TokenUsageUnknown,
				model:       "identity-model",
				serviceType: "gemini",
				serviceID:   "svc-2",
				serviceName: "Secondary",
			},
		},
		{
			name:     "missing service identity normalizes both service fields to unknown",
			identity: TokenUsageIdentity{BotUsername: "identitybot"},
			request:  CompletionRequest{},
			want: tokenUsageDimensions{
				botName:     "identitybot",
				botUsername: "identitybot",
				botUserID:   TokenUsageUnknown,
				model:       TokenUsageUnknown,
				serviceType: TokenUsageUnknown,
				serviceID:   TokenUsageUnknown,
				serviceName: TokenUsageUnknown,
			},
		},
		{
			name: "service-only identity blanks the agent dimensions",
			identity: TokenUsageIdentity{
				ServiceID:    "svc-3",
				ServiceName:  "Bridge",
				DefaultModel: "gpt-4o",
				ServiceType:  "openai",
			},
			request: CompletionRequest{},
			want: tokenUsageDimensions{
				botName:     "",
				botUsername: "",
				botUserID:   "",
				model:       "gpt-4o",
				serviceType: "openai",
				serviceID:   "svc-3",
				serviceName: "Bridge",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTokenUsageDimensions(tt.request, tt.identity, tt.optionModel)

			assert.Equal(t, tt.want.botName, got.botName, "agent_name")
			assert.Equal(t, tt.want.botUsername, got.botUsername, "agent_username")
			assert.Equal(t, tt.want.botUserID, got.botUserID, "agent_user_id")
			assert.Equal(t, tt.want.model, got.model, "model")
			assert.Equal(t, tt.want.serviceType, got.serviceType, "service_type")
			assert.Equal(t, tt.want.serviceID, got.serviceID, "service_id")
			assert.Equal(t, tt.want.serviceName, got.serviceName, "service_name")
		})
	}
}

func TestTokenUsageKeyValuePairsToMlogFields(t *testing.T) {
	out := tokenUsageKeyValuePairsToMlogFields([]any{"foo", "bar", "n", int64(42)})
	assert.Equal(t, []mlog.Field{mlog.Any("foo", "bar"), mlog.Any("n", int64(42))}, out)
}

func TestTokenTrackingWrapper_DefaultOperationSubType(t *testing.T) {
	tests := []struct {
		name               string
		expectedSubType    string
		invokeAndDrainFunc func(wrapper *TokenUsageLoggingWrapper, request CompletionRequest) error
	}{
		{
			name:            "ChatCompletion defaults to streaming subtype",
			expectedSubType: SubTypeStreaming,
			invokeAndDrainFunc: func(wrapper *TokenUsageLoggingWrapper, request CompletionRequest) error {
				result, err := wrapper.ChatCompletion(context.Background(), request)
				if err != nil {
					return err
				}
				_, err = result.ReadAll()
				return err
			},
		},
		{
			name:            "ChatCompletionNoStream defaults to nostream subtype",
			expectedSubType: SubTypeNoStream,
			invokeAndDrainFunc: func(wrapper *TokenUsageLoggingWrapper, request CompletionRequest) error {
				_, err := wrapper.ChatCompletionNoStream(context.Background(), request)
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockLLM := &MockLanguageModel{}
			pluginLogger := &observedPluginLogger{}
			sinks := makeTestTokenUsageSinks(true, pluginLogger, nil)
			wrapper := NewTokenUsageLoggingWrapper(mockLLM, TokenUsageIdentity{BotUsername: "fallback-bot"}, sinks, nil)

			mockLLM.On("ChatCompletion", mock.Anything, mock.Anything, mock.Anything).Return(
				makeStream(
					TextStreamEvent{Type: EventTypeText, Value: "hello"},
					TextStreamEvent{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 2, OutputTokens: 3}},
					TextStreamEvent{Type: EventTypeEnd, Value: nil},
				),
				nil,
			).Once()

			err := tc.invokeAndDrainFunc(wrapper, CompletionRequest{
				Operation: OperationConversation,
				Context:   &Context{},
			})
			require.NoError(t, err)

			require.Eventually(t, func() bool {
				return len(pluginLogger.Entries()) == 1
			}, time.Second, 10*time.Millisecond)
			entries := pluginLogger.Entries()
			require.Len(t, entries, 1)
			assert.Equal(t, tc.expectedSubType, entries[0].fields["operation_subtype"])
		})
	}
}

func TestTokenTrackingWrapper_ChatCompletionNoStream(t *testing.T) {
	t.Run("delegates to streaming method", func(t *testing.T) {
		mockLLM := &MockLanguageModel{}
		sinks := makeTestTokenUsageSinks(true, &observedPluginLogger{}, nil)
		wrapper := NewTokenUsageLoggingWrapper(mockLLM, TokenUsageIdentity{BotUsername: "test-bot"}, sinks, nil)

		mockStream := make(chan TextStreamEvent, 3)
		mockStream <- TextStreamEvent{Type: EventTypeText, Value: "Hello world"}
		mockStream <- TextStreamEvent{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 5, OutputTokens: 10}}
		mockStream <- TextStreamEvent{Type: EventTypeEnd, Value: nil}
		close(mockStream)

		mockResult := &TextStreamResult{Stream: mockStream}
		mockLLM.On("ChatCompletion", mock.Anything, mock.Anything, mock.Anything).Return(mockResult, nil)

		request := CompletionRequest{Context: &Context{}}
		result, err := wrapper.ChatCompletionNoStream(context.Background(), request)
		require.NoError(t, err)
		assert.Equal(t, "Hello world", result)

		mockLLM.AssertExpectations(t)
	})
}

func TestTokenTrackingWrapper_DelegatedMethods(t *testing.T) {
	mockLLM := &MockLanguageModel{}
	sinks := makeTestTokenUsageSinks(true, &observedPluginLogger{}, nil)
	wrapper := NewTokenUsageLoggingWrapper(mockLLM, TokenUsageIdentity{BotUsername: "test-llm"}, sinks, nil)

	t.Run("CountTokens delegates to wrapped model", func(t *testing.T) {
		req := CompletionRequest{Posts: []Post{{Role: PostRoleUser, Message: "test text"}}}
		mockLLM.On("CountTokens", mock.Anything, req, mock.Anything).Return(42, nil)

		result, err := wrapper.CountTokens(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, 42, result)

		mockLLM.AssertExpectations(t)
	})

	t.Run("InputTokenLimit delegates to wrapped model", func(t *testing.T) {
		mockLLM.On("InputTokenLimit").Return(4096)

		result := wrapper.InputTokenLimit()
		assert.Equal(t, 4096, result)

		mockLLM.AssertExpectations(t)
	})
}
