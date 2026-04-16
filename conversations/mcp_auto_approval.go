// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
)

// wrapStreamWithMCPAutoApproval wraps a text stream to automatically execute
// MCP tool calls whose per-tool policy satisfies mcp.IsToolPolicyAutoRun + enabled.
//
// When ALL tool calls in a batch are auto-runnable, the wrapper:
//  1. Executes each tool via the ToolStore
//  2. Sets the status to ToolCallStatusAutoApproved (or ToolCallStatusError on failure)
//  3. Includes the results in the emitted event
//
// The streaming layer detects the auto-approved status and skips the
// call-approval UI, proceeding directly to result-sharing.
//
// When ANY tool call is NOT auto-runnable, the batch passes through
// unchanged for the normal approval flow.
func wrapStreamWithMCPAutoApproval(
	stream *llm.TextStreamResult,
	llmContext *llm.Context,
	policyChecker streaming.ToolPolicyChecker,
) *llm.TextStreamResult {
	if stream == nil || llmContext == nil || llmContext.Tools == nil || policyChecker == nil {
		return stream
	}

	output := make(chan llm.TextStreamEvent)

	go func() {
		defer close(output)
		for event := range stream.Stream {
			if event.Type != llm.EventTypeToolCalls {
				output <- event
				continue
			}

			toolCalls, ok := event.Value.([]llm.ToolCall)
			if !ok || len(toolCalls) == 0 {
				output <- event
				continue
			}

			// Enrich each tool call with ServerOrigin from the ToolStore
			// and check whether all are auto-runnable.
			allAutoRun := true
			for i := range toolCalls {
				if tool := llmContext.Tools.GetTool(toolCalls[i].Name); tool != nil {
					toolCalls[i].ServerOrigin = tool.ServerOrigin
				}
				policy, enabled := policyChecker.GetToolPolicy(toolCalls[i].ServerOrigin, toolCalls[i].Name)
				if !mcp.IsToolPolicyAutoRun(policy) || !enabled {
					allAutoRun = false
				}
			}

			if !allAutoRun {
				// At least one tool is not auto-runnable — pass through unchanged
				output <- event
				continue
			}

			// All tools are auto-runnable: execute them
			for i := range toolCalls {
				result, err := llmContext.Tools.ResolveTool(toolCalls[i].Name, func(args any) error {
					return json.Unmarshal(toolCalls[i].Arguments, args)
				}, llmContext)
				if err != nil {
					toolCalls[i].Result = err.Error()
					toolCalls[i].Status = llm.ToolCallStatusError
				} else {
					toolCalls[i].Result = result
					toolCalls[i].Status = llm.ToolCallStatusAutoApproved
				}
			}

			output <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		}
	}()

	return &llm.TextStreamResult{Stream: output}
}
