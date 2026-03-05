// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import "go.opentelemetry.io/otel/attribute"

// Attribute keys for LLM operations
var (
	LLMProvider  = attribute.Key("ai.llm.provider")
	LLMModel     = attribute.Key("ai.llm.model")
	LLMOperation = attribute.Key("ai.llm.operation")
	LLMStreaming  = attribute.Key("ai.llm.streaming")

	LLMInputTokens  = attribute.Key("ai.llm.input_tokens")
	LLMOutputTokens = attribute.Key("ai.llm.output_tokens")
)

// Attribute keys for bot context
var (
	BotName = attribute.Key("ai.bot.name")
	BotID   = attribute.Key("ai.bot.id")
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
	UserID    = attribute.Key("ai.user.id")
	ChannelID = attribute.Key("ai.channel.id")
	PostID    = attribute.Key("ai.post.id")
)
