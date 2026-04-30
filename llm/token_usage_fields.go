// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

const (
	// TokenUsageLogEvent is the canonical event name for structured token usage logs.
	// #nosec G101 -- this is a non-secret log event identifier.
	TokenUsageLogEvent = "llm_token_usage"
	// TokenUsageLogSchemaVersion is the version of the token usage log field contract.
	TokenUsageLogSchemaVersion = 1
	// TokenUsageUnknown is the normalized value for missing dimensions.
	TokenUsageUnknown = "unknown"
)

// operation identifies the high-level feature or action producing token usage.
const (
	OperationConversation             = "conversation"
	OperationConversationToolFollowup = "conversation_tool_followup"
	OperationTitleGeneration          = "title_generation"
	OperationChannelSummary           = "channel_summary"
	OperationChannelInterval          = "channel_interval"
	OperationThreadAnalysis           = "thread_analysis"
	OperationSearch                   = "search"
	OperationMeetingSummary           = "meeting_summary"
	OperationMeetingChunkSummary      = "meeting_chunk_summary"
	OperationEmojiSelection           = "emoji_selection"
	OperationWebSearchSummarization   = "web_search_summarization"
	OperationEvalGrading              = "eval_grading"
	OperationBridgeAgent              = "bridge_agent"
	OperationBridgeService            = "bridge_service"
)

// operation_subtype is a low-cardinality detail for the operation.
// Typical values represent modality or a small scenario class
// (for example streaming vs non-streaming, tool calls, or chunk modes).
const (
	SubTypeStreaming          = "streaming"
	SubTypeNoStream           = "nostream"
	SubTypeToolCall           = "tool_call"
	SubTypeTranscriptionChunk = "transcription_chunk"
	SubTypeChunkedTrue        = "chunked_true"
	SubTypeChunkedFalse       = "chunked_false"
)
