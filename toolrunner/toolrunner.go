// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package toolrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
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

		// No tool calls = final response.
		if len(toolCalls) == 0 {
			r.deliverToolTurns(result, onToolTurns)
			output <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
			return
		}

		store := toolStoreFromRequest(request)
		if containsUnavailableTools(toolCalls, store) {
			output <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}

			toolResults := unavailableToolBatchResults(toolCalls, store, request.Context)
			resolvedToolCalls := appendToolTurnAndPost(result, &request, text.String(), reasoningData, toolCalls, toolResults, usage)

			output <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: resolvedToolCalls}

			if llm.CountTrailingFailedToolCalls(request.Posts) >= llm.MaxConsecutiveToolCallFailures {
				request.Posts = llm.EnsureToolRetryLimitSystemMessage(request.Posts)
				currentOpts = append(currentOpts, llm.WithToolsDisabled())
			}
			continue
		}

		toolCalls = enrichToolCallsForApproval(toolCalls, store)

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
		recordMCPDynamicSearchLoadCallSuccess(request.Context, toolCalls, toolResults)

		resolvedToolCalls := appendToolTurnAndPost(result, &request, text.String(), reasoningData, toolCalls, toolResults, usage)

		// Forward resolved tool calls so the UI can show success/error states.
		output <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: resolvedToolCalls}

		// Check for consecutive tool call failures and disable tools if needed.
		if llm.CountTrailingFailedToolCalls(request.Posts) >= llm.MaxConsecutiveToolCallFailures {
			request.Posts = llm.EnsureToolRetryLimitSystemMessage(request.Posts)
			currentOpts = append(currentOpts, llm.WithToolsDisabled())
		}
	}

	// Exhausted MaxToolRounds.
	r.deliverToolTurns(result, onToolTurns)
	output <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
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
		switch {
		case request.Context == nil || request.Context.Tools == nil:
			resolveErr = fmt.Errorf("no tool store available")
		case request.Context.Tools.IsUnloadedMCPTool(tc.Name):
			resolveErr = fmt.Errorf("%s", mcp.UnloadedMCPToolUserHint(tc.Name))
		case request.Context.Tools.GetTool(tc.Name) == nil:
			resolveErr = fmt.Errorf("unknown tool %s", tc.Name)
		default:
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

func toolStoreFromRequest(request llm.CompletionRequest) *llm.ToolStore {
	if request.Context == nil {
		return nil
	}
	return request.Context.Tools
}

func unavailableToolNames(toolCalls []llm.ToolCall, store *llm.ToolStore) []string {
	unavailable := make([]string, 0)
	for _, tc := range toolCalls {
		if store == nil || store.GetTool(tc.Name) == nil {
			unavailable = append(unavailable, tc.Name)
		}
	}
	return unavailable
}

func containsUnavailableTools(toolCalls []llm.ToolCall, store *llm.ToolStore) bool {
	for _, tc := range toolCalls {
		if store == nil || store.GetTool(tc.Name) == nil {
			return true
		}
	}
	return false
}

func unavailableToolBatchResults(toolCalls []llm.ToolCall, store *llm.ToolStore, llmContext *llm.Context) []ToolResult {
	unavailableNames := unavailableToolNames(toolCalls, store)
	unavailableSet := make(map[string]struct{}, len(unavailableNames))
	for _, name := range unavailableNames {
		unavailableSet[name] = struct{}{}
	}

	toolResults := make([]ToolResult, len(toolCalls))
	for i, tc := range toolCalls {
		if _, ok := unavailableSet[tc.Name]; ok {
			if store != nil && store.IsUnloadedMCPTool(tc.Name) {
				llmContext.ObserveMCPDynamicToolEvent("unloaded_tool_error", "error")
				toolResults[i] = ToolResult{
					ToolCallID: tc.ID,
					Name:       tc.Name,
					Result:     mcp.UnloadedMCPToolUserHint(tc.Name),
					IsError:    true,
				}
				continue
			}

			if store != nil {
				store.LogUnknownToolWarning(tc.Name, func(args any) error {
					return json.Unmarshal(tc.Arguments, args)
				})
			}
			toolResults[i] = ToolResult{
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Result:     "unknown tool " + tc.Name,
				IsError:    true,
			}
			continue
		}

		toolResults[i] = ToolResult{
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Result:     fmt.Sprintf("tool %s was not executed because the batch contained unavailable tool(s): %s", tc.Name, strings.Join(unavailableNames, ", ")),
			IsError:    true,
		}
	}
	return toolResults
}

func recordMCPDynamicSearchLoadCallSuccess(llmContext *llm.Context, toolCalls []llm.ToolCall, toolResults []ToolResult) {
	if llmContext == nil {
		return
	}
	for i, toolResult := range toolResults {
		if i >= len(toolCalls) || toolResult.IsError {
			continue
		}
		toolName := toolCalls[i].Name
		if mcp.IsMCPMetaTool(toolName) {
			continue
		}
		if llmContext.ShouldRecordMCPDynamicSearchLoadCallSuccess(toolName) {
			llmContext.ObserveMCPDynamicToolEvent("search_load_call_success", "success")
		}
	}
}

func enrichToolCallsForApproval(toolCalls []llm.ToolCall, store *llm.ToolStore) []llm.ToolCall {
	enriched := make([]llm.ToolCall, len(toolCalls))
	copy(enriched, toolCalls)
	if store == nil {
		return enriched
	}

	for i := range enriched {
		tool := store.GetTool(enriched[i].Name)
		if tool == nil {
			continue
		}
		if enriched[i].Description == "" {
			enriched[i].Description = tool.Description
		}
		if enriched[i].ServerOrigin == "" {
			enriched[i].ServerOrigin = tool.ServerOrigin
		}
		enriched[i].Schema = tool.Schema
		if enriched[i].ServerOrigin != "" {
			enriched[i].MCPBareName = llm.BareMCPToolName(enriched[i].Name)
		}
	}
	return enriched
}

func appendToolTurnAndPost(
	result *ToolRunResult,
	request *llm.CompletionRequest,
	text string,
	reasoningData llm.ReasoningData,
	toolCalls []llm.ToolCall,
	toolResults []ToolResult,
	usage llm.TokenUsage,
) []llm.ToolCall {
	// Build resolved tool calls with post-execution status
	// (AutoApproved / Error). These flow into the ToolTurn so downstream
	// persistence (WriteToolTurns → toolUseBlocks) can read the resolved
	// status directly from tc.Status instead of inferring it from the
	// fact that only the auto-execute path calls this function.
	resolvedToolCalls := buildResolvedToolCalls(toolCalls, toolResults)

	turn := ToolTurn{
		AssistantMessage:   text,
		AssistantToolCalls: resolvedToolCalls,
		AssistantReasoning: reasoningData,
		ToolResults:        toolResults,
		TokensIn:           usage.InputTokens,
		TokensOut:          usage.OutputTokens,
	}
	result.ToolTurns = append(result.ToolTurns, turn)

	request.Posts = append(request.Posts, llm.Post{
		Role:               llm.PostRoleBot,
		Message:            text,
		ToolUse:            resolvedToolCalls,
		Reasoning:          reasoningData.Text,
		ReasoningSignature: reasoningData.Signature,
	})

	return resolvedToolCalls
}

// buildResolvedToolCalls creates resolved ToolCall entries from executed results.
func buildResolvedToolCalls(toolCalls []llm.ToolCall, toolResults []ToolResult) []llm.ToolCall {
	resolved := make([]llm.ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		resolved[i] = llm.ToolCall{
			ID:           tc.ID,
			Name:         tc.Name,
			Description:  tc.Description,
			Arguments:    tc.Arguments,
			Schema:       tc.Schema,
			ServerOrigin: tc.ServerOrigin,
			MCPBareName:  tc.MCPBareName,
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
