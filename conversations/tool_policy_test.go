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
			llmCtx := &llm.Context{Tools: llm.NewToolStore()}
			llmCtx.Tools.AddTools([]llm.Tool{{Name: toolName, ServerOrigin: origin}})
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
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: "x", ServerOrigin: "y"}})
	for _, isDM := range []bool{true, false} {
		got := c.shouldAutoExecuteTool(llmCtx, isDM)(llm.ToolCall{Name: "x", ServerOrigin: "y"})
		assert.False(t, got, "isDM=%v", isDM)
	}
}

func TestShouldAutoExecuteTool_MetaToolsAutoExecute(t *testing.T) {
	c := &Conversations{toolPolicyChecker: nil}
	store := llm.NewToolStore()
	store.AddTools(mcp.NewMetaTools(nil))
	llmCtx := &llm.Context{Tools: store}

	for _, isDM := range []bool{true, false} {
		got := c.shouldAutoExecuteTool(llmCtx, isDM)(llm.ToolCall{Name: mcp.LoadToolName})
		assert.True(t, got, "isDM=%v", isDM)
	}

	got := c.shouldAutoExecuteTool(llmCtx, true)(llm.ToolCall{Name: mcp.LoadToolName, ServerOrigin: "https://mcp.example.com/mcp"})
	assert.False(t, got)
}

func TestShouldAutoExecuteTool_NamespacedToolUsesBarePolicy(t *testing.T) {
	const origin = "https://mcp.example.com/mcp"

	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"example_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
			},
		},
	}
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: "example__example_tool", ServerOrigin: origin}})

	got := c.shouldAutoExecuteTool(llmCtx, false)(llm.ToolCall{Name: "example__example_tool"})

	assert.True(t, got)
}

func TestShouldAutoExecuteToolMetaToolsBypassPolicy(t *testing.T) {
	c := &Conversations{toolPolicyChecker: nil}

	assert.True(t, c.shouldAutoExecuteTool(nil, true)(llm.ToolCall{Name: mcp.SearchToolsName}))
	assert.True(t, c.shouldAutoExecuteTool(nil, false)(llm.ToolCall{Name: mcp.LoadToolName}))
}

func TestShouldAutoExecuteToolMetaToolDoesNotAuthorizeBusinessTool(t *testing.T) {
	c := &Conversations{toolPolicyChecker: nil}

	assert.False(t, c.shouldAutoExecuteTool(nil, true)(llm.ToolCall{Name: "jira__get_issue"}))
}

type countingPolicyChecker struct {
	calls        int
	lastOrigin   string
	lastToolName string
	policy       string
	enabled      bool
}

func (c *countingPolicyChecker) GetToolPolicy(origin, toolName string) (string, bool) {
	c.calls++
	c.lastOrigin = origin
	c.lastToolName = toolName
	return c.policy, c.enabled
}

func TestShouldAutoExecuteTool_UnknownToolSkipsPolicyLookup(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}

	got := c.shouldAutoExecuteTool(llmCtx, true)(llm.ToolCall{Name: "unknown_tool", ServerOrigin: "https://mcp.example.com"})

	assert.False(t, got)
	assert.Zero(t, checker.calls)
}

func TestShouldAutoExecuteTool_KnownToolUsesPolicy(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunInDM, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	const origin = "https://mcp.example.com"
	const toolName = "known_tool"
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: toolName, ServerOrigin: origin}})

	got := c.shouldAutoExecuteTool(llmCtx, true)(llm.ToolCall{Name: toolName})

	assert.True(t, got)
	assert.Equal(t, 1, checker.calls)
}

func TestShouldAutoExecuteToolDenormalizesNamespacedTool(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	const origin = "https://mcp.atlassian.com"
	const runtimeToolName = "jira__get_issue"
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: runtimeToolName, ServerOrigin: origin}})

	got := c.shouldAutoExecuteTool(llmCtx, false)(llm.ToolCall{Name: runtimeToolName})

	assert.True(t, got)
	assert.Equal(t, 1, checker.calls)
	assert.Equal(t, "get_issue", checker.lastToolName)
}

func TestShouldAutoExecuteToolFailsClosedOnAmbiguousBareName(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{
		{Name: "jira__get_issue", ServerOrigin: "https://jira.example.com"},
		{Name: "github__get_issue", ServerOrigin: "https://github.example.com"},
	})

	got := c.shouldAutoExecuteTool(llmCtx, false)(llm.ToolCall{Name: "get_issue"})

	assert.False(t, got)
	assert.Zero(t, checker.calls)
}

func TestShouldAutoExecuteToolUsesServerOriginToDisambiguateBareName(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	const origin = "https://github.example.com"
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{
		{Name: "jira__get_issue", ServerOrigin: "https://jira.example.com"},
		{Name: "github__get_issue", ServerOrigin: origin},
	})

	got := c.shouldAutoExecuteTool(llmCtx, false)(llm.ToolCall{Name: "get_issue", ServerOrigin: origin})

	assert.True(t, got)
	assert.Equal(t, 1, checker.calls)
	assert.Equal(t, origin, checker.lastOrigin)
	assert.Equal(t, "get_issue", checker.lastToolName)
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
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: toolName, ServerOrigin: origin}})

	turns := []toolrunner.ToolTurn{{
		AssistantToolCalls: []llm.ToolCall{{Name: toolName, ServerOrigin: origin}},
	}}

	assert.False(t, c.allToolsAutoRunEverywhere(turns, llmCtx),
		"a disabled tool must not auto-share results even when the policy is auto_run_everywhere")
}

func TestAllToolsAutoRunEverywhere_NamespacedToolUsesBarePolicy(t *testing.T) {
	const origin = "https://mcp.example.com/mcp"

	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"example_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
			},
		},
	}
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: "example__example_tool", ServerOrigin: origin}})

	turns := []toolrunner.ToolTurn{{
		AssistantToolCalls: []llm.ToolCall{{Name: "example__example_tool", ServerOrigin: origin}},
	}}

	assert.True(t, c.allToolsAutoRunEverywhere(turns, llmCtx))
}

func TestAllToolsAutoRunEverywhere_AllowsMetaTools(t *testing.T) {
	const origin = "https://mcp.example.com/mcp"
	const toolName = "example_tool"

	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				toolName: {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
			},
		},
	}
	store := llm.NewToolStore()
	store.AddTools(mcp.NewMetaTools(nil))
	store.AddTools([]llm.Tool{{Name: toolName, ServerOrigin: origin}})
	llmCtx := &llm.Context{Tools: store}

	turns := []toolrunner.ToolTurn{
		{AssistantToolCalls: []llm.ToolCall{{Name: mcp.SearchToolsName}}},
		{AssistantToolCalls: []llm.ToolCall{{Name: toolName, ServerOrigin: origin}}},
	}

	assert.True(t, c.allToolsAutoRunEverywhere(turns, llmCtx))
}

func TestAllToolsAutoRunEverywhere_UnknownToolReturnsFalse(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	turns := []toolrunner.ToolTurn{{
		AssistantToolCalls: []llm.ToolCall{{Name: "unknown_tool", ServerOrigin: "https://mcp.example.com"}},
	}}

	assert.False(t, c.allToolsAutoRunEverywhere(turns, llmCtx))
	assert.Zero(t, checker.calls)
}

func TestAllToolsAutoRunEverywhereMetaOnlyBypassesPolicy(t *testing.T) {
	c := &Conversations{toolPolicyChecker: nil}
	turns := []toolrunner.ToolTurn{{
		AssistantToolCalls: []llm.ToolCall{
			{Name: mcp.SearchToolsName},
			{Name: mcp.LoadToolName},
		},
	}}

	assert.True(t, c.allToolsAutoRunEverywhere(turns, nil))
}

func TestAllToolsAutoRunEverywhereMixedMetaAndAutoRunBusinessTool(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	const origin = "https://mcp.atlassian.com"
	const runtimeToolName = "jira__get_issue"
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: runtimeToolName, ServerOrigin: origin}})
	turns := []toolrunner.ToolTurn{{
		AssistantToolCalls: []llm.ToolCall{
			{Name: mcp.SearchToolsName},
			{Name: runtimeToolName},
			{Name: mcp.LoadToolName},
		},
	}}

	assert.True(t, c.allToolsAutoRunEverywhere(turns, llmCtx))
	assert.Equal(t, 1, checker.calls)
	assert.Equal(t, "get_issue", checker.lastToolName)
}

func TestAllToolsAutoRunEverywhereMixedMetaAndNonAutoBusinessTool(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAsk, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	const origin = "https://mcp.atlassian.com"
	const runtimeToolName = "jira__get_issue"
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: runtimeToolName, ServerOrigin: origin}})
	turns := []toolrunner.ToolTurn{{
		AssistantToolCalls: []llm.ToolCall{
			{Name: mcp.SearchToolsName},
			{Name: runtimeToolName},
		},
	}}

	assert.False(t, c.allToolsAutoRunEverywhere(turns, llmCtx))
	assert.Equal(t, 1, checker.calls)
	assert.Equal(t, "get_issue", checker.lastToolName)
}

func TestAllToolsAutoRunEverywhereDenormalizesNamespacedTool(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	const origin = "https://mcp.atlassian.com"
	const runtimeToolName = "jira__get_issue"
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: runtimeToolName, ServerOrigin: origin}})
	turns := []toolrunner.ToolTurn{{
		AssistantToolCalls: []llm.ToolCall{{Name: runtimeToolName}},
	}}

	assert.True(t, c.allToolsAutoRunEverywhere(turns, llmCtx))
	assert.Equal(t, 1, checker.calls)
	assert.Equal(t, "get_issue", checker.lastToolName)
}

func TestAllToolsAutoRunEverywhereFailsClosedOnAmbiguousBareName(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{
		{Name: "jira__get_issue", ServerOrigin: "https://jira.example.com"},
		{Name: "github__get_issue", ServerOrigin: "https://github.example.com"},
	})
	turns := []toolrunner.ToolTurn{{
		AssistantToolCalls: []llm.ToolCall{{Name: "get_issue"}},
	}}

	assert.False(t, c.allToolsAutoRunEverywhere(turns, llmCtx))
	assert.Zero(t, checker.calls)
}

func TestAllToolsAutoRunEverywhereUsesServerOriginToDisambiguateBareName(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	const origin = "https://github.example.com"
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{
		{Name: "jira__get_issue", ServerOrigin: "https://jira.example.com"},
		{Name: "github__get_issue", ServerOrigin: origin},
	})
	turns := []toolrunner.ToolTurn{{
		AssistantToolCalls: []llm.ToolCall{{Name: "get_issue", ServerOrigin: origin}},
	}}

	assert.True(t, c.allToolsAutoRunEverywhere(turns, llmCtx))
	assert.Equal(t, 1, checker.calls)
	assert.Equal(t, origin, checker.lastOrigin)
	assert.Equal(t, "get_issue", checker.lastToolName)
}
