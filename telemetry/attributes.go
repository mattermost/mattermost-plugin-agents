// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Attribute keys for LLM operations
var (
	LLMProvider  = attribute.Key("agents.llm.provider")
	LLMModel     = attribute.Key("agents.llm.model")
	LLMOperation = attribute.Key("agents.llm.operation")
	LLMStreaming = attribute.Key("agents.llm.streaming")

	LLMInputTokens       = attribute.Key("agents.llm.input_tokens")
	LLMOutputTokens      = attribute.Key("agents.llm.output_tokens")
	LLMCachedReadTokens  = attribute.Key("agents.llm.cached_read_tokens")
	LLMCachedWriteTokens = attribute.Key("agents.llm.cached_write_tokens")
	LLMReasoningTokens   = attribute.Key("agents.llm.reasoning_tokens")
	LLMCost              = attribute.Key("agents.llm.cost")

	// Routing — which bifrost code path the request took.
	LLMPath            = attribute.Key("agents.llm.path")              // "chat" | "responses"
	LLMUseResponsesAPI = attribute.Key("agents.llm.use_responses_api") // bot/service-level toggle

	// Reasoning — what (if anything) was attached to the outbound request.
	LLMReasoningEffort    = attribute.Key("agents.llm.reasoning.effort")
	LLMReasoningMaxTokens = attribute.Key("agents.llm.reasoning.max_tokens")
	LLMReasoningSent      = attribute.Key("agents.llm.reasoning.sent") // true when the request includes a reasoning block

	// Bifrost error surface — populated when a request fails so opaque
	// "bifrost error: …" log lines can be correlated with status/type/code.
	LLMBifrostStatusCode    = attribute.Key("agents.llm.bifrost.status_code")
	LLMBifrostErrorType     = attribute.Key("agents.llm.bifrost.error_type")
	LLMBifrostErrorCode     = attribute.Key("agents.llm.bifrost.error_code")
	LLMBifrostErrorProvider = attribute.Key("agents.llm.bifrost.error_provider")
	LLMBifrostIsBifrostErr  = attribute.Key("agents.llm.bifrost.is_bifrost_error")

	// Per-source breakdown of agents.llm.input_tokens, derived from the
	// request and emitted on the LLM-call span. One attribute per source.
	LLMTokensSystem      = attribute.Key("agents.llm.tokens.system")
	LLMTokensHistory     = attribute.Key("agents.llm.tokens.history")
	LLMTokensToolDefs    = attribute.Key("agents.llm.tokens.tool_defs")
	LLMTokensToolResults = attribute.Key("agents.llm.tokens.tool_results")
	LLMTokensImages      = attribute.Key("agents.llm.tokens.images")

	// Per-round debugging signal.
	LLMFinishReason       = attribute.Key("agents.llm.finish_reason")
	LLMTimeToFirstTokenMs = attribute.Key("agents.llm.time_to_first_token_ms")
	LLMRound              = attribute.Key("agents.llm.round") // 0-based index of the LLM call within one tool loop
)

// Attribute keys for agent context
var (
	AgentName = attribute.Key("agents.agent.name")
	AgentID   = attribute.Key("agents.agent.id")
	ServiceID = attribute.Key("agents.service.id")
	// BridgePluginID is the plugin calling the LLM bridge API.
	BridgePluginID        = attribute.Key("agents.bridge.plugin_id")
	AgentProgressPhase    = attribute.Key("agents.agent.progress.phase")
	AgentProgressSequence = attribute.Key("agents.agent.progress.sequence")
)

// Attribute keys for tool operations
var (
	ToolName   = attribute.Key("agents.tool.name")
	ToolID     = attribute.Key("agents.tool.id")
	ToolStatus = attribute.Key("agents.tool.status")

	// ToolResultBytes and ToolResultTokens size the result the model will
	// see; the result itself is never attached.
	ToolResultBytes  = attribute.Key("agents.tool.result.bytes")
	ToolResultTokens = attribute.Key("agents.tool.result.tokens_estimate")
	// ToolExecution is ToolExecutionAuto or ToolExecutionApproved.
	ToolExecution = attribute.Key("agents.tool.execution")
	// ToolReturnedNames lists the tool names search_tools returned;
	// ToolLoadedName is the tool load_tool loaded.
	ToolReturnedNames = attribute.Key("agents.tool.returned_names")
	ToolLoadedName    = attribute.Key("agents.tool.loaded_name")
)

const (
	ToolExecutionAuto     = "auto"
	ToolExecutionApproved = "approved"
)

// Attribute keys for MCP operations
var (
	MCPServer = attribute.Key("agents.mcp.server")
	MCPTool   = attribute.Key("agents.mcp.tool")
)

// Attribute keys for web search operations
var (
	WebSearchProvider    = attribute.Key("agents.websearch.provider")
	WebSearchResultLimit = attribute.Key("agents.websearch.result_limit")

	// Failure surface: which early return a search bailed out on, plus the
	// upstream HTTP status when there was one. The query is never attached.
	WebSearchFailStage  = attribute.Key("agents.websearch.fail_stage")
	WebSearchStatusCode = attribute.Key("agents.websearch.status_code")
)

// Attribute keys for Mattermost entities
var (
	UserID           = attribute.Key("agents.user.id")
	ChannelID        = attribute.Key("agents.channel.id")
	PostID           = attribute.Key("agents.post.id")
	ResponsePostID   = attribute.Key("agents.response_post.id")
	ThreadRootPostID = attribute.Key("agents.thread.root_post.id")

	ConversationID = attribute.Key("agents.conversation.id")
	UserTurnID     = attribute.Key("agents.conversation.user_turn.id")
	// RunTraceID is set on spans outside an agent run's trace (the message
	// hook) to name the trace the run was recorded under.
	RunTraceID = attribute.Key("agents.agent.run.trace_id")
)

// Attribute keys for ambient auto-reply
var (
	// AmbientShouldReply is the ambient classifier's decision; it is unset
	// when classification failed.
	AmbientShouldReply = attribute.Key("agents.ambient.should_reply")
)

// Attribute keys for post streaming
var (
	StreamOutcome       = attribute.Key("agents.stream.outcome")
	StreamMessageLength = attribute.Key("agents.stream.message_length") // bytes
	StreamContinuation  = attribute.Key("agents.stream.continuation")
)

// StreamOutcome values.
const (
	StreamOutcomeCompleted = "completed"
	StreamOutcomeCancelled = "canceled"
	StreamOutcomeErrored   = "errored"
	StreamOutcomeEmpty     = "empty"
)

// Attribute keys for attribute-based access control (ABAC) decisions
var (
	ABACResourceType = attribute.Key("agents.abac.resource_type")
	ABACResourceID   = attribute.Key("agents.abac.resource_id")
	ABACResourceName = attribute.Key("agents.abac.resource_name")
	ABACOutcome      = attribute.Key("agents.abac.outcome")
	// ABACDenied is true when the check denied access. A denial is an
	// expected decision, so it does not set the span status to Error.
	ABACDenied = attribute.Key("agents.abac.denied")
	// ABACDecisionSource is set when something other than an access policy
	// made the decision ("legacy": the agent's allow/block lists).
	ABACDecisionSource = attribute.Key("agents.abac.decision_source")
	// ABACAvailable is the result of the ABAC availability probe; on failure
	// ABACProbeStatusCode and ABACProbeErrorID say why.
	ABACAvailable       = attribute.Key("agents.abac.available")
	ABACProbeStatusCode = attribute.Key("agents.abac.probe_status_code")
	ABACProbeErrorID    = attribute.Key("agents.abac.probe_error_id")
)

// WithLLMAttributes returns a SpanStartOption with standard LLM attributes.
func WithLLMAttributes(provider, model, operation string, streaming bool) trace.SpanStartOption {
	return trace.WithAttributes(
		LLMProvider.String(provider),
		LLMModel.String(model),
		LLMOperation.String(operation),
		LLMStreaming.Bool(streaming),
	)
}

type llmRoundKey struct{}

// WithLLMRound records on ctx the 0-based index of the next LLM call within
// a tool loop, so the LLM span can carry agents.llm.round.
func WithLLMRound(ctx context.Context, round int) context.Context {
	return context.WithValue(ctx, llmRoundKey{}, round)
}

// LLMRoundFromContext returns the round set by WithLLMRound.
func LLMRoundFromContext(ctx context.Context) (int, bool) {
	round, ok := ctx.Value(llmRoundKey{}).(int)
	return round, ok
}
