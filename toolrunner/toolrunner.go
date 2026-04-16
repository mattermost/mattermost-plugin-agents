// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package toolrunner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// MaxToolRounds is the maximum number of tool-call-execute-recall iterations
// before the runner gives up and returns whatever it has. This prevents
// infinite loops from models that keep requesting tools.
const MaxToolRounds = 10

// ToolRunner manages the call-execute-recall loop for LLM tool use.
// It calls the LLM, checks for tool calls in the stream, executes
// approved ones, appends results back to the request, and calls again.
type ToolRunner struct {
	llm llm.LanguageModel
}

// New creates a ToolRunner bound to the given language model.
func New(lm llm.LanguageModel) *ToolRunner {
	return &ToolRunner{llm: lm}
}

// ToolRunResult is the return value of Run(). It contains the final
// stream (no more tool calls) and all intermediate tool rounds.
type ToolRunResult struct {
	// Stream is the final LLM response stream. The caller should
	// consume this stream (e.g. via StreamToPost). It contains text,
	// reasoning, annotations, usage events -- but no tool calls.
	// If the runner stopped because shouldExecute returned false,
	// this stream DOES contain the unresolved tool calls.
	Stream *llm.TextStreamResult

	// ToolTurns records each intermediate tool round that was executed.
	// Empty if the LLM returned text without any tool calls, or if
	// shouldExecute returned false on the first round.
	ToolTurns []ToolTurn
}

// ToolTurn represents one round of tool execution. Each round
// corresponds to one LLM call that returned tool_use blocks,
// followed by tool execution and the resulting tool_result blocks.
type ToolTurn struct {
	// AssistantMessage is the accumulated text from the assistant response
	// that contained tool calls.
	AssistantMessage string

	// AssistantToolCalls holds the tool calls from the assistant response.
	AssistantToolCalls []llm.ToolCall

	// AssistantReasoning holds the reasoning data from the assistant response.
	AssistantReasoning llm.ReasoningData

	// ToolResults holds the executed tool results, one per tool call.
	// Includes both successful and errored results.
	ToolResults []ToolResult

	// TokensIn and TokensOut are the token counts for the LLM call
	// that produced this round's assistant response.
	TokensIn  int64
	TokensOut int64
}

// ToolResult holds the result of executing a single tool call.
type ToolResult struct {
	ToolCallID string
	Name       string
	Result     string
	IsError    bool
}

// streamAccumulator consumes a TextStreamResult and records all events.
type streamAccumulator struct {
	events        []llm.TextStreamEvent
	text          strings.Builder
	reasoning     strings.Builder
	reasoningData llm.ReasoningData
	toolCalls     []llm.ToolCall
	usage         llm.TokenUsage
	hasUsage      bool
	err           error
}

func (a *streamAccumulator) consume(stream *llm.TextStreamResult) {
	for event := range stream.Stream {
		a.events = append(a.events, event)
		switch event.Type {
		case llm.EventTypeText:
			if text, ok := event.Value.(string); ok {
				a.text.WriteString(text)
			}
		case llm.EventTypeReasoning:
			if text, ok := event.Value.(string); ok {
				a.reasoning.WriteString(text)
			}
		case llm.EventTypeReasoningEnd:
			if data, ok := event.Value.(llm.ReasoningData); ok {
				a.reasoningData = data
			}
		case llm.EventTypeToolCalls:
			if tcs, ok := event.Value.([]llm.ToolCall); ok {
				a.toolCalls = append(a.toolCalls, tcs...)
			}
		case llm.EventTypeUsage:
			if usage, ok := event.Value.(llm.TokenUsage); ok {
				a.usage.InputTokens += usage.InputTokens
				a.usage.OutputTokens += usage.OutputTokens
				a.hasUsage = true
			}
		case llm.EventTypeError:
			if errVal, ok := event.Value.(error); ok {
				a.err = errVal
			}
		case llm.EventTypeEnd:
			// Stream complete, nothing to do.
		case llm.EventTypeAnnotations:
			// Recorded in events slice, no special handling.
		}
	}
}

func (a *streamAccumulator) hasToolCalls() bool {
	return len(a.toolCalls) > 0
}

func (a *streamAccumulator) toStream() *llm.TextStreamResult {
	ch := make(chan llm.TextStreamEvent, len(a.events)+1)
	go func() {
		defer close(ch)
		for _, event := range a.events {
			ch <- event
		}
	}()
	return &llm.TextStreamResult{Stream: ch}
}

// Run calls the LLM and handles tool execution in a loop.
//
// Parameters:
//   - request: The CompletionRequest to send to the LLM. The request's
//     Context.Tools must contain the ToolStore with available tools.
//   - shouldExecute: Called for each tool call to decide whether to
//     auto-execute it. If ANY tool call in a batch returns false,
//     the entire batch is left unresolved and the runner returns.
//   - opts: Additional LanguageModelOption values (e.g. WithReasoningDisabled).
//     The runner does NOT add WithAutoRunTools -- that option is being
//     replaced by this runner.
//
// Returns:
//   - *ToolRunResult with the final stream and intermediate tool turns.
//   - error if the LLM call itself fails or the stream produces an error event.
func (r *ToolRunner) Run(
	request llm.CompletionRequest,
	shouldExecute func(llm.ToolCall) bool,
	opts ...llm.LanguageModelOption,
) (*ToolRunResult, error) {
	var toolTurns []ToolTurn
	currentOpts := append([]llm.LanguageModelOption(nil), opts...)

	for round := 0; round < MaxToolRounds; round++ {
		// Step 1: Call LLM.
		stream, err := r.llm.ChatCompletion(request, currentOpts...)
		if err != nil {
			return nil, fmt.Errorf("llm completion failed: %w", err)
		}

		// Step 2: Consume the entire stream.
		acc := &streamAccumulator{}
		acc.consume(stream)

		if acc.err != nil {
			return &ToolRunResult{
				Stream:    acc.toStream(),
				ToolTurns: toolTurns,
			}, acc.err
		}

		// Step 3: If no tool calls, this is the final response.
		if !acc.hasToolCalls() {
			return &ToolRunResult{
				Stream:    acc.toStream(),
				ToolTurns: toolTurns,
			}, nil
		}

		// Step 4: Check shouldExecute for ALL tool calls.
		allApproved := true
		for _, tc := range acc.toolCalls {
			if !shouldExecute(tc) {
				allApproved = false
				break
			}
		}

		// Step 5: If NOT all approved, return with unresolved tool calls.
		if !allApproved {
			return &ToolRunResult{
				Stream:    acc.toStream(),
				ToolTurns: toolTurns,
			}, nil
		}

		// Step 6: Execute each tool call.
		toolResults := make([]ToolResult, len(acc.toolCalls))
		for i, tc := range acc.toolCalls {
			var result string
			var resolveErr error
			if request.Context != nil && request.Context.Tools != nil {
				result, resolveErr = request.Context.Tools.ResolveTool(
					tc.Name,
					func(args any) error { return json.Unmarshal(tc.Arguments, args) },
					request.Context,
				)
			} else {
				resolveErr = fmt.Errorf("no tool store available")
			}

			if resolveErr != nil {
				toolResults[i] = ToolResult{
					ToolCallID: tc.ID,
					Name:       tc.Name,
					Result:     resolveErr.Error(),
					IsError:    true,
				}
			} else {
				toolResults[i] = ToolResult{
					ToolCallID: tc.ID,
					Name:       tc.Name,
					Result:     result,
					IsError:    false,
				}
			}
		}

		// Step 7: Build the ToolTurn for this round.
		turn := ToolTurn{
			AssistantMessage:   acc.text.String(),
			AssistantToolCalls: acc.toolCalls,
			AssistantReasoning: acc.reasoningData,
			ToolResults:        toolResults,
			TokensIn:           acc.usage.InputTokens,
			TokensOut:          acc.usage.OutputTokens,
		}
		toolTurns = append(toolTurns, turn)

		// Step 8: Build resolved tool calls and append bot post to request.
		resolvedToolCalls := make([]llm.ToolCall, len(acc.toolCalls))
		for i, tc := range acc.toolCalls {
			resolvedToolCalls[i] = llm.ToolCall{
				ID:           tc.ID,
				Name:         tc.Name,
				Arguments:    tc.Arguments,
				ServerOrigin: tc.ServerOrigin,
			}
			if toolResults[i].IsError {
				resolvedToolCalls[i].Status = llm.ToolCallStatusError
				resolvedToolCalls[i].Result = toolResults[i].Result
			} else {
				resolvedToolCalls[i].Status = llm.ToolCallStatusSuccess
				resolvedToolCalls[i].Result = toolResults[i].Result
			}
		}

		request.Posts = append(request.Posts, llm.Post{
			Role:               llm.PostRoleBot,
			Message:            acc.text.String(),
			ToolUse:            resolvedToolCalls,
			Reasoning:          acc.reasoningData.Text,
			ReasoningSignature: acc.reasoningData.Signature,
		})

		// Step 9: Check for consecutive tool call failures and disable tools if needed.
		if llm.CountTrailingFailedToolCalls(request.Posts) >= llm.MaxConsecutiveToolCallFailures {
			request.Posts = llm.EnsureToolRetryLimitSystemMessage(request.Posts)
			currentOpts = append(currentOpts, llm.WithToolsDisabled())
		}

		// Step 10: Loop back to step 1 with the updated request.
	}

	// Exhausted MaxToolRounds: return an end-of-stream and all accumulated tool turns.
	ch := make(chan llm.TextStreamEvent, 1)
	ch <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	close(ch)

	return &ToolRunResult{
		Stream:    &llm.TextStreamResult{Stream: ch},
		ToolTurns: toolTurns,
	}, nil
}
