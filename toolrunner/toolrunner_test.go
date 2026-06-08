// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package toolrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// testLLM implements llm.LanguageModel with scripted responses.
type testLLM struct {
	responses []testResponse
	callCount int
	mu        sync.Mutex
	// capturedRequests stores the CompletionRequest from each call for assertion.
	capturedRequests []llm.CompletionRequest
}

type testResponse struct {
	events []llm.TextStreamEvent
	err    error // if non-nil, ChatCompletion returns this error
}

func (m *testLLM) ChatCompletion(_ context.Context, req llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.capturedRequests = append(m.capturedRequests, req)
	if m.callCount >= len(m.responses) {
		return nil, fmt.Errorf("unexpected call %d", m.callCount)
	}
	resp := m.responses[m.callCount]
	m.callCount++

	if resp.err != nil {
		return nil, resp.err
	}

	stream := make(chan llm.TextStreamEvent, len(resp.events))
	for _, e := range resp.events {
		stream <- e
	}
	close(stream)
	return &llm.TextStreamResult{Stream: stream}, nil
}

func (m *testLLM) ChatCompletionNoStream(ctx context.Context, req llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	result, err := m.ChatCompletion(ctx, req, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (m *testLLM) CountTokens(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}
func (m *testLLM) InputTokenLimit() int  { return 4096 }
func (m *testLLM) OutputTokenLimit() int { return 4096 }

// testToolDef defines a test tool for newTestToolStore.
type testToolDef struct {
	name         string
	serverOrigin string
	result       string
	err          error
}

// newTestToolStore creates a ToolStore with the given test tools.
func newTestToolStore(tools ...testToolDef) *llm.ToolStore {
	store := llm.NewNoTools()
	llmTools := make([]llm.Tool, len(tools))
	for i, t := range tools {
		result := t.result
		toolErr := t.err
		llmTools[i] = llm.Tool{
			Name:         t.name,
			Description:  "test tool",
			ServerOrigin: t.serverOrigin,
			Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return result, toolErr
			},
		}
	}
	store.AddTools(llmTools)
	return store
}

func alwaysExecute(_ llm.ToolCall) bool { return true }
func neverExecute(_ llm.ToolCall) bool  { return false }

// optCapturingLLM wraps a testLLM to capture the opts from each ChatCompletion call.
type optCapturingLLM struct {
	inner        *testLLM
	capturedOpts *[][]llm.LanguageModelOption
}

func (c *optCapturingLLM) ChatCompletion(ctx context.Context, req llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	*c.capturedOpts = append(*c.capturedOpts, opts)
	return c.inner.ChatCompletion(ctx, req, opts...)
}

func (c *optCapturingLLM) ChatCompletionNoStream(ctx context.Context, req llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	return c.inner.ChatCompletionNoStream(ctx, req, opts...)
}

func (c *optCapturingLLM) CountTokens(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (int, error) {
	return c.inner.CountTokens(ctx, request, opts...)
}
func (c *optCapturingLLM) InputTokenLimit() int  { return c.inner.InputTokenLimit() }
func (c *optCapturingLLM) OutputTokenLimit() int { return c.inner.OutputTokenLimit() }

func TestToolRunner_NoToolCalls(t *testing.T) {
	// LLM returns text only, no tool calls.
	inner := &testLLM{
		responses: []testResponse{{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Hello world"},
				{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 10, OutputTokens: 5}},
				{Type: llm.EventTypeEnd},
			},
		}},
	}

	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "Hi"}},
		Context: &llm.Context{Tools: llm.NewNoTools()},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Stream should contain the text.
	text, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)
	assert.Equal(t, "Hello world", text)

	// No tool turns.
	assert.Empty(t, result.ToolTurns)

	// LLM called exactly once.
	assert.Equal(t, 1, inner.callCount)
}

func TestToolRunner_SingleToolRound(t *testing.T) {
	// Round 1: LLM returns tool call.
	// Round 2: LLM returns final text.
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Let me check..."},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"NYC"}`)},
				}},
				{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 100, OutputTokens: 50}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "It's 72F in NYC"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(testToolDef{name: "get_weather", result: "72F sunny"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "What's the weather?"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)

	text, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)
	// Text from both rounds is forwarded (intermediate + final).
	assert.Contains(t, text, "It's 72F in NYC")

	// One tool turn.
	require.Len(t, result.ToolTurns, 1)
	turn := result.ToolTurns[0]
	assert.Equal(t, "Let me check...", turn.AssistantMessage)
	require.Len(t, turn.AssistantToolCalls, 1)
	assert.Equal(t, "tc1", turn.AssistantToolCalls[0].ID)
	assert.Equal(t, "get_weather", turn.AssistantToolCalls[0].Name)

	require.Len(t, turn.ToolResults, 1)
	assert.Equal(t, "72F sunny", turn.ToolResults[0].Result)
	assert.False(t, turn.ToolResults[0].IsError)

	// Token counts captured from first round.
	assert.Equal(t, int64(100), turn.TokensIn)
	assert.Equal(t, int64(50), turn.TokensOut)

	// LLM called twice.
	assert.Equal(t, 2, inner.callCount)

	// Verify the second request contains the tool result post.
	require.Len(t, inner.capturedRequests, 2)
	secondReq := inner.capturedRequests[1]
	require.True(t, len(secondReq.Posts) >= 2)
	botPost := secondReq.Posts[len(secondReq.Posts)-1]
	assert.Equal(t, llm.PostRoleBot, botPost.Role)
	assert.Equal(t, "Let me check...", botPost.Message)
	require.Len(t, botPost.ToolUse, 1)
	assert.Equal(t, "72F sunny", botPost.ToolUse[0].Result)
	assert.Equal(t, llm.ToolCallStatusAutoApproved, botPost.ToolUse[0].Status)
}

func TestToolRunner_MultipleToolRounds(t *testing.T) {
	// Round 1: tool call -> execute -> Round 2: another tool call -> execute -> Round 3: final text.
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "tool_a", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 50, OutputTokens: 20}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc2", Name: "tool_b", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 80, OutputTokens: 30}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Done"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(
		testToolDef{name: "tool_a", result: "result_a"},
		testToolDef{name: "tool_b", result: "result_b"},
	)
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)

	text, _ := result.Stream.ReadAll()
	assert.Equal(t, "Done", text)
	require.Len(t, result.ToolTurns, 2)
	assert.Equal(t, int64(50), result.ToolTurns[0].TokensIn)
	assert.Equal(t, int64(20), result.ToolTurns[0].TokensOut)
	assert.Equal(t, int64(80), result.ToolTurns[1].TokensIn)
	assert.Equal(t, int64(30), result.ToolTurns[1].TokensOut)
	assert.Equal(t, 3, inner.callCount)
}

func TestToolRunner_PartialApproval_NoneExecuted(t *testing.T) {
	// LLM returns tool call, shouldExecute returns false.
	inner := &testLLM{
		responses: []testResponse{{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "I need to use a tool"},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "dangerous_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			},
		}},
	}

	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "do something"}},
		Context: &llm.Context{Tools: llm.NewNoTools()},
	}

	result, err := runner.Run(context.Background(), request, neverExecute, nil)
	require.NoError(t, err)

	// Stream should still contain text AND the tool call events.
	var gotText, gotToolCalls bool
	for event := range result.Stream.Stream {
		switch event.Type {
		case llm.EventTypeText:
			gotText = true
		case llm.EventTypeToolCalls:
			gotToolCalls = true
			tcs := event.Value.([]llm.ToolCall)
			assert.Equal(t, "tc1", tcs[0].ID)
		}
	}
	assert.True(t, gotText)
	assert.True(t, gotToolCalls)

	// No tool turns recorded.
	assert.Empty(t, result.ToolTurns)

	// LLM called once.
	assert.Equal(t, 1, inner.callCount)
}

func TestToolRunner_MixedBatch_AllOrNothing(t *testing.T) {
	// LLM returns 2 tool calls: one approved, one not.
	// shouldExecute returns false for the second -> entire batch unapproved.
	inner := &testLLM{
		responses: []testResponse{{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "read_tool", Arguments: json.RawMessage(`{}`)},
					{ID: "tc2", Name: "write_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			},
		}},
	}

	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: llm.NewNoTools()},
	}

	// Only approve read_tool, not write_tool.
	result, err := runner.Run(context.Background(), request, func(tc llm.ToolCall) bool {
		return tc.Name == "read_tool"
	}, nil)
	require.NoError(t, err)

	// All tool calls returned unresolved.
	var toolCallEvents []llm.ToolCall
	for event := range result.Stream.Stream {
		if event.Type == llm.EventTypeToolCalls {
			toolCallEvents = append(toolCallEvents, event.Value.([]llm.ToolCall)...)
		}
	}
	require.Len(t, toolCallEvents, 2)
	assert.Equal(t, "read_tool", toolCallEvents[0].Name)
	assert.Equal(t, "write_tool", toolCallEvents[1].Name)

	// No tool turns.
	assert.Empty(t, result.ToolTurns)
	assert.Equal(t, 1, inner.callCount)
}

func TestToolRunner_ToolExecutionError(t *testing.T) {
	// Tool resolver returns error -> error recorded, LLM called again with error result.
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "failing_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "The tool failed, sorry"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(testToolDef{
		name: "failing_tool",
		err:  fmt.Errorf("connection timeout"),
	})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err) // runner itself doesn't fail

	text, _ := result.Stream.ReadAll()
	assert.Equal(t, "The tool failed, sorry", text)

	require.Len(t, result.ToolTurns, 1)
	require.Len(t, result.ToolTurns[0].ToolResults, 1)
	assert.True(t, result.ToolTurns[0].ToolResults[0].IsError)
	assert.Contains(t, result.ToolTurns[0].ToolResults[0].Result, "connection timeout")

	// Verify the error status was sent to the LLM.
	secondReq := inner.capturedRequests[1]
	botPost := secondReq.Posts[len(secondReq.Posts)-1]
	assert.Equal(t, llm.ToolCallStatusError, botPost.ToolUse[0].Status)
	assert.Contains(t, botPost.ToolUse[0].Result, "connection timeout")
}

func TestToolRunner_LLMError(t *testing.T) {
	// ChatCompletion returns an error directly on the first call.
	inner := &testLLM{
		responses: []testResponse{{
			err: fmt.Errorf("rate limit exceeded"),
		}},
	}

	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: llm.NewNoTools()},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "rate limit exceeded")
}

func TestToolRunner_LLMStreamError(t *testing.T) {
	// Stream emits an error event — delivered through the stream, not as return value.
	inner := &testLLM{
		responses: []testResponse{{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "partial..."},
				{Type: llm.EventTypeError, Value: fmt.Errorf("stream interrupted")},
			},
		}},
	}

	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: llm.NewNoTools()},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err, "first ChatCompletion succeeds, stream error comes through stream")
	require.NotNil(t, result)

	// ReadAll discards accumulated text on error (returns "").
	// The error is delivered through the stream, not as Run's return value.
	_, streamErr := result.Stream.ReadAll()
	assert.ErrorContains(t, streamErr, "stream interrupted")
	assert.Empty(t, result.ToolTurns)
}

func TestToolRunner_StreamEventPassthrough(t *testing.T) {
	// Verify reasoning, annotations, usage events pass through on final stream.
	inner := &testLLM{
		responses: []testResponse{{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeReasoning, Value: "thinking..."},
				{Type: llm.EventTypeReasoningEnd, Value: llm.ReasoningData{Text: "thinking...", Signature: "sig"}},
				{Type: llm.EventTypeText, Value: "Answer"},
				{Type: llm.EventTypeAnnotations, Value: []llm.Annotation{{Type: llm.AnnotationTypeURLCitation, URL: "https://example.com"}}},
				{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 100, OutputTokens: 50}},
				{Type: llm.EventTypeEnd},
			},
		}},
	}

	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: llm.NewNoTools()},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)

	var eventTypes []llm.EventType
	for event := range result.Stream.Stream {
		eventTypes = append(eventTypes, event.Type)
	}

	assert.Contains(t, eventTypes, llm.EventTypeReasoning)
	assert.Contains(t, eventTypes, llm.EventTypeReasoningEnd)
	assert.Contains(t, eventTypes, llm.EventTypeText)
	assert.Contains(t, eventTypes, llm.EventTypeAnnotations)
	assert.Contains(t, eventTypes, llm.EventTypeUsage)
	assert.Contains(t, eventTypes, llm.EventTypeEnd)
}

func TestToolRunner_MaxRoundsExhausted(t *testing.T) {
	responses := make([]testResponse, MaxToolRounds)
	for i := 0; i < MaxToolRounds-1; i++ {
		responses[i] = testResponse{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: fmt.Sprintf("tc%d", i), Name: "loop_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			},
		}
	}
	responses[MaxToolRounds-1] = testResponse{
		events: []llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "synthesized answer"},
			{Type: llm.EventTypeEnd},
		},
	}

	inner := &testLLM{responses: responses}
	store := newTestToolStore(testToolDef{name: "loop_tool", result: "ok"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)

	// (read stream first to ensure goroutine completes)
	text, readErr := result.Stream.ReadAll()
	assert.NoError(t, readErr)
	assert.Equal(t, "synthesized answer", text)
	assert.Equal(t, "synthesized answer", result.FinalText)

	// Tools ran on MaxToolRounds-1 rounds; the last round was the synthesis.
	assert.Len(t, result.ToolTurns, MaxToolRounds-1)
	assert.Equal(t, MaxToolRounds, inner.callCount)
}

func TestToolRunner_MaxRoundsExhausted_SynthesisCallHasToolsDisabled(t *testing.T) {
	// Verify the final round disables tools and seeds the iteration-limit message.
	responses := make([]testResponse, MaxToolRounds)
	for i := 0; i < MaxToolRounds-1; i++ {
		responses[i] = testResponse{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: fmt.Sprintf("tc%d", i), Name: "loop_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			},
		}
	}
	responses[MaxToolRounds-1] = testResponse{
		events: []llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "wrapping up"},
			{Type: llm.EventTypeEnd},
		},
	}

	var capturedOpts [][]llm.LanguageModelOption
	inner := &optCapturingLLM{
		inner:        &testLLM{responses: responses},
		capturedOpts: &capturedOpts,
	}

	store := newTestToolStore(testToolDef{name: "loop_tool", result: "ok"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)
	_, _ = result.Stream.ReadAll()

	require.Len(t, capturedOpts, MaxToolRounds)

	// Earlier calls must not have tools disabled.
	for round := 0; round < MaxToolRounds-1; round++ {
		var cfg llm.LanguageModelConfig
		for _, opt := range capturedOpts[round] {
			opt(&cfg)
		}
		assert.Falsef(t, cfg.ToolsDisabled, "round %d should not have tools disabled", round)
	}

	// The final synthesis call must have tools disabled.
	var finalCfg llm.LanguageModelConfig
	for _, opt := range capturedOpts[MaxToolRounds-1] {
		opt(&finalCfg)
	}
	assert.True(t, finalCfg.ToolsDisabled, "final synthesis call must disable tools")

	// The final request's posts must contain the iteration-limit user message.
	require.Len(t, inner.inner.capturedRequests, MaxToolRounds)
	finalReq := inner.inner.capturedRequests[MaxToolRounds-1]
	var foundUserMessage bool
	for _, post := range finalReq.Posts {
		if post.Role == llm.PostRoleUser && strings.Contains(post.Message, llm.ToolIterationLimitUserMessage) {
			foundUserMessage = true
			break
		}
	}
	assert.True(t, foundUserMessage, "final request must include the iteration-limit user message")
}

// When the provider ignores tool_choice="none" and returns tool calls on the
// synthesis round, the runner drops them and emits End with no fallback text.
func TestToolRunner_MaxRoundsExhausted_ProviderEmitsToolCallDuringSynthesis(t *testing.T) {
	responses := make([]testResponse, MaxToolRounds)
	for i := 0; i < MaxToolRounds-1; i++ {
		responses[i] = testResponse{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: fmt.Sprintf("tc%d", i), Name: "loop_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			},
		}
	}
	// Final round emits a tool call despite WithToolsDisabled.
	responses[MaxToolRounds-1] = testResponse{
		events: []llm.TextStreamEvent{
			{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
				{ID: "tc-final", Name: "loop_tool", Arguments: json.RawMessage(`{}`)},
			}},
			{Type: llm.EventTypeEnd},
		},
	}

	inner := &testLLM{responses: responses}
	store := newTestToolStore(testToolDef{name: "loop_tool", result: "ok"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)

	text, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)
	assert.Empty(t, text, "no synthetic fallback when synthesis round emits only tool calls")
	assert.Empty(t, result.FinalText)

	// MaxToolRounds-1 tool turns; the final round's tool call was ignored.
	assert.Len(t, result.ToolTurns, MaxToolRounds-1)
	assert.Equal(t, MaxToolRounds, inner.callCount, "still exactly MaxToolRounds LLM calls")
}

func TestToolRunner_FinalText_OmitsToolRoundPreamble(t *testing.T) {
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Let me search. "},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "loop_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "final answer"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(testToolDef{name: "loop_tool", result: "ok"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)

	_, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)
	assert.Equal(t, "final answer", result.FinalText)
}

func TestToolRunner_FinalText_DropsFailedSynthesisPreamble(t *testing.T) {
	responses := make([]testResponse, MaxToolRounds)
	for i := 0; i < MaxToolRounds-1; i++ {
		responses[i] = testResponse{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: fmt.Sprintf("preamble %d ", i)},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: fmt.Sprintf("tc%d", i), Name: "loop_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			},
		}
	}
	responses[MaxToolRounds-1] = testResponse{
		events: []llm.TextStreamEvent{
			{Type: llm.EventTypeText, Value: "Let me try broader searches. "},
			{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
				{ID: "tc-final", Name: "loop_tool", Arguments: json.RawMessage(`{}`)},
			}},
			{Type: llm.EventTypeEnd},
		},
	}

	inner := &testLLM{responses: responses}
	store := newTestToolStore(testToolDef{name: "loop_tool", result: "ok"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)

	_, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)
	assert.Empty(t, result.FinalText, "failed synthesis preamble must not count as final text")
}

func TestFinalAssistantText(t *testing.T) {
	tests := []struct {
		name            string
		text            string
		synthesisCalled bool
		droppedCalls    int
		want            string
	}{
		{
			name:            "returns text for normal final response",
			text:            "answer",
			synthesisCalled: false,
			droppedCalls:    0,
			want:            "answer",
		},
		{
			name:            "discards preamble when synthesis tool calls were dropped",
			text:            "Let me search.",
			synthesisCalled: true,
			droppedCalls:    1,
			want:            "",
		},
		{
			name:            "keeps text when synthesis succeeded without dropped calls",
			text:            "summary",
			synthesisCalled: true,
			droppedCalls:    0,
			want:            "summary",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := finalAssistantText(tc.text, tc.synthesisCalled, tc.droppedCalls)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestToolRunner_ReasoningPreservedInToolTurn(t *testing.T) {
	// Verify that reasoning from a tool-call round is captured in the ToolTurn
	// and forwarded to the LLM in the next request.
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeReasoning, Value: "I should use the tool"},
				{Type: llm.EventTypeReasoningEnd, Value: llm.ReasoningData{
					Text: "I should use the tool", Signature: "sig123",
				}},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "tool_a", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Done"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(testToolDef{name: "tool_a", result: "ok"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)
	_, _ = result.Stream.ReadAll()

	require.Len(t, result.ToolTurns, 1)
	assert.Equal(t, "I should use the tool", result.ToolTurns[0].AssistantReasoning.Text)
	assert.Equal(t, "sig123", result.ToolTurns[0].AssistantReasoning.Signature)

	// Verify reasoning was forwarded to LLM in second request.
	secondReq := inner.capturedRequests[1]
	botPost := secondReq.Posts[len(secondReq.Posts)-1]
	assert.Equal(t, "I should use the tool", botPost.Reasoning)
	assert.Equal(t, "sig123", botPost.ReasoningSignature)
}

func TestToolRunner_MultipleToolCallsInOneBatch(t *testing.T) {
	// LLM returns multiple tool calls in a single batch.
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "tool_a", Arguments: json.RawMessage(`{}`)},
					{ID: "tc2", Name: "tool_b", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Both done"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(
		testToolDef{name: "tool_a", result: "result_a"},
		testToolDef{name: "tool_b", result: "result_b"},
	)
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)

	text, _ := result.Stream.ReadAll()
	assert.Equal(t, "Both done", text)

	require.Len(t, result.ToolTurns, 1)
	require.Len(t, result.ToolTurns[0].AssistantToolCalls, 2)
	require.Len(t, result.ToolTurns[0].ToolResults, 2)
	assert.Equal(t, "result_a", result.ToolTurns[0].ToolResults[0].Result)
	assert.Equal(t, "result_b", result.ToolTurns[0].ToolResults[1].Result)
}

func TestToolRunner_NilContext(t *testing.T) {
	// Run should work gracefully with nil Context (no tools available).
	inner := &testLLM{
		responses: []testResponse{{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Hello"},
				{Type: llm.EventTypeEnd},
			},
		}},
	}

	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: nil,
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)
	text, _ := result.Stream.ReadAll()
	assert.Equal(t, "Hello", text)
	assert.Empty(t, result.ToolTurns)
}

func TestToolRunner_OptsPassedThrough(t *testing.T) {
	// Verify that LanguageModelOption values are passed to every LLM call.
	var capturedOpts [][]llm.LanguageModelOption

	inner := &optCapturingLLM{
		inner: &testLLM{
			responses: []testResponse{
				{events: []llm.TextStreamEvent{
					{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
						{ID: "tc1", Name: "tool_a", Arguments: json.RawMessage(`{}`)},
					}},
					{Type: llm.EventTypeEnd},
				}},
				{events: []llm.TextStreamEvent{
					{Type: llm.EventTypeText, Value: "done"},
					{Type: llm.EventTypeEnd},
				}},
			},
		},
		capturedOpts: &capturedOpts,
	}

	store := newTestToolStore(testToolDef{name: "tool_a", result: "ok"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil, llm.WithReasoningDisabled())
	require.NoError(t, err)
	_, _ = result.Stream.ReadAll()

	// Both LLM calls should have received the opts.
	require.Len(t, capturedOpts, 2)
	assert.Len(t, capturedOpts[0], 1) // WithReasoningDisabled
	assert.Len(t, capturedOpts[1], 1)
}

func TestToolRunner_ServerOriginPreserved(t *testing.T) {
	// Verify that ServerOrigin is preserved through tool execution and in the
	// resubmitted request posts.
	const serverOrigin = "https://mcp.example.com"

	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "mcp_tool", Arguments: json.RawMessage(`{}`), ServerOrigin: serverOrigin},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "done"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(testToolDef{name: "mcp_tool", serverOrigin: serverOrigin, result: "mcp_result"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "test"}},
		Context: &llm.Context{Tools: store},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)
	_, _ = result.Stream.ReadAll()

	// Verify the tool turn preserves server origin.
	require.Len(t, result.ToolTurns, 1)
	assert.Equal(t, serverOrigin, result.ToolTurns[0].AssistantToolCalls[0].ServerOrigin)

	// Verify the resubmitted request preserves server origin in bot post.
	secondReq := inner.capturedRequests[1]
	botPost := secondReq.Posts[len(secondReq.Posts)-1]
	require.Len(t, botPost.ToolUse, 1)
	assert.Equal(t, serverOrigin, botPost.ToolUse[0].ServerOrigin)
	assert.Equal(t, "mcp_result", botPost.ToolUse[0].Result)
	assert.Equal(t, llm.ToolCallStatusAutoApproved, botPost.ToolUse[0].Status)
}

func TestToolRunner_ApprovalAfterToolRound(t *testing.T) {
	// Round 1: auto-approved tool call executes.
	// Round 2: LLM returns a tool call that is NOT approved -> return unresolved.
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "safe_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 40, OutputTokens: 10}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Now I need approval"},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc2", Name: "dangerous_tool", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(
		testToolDef{name: "safe_tool", result: "safe_result"},
		testToolDef{name: "dangerous_tool", result: "never_called"},
	)
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	// Only approve safe_tool.
	result, err := runner.Run(context.Background(), request, func(tc llm.ToolCall) bool {
		return tc.Name == "safe_tool"
	}, nil)
	require.NoError(t, err)

	// Consume stream first to ensure goroutine completes.
	var gotText bool
	var gotToolCalls bool
	for event := range result.Stream.Stream {
		switch event.Type {
		case llm.EventTypeText:
			gotText = true
		case llm.EventTypeToolCalls:
			gotToolCalls = true
		}
	}
	assert.True(t, gotText)
	assert.True(t, gotToolCalls)

	// One tool turn was executed (safe_tool).
	require.Len(t, result.ToolTurns, 1)
	assert.Equal(t, "safe_tool", result.ToolTurns[0].AssistantToolCalls[0].Name)
	assert.Equal(t, int64(40), result.ToolTurns[0].TokensIn)

	// LLM called twice.
	assert.Equal(t, 2, inner.callCount)
}

func TestToolRunner_OnToolTurnsCallback(t *testing.T) {
	// Verify that onToolTurns callback is called with accumulated tool turns.
	inner := &testLLM{
		responses: []testResponse{
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
					{ID: "tc1", Name: "tool_a", Arguments: json.RawMessage(`{}`)},
				}},
				{Type: llm.EventTypeEnd},
			}},
			{events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Done"},
				{Type: llm.EventTypeEnd},
			}},
		},
	}

	store := newTestToolStore(testToolDef{name: "tool_a", result: "result_a"})
	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: store},
	}

	var callbackTurns []ToolTurn
	var callbackCalled bool
	result, err := runner.Run(context.Background(), request, alwaysExecute, func(turns []ToolTurn) {
		callbackCalled = true
		callbackTurns = turns
	})
	require.NoError(t, err)

	_, _ = result.Stream.ReadAll()
	assert.True(t, callbackCalled)
	require.Len(t, callbackTurns, 1)
	assert.Equal(t, "tool_a", callbackTurns[0].AssistantToolCalls[0].Name)
	assert.Equal(t, "result_a", callbackTurns[0].ToolResults[0].Result)
}

func TestToolRunner_OnToolTurnsNotCalledWithoutToolUse(t *testing.T) {
	// Verify that onToolTurns callback is NOT called when there are no tool turns.
	inner := &testLLM{
		responses: []testResponse{{
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "Hello"},
				{Type: llm.EventTypeEnd},
			},
		}},
	}

	runner := New(inner)
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: llm.NewNoTools()},
	}

	callbackCalled := false
	result, err := runner.Run(context.Background(), request, alwaysExecute, func(_ []ToolTurn) {
		callbackCalled = true
	})
	require.NoError(t, err)

	_, _ = result.Stream.ReadAll()
	assert.False(t, callbackCalled)
}
