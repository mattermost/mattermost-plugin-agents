// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// emptyToolResultPlaceholder stands in for a blank tool result. Anthropic
// rejects blank text ("text content blocks must be non-empty"), so no message
// or tool result may be replayed with blank content.
const emptyToolResultPlaceholder = "(no output)"

// missingAnswerPlaceholder stands in for the answer of a tool round that a
// user post directly follows. Bedrock's chat converter sends tool results as a
// user message without merging it into the next one, and Converse rejects
// consecutive user messages.
const missingAnswerPlaceholder = "(no response)"

func isBlank(s string) bool {
	return strings.TrimSpace(s) == ""
}

// normalizePosts removes what a hop with rules would reject from a
// conversation, so the chat and Responses paths render the same valid history:
// blank text, tool calls that cannot form a tool_use/tool_result pair, and,
// for strict hops, reasoning without a provider signature. Posts left with
// nothing to send are dropped. For strict hops, a tool round directly followed
// by a user post (one that was stopped, awaited approval, or ended without an
// answer) gets a placeholder answer, so roles keep alternating. The text of a
// user post with files is checked by multimodalContent.
func normalizePosts(posts []llm.Post, rules messageRules) []llm.Post {
	normalized := make([]llm.Post, 0, len(posts))
	for _, post := range posts {
		switch post.Role {
		case llm.PostRoleSystem:
			if isBlank(post.Message) {
				continue
			}
		case llm.PostRoleUser:
			if isBlank(post.Message) && len(post.Files) == 0 {
				continue
			}
			if rules.strictMessages && len(normalized) > 0 {
				if prev := normalized[len(normalized)-1]; prev.Role == llm.PostRoleBot && len(prev.ToolUse) > 0 {
					normalized = append(normalized, llm.Post{Role: llm.PostRoleBot, Message: missingAnswerPlaceholder})
				}
			}
		case llm.PostRoleBot:
			p, ok := normalizeBotPost(post, rules.strictMessages)
			if !ok {
				continue
			}
			post = p
		}
		normalized = append(normalized, post)
	}
	return normalized
}

// normalizeBotPost returns the replayable part of a bot post, and false when
// nothing of it is left to send.
func normalizeBotPost(post llm.Post, strictMessages bool) (llm.Post, bool) {
	if isBlank(post.Message) {
		post.Message = ""
	}
	post.ToolUse = replayableToolCalls(post.ToolUse)
	post.AssistantSegments = slices.DeleteFunc(slices.Clone(post.AssistantSegments), func(segment llm.TurnSegment) bool {
		return segment.Kind == llm.TurnSegmentText && isBlank(segment.Text)
	})
	// A stream that failed before the signature arrived persists its partial
	// reasoning for display only. Anthropic rejects it as an unsigned thinking
	// block; other providers accept unsigned reasoning.
	if strictMessages && post.ReasoningSignature == "" {
		post.Reasoning = ""
	}

	hasText := post.Message != "" || slices.ContainsFunc(post.AssistantSegments, func(segment llm.TurnSegment) bool {
		return segment.Kind == llm.TurnSegmentText
	})
	return post, hasText || len(post.ToolUse) > 0 || post.Reasoning != "" || serverToolActivityRecord(post.ServerTools) != ""
}

// replayableToolCalls returns the tool calls of a bot post that can be replayed
// as tool_use/tool_result pairs, with blank results replaced. Calls without an
// ID or name are results whose tool_use was never matched; they cannot form a
// valid pair and are dropped.
func replayableToolCalls(calls []llm.ToolCall) []llm.ToolCall {
	replayable := make([]llm.ToolCall, 0, len(calls))
	for _, tc := range calls {
		if tc.ID == "" || tc.Name == "" {
			continue
		}
		if isBlank(tc.Result) {
			tc.Result = emptyToolResultPlaceholder
		}
		replayable = append(replayable, tc)
	}
	return replayable
}
