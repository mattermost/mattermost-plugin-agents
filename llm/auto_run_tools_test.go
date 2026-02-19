// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testLLM is a simple test double implementing LanguageModel that returns
// a sequence of pre-configured responses.
type testLLM struct {
	responses []testResponse
	callCount int
}

type testResponse struct {
	events []TextStreamEvent
}

func (m *testLLM) ChatCompletion(_ CompletionRequest, _ ...LanguageModelOption) (*TextStreamResult, error) {
	if m.callCount >= len(m.responses) {
		return nil, fmt.Errorf("unexpected call to ChatCompletion (call %d, only %d responses configured)", m.callCount, len(m.responses))
	}
	resp := m.responses[m.callCount]
	m.callCount++

	stream := make(chan TextStreamEvent, len(resp.events))
	for _, e := range resp.events {
		stream <- e
	}
	close(stream)

	return &TextStreamResult{Stream: stream}, nil
}

func (m *testLLM) ChatCompletionNoStream(request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	result, err := m.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (m *testLLM) CountTokens(_ string) int {
	return 1
}

func (m *testLLM) InputTokenLimit() int {
	return 4096
}

// newTestToolStore creates a ToolStore with a simple test tool that returns a known result.
func newTestToolStore(toolName, toolResult string) *ToolStore {
	store := NewNoTools()
	store.AddTools([]Tool{
		{
			Name:        toolName,
			Description: "A test tool",
			Resolver: func(_ *Context, _ ToolArgumentGetter) (string, error) {
				return toolResult, nil
			},
		},
	})
	return store
}

func TestAutoRunToolsWrapper(t *testing.T) {
	tests := []struct {
		name             string
		autoRunTools     []string
		context          *Context
		responses        []testResponse
		expectedTexts    []string
		expectedToolCall bool // whether EventTypeToolCalls should be forwarded
		expectedCalls    int
	}{
		{
			name:         "no auto-run config delegates directly",
			autoRunTools: nil,
			context:      &Context{Tools: newTestToolStore("test_tool", "result")},
			responses: []testResponse{
				{events: []TextStreamEvent{
					{Type: EventTypeText, Value: "hello"},
					{Type: EventTypeEnd},
				}},
			},
			expectedTexts:    []string{"hello"},
			expectedToolCall: false,
			expectedCalls:    1,
		},
		{
			name:         "no context delegates directly",
			autoRunTools: []string{"test_tool"},
			context:      nil,
			responses: []testResponse{
				{events: []TextStreamEvent{
					{Type: EventTypeText, Value: "hello"},
					{Type: EventTypeEnd},
				}},
			},
			expectedTexts:    []string{"hello"},
			expectedToolCall: false,
			expectedCalls:    1,
		},
		{
			name:         "no tools in context delegates directly",
			autoRunTools: []string{"test_tool"},
			context:      &Context{},
			responses: []testResponse{
				{events: []TextStreamEvent{
					{Type: EventTypeText, Value: "hello"},
					{Type: EventTypeEnd},
				}},
			},
			expectedTexts:    []string{"hello"},
			expectedToolCall: false,
			expectedCalls:    1,
		},
		{
			name:         "auto-run executes tools and re-invokes LLM",
			autoRunTools: []string{"test_tool"},
			context:      &Context{Tools: newTestToolStore("test_tool", "tool_result")},
			responses: []testResponse{
				{events: []TextStreamEvent{
					{Type: EventTypeText, Value: "thinking..."},
					{Type: EventTypeToolCalls, Value: []ToolCall{
						{ID: "tc1", Name: "test_tool", Arguments: json.RawMessage(`{}`)},
					}},
					{Type: EventTypeEnd},
				}},
				{events: []TextStreamEvent{
					{Type: EventTypeText, Value: "final answer"},
					{Type: EventTypeEnd},
				}},
			},
			expectedTexts:    []string{"thinking...", "final answer"},
			expectedToolCall: false,
			expectedCalls:    2,
		},
		{
			name:         "non-auto-run tools forwarded to output",
			autoRunTools: []string{"other_tool"},
			context:      &Context{Tools: newTestToolStore("test_tool", "result")},
			responses: []testResponse{
				{events: []TextStreamEvent{
					{Type: EventTypeText, Value: "need input"},
					{Type: EventTypeToolCalls, Value: []ToolCall{
						{ID: "tc1", Name: "test_tool", Arguments: json.RawMessage(`{}`)},
					}},
					{Type: EventTypeEnd},
				}},
			},
			expectedTexts:    []string{"need input"},
			expectedToolCall: true,
			expectedCalls:    1,
		},
		{
			name:         "events forwarded during iterations",
			autoRunTools: []string{"test_tool"},
			context:      &Context{Tools: newTestToolStore("test_tool", "result")},
			responses: []testResponse{
				{events: []TextStreamEvent{
					{Type: EventTypeText, Value: "step1 "},
					{Type: EventTypeReasoning, Value: ReasoningData{Text: "reasoning"}},
					{Type: EventTypeToolCalls, Value: []ToolCall{
						{ID: "tc1", Name: "test_tool", Arguments: json.RawMessage(`{}`)},
					}},
					{Type: EventTypeEnd},
				}},
				{events: []TextStreamEvent{
					{Type: EventTypeText, Value: "step2"},
					{Type: EventTypeEnd},
				}},
			},
			expectedTexts:    []string{"step1 ", "step2"},
			expectedToolCall: false,
			expectedCalls:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &testLLM{responses: tt.responses}
			wrapper := NewAutoRunToolsWrapper(inner)

			request := CompletionRequest{
				Posts:   []Post{{Role: PostRoleUser, Message: "test"}},
				Context: tt.context,
			}

			var opts []LanguageModelOption
			if len(tt.autoRunTools) > 0 {
				opts = append(opts, WithAutoRunTools(tt.autoRunTools))
			}

			result, err := wrapper.ChatCompletion(request, opts...)
			require.NoError(t, err)
			require.NotNil(t, result)

			var texts []string
			var gotToolCalls bool
			for event := range result.Stream {
				switch event.Type {
				case EventTypeText:
					if text, ok := event.Value.(string); ok {
						texts = append(texts, text)
					}
				case EventTypeToolCalls:
					gotToolCalls = true
				}
			}

			assert.Equal(t, tt.expectedTexts, texts)
			assert.Equal(t, tt.expectedToolCall, gotToolCalls)
			assert.Equal(t, tt.expectedCalls, inner.callCount)
		})
	}
}

func TestAutoRunToolsWrapper_MaxDepthStopsLoop(t *testing.T) {
	// Create a testLLM that always returns tool calls
	responses := make([]testResponse, MaxToolResolutionDepth+1)
	for i := range responses {
		responses[i] = testResponse{
			events: []TextStreamEvent{
				{Type: EventTypeText, Value: "loop "},
				{Type: EventTypeToolCalls, Value: []ToolCall{
					{ID: fmt.Sprintf("tc%d", i), Name: "test_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: EventTypeEnd},
			},
		}
	}

	inner := &testLLM{responses: responses}
	wrapper := NewAutoRunToolsWrapper(inner)

	request := CompletionRequest{
		Posts:   []Post{{Role: PostRoleUser, Message: "test"}},
		Context: &Context{Tools: newTestToolStore("test_tool", "result")},
	}

	result, err := wrapper.ChatCompletion(request, WithAutoRunTools([]string{"test_tool"}))
	require.NoError(t, err)

	var textCount int
	for event := range result.Stream {
		if event.Type == EventTypeText {
			textCount++
		}
	}

	// Should have called inner exactly MaxToolResolutionDepth times
	assert.Equal(t, MaxToolResolutionDepth, inner.callCount)
	assert.Equal(t, MaxToolResolutionDepth, textCount)
}

func TestAutoRunToolsWrapper_ChatCompletionNoStream(t *testing.T) {
	inner := &testLLM{
		responses: []testResponse{
			{events: []TextStreamEvent{
				{Type: EventTypeText, Value: "thinking..."},
				{Type: EventTypeToolCalls, Value: []ToolCall{
					{ID: "tc1", Name: "test_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: EventTypeEnd},
			}},
			{events: []TextStreamEvent{
				{Type: EventTypeText, Value: "done"},
				{Type: EventTypeEnd},
			}},
		},
	}
	wrapper := NewAutoRunToolsWrapper(inner)

	request := CompletionRequest{
		Posts:   []Post{{Role: PostRoleUser, Message: "test"}},
		Context: &Context{Tools: newTestToolStore("test_tool", "result")},
	}

	text, err := wrapper.ChatCompletionNoStream(request, WithAutoRunTools([]string{"test_tool"}))
	require.NoError(t, err)
	assert.Equal(t, "thinking...done", text)
	assert.Equal(t, 2, inner.callCount)
}

func TestAutoRunToolsWrapper_DelegatedMethods(t *testing.T) {
	inner := &testLLM{}
	wrapper := NewAutoRunToolsWrapper(inner)

	t.Run("CountTokens delegates to inner", func(t *testing.T) {
		assert.Equal(t, 1, wrapper.CountTokens("test"))
	})

	t.Run("InputTokenLimit delegates to inner", func(t *testing.T) {
		assert.Equal(t, 4096, wrapper.InputTokenLimit())
	})
}

func TestAutoRunToolsWrapper_ToolResultsInPost(t *testing.T) {
	// Verify that tool results are correctly added to the request posts
	var capturedPosts []Post
	inner := &testLLM{
		responses: []testResponse{
			{events: []TextStreamEvent{
				{Type: EventTypeToolCalls, Value: []ToolCall{
					{ID: "tc1", Name: "test_tool", Arguments: json.RawMessage(`{"key":"val"}`)},
				}},
				{Type: EventTypeEnd},
			}},
			{events: []TextStreamEvent{
				{Type: EventTypeText, Value: "result"},
				{Type: EventTypeEnd},
			}},
		},
	}

	// Wrap inner to capture the request on second call
	capturingInner := &capturingLLM{inner: inner, capturedPosts: &capturedPosts}

	wrapper := NewAutoRunToolsWrapper(capturingInner)

	request := CompletionRequest{
		Posts:   []Post{{Role: PostRoleUser, Message: "do something"}},
		Context: &Context{Tools: newTestToolStore("test_tool", "tool output")},
	}

	result, err := wrapper.ChatCompletion(request, WithAutoRunTools([]string{"test_tool"}))
	require.NoError(t, err)

	// Consume stream
	for range result.Stream { //nolint:revive
	}

	// The second call should have posts with a bot post containing tool results
	require.NotEmpty(t, capturedPosts)
	lastPost := capturedPosts[len(capturedPosts)-1]
	assert.Equal(t, PostRoleBot, lastPost.Role)
	require.Len(t, lastPost.ToolUse, 1)
	assert.Equal(t, "tc1", lastPost.ToolUse[0].ID)
	assert.Equal(t, "test_tool", lastPost.ToolUse[0].Name)
	assert.Equal(t, "tool output", lastPost.ToolUse[0].Result)
	assert.Equal(t, ToolCallStatusSuccess, lastPost.ToolUse[0].Status)
	assert.Equal(t, json.RawMessage(`{"key":"val"}`), lastPost.ToolUse[0].Arguments)
}

// capturingLLM wraps another LanguageModel to capture the posts from each call.
type capturingLLM struct {
	inner         *testLLM
	capturedPosts *[]Post
}

func (c *capturingLLM) ChatCompletion(request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	// Capture posts from each call after the first
	if c.inner.callCount > 0 {
		*c.capturedPosts = append([]Post{}, request.Posts...)
	}
	return c.inner.ChatCompletion(request, opts...)
}

func (c *capturingLLM) ChatCompletionNoStream(request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	return c.inner.ChatCompletionNoStream(request, opts...)
}

func (c *capturingLLM) CountTokens(text string) int {
	return c.inner.CountTokens(text)
}

func (c *capturingLLM) InputTokenLimit() int {
	return c.inner.InputTokenLimit()
}
