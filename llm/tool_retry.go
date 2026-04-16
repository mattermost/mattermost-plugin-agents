// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

const MaxConsecutiveToolCallFailures = 3

const ToolRetryLimitSystemMessage = "The last 3 tool attempts failed. Do not call any more tools. Explain the latest error to the user and ask for guidance or missing information."

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
	for i := range posts {
		if posts[i].Role != PostRoleSystem {
			continue
		}
		if posts[i].Message == ToolRetryLimitSystemMessage {
			return posts
		}

		postsCopy := append([]Post(nil), posts...)
		if postsCopy[i].Message == "" {
			postsCopy[i].Message = ToolRetryLimitSystemMessage
		} else {
			postsCopy[i].Message += "\n\n" + ToolRetryLimitSystemMessage
		}
		return postsCopy
	}

	return append([]Post{{
		Role:    PostRoleSystem,
		Message: ToolRetryLimitSystemMessage,
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
