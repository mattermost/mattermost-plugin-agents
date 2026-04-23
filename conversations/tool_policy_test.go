// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
	"github.com/stretchr/testify/assert"
)

// TestShouldAutoExecuteTool captures the channel-vs-DM policy matrix. In
// DMs, both auto_run and auto_run_everywhere bypass approval. In channels,
// only auto_run_everywhere bypasses approval — auto_run tools fall through
// to the manual approve → share flow so the channel-visible follow-up
// cannot reveal unshared tool output.
func TestShouldAutoExecuteTool(t *testing.T) {
	const origin = "https://mcp.example.com/mcp"
	const toolName = "example_tool"

	cases := []struct {
		name    string
		isDM    bool
		policy  string
		enabled bool
		want    bool
	}{
		{name: "DM + auto_run_in_dm enabled -> auto-execute", isDM: true, policy: mcp.ToolPolicyAutoRunInDM, enabled: true, want: true},
		{name: "DM + auto_run_everywhere enabled -> auto-execute", isDM: true, policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true, want: true},
		{name: "DM + ask -> approve", isDM: true, policy: mcp.ToolPolicyAsk, enabled: true, want: false},
		{name: "DM + disabled -> approve", isDM: true, policy: mcp.ToolPolicyAutoRunInDM, enabled: false, want: false},

		{name: "channel + auto_run_in_dm enabled -> approve (DM-only policy)", isDM: false, policy: mcp.ToolPolicyAutoRunInDM, enabled: true, want: false},
		{name: "channel + auto_run_everywhere enabled -> auto-execute", isDM: false, policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true, want: true},
		{name: "channel + ask -> approve", isDM: false, policy: mcp.ToolPolicyAsk, enabled: true, want: false},
		{name: "channel + auto_run_everywhere disabled -> approve", isDM: false, policy: mcp.ToolPolicyAutoRunEverywhere, enabled: false, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Conversations{
				toolPolicyChecker: mapPolicyChecker{
					origin: {
						toolName: {policy: tc.policy, enabled: tc.enabled},
					},
				},
			}
			llmCtx := &llm.Context{Tools: llm.NewToolStore(nil, false)}
			callback := c.shouldAutoExecuteTool(llmCtx, tc.isDM)
			got := callback(llm.ToolCall{Name: toolName, ServerOrigin: origin})
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestShouldAutoExecuteTool_NilChecker covers the fail-closed branch — with
// no policy checker wired up, no tool should ever auto-execute.
func TestShouldAutoExecuteTool_NilChecker(t *testing.T) {
	c := &Conversations{toolPolicyChecker: nil}
	llmCtx := &llm.Context{Tools: llm.NewToolStore(nil, false)}
	for _, isDM := range []bool{true, false} {
		got := c.shouldAutoExecuteTool(llmCtx, isDM)(llm.ToolCall{Name: "x", ServerOrigin: "y"})
		assert.False(t, got, "isDM=%v", isDM)
	}
}

// TestAllToolsAutoRunEverywhere_RespectsEnabledFlag pins the result-sharing
// contract: a disabled tool must never drive results to shared=true, even if
// its policy is auto_run_everywhere. The enabled flag is authoritative —
// matching shouldAutoExecuteTool, which also refuses to auto-execute a
// disabled tool.
func TestAllToolsAutoRunEverywhere_RespectsEnabledFlag(t *testing.T) {
	const origin = "https://mcp.example.com/mcp"
	const toolName = "example_tool"

	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				toolName: {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: false},
			},
		},
	}
	llmCtx := &llm.Context{Tools: llm.NewToolStore(nil, false)}

	turns := []toolrunner.ToolTurn{{
		AssistantToolCalls: []llm.ToolCall{{Name: toolName, ServerOrigin: origin}},
	}}

	assert.False(t, c.allToolsAutoRunEverywhere(turns, llmCtx),
		"a disabled tool must not auto-share results even when the policy is auto_run_everywhere")
}
