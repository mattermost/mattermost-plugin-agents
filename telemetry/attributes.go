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

	LLMInputTokens  = attribute.Key("agents.llm.input_tokens")
	LLMOutputTokens = attribute.Key("agents.llm.output_tokens")
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
