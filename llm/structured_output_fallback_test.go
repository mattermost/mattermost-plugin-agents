// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLLMForFallback struct {
	response string
	// streamEvents, when set, is replayed by ChatCompletion instead of
	// synthesizing a single text event from response.
	streamEvents []TextStreamEvent
}

func (f *fakeLLMForFallback) ChatCompletion(_ context.Context, _ CompletionRequest, _ ...LanguageModelOption) (*TextStreamResult, error) {
	events := f.streamEvents
	if events == nil {
		events = []TextStreamEvent{
			{Type: EventTypeText, Value: f.response},
			{Type: EventTypeEnd},
		}
	}

	stream := make(chan TextStreamEvent)
	go func() {
		defer close(stream)
		for _, event := range events {
			stream <- event
		}
	}()

	return &TextStreamResult{Stream: stream}, nil
}

func (f *fakeLLMForFallback) ChatCompletionNoStream(_ context.Context, _ CompletionRequest, _ ...LanguageModelOption) (string, error) {
	return f.response, nil
}

func (f *fakeLLMForFallback) CountTokens(_ context.Context, _ CompletionRequest, _ ...LanguageModelOption) (int, error) {
	return 0, ErrUnsupportedTokenCount
}
func (f *fakeLLMForFallback) InputTokenLimit() int  { return 4096 }
func (f *fakeLLMForFallback) OutputTokenLimit() int { return 4096 }

func TestStructuredOutputFallbackWrapper(t *testing.T) {
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()
	withSchema := func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	}

	tests := []struct {
		name                    string
		response                string
		structuredOutputEnabled bool
		opts                    []LanguageModelOption
		expected                string
	}{
		{
			name:                    "schema requested, structured output disabled: strips fencing",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                `{"name": "test"}`,
		},
		{
			name:                    "schema requested, structured output enabled: untouched",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: true,
			opts:                    []LanguageModelOption{withSchema},
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:                    "no schema, structured output disabled: untouched",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: false,
			opts:                    nil,
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:                    "no schema, structured output enabled: untouched",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: true,
			opts:                    nil,
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:                    "no fencing, schema requested, structured output disabled: untouched",
			response:                `{"name": "test"}`,
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                `{"name": "test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapper := NewStructuredOutputFallbackWrapper(
				&fakeLLMForFallback{response: tt.response},
				tt.structuredOutputEnabled,
			)
			result, err := wrapper.ChatCompletionNoStream(context.Background(), CompletionRequest{}, tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestStructuredOutputFallbackWrapperStreaming covers the streaming path, which
// is what bridge callers like Rewrites use. The accumulated stream text must
// match the non-streaming behavior so callers can parse it as JSON.
func TestStructuredOutputFallbackWrapperStreaming(t *testing.T) {
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()
	withSchema := func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	}

	tests := []struct {
		name                    string
		streamEvents            []TextStreamEvent
		structuredOutputEnabled bool
		opts                    []LanguageModelOption
		expected                string
	}{
		{
			name: "schema requested, structured output disabled: strips fencing across chunks",
			streamEvents: []TextStreamEvent{
				{Type: EventTypeText, Value: "```json\n"},
				{Type: EventTypeText, Value: `{"name": `},
				{Type: EventTypeText, Value: `"test"}`},
				{Type: EventTypeText, Value: "\n```"},
				{Type: EventTypeEnd},
			},
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                `{"name": "test"}`,
		},
		{
			// Fence markers split across chunk boundaries would defeat naive
			// per-chunk stripping; only buffering until the end recovers the JSON.
			name: "schema requested, structured output disabled: strips fencing split across chunk boundaries",
			streamEvents: []TextStreamEvent{
				{Type: EventTypeText, Value: "``"},
				{Type: EventTypeText, Value: "`json\n{\"na"},
				{Type: EventTypeText, Value: "me\": \"test\"}\n`"},
				{Type: EventTypeText, Value: "``"},
				{Type: EventTypeEnd},
			},
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                `{"name": "test"}`,
		},
		{
			name: "schema requested, structured output disabled: empty fenced block yields empty text",
			streamEvents: []TextStreamEvent{
				{Type: EventTypeText, Value: "```json\n```"},
				{Type: EventTypeEnd},
			},
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                "",
		},
		{
			// Some providers close the stream without an explicit end event; the
			// buffered (stripped) text must still reach the caller.
			name: "schema requested, structured output disabled: flushes when stream closes without end event",
			streamEvents: []TextStreamEvent{
				{Type: EventTypeText, Value: "```json\n"},
				{Type: EventTypeText, Value: `{"name": "test"}`},
				{Type: EventTypeText, Value: "\n```"},
			},
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                `{"name": "test"}`,
		},
		{
			name: "schema requested, structured output enabled: untouched",
			streamEvents: []TextStreamEvent{
				{Type: EventTypeText, Value: "```json\n{\"name\": \"test\"}\n```"},
				{Type: EventTypeEnd},
			},
			structuredOutputEnabled: true,
			opts:                    []LanguageModelOption{withSchema},
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name: "no schema, structured output disabled: untouched",
			streamEvents: []TextStreamEvent{
				{Type: EventTypeText, Value: "```json\n{\"name\": \"test\"}\n```"},
				{Type: EventTypeEnd},
			},
			structuredOutputEnabled: false,
			opts:                    nil,
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name: "no fencing, schema requested, structured output disabled: untouched",
			streamEvents: []TextStreamEvent{
				{Type: EventTypeText, Value: `{"name": `},
				{Type: EventTypeText, Value: `"test"}`},
				{Type: EventTypeEnd},
			},
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                `{"name": "test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapper := NewStructuredOutputFallbackWrapper(
				&fakeLLMForFallback{streamEvents: tt.streamEvents},
				tt.structuredOutputEnabled,
			)
			result, err := wrapper.ChatCompletion(context.Background(), CompletionRequest{}, tt.opts...)
			require.NoError(t, err)
			require.NotNil(t, result)

			text, err := result.ReadAll()
			require.NoError(t, err)
			assert.Equal(t, tt.expected, text)
		})
	}
}

// TestStructuredOutputFallbackWrapperStreamingForwardsEventsAndErrors verifies
// that, while buffering text for fence stripping, the wrapper still forwards
// non-text events (reasoning, usage, etc.) and propagates stream errors.
func TestStructuredOutputFallbackWrapperStreamingForwardsEventsAndErrors(t *testing.T) {
	withSchema := func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = NewJSONSchemaFromStruct[struct {
			Name string `json:"name"`
		}]()
	}

	collect := func(t *testing.T, result *TextStreamResult) ([]EventType, string, error) {
		t.Helper()
		var types []EventType
		var text strings.Builder
		var streamErr error
		for event := range result.Stream {
			types = append(types, event.Type)
			switch event.Type {
			case EventTypeText:
				if chunk, ok := event.Value.(string); ok {
					text.WriteString(chunk)
				}
			case EventTypeError:
				if err, ok := event.Value.(error); ok {
					streamErr = err
				}
			}
		}
		return types, text.String(), streamErr
	}

	t.Run("forwards non-text events and strips final text", func(t *testing.T) {
		wrapper := NewStructuredOutputFallbackWrapper(
			&fakeLLMForFallback{streamEvents: []TextStreamEvent{
				{Type: EventTypeReasoning, Value: "thinking"},
				{Type: EventTypeText, Value: "```json\n"},
				{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 1}},
				{Type: EventTypeText, Value: `{"name": "test"}`},
				{Type: EventTypeText, Value: "\n```"},
				{Type: EventTypeEnd},
			}},
			false,
		)

		result, err := wrapper.ChatCompletion(context.Background(), CompletionRequest{}, withSchema)
		require.NoError(t, err)

		types, text, streamErr := collect(t, result)
		require.NoError(t, streamErr)
		assert.Equal(t, `{"name": "test"}`, text)
		assert.Contains(t, types, EventTypeReasoning)
		assert.Contains(t, types, EventTypeUsage)
		// The single stripped text event must precede the end event.
		assert.Equal(t, EventTypeText, types[len(types)-2])
		assert.Equal(t, EventTypeEnd, types[len(types)-1])
	})

	t.Run("propagates mid-stream error and drops buffered text", func(t *testing.T) {
		sentinel := errors.New("provider failure")
		wrapper := NewStructuredOutputFallbackWrapper(
			&fakeLLMForFallback{streamEvents: []TextStreamEvent{
				{Type: EventTypeText, Value: "```json\n{\"name\":"},
				{Type: EventTypeError, Value: sentinel},
			}},
			false,
		)

		result, err := wrapper.ChatCompletion(context.Background(), CompletionRequest{}, withSchema)
		require.NoError(t, err)

		_, text, streamErr := collect(t, result)
		require.ErrorIs(t, streamErr, sentinel)
		assert.Empty(t, text)
	})
}
