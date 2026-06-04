// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package toolrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/telemetry"
	"go.opentelemetry.io/otel/trace"
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

// finalAssistantText drops preamble text from a forced synthesis round that
// still emitted (and had dropped) tool calls.
func finalAssistantText(text string, synthesisForced bool, droppedToolCalls int) string {
	if synthesisForced && droppedToolCalls > 0 {
		return ""
	}
	return text
}

// ToolRunResult is the return value of Run(). It contains the final
// stream (no more tool calls) and all intermediate tool rounds.
type ToolRunResult struct {
	// Stream is the live LLM response stream. Events are forwarded
	// in real-time from the LLM, enabling token-by-token streaming.
	// The caller should consume this stream (e.g. via StreamToPost).
	// If the runner stopped because shouldExecute returned false,
	// this stream DOES contain the unresolved tool calls.
	Stream *llm.TextStreamResult

	// ToolTurns records each intermediate tool round that was executed.
	// Empty if the LLM returned text without any tool calls, or if
	// shouldExecute returned false on the first round.
	//
	// NOTE: ToolTurns is populated asynchronously by the streaming
	// goroutine. It is safe to read after the Stream has been fully
	// consumed (channel happens-before guarantees this).
	ToolTurns []ToolTurn

	// FinalText is the assistant text from the round where the loop exited
	// with no tool calls. Empty on stream error, unresolved tool calls, or a
	// failed forced synthesis. Read after Stream is fully consumed.
	FinalText string
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

// Run calls the LLM and handles tool execution in a loop.
//
// Events (text, reasoning, annotations, etc.) are forwarded in real-time
// to the returned stream, enabling token-by-token streaming to the client.
// Tool call events are buffered internally to detect and execute tools.
//
// Parameters:
//   - request: The CompletionRequest to send to the LLM. The request's
//     Context.Tools must contain the ToolStore with available tools.
//   - shouldExecute: Called for each tool call to decide whether to
//     auto-execute it. If ANY tool call in a batch returns false,
//     the entire batch is left unresolved and the runner returns.
//   - onToolTurns: Optional callback invoked with accumulated tool turns
//     after all intermediate tool rounds complete, before the final text
//     response starts streaming. May be nil.
//   - opts: Additional LanguageModelOption values (e.g. WithReasoningDisabled).
//
// Returns:
//   - *ToolRunResult with the live stream and (asynchronously populated) tool turns.
//   - error if the initial LLM call fails. Errors from subsequent LLM calls
//     (after tool execution) are delivered through the stream as EventTypeError.
func (r *ToolRunner) Run(
	ctx context.Context,
	request llm.CompletionRequest,
	shouldExecute func(llm.ToolCall) bool,
	onToolTurns func([]ToolTurn),
	opts ...llm.LanguageModelOption,
) (*ToolRunResult, error) {
	currentOpts := append([]llm.LanguageModelOption(nil), opts...)

	// Make the first LLM call synchronously so initialization errors
	// (auth failures, rate limits, etc.) are returned directly.
	firstStream, err := r.llm.ChatCompletion(ctx, request, currentOpts...)
	if err != nil {
		return nil, fmt.Errorf("llm completion failed: %w", err)
	}

	output := make(chan llm.TextStreamEvent)
	result := &ToolRunResult{
		Stream: &llm.TextStreamResult{Stream: output},
	}

	go func() {
		defer close(output)
		r.runLoop(ctx, firstStream, request, shouldExecute, onToolTurns, result, output, currentOpts)
	}()

	return result, nil
}

// runLoop processes the tool execution loop in a goroutine.
// It forwards events to the output channel in real-time while handling
// tool call detection and execution internally.
func (r *ToolRunner) runLoop(
	ctx context.Context,
	firstStream *llm.TextStreamResult,
	request llm.CompletionRequest,
	shouldExecute func(llm.ToolCall) bool,
	onToolTurns func([]ToolTurn),
	result *ToolRunResult,
	output chan<- llm.TextStreamEvent,
	currentOpts []llm.LanguageModelOption,
) {
	stream := firstStream

	var synthesisForced bool

	for round := 0; round < MaxToolRounds; round++ {
		// For round > 0, make a new LLM call.
		if round > 0 {
			var err error
			stream, err = r.llm.ChatCompletion(ctx, request, currentOpts...)
			if err != nil {
				r.deliverToolTurns(result, onToolTurns)
				output <- llm.TextStreamEvent{
					Type:  llm.EventTypeError,
					Value: fmt.Errorf("llm completion failed: %w", err),
				}
				return
			}
		}

		// Consume the stream, forwarding non-tool-call events in real-time.
		var text strings.Builder
		var reasoning strings.Builder
		var reasoningData llm.ReasoningData
		var toolCalls []llm.ToolCall
		var usage llm.TokenUsage
		var streamErr error

		for event := range stream.Stream {
			switch event.Type {
			case llm.EventTypeToolCalls:
				if tcs, ok := event.Value.([]llm.ToolCall); ok {
					toolCalls = append(toolCalls, tcs...)
				}
			case llm.EventTypeEnd:
				// Don't forward yet — handle after consuming the full stream.
			case llm.EventTypeText:
				if t, ok := event.Value.(string); ok {
					text.WriteString(t)
				}
				output <- event
			case llm.EventTypeReasoning:
				if t, ok := event.Value.(string); ok {
					reasoning.WriteString(t)
				}
				output <- event
			case llm.EventTypeReasoningEnd:
				if data, ok := event.Value.(llm.ReasoningData); ok {
					reasoningData = data
				}
				output <- event
			case llm.EventTypeUsage:
				if u, ok := event.Value.(llm.TokenUsage); ok {
					usage.InputTokens += u.InputTokens
					usage.OutputTokens += u.OutputTokens
				}
				output <- event
			case llm.EventTypeError:
				if e, ok := event.Value.(error); ok {
					streamErr = e
				}
				output <- event
			default:
				output <- event // annotations, etc.
			}
		}

		if streamErr != nil {
			r.deliverToolTurns(result, onToolTurns)
			return
		}

		// Drop any tool calls the model returned on a forced synthesis round;
		droppedToolCalls := 0
		if synthesisForced && len(toolCalls) > 0 {
			droppedToolCalls = len(toolCalls)
			toolCalls = nil
		}

		// No tool calls = final response.
		if len(toolCalls) == 0 {
			result.FinalText = finalAssistantText(text.String(), synthesisForced, droppedToolCalls)
			r.deliverToolTurns(result, onToolTurns)
			output <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
			return
		}

		// Check shouldExecute for ALL tool calls.
		allApproved := true
		for _, tc := range toolCalls {
			if !shouldExecute(tc) {
				allApproved = false
				break
			}
		}

		// If NOT all approved, return with unresolved tool calls.
		if !allApproved {
			r.deliverToolTurns(result, onToolTurns)
			output <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
			output <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
			return
		}

		// Forward pending tool calls so the UI can show spinners.
		output <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}

		// Execute each tool call.
		toolResults := r.executeTools(ctx, toolCalls, request)

		// Build resolved tool calls with post-execution status
		// (AutoApproved / Error). These flow into the ToolTurn so downstream
		// persistence (WriteToolTurns → toolUseBlocks) can read the resolved
		// status directly from tc.Status instead of inferring it from the
		// fact that only the auto-execute path calls this function.
		resolvedToolCalls := buildResolvedToolCalls(toolCalls, toolResults)

		// Build the ToolTurn for this round.
		turn := ToolTurn{
			AssistantMessage:   text.String(),
			AssistantToolCalls: resolvedToolCalls,
			AssistantReasoning: reasoningData,
			ToolResults:        toolResults,
			TokensIn:           usage.InputTokens,
			TokensOut:          usage.OutputTokens,
		}
		result.ToolTurns = append(result.ToolTurns, turn)

		// Forward resolved tool calls so the UI can show success/error states.
		output <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: resolvedToolCalls}

		request.Posts = append(request.Posts, llm.Post{
			Role:               llm.PostRoleBot,
			Message:            text.String(),
			ToolUse:            resolvedToolCalls,
			Reasoning:          reasoningData.Text,
			ReasoningSignature: reasoningData.Signature,
		})

		// Check for consecutive tool call failures and disable tools if needed.
		if llm.CountTrailingFailedToolCalls(request.Posts) >= llm.MaxConsecutiveToolCallFailures {
			request.Posts = llm.EnsureToolRetryLimitSystemMessage(request.Posts)
			currentOpts = append(currentOpts, llm.WithToolsDisabled())
		}

		// Force the last allowed round to be a tools-disabled synthesis so the
		// caller always gets a final answer when the cap is hit.
		if round == MaxToolRounds-2 {
			request.Posts = llm.EnsureToolIterationLimitUserMessage(request.Posts)
			currentOpts = append(currentOpts, llm.WithToolsDisabled())
			synthesisForced = true
		}
	}
}

// deliverToolTurns calls the onToolTurns callback if there are accumulated turns.
func (r *ToolRunner) deliverToolTurns(result *ToolRunResult, onToolTurns func([]ToolTurn)) {
	if onToolTurns != nil && len(result.ToolTurns) > 0 {
		onToolTurns(result.ToolTurns)
	}
}

// executeTools runs each tool call and returns results.
func (r *ToolRunner) executeTools(ctx context.Context, toolCalls []llm.ToolCall, request llm.CompletionRequest) []ToolResult {
	toolResults := make([]ToolResult, len(toolCalls))
	for i, tc := range toolCalls {
		var result string
		var resolveErr error
		if request.Context != nil && request.Context.Tools != nil {
			toolCtx, span := telemetry.Tracer().Start(ctx, "resolve tool",
				trace.WithAttributes(
					telemetry.ToolName.String(tc.Name),
					telemetry.ToolID.String(tc.ID),
				),
			)
			result, resolveErr = request.Context.Tools.ResolveTool(
				toolCtx,
				tc.Name,
				func(args any) error { return json.Unmarshal(tc.Arguments, args) },
				request.Context,
			)
			if resolveErr != nil {
				span.SetAttributes(telemetry.ToolStatus.String("error"))
			} else {
				span.SetAttributes(telemetry.ToolStatus.String("success"))
			}
			span.End()
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
	return toolResults
}

// buildResolvedToolCalls creates resolved ToolCall entries from executed results.
func buildResolvedToolCalls(toolCalls []llm.ToolCall, toolResults []ToolResult) []llm.ToolCall {
	resolved := make([]llm.ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		resolved[i] = llm.ToolCall{
			ID:           tc.ID,
			Name:         tc.Name,
			Arguments:    tc.Arguments,
			ServerOrigin: tc.ServerOrigin,
		}
		if toolResults[i].IsError {
			resolved[i].Status = llm.ToolCallStatusError
			resolved[i].Result = toolResults[i].Result
		} else {
			resolved[i].Status = llm.ToolCallStatusAutoApproved
			resolved[i].Result = toolResults[i].Result
		}
	}
	return resolved
}
