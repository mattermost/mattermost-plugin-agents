// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package toolrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// newDeferredToolStore builds a store with a deferred-result tool (backstop
// resolver) plus any normal tools defined the usual way.
func newDeferredToolStore(deferredName string, normalTools ...llm.Tool) *llm.ToolStore {
	store := llm.NewNoTools()
	store.AddTools(append([]llm.Tool{{
		Name:           deferredName,
		Description:    "deferred test tool",
		DeferredResult: true,
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "", fmt.Errorf("%s is dispatched by the conversation layer and cannot be executed directly", deferredName)
		},
	}}, normalTools...))
	return store
}

// dispatchRecorder records DeferredDispatcher invocations and returns a
// scripted error.
type dispatchRecorder struct {
	calls []llm.ToolCall
	err   error
}

func (d *dispatchRecorder) dispatch(_ context.Context, call llm.ToolCall) error {
	d.calls = append(d.calls, call)
	return d.err
}

func TestRunLoopDeferredDispatch(t *testing.T) {
	deferredBatch := []llm.TextStreamEvent{
		{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
			{ID: "ask1", Name: "AskAnotherUser", Arguments: json.RawMessage(`{"username":"bob","question":"Q?"}`)},
		}},
		{Type: llm.EventTypeEnd},
	}
	finalText := testResponse{events: []llm.TextStreamEvent{
		{Type: llm.EventTypeText, Value: "recovered"},
		{Type: llm.EventTypeEnd},
	}}

	// collectToolCallEvents drains the stream and returns every ToolCall from
	// EventTypeToolCalls events, in order.
	collectToolCallEvents := func(t *testing.T, result *ToolRunResult) []llm.ToolCall {
		t.Helper()
		var events []llm.ToolCall
		for event := range result.Stream.Stream {
			if event.Type == llm.EventTypeToolCalls {
				events = append(events, event.Value.([]llm.ToolCall)...)
			}
		}
		return events
	}

	t.Run("deferred dispatch success stops run waiting", func(t *testing.T) {
		inner := &testLLM{responses: []testResponse{{events: deferredBatch}}}
		recorder := &dispatchRecorder{}
		runner := New(inner, WithDeferredDispatcher(recorder.dispatch))
		request := llm.CompletionRequest{
			Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
			Context: &llm.Context{Tools: newDeferredToolStore("AskAnotherUser")},
		}

		result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
		require.NoError(t, err)

		events := collectToolCallEvents(t, result)
		require.Len(t, events, 1)
		assert.Equal(t, llm.ToolCallStatusWaiting, events[0].Status)
		assert.True(t, events[0].DeferredResult)

		require.Len(t, recorder.calls, 1)
		assert.Equal(t, "ask1", recorder.calls[0].ID)
		assert.True(t, recorder.calls[0].DeferredResult, "dispatcher must receive the enriched call")

		assert.Empty(t, result.ToolTurns)
		assert.Equal(t, 1, inner.callCount, "run must stop after dispatch")
	})

	t.Run("dispatch failure alone continues loop", func(t *testing.T) {
		inner := &testLLM{responses: []testResponse{{events: deferredBatch}, finalText}}
		recorder := &dispatchRecorder{err: fmt.Errorf("user \"bob\" not found")}
		runner := New(inner, WithDeferredDispatcher(recorder.dispatch))
		request := llm.CompletionRequest{
			Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
			Context: &llm.Context{Tools: newDeferredToolStore("AskAnotherUser")},
		}

		result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
		require.NoError(t, err)

		text, readErr := result.Stream.ReadAll()
		require.NoError(t, readErr)
		assert.Equal(t, "recovered", text)

		require.Len(t, result.ToolTurns, 1)
		require.Len(t, result.ToolTurns[0].ToolResults, 1)
		assert.True(t, result.ToolTurns[0].ToolResults[0].IsError)
		assert.Contains(t, result.ToolTurns[0].ToolResults[0].Result, "not found")

		assert.Equal(t, 2, inner.callCount, "loop must continue so the model can correct itself")
		secondReq := inner.capturedRequests[1]
		require.Greater(t, len(secondReq.Posts), 1, "request posts must grow with the failed round")
		botPost := secondReq.Posts[len(secondReq.Posts)-1]
		require.Len(t, botPost.ToolUse, 1)
		assert.Equal(t, llm.ToolCallStatusError, botPost.ToolUse[0].Status)
	})

	t.Run("no dispatcher falls back to pending", func(t *testing.T) {
		inner := &testLLM{responses: []testResponse{{events: deferredBatch}}}
		runner := New(inner)
		request := llm.CompletionRequest{
			Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
			Context: &llm.Context{Tools: newDeferredToolStore("AskAnotherUser")},
		}

		result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
		require.NoError(t, err)

		events := collectToolCallEvents(t, result)
		require.Len(t, events, 1)
		assert.Equal(t, llm.ToolCallStatusPending, events[0].Status)
		assert.True(t, events[0].WouldAutoExecute)

		assert.Empty(t, result.ToolTurns)
		assert.Equal(t, 1, inner.callCount)
	})

	t.Run("mixed batch tags non-deferred pending without executing it", func(t *testing.T) {
		normalResolverCalled := false
		normalTool := llm.Tool{
			Name:        "normal_tool",
			Description: "normal test tool",
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				normalResolverCalled = true
				return "should_not_run", nil
			},
		}
		inner := &testLLM{responses: []testResponse{{events: []llm.TextStreamEvent{
			{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
				{ID: "ask1", Name: "AskAnotherUser", Arguments: json.RawMessage(`{"username":"bob","question":"Q?"}`)},
				{ID: "tc1", Name: "normal_tool", Arguments: json.RawMessage(`{}`)},
			}},
			{Type: llm.EventTypeEnd},
		}}}}
		recorder := &dispatchRecorder{}
		runner := New(inner, WithDeferredDispatcher(recorder.dispatch))
		request := llm.CompletionRequest{
			Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
			Context: &llm.Context{Tools: newDeferredToolStore("AskAnotherUser", normalTool)},
		}

		result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
		require.NoError(t, err)

		events := collectToolCallEvents(t, result)
		require.Len(t, events, 2)
		byID := map[string]llm.ToolCall{events[0].ID: events[0], events[1].ID: events[1]}
		assert.Equal(t, llm.ToolCallStatusWaiting, byID["ask1"].Status)
		assert.Equal(t, llm.ToolCallStatusPending, byID["tc1"].Status)
		assert.True(t, byID["tc1"].WouldAutoExecute)

		assert.False(t, normalResolverCalled, "non-deferred call must not execute this round")
		require.Len(t, recorder.calls, 1)
		assert.Empty(t, result.ToolTurns)
		assert.Equal(t, 1, inner.callCount)
	})

	t.Run("not-all-approved leaves deferred pending without dispatching", func(t *testing.T) {
		inner := &testLLM{responses: []testResponse{{events: deferredBatch}}}
		recorder := &dispatchRecorder{}
		runner := New(inner, WithDeferredDispatcher(recorder.dispatch))
		request := llm.CompletionRequest{
			Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
			Context: &llm.Context{Tools: newDeferredToolStore("AskAnotherUser")},
		}

		result, err := runner.Run(context.Background(), request, neverExecute, nil)
		require.NoError(t, err)

		events := collectToolCallEvents(t, result)
		require.Len(t, events, 1)
		assert.Equal(t, llm.ToolCallStatusPending, events[0].Status)
		assert.False(t, events[0].WouldAutoExecute)

		assert.Empty(t, recorder.calls, "dispatcher must not run under the ask policy")
		assert.Empty(t, result.ToolTurns)
		assert.Equal(t, 1, inner.callCount)
	})

	t.Run("all deferred fail with non-deferred present stops the run", func(t *testing.T) {
		normalTool := llm.Tool{
			Name:        "normal_tool",
			Description: "normal test tool",
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "should_not_run", nil
			},
		}
		inner := &testLLM{responses: []testResponse{{events: []llm.TextStreamEvent{
			{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
				{ID: "ask1", Name: "AskAnotherUser", Arguments: json.RawMessage(`{}`)},
				{ID: "ask2", Name: "AskAnotherUser", Arguments: json.RawMessage(`{}`)},
				{ID: "tc1", Name: "normal_tool", Arguments: json.RawMessage(`{}`)},
			}},
			{Type: llm.EventTypeEnd},
		}}}}
		recorder := &dispatchRecorder{err: fmt.Errorf("dispatch broken")}
		runner := New(inner, WithDeferredDispatcher(recorder.dispatch))
		request := llm.CompletionRequest{
			Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
			Context: &llm.Context{Tools: newDeferredToolStore("AskAnotherUser", normalTool)},
		}

		result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
		require.NoError(t, err)

		events := collectToolCallEvents(t, result)
		require.Len(t, events, 3)
		byID := map[string]llm.ToolCall{}
		for _, e := range events {
			byID[e.ID] = e
		}
		assert.Equal(t, llm.ToolCallStatusError, byID["ask1"].Status)
		assert.Equal(t, llm.ToolCallStatusError, byID["ask2"].Status)
		assert.Equal(t, llm.ToolCallStatusPending, byID["tc1"].Status)

		assert.Equal(t, 1, inner.callCount, "run must stop, not continue, when a non-deferred call is pending")
		assert.Empty(t, result.ToolTurns)
	})
}

func TestRunLoopDeferredDispatchFailureCountsTowardRetryLimit(t *testing.T) {
	deferredRound := testResponse{events: []llm.TextStreamEvent{
		{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
			{ID: "ask", Name: "AskAnotherUser", Arguments: json.RawMessage(`{}`)},
		}},
		{Type: llm.EventTypeEnd},
	}}

	responses := make([]testResponse, 0, llm.MaxConsecutiveToolCallFailures+1)
	for range llm.MaxConsecutiveToolCallFailures {
		responses = append(responses, deferredRound)
	}
	responses = append(responses, testResponse{events: []llm.TextStreamEvent{
		{Type: llm.EventTypeText, Value: "giving up"},
		{Type: llm.EventTypeEnd},
	}})

	var capturedOpts [][]llm.LanguageModelOption
	inner := &optCapturingLLM{
		inner:        &testLLM{responses: responses},
		capturedOpts: &capturedOpts,
	}
	recorder := &dispatchRecorder{err: fmt.Errorf("dispatch always fails")}
	runner := New(inner, WithDeferredDispatcher(recorder.dispatch))
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
		Context: &llm.Context{Tools: newDeferredToolStore("AskAnotherUser")},
	}

	result, err := runner.Run(context.Background(), request, alwaysExecute, nil)
	require.NoError(t, err)
	_, readErr := result.Stream.ReadAll()
	require.NoError(t, readErr)

	require.Len(t, result.ToolTurns, llm.MaxConsecutiveToolCallFailures)
	require.Len(t, capturedOpts, llm.MaxConsecutiveToolCallFailures+1)

	// The call after the limit is reached must have tools disabled.
	var finalCfg llm.LanguageModelConfig
	for _, opt := range capturedOpts[llm.MaxConsecutiveToolCallFailures] {
		opt(&finalCfg)
	}
	assert.True(t, finalCfg.ToolsDisabled, "tools must be disabled after consecutive dispatch failures")

	// The final request carries the retry-limit system message.
	finalReq := inner.inner.capturedRequests[llm.MaxConsecutiveToolCallFailures]
	found := false
	for _, post := range finalReq.Posts {
		if post.Role == llm.PostRoleSystem && strings.Contains(post.Message, llm.ToolRetryLimitSystemMessage) {
			found = true
		}
	}
	assert.True(t, found, "final request must include the retry-limit system message")
}
