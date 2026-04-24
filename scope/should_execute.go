// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scope

import (
	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// BuildScopedShouldExecute returns a shouldExecute callback for
// toolrunner.Run that only allows tool calls whose name is in allowed.
// Rejected calls cause the runner to bail out of the auto-run loop and return
// with the unresolved tool calls on the stream, which the dispatcher treats
// as the final (no-op) answer.
//
// Tools are dropped from the store in ApplyToolScope, so the LLM normally
// cannot see disallowed tools in the first place. This callback is a
// belt-and-braces guard: an older turn in the conversation history could
// contain a call to a tool that is no longer in the allowlist.
func BuildScopedShouldExecute(allowed []string, log Logger) func(llm.ToolCall) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		set[name] = struct{}{}
	}
	return func(call llm.ToolCall) bool {
		if _, ok := set[call.Name]; ok {
			return true
		}
		if log != nil {
			log.Warn("scope: rejecting out-of-scope tool call", "tool", call.Name, "tool_call_id", call.ID)
		}
		return false
	}
}
