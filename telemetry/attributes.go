// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
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
)

// Attribute keys for agent context
var (
	AgentName = attribute.Key("agents.agent.name")
	AgentID   = attribute.Key("agents.agent.id")
)

// Attribute keys for tool operations
var (
	ToolName   = attribute.Key("agents.tool.name")
	ToolID     = attribute.Key("agents.tool.id")
	ToolStatus = attribute.Key("agents.tool.status")
)

// Attribute keys for MCP operations
var (
	MCPServer = attribute.Key("agents.mcp.server")
	MCPTool   = attribute.Key("agents.mcp.tool")
)

// Attribute keys for Mattermost entities
var (
	UserID           = attribute.Key("agents.user.id")
	ChannelID        = attribute.Key("agents.channel.id")
	PostID           = attribute.Key("agents.post.id")
	ThreadRootPostID = attribute.Key("agents.thread.root_post.id")
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
