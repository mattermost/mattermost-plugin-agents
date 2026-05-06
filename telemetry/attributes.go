// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Attribute keys for LLM operations
var (
	LLMProvider  = attribute.Key("ai.llm.provider")
	LLMModel     = attribute.Key("ai.llm.model")
	LLMOperation = attribute.Key("ai.llm.operation")
	LLMStreaming = attribute.Key("ai.llm.streaming")

	LLMInputTokens  = attribute.Key("ai.llm.input_tokens")
	LLMOutputTokens = attribute.Key("ai.llm.output_tokens")
)

// Attribute keys for agent context
var (
	AgentName = attribute.Key("ai.agent.name")
	AgentID   = attribute.Key("ai.agent.id")
)

// Attribute keys for tool operations
var (
	ToolName   = attribute.Key("ai.tool.name")
	ToolID     = attribute.Key("ai.tool.id")
	ToolStatus = attribute.Key("ai.tool.status")
)

// Attribute keys for MCP operations
var (
	MCPServer = attribute.Key("ai.mcp.server")
	MCPTool   = attribute.Key("ai.mcp.tool")
)

// Attribute keys for Mattermost entities
var (
	UserID           = attribute.Key("ai.user.id")
	ChannelID        = attribute.Key("ai.channel.id")
	PostID           = attribute.Key("ai.post.id")
	ThreadRootPostID = attribute.Key("ai.thread.root_post.id")
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
