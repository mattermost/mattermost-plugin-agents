// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "strings"

const MaxConsecutiveToolCallFailures = 3

const ToolRetryLimitSystemMessage = "The last 3 tool attempts failed. Do not call any more tools. Explain the latest error to the user and ask for guidance or missing information."

const ToolIterationLimitUserMessage = "You have used all available tool calls. Do not call any more tools. Answer the user's question using the results from your previous tool calls. If those results did not provide enough information, say so and summarize what you tried."

// CountTrailingFailedToolCalls counts consecutive trailing tool executions that
// failed. A successful tool execution resets the streak. Posts without executed
// tool results stop the scan because they represent a new agent turn.
func CountTrailingFailedToolCalls(posts []Post) int {
	failures := 0

	for i := len(posts) - 1; i >= 0; i-- {
		post := posts[i]
		if post.Role == PostRoleSystem {
			continue
		}

		postFailures, allFailed, hasExecutedTool := trailingFailedToolCalls(post.ToolUse)
		if !hasExecutedTool || !allFailed {
			break
		}

		failures += postFailures
	}

	return failures
}

func EnsureToolRetryLimitSystemMessage(posts []Post) []Post {
	return ensureSystemMessage(posts, ToolRetryLimitSystemMessage)
}

func EnsureToolIterationLimitUserMessage(posts []Post) []Post {
	for _, post := range posts {
		if post.Role == PostRoleUser && strings.Contains(post.Message, ToolIterationLimitUserMessage) {
			return posts
		}
	}

	postsCopy := append([]Post(nil), posts...)
	return append(postsCopy, Post{
		Role:    PostRoleUser,
		Message: ToolIterationLimitUserMessage,
	})
}

// ensureSystemMessage appends message to the first existing system post, or
// prepends a new system post if none exists. If the message is already present
// on a system post, posts is returned unchanged.
func ensureSystemMessage(posts []Post, message string) []Post {
	for i := range posts {
		if posts[i].Role != PostRoleSystem {
			continue
		}
		if strings.Contains(posts[i].Message, message) {
			return posts
		}

		postsCopy := append([]Post(nil), posts...)
		if postsCopy[i].Message == "" {
			postsCopy[i].Message = message
		} else {
			postsCopy[i].Message += "\n\n" + message
		}
		return postsCopy
	}

	return append([]Post{{
		Role:    PostRoleSystem,
		Message: message,
	}}, posts...)
}

func trailingFailedToolCalls(toolCalls []ToolCall) (count int, allFailed bool, hasExecutedTool bool) {
	if len(toolCalls) == 0 {
		return 0, false, false
	}

	for _, toolCall := range toolCalls {
		switch toolCall.Status {
		case ToolCallStatusError:
			count++
			hasExecutedTool = true
		case ToolCallStatusSuccess, ToolCallStatusAutoApproved:
			return 0, false, true
		case ToolCallStatusRejected, ToolCallStatusPending, ToolCallStatusAccepted:
			continue
		default:
			return 0, false, hasExecutedTool
		}
	}

	return count, count > 0, hasExecutedTool
}
