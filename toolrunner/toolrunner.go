// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package toolrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner/limits"
)

// MaxToolRounds is the maximum number of tool-call-execute-recall iterations
// before the runner gives up and returns whatever it has. This prevents
// infinite loops from models that keep requesting tools.
const MaxToolRounds = limits.MaxToolRounds

// ToolRunner manages the call-execute-recall loop for LLM tool use.
// It calls the LLM, checks for tool calls in the stream, executes
// approved ones, appends results back to the request, and calls again.
type ToolRunner struct {
	llm         llm.LanguageModel
	maxRounds   int
	errorResult func(*llm.Context, error) string
}

// Option configures a ToolRunner at construction time.
type Option func(*ToolRunner)

// WithMaxRounds overrides the default maximum number of tool rounds (LLM
// calls whose tool calls are executed or rejected). One more LLM call, with
// tools disabled, follows the last tool round so the run always ends with an
// answer. Non-positive values fall back to llm.DefaultMaxToolTurns.
func WithMaxRounds(n int) Option {
	return func(r *ToolRunner) {
		if n > 0 {
			r.maxRounds = n
		}
	}
}

// WithToolErrorResult sets how a failed tool call's error becomes its result
// text. It runs on the loop's goroutine between tool calls, so it may change
// the request context's tool store for the rest of the run. The default is
// the error's text.
func WithToolErrorResult(f func(*llm.Context, error) string) Option {
	return func(r *ToolRunner) {
		r.errorResult = f
	}
}

// New creates a ToolRunner bound to the given language model. Pass
// WithMaxRounds to override the default per-agent tool-call ceiling.
func New(lm llm.LanguageModel, opts ...Option) *ToolRunner {
	r := &ToolRunner{llm: lm, maxRounds: llm.DefaultMaxToolTurns}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// finalAssistantText drops preamble text from a tools-disabled round that
// still emitted (and had dropped) tool calls.
func finalAssistantText(text string, toolsDisabled bool, droppedToolCalls int) string {
	if toolsDisabled && droppedToolCalls > 0 {
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

	// AssistantServerTools is the final cumulative snapshot. Without it, a
	// round that mixed server tools with client tool calls would lose activity.
	AssistantServerTools []llm.ServerToolUse

	// AssistantSegments is arrival order. Without it, narration between two
	// sandbox runs collapses above both of them.
	AssistantSegments []llm.TurnSegment

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
	firstStream, err := r.llm.ChatCompletion(telemetry.WithLLMRound(ctx, 0), request, currentOpts...)
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

	// send forwards an event unless ctx is canceled: once the consumer has
	// stopped reading (a stopped reply), a plain send would block forever.
	send := func(event llm.TextStreamEvent) bool {
		select {
		case output <- event:
			return true
		case <-ctx.Done():
			return false
		}
	}
	// abandon persists the rounds completed so far and stops the loop.
	abandon := func() { r.deliverToolTurns(result, onToolTurns) }

	// toolsDisabled means the next call must answer without tools: the
	// tool-round cap or the consecutive-failure limit was reached.
	var toolsDisabled bool
	disableTools := func() {
		if !toolsDisabled {
			currentOpts = append(currentOpts, llm.WithToolsDisabled())
			toolsDisabled = true
		}
	}
	// finishToolRound runs after every tool round, executed or rejected, so a
	// tools-off answer always follows the last one.
	finishToolRound := func(round int) {
		if llm.CountTrailingFailedToolCalls(request.Posts) >= llm.MaxConsecutiveToolCallFailures {
			request.Posts = llm.EnsureToolRetryLimitSystemMessage(request.Posts)
			disableTools()
		}
		if round+1 >= r.maxRounds {
			request.Posts = llm.EnsureToolIterationLimitUserMessage(request.Posts)
			disableTools()
		}
	}

	for round := 0; round <= r.maxRounds; round++ {
		// For round > 0, make a new LLM call.
		if round > 0 {
			var err error
			stream, err = r.llm.ChatCompletion(telemetry.WithLLMRound(ctx, round), request, currentOpts...)
			if err != nil {
				r.deliverToolTurns(result, onToolTurns)
				send(llm.TextStreamEvent{
					Type:  llm.EventTypeError,
					Value: fmt.Errorf("llm completion failed: %w", err),
				})
				return
			}
		}

		// Consume the stream, forwarding non-tool-call events in real-time.
		resp := drainStream(stream, send, request.Context)

		if ctx.Err() != nil {
			abandon()
			return
		}
		if resp.err != nil {
			r.deliverToolTurns(result, onToolTurns)
			return
		}

		toolCalls := resp.toolCalls

		// Providers may ignore tool_choice="none"; never execute those calls.
		droppedToolCalls := 0
		if toolsDisabled && len(toolCalls) > 0 {
			droppedToolCalls = len(toolCalls)
			toolCalls = nil
		}

		// No tool calls = final response.
		if len(toolCalls) == 0 {
			result.FinalText = finalAssistantText(resp.text, toolsDisabled, droppedToolCalls)
			r.deliverToolTurns(result, onToolTurns)
			send(llm.TextStreamEvent{Type: llm.EventTypeEnd})
			return
		}

		store := toolStoreFromRequest(request)
		if toolResults := rejectedToolBatchResults(toolCalls, store, request.Context); toolResults != nil {
			resolvedToolCalls := buildResolvedToolCalls(toolCalls, toolResults)
			appendToolTurnAndPost(result, &request, resp.text, resp.reasoningData, resp.serverTools, resp.segments, resolvedToolCalls, toolResults, resp.usage)

			if !send(toolCallBatchEvent(llm.ToolCallPhaseResolved, resolvedToolCalls)) {
				abandon()
				return
			}

			finishToolRound(round)
			continue
		}

		toolCalls = enrichToolCallsForApproval(toolCalls, store)

		// Check shouldExecute for ALL tool calls. A user-interaction call is
		// answered by the user, so no policy can auto-execute it.
		allApproved := true
		approved := make([]bool, len(toolCalls))
		for i, tc := range toolCalls {
			approved[i] = shouldExecute(tc) && tc.UserInteraction == ""
			if !approved[i] {
				allApproved = false
			}
		}

		// If NOT all approved, return with unresolved tool calls. Calls that
		// passed the policy are tagged so the UI hides their approval
		// controls; they execute on resume (HandleToolCall re-checks the
		// policy) once the user resolves the rest of the batch.
		if !allApproved {
			for i := range toolCalls {
				toolCalls[i].WouldAutoExecute = approved[i]
			}
			r.deliverToolTurns(result, onToolTurns)
			if send(toolCallBatchEvent(llm.ToolCallPhaseAwaitingDecision, toolCalls)) {
				send(llm.TextStreamEvent{Type: llm.EventTypeEnd})
			}
			return
		}

		// All calls passed the policy. The marker keeps persisted blocks from
		// implying an approval request if the stream is interrupted
		// mid-execution.
		for i := range toolCalls {
			toolCalls[i].WouldAutoExecute = true
		}
		if !send(toolCallBatchEvent(llm.ToolCallPhaseExecuting, toolCalls)) {
			abandon()
			return
		}

		// Execute each tool call.
		toolResults := r.executeTools(ctx, toolCalls, request)
		if ctx.Err() != nil {
			// Stopped mid-round: the tools' "context canceled" errors are
			// not real failures, so drop this round rather than persist them
			// as tool results the next turn would read as broken tools.
			abandon()
			return
		}
		recordMCPDynamicSearchLoadCallSuccess(request.Context, toolCalls, toolResults)

		resolvedToolCalls := buildResolvedToolCalls(toolCalls, toolResults)
		appendToolTurnAndPost(result, &request, resp.text, resp.reasoningData, resp.serverTools, resp.segments, resolvedToolCalls, toolResults, resp.usage)

		// Forward resolved tool calls so the UI can show success/error states.
		if !send(toolCallBatchEvent(llm.ToolCallPhaseResolved, resolvedToolCalls)) {
			abandon()
			return
		}

		finishToolRound(round)
	}

	// Unreachable while every tool round ends in finishToolRound; kept so a
	// future path can't close the stream without persisting turns or ending.
	r.deliverToolTurns(result, onToolTurns)
	send(llm.TextStreamEvent{
		Type:  llm.EventTypeError,
		Value: fmt.Errorf("tool loop stopped after %d rounds without a final response", r.maxRounds+1),
	})
}

// toolCallBatchEvent copies calls because consumers sanitize them in place
// while the runner is still executing and replaying the originals.
func toolCallBatchEvent(phase llm.ToolCallPhase, calls []llm.ToolCall) llm.TextStreamEvent {
	return llm.TextStreamEvent{Type: llm.EventTypeToolCallBatch, Value: llm.ToolCallBatch{Phase: phase, Calls: slices.Clone(calls)}}
}

// assistantResponse holds the accumulated contents of one fully consumed
// LLM response stream.
type assistantResponse struct {
	text          string
	reasoningData llm.ReasoningData
	toolCalls     []llm.ToolCall
	serverTools   []llm.ServerToolUse
	// segments is the arrival order of text, reasoning, and server tool
	// activity. Without it, narration between two sandbox runs collapses
	// above both of them.
	segments []llm.TurnSegment
	usage    llm.TokenUsage
	err      error
}

// drainStream consumes one LLM response stream, forwarding non-tool-call
// events to output in real-time while buffering tool calls and accumulating
// the round's text, reasoning, server tool activity, and usage. Sandbox files
// reported by server tools are registered on llmCtx as they arrive.
//
// Once send fails (the consumer is gone) the rest of the stream is still
// read so the provider's goroutine can finish, but nothing more is forwarded.
func drainStream(stream *llm.TextStreamResult, send func(llm.TextStreamEvent) bool, llmCtx *llm.Context) assistantResponse {
	var sequence llm.TurnSequence
	var resp assistantResponse

	for event := range stream.Stream {
		switch event.Type {
		case llm.EventTypeToolCalls:
			if tcs, ok := event.Value.([]llm.ToolCall); ok {
				resp.toolCalls = append(resp.toolCalls, tcs...)
			}
		case llm.EventTypeServerToolUse:
			// Cumulative snapshot of provider-executed tool activity;
			// keep the latest so a tool round persists it (see ToolTurn).
			if uses, ok := event.Value.([]llm.ServerToolUse); ok {
				resp.serverTools = uses
				sequence.RecordServerTools(uses)
				// Register files before presentation sanitation; downloads need exact ids and fallback routes.
				for _, use := range uses {
					refs := make([]llm.ProviderFileReference, 0, len(use.FileIDs))
					for _, id := range use.FileIDs {
						refs = append(refs, llm.ProviderFileReference{
							ID:            id,
							ProviderRoute: use.ProviderRoute,
						})
					}
					llmCtx.AddSandboxFiles(refs...)
				}
			}
			send(event)
		case llm.EventTypeEnd:
			// Don't forward yet — handle after consuming the full stream.
		case llm.EventTypeText:
			if t, ok := event.Value.(string); ok {
				sequence.AppendText(t)
			}
			send(event)
		case llm.EventTypeReasoning:
			if t, ok := event.Value.(string); ok {
				sequence.AppendReasoning(t)
			}
			send(event)
		case llm.EventTypeReasoningEnd:
			if data, ok := event.Value.(llm.ReasoningData); ok {
				resp.reasoningData = data
				sequence.FinishReasoning(data)
			}
			send(event)
		case llm.EventTypeUsage:
			if u, ok := event.Value.(llm.TokenUsage); ok {
				resp.usage.InputTokens += u.InputTokens
				resp.usage.OutputTokens += u.OutputTokens
			}
			send(event)
		case llm.EventTypeError:
			if e, ok := event.Value.(error); ok {
				resp.err = e
			}
			send(event)
		default:
			send(event) // annotations, etc.
		}
	}

	resp.text = sequence.Text()
	resp.segments = sequence.Segments()
	return resp
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
		default:
			lookup, ok := request.Context.Tools.LookupTool(tc.Name, tc.ServerOrigin)
			if !ok {
				resolveErr = fmt.Errorf("unknown tool %s", tc.Name)
				break
			}
			// executeTools only runs batches shouldExecute approved
			// without asking the user.
			result, resolveErr = request.Context.Tools.ResolveToolCall(
				ctx,
				lookup.RuntimeName,
				llm.ToolCallInfo{ID: tc.ID, Execution: telemetry.ToolExecutionAuto},
				func(args any) error { return json.Unmarshal(tc.Arguments, args) },
				request.Context,
			)
		}

		if resolveErr != nil {
			result = resolveErr.Error()
			if r.errorResult != nil {
				result = r.errorResult(request.Context, resolveErr)
			}
		}
		toolResults[i] = ToolResult{
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Result:     result,
			IsError:    resolveErr != nil,
		}
	}
	return toolResults
}

// rejectToolCall reports why a call cannot proceed: its tool is unavailable,
// or the tool's ValidateArguments hook rejects the arguments.
func rejectToolCall(tc llm.ToolCall, store *llm.ToolStore, llmContext *llm.Context) (string, bool) {
	if store == nil {
		return "unknown tool " + tc.Name, true
	}
	lookup, ok := store.LookupTool(tc.Name, tc.ServerOrigin)
	if !ok {
		if store.IsUnloadedMCPTool(tc.Name) {
			llmContext.ObserveMCPDynamicToolEvent("unloaded_tool_error", "error")
			return mcp.UnloadedMCPToolUserHint(tc.Name), true
		}
		return "unknown tool " + tc.Name, true
	}
	if lookup.Tool.ValidateArguments != nil {
		if err := lookup.Tool.ValidateArguments(tc.Arguments); err != nil {
			return fmt.Sprintf("invalid arguments for tool %s: %s. Call the tool again with arguments matching its schema.", tc.Name, err), true
		}
	}
	return "", false
}

// rejectedToolBatchResults returns results for the whole batch when any call is
// rejected: rejected calls fail with the reason and the rest are skipped, so
// the model retries the batch. Returns nil when every call can proceed.
func rejectedToolBatchResults(toolCalls []llm.ToolCall, store *llm.ToolStore, llmContext *llm.Context) []ToolResult {
	toolResults := make([]ToolResult, len(toolCalls))
	rejected := make([]bool, len(toolCalls))
	var rejectedNames []string
	for i, tc := range toolCalls {
		reason, isRejected := rejectToolCall(tc, store, llmContext)
		if !isRejected {
			continue
		}
		rejected[i] = true
		rejectedNames = append(rejectedNames, tc.Name)
		toolResults[i] = ToolResult{ToolCallID: tc.ID, Name: tc.Name, Result: reason, IsError: true}
	}
	if len(rejectedNames) == 0 {
		return nil
	}

	for i, tc := range toolCalls {
		if rejected[i] {
			continue
		}
		toolResults[i] = ToolResult{
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Result:     llm.BatchSkippedToolResult(tc.Name, rejectedNames),
			IsError:    true,
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
	for i := range enriched {
		llm.EnrichToolCall(&enriched[i], store, llm.EnrichToolCallOptions{})
	}
	return enriched
}

func appendToolTurnAndPost(
	result *ToolRunResult,
	request *llm.CompletionRequest,
	text string,
	reasoningData llm.ReasoningData,
	serverTools []llm.ServerToolUse,
	segments []llm.TurnSegment,
	resolvedToolCalls []llm.ToolCall,
	toolResults []ToolResult,
	usage llm.TokenUsage,
) {
	turn := ToolTurn{
		AssistantMessage:     text,
		AssistantToolCalls:   resolvedToolCalls,
		AssistantReasoning:   reasoningData,
		AssistantServerTools: serverTools,
		AssistantSegments:    segments,
		ToolResults:          toolResults,
		TokensIn:             usage.InputTokens,
		TokensOut:            usage.OutputTokens,
	}
	result.ToolTurns = append(result.ToolTurns, turn)

	request.Posts = append(request.Posts, llm.Post{
		Role:               llm.PostRoleBot,
		Message:            text,
		ToolUse:            resolvedToolCalls,
		Reasoning:          reasoningData.Text,
		ReasoningSignature: reasoningData.Signature,
		ServerTools:        serverTools,
		AssistantSegments:  segments,
	})
}

// buildResolvedToolCalls creates resolved ToolCall entries from executed results.
func buildResolvedToolCalls(toolCalls []llm.ToolCall, toolResults []ToolResult) []llm.ToolCall {
	resolved := make([]llm.ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		status := llm.ToolCallStatusAutoApproved
		if toolResults[i].IsError {
			status = llm.ToolCallStatusError
		}
		resolved[i] = llm.ToolCall{
			ID:           tc.ID,
			Name:         tc.Name,
			Description:  tc.Description,
			Title:        tc.Title,
			Arguments:    tc.Arguments,
			ServerOrigin: tc.ServerOrigin,
			MCPBareName:  tc.MCPBareName,
			Status:       status,
			Result:       toolResults[i].Result,
		}
	}
	return resolved
}
