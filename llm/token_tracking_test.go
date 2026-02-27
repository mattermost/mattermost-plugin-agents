// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"sync"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockLanguageModel is a mock implementation of the LanguageModel interface
type MockLanguageModel struct {
	mock.Mock
}

func (m *MockLanguageModel) ChatCompletion(request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	args := m.Called(request, opts)
	return args.Get(0).(*TextStreamResult), args.Error(1)
}

func (m *MockLanguageModel) ChatCompletionNoStream(request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	args := m.Called(request, opts)
	return args.String(0), args.Error(1)
}

func (m *MockLanguageModel) CountTokens(text string) int {
	args := m.Called(text)
	return args.Int(0)
}

func (m *MockLanguageModel) InputTokenLimit() int {
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

func TestTokenTrackingWrapper_ChatCompletion_TableDriven(t *testing.T) {
	tests := []struct {
		name               string
		request            CompletionRequest
		opts               []LanguageModelOption
		stream             *TextStreamResult
		expectedEventTypes []EventType
		expectedMetrics    []observedTokenUsage
		expectedLogFields  map[string]any
	}{
		{
			name: "aggregates usage and emits rich dimensions",
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
				OperationSubType: "streaming",
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
				"agent_user_id":     "bot-user-id",
				"model":             "override-model",
				"service_type":      "openai",
				"operation":         OperationConversation,
				"operation_subtype": "streaming",
				"input_tokens":      int64(12),
				"output_tokens":     int64(8),
				"total_tokens":      int64(20),
			},
		},
		{
			name: "uses unknown defaults for nil context",
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
				"agent_user_id":     TokenUsageUnknown,
				"model":             TokenUsageUnknown,
				"service_type":      TokenUsageUnknown,
				"operation":         TokenUsageUnknown,
				"operation_subtype": TokenUsageUnknown,
				"input_tokens":      int64(1),
				"output_tokens":     int64(2),
				"total_tokens":      int64(3),
			},
		},
		{
			name: "maps DM channels to dm team dimension",
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
			name:    "ignores invalid usage payloads",
			request: CompletionRequest{},
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
			wrapper := NewTokenUsageLoggingWrapper(mockLLM, "fallback-bot", pluginLogger, nil, metrics)

			mockLLM.On("ChatCompletion", mock.Anything, mock.Anything).Return(tc.stream, nil).Once()

			result, err := wrapper.ChatCompletion(tc.request, tc.opts...)
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

func TestTokenTrackingWrapper_ChatCompletionNoStream(t *testing.T) {
	t.Run("delegates to streaming method", func(t *testing.T) {
		mockLLM := &MockLanguageModel{}
		wrapper := NewTokenUsageLoggingWrapper(mockLLM, "test-bot", &observedPluginLogger{}, nil, nil)

		mockStream := make(chan TextStreamEvent, 3)
		mockStream <- TextStreamEvent{Type: EventTypeText, Value: "Hello world"}
		mockStream <- TextStreamEvent{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 5, OutputTokens: 10}}
		mockStream <- TextStreamEvent{Type: EventTypeEnd, Value: nil}
		close(mockStream)

		mockResult := &TextStreamResult{Stream: mockStream}
		mockLLM.On("ChatCompletion", mock.Anything, mock.Anything).Return(mockResult, nil)

		request := CompletionRequest{Context: &Context{}}
		result, err := wrapper.ChatCompletionNoStream(request)
		require.NoError(t, err)
		assert.Equal(t, "Hello world", result)

		mockLLM.AssertExpectations(t)
	})
}

func TestTokenTrackingWrapper_DelegatedMethods(t *testing.T) {
	mockLLM := &MockLanguageModel{}
	wrapper := NewTokenUsageLoggingWrapper(mockLLM, "test-llm", &observedPluginLogger{}, nil, nil)

	t.Run("CountTokens delegates to wrapped model", func(t *testing.T) {
		mockLLM.On("CountTokens", "test text").Return(42)

		result := wrapper.CountTokens("test text")
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
