// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
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

// TestShouldAutoExecuteTool_ConfigPolicyChecker runs the production
// config-backed checker against runtime tool names as the MCP client builds
// them. Plugin tools carry a "{pluginID}__" prefix from pluginmcp, so their
// configured names contain the namespace separator themselves.
func TestShouldAutoExecuteTool_ConfigPolicyChecker(t *testing.T) {
	const (
		pluginOrigin = "plugin://com.example.demo"
		remoteOrigin = "https://mcp.example.com/mcp"
	)

	cases := []struct {
		name           string
		origin         string
		serverSlug     string
		configuredName string
		policy         string
		unlicensed     bool
		wantDM         bool
		wantChannel    bool
	}{
		{name: "plugin tool auto_run_everywhere without policy license falls back to ask", origin: pluginOrigin, serverSlug: "demo_plugin", configuredName: "com_example_demo__add", policy: mcp.ToolPolicyAutoRunEverywhere, unlicensed: true, wantDM: false, wantChannel: false},
		{name: "plugin tool auto_run_everywhere", origin: pluginOrigin, serverSlug: "demo_plugin", configuredName: "com_example_demo__add", policy: mcp.ToolPolicyAutoRunEverywhere, wantDM: true, wantChannel: true},
		{name: "plugin tool auto_run_in_dm", origin: pluginOrigin, serverSlug: "demo_plugin", configuredName: "com_example_demo__add", policy: mcp.ToolPolicyAutoRunInDM, wantDM: true, wantChannel: false},
		{name: "plugin tool ask", origin: pluginOrigin, serverSlug: "demo_plugin", configuredName: "com_example_demo__add", policy: mcp.ToolPolicyAsk, wantDM: false, wantChannel: false},
		{name: "remote tool name containing separator", origin: remoteOrigin, serverSlug: "remote", configuredName: "issues__create", policy: mcp.ToolPolicyAutoRunEverywhere, wantDM: true, wantChannel: true},
		{name: "remote tool plain name", origin: remoteOrigin, serverSlug: "remote", configuredName: "get_issue", policy: mcp.ToolPolicyAutoRunEverywhere, wantDM: true, wantChannel: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolConfigs := []mcp.ToolConfig{{Name: tc.configuredName, Policy: tc.policy, Enabled: true}}
			cfg := mcp.Config{}
			if tc.origin == pluginOrigin {
				cfg.PluginServers = []mcp.PluginServerConfig{{PluginID: "com.example.demo", Name: "Demo Plugin", Enabled: true, ToolConfigs: toolConfigs}}
			} else {
				cfg.Servers = []mcp.ServerConfig{{Name: "Remote", Enabled: true, BaseURL: remoteOrigin, ToolConfigs: toolConfigs}}
			}

			checker := mcp.NewConfigToolPolicyChecker(func() mcp.Config { return cfg }, func() bool { return !tc.unlicensed })
			c := &Conversations{toolPolicyChecker: checker}
			runtimeName := llm.NamespaceMCPToolName(tc.serverSlug, tc.configuredName)
			llmCtx := &llm.Context{Tools: llm.NewToolStore()}
			llmCtx.Tools.AddTools([]llm.Tool{{Name: runtimeName, ServerOrigin: tc.origin}})
			call := llm.ToolCall{Name: runtimeName, ServerOrigin: tc.origin}

			assert.Equal(t, tc.wantDM, c.shouldAutoExecuteTool(llmCtx, true)(call), "DM")
			assert.Equal(t, tc.wantChannel, c.shouldAutoExecuteTool(llmCtx, false)(call), "channel")
		})
	}
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

// TestShouldAutoExecuteTool_AutoExecuteBuiltIn pins that auto-execute
// built-ins (e.g. CreateFile) bypass approval in both DMs and channels — even
// with no policy checker wired up — while an MCP tool carrying the flag must
// still go through policy and therefore fails closed here.
func TestShouldAutoExecuteTool_AutoExecuteBuiltIn(t *testing.T) {
	const mcpOrigin = "https://mcp.example.com/mcp"

	cases := []struct {
		name   string
		isDM   bool
		origin string
		want   bool
	}{
		{name: "DM built-in auto-executes", isDM: true, want: true},
		{name: "channel built-in auto-executes", isDM: false, want: true},
		{name: "DM MCP tool with flag follows policy", isDM: true, origin: mcpOrigin, want: false},
		{name: "channel MCP tool with flag follows policy", isDM: false, origin: mcpOrigin, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Conversations{toolPolicyChecker: nil}
			llmCtx := &llm.Context{Tools: llm.NewToolStore()}
			llmCtx.Tools.AddTools([]llm.Tool{{Name: "CreateFile", ServerOrigin: tc.origin, AutoExecute: true}})

			got := c.shouldAutoExecuteTool(llmCtx, tc.isDM)(llm.ToolCall{Name: "CreateFile", ServerOrigin: tc.origin})

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestShouldAutoExecuteTool_AutoExecuteBuiltInSkipsPolicyLookup(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAsk, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: "CreateFile", AutoExecute: true}})

	got := c.shouldAutoExecuteTool(llmCtx, false)(llm.ToolCall{Name: "CreateFile"})

	assert.True(t, got)
	assert.Zero(t, checker.calls)
}

// TestShouldAutoExecuteTool_UserInteractionNeverAutoExecutes pins the contract
// that tools answered by the user (e.g. AskUserQuestion) never auto-execute,
// even if a policy claims auto_run — auto-running one would skip the question.
func TestShouldAutoExecuteTool_UserInteractionNeverAutoExecutes(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := &Conversations{toolPolicyChecker: checker}
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: "AskUserQuestion", UserInteraction: llm.UserInteractionSelect}})

	for _, isDM := range []bool{true, false} {
		got := c.shouldAutoExecuteTool(llmCtx, isDM)(llm.ToolCall{Name: "AskUserQuestion"})
		assert.False(t, got, "isDM=%v", isDM)
	}
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

// TestAllToolsAutoRunEverywhere_AutoExecuteBuiltIn pins that a round made up
// only of auto-execute built-ins (e.g. CreateFile) is written shared=true even
// with no policy checker, while an MCP tool carrying the flag still requires
// an auto_run_everywhere policy and so fails closed here.
func TestAllToolsAutoRunEverywhere_AutoExecuteBuiltIn(t *testing.T) {
	const mcpOrigin = "https://mcp.example.com/mcp"

	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "built-in only round is shared", origin: "", want: true},
		{name: "MCP tool with flag still requires policy", origin: mcpOrigin, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Conversations{toolPolicyChecker: nil}
			llmCtx := &llm.Context{Tools: llm.NewToolStore()}
			llmCtx.Tools.AddTools([]llm.Tool{{Name: "CreateFile", ServerOrigin: tc.origin, AutoExecute: true}})
			turns := []toolrunner.ToolTurn{{
				AssistantToolCalls: []llm.ToolCall{{Name: "CreateFile", ServerOrigin: tc.origin}},
			}}

			assert.Equal(t, tc.want, c.allToolsAutoRunEverywhere(turns, llmCtx))
		})
	}
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

// TestAllToolsAutoRunEverywhere_RespectsDestinationAudience checks that
// auto-publish requires every tool source to be readable by the destination
// audience. Tools still auto-run when policy allows; only publication is gated.
func TestAllToolsAutoRunEverywhere_RespectsDestinationAudience(t *testing.T) {
	const toolName = "read_channel"

	destPublic := &model.Channel{Id: "dest-public", Type: model.ChannelTypeOpen, TeamId: "team-a"}
	destPrivate := &model.Channel{Id: "dest-private", Type: model.ChannelTypePrivate, TeamId: "team-a"}
	destDM := &model.Channel{Id: "dest-dm", Type: model.ChannelTypeDirect}
	destGM := &model.Channel{Id: "dest-gm", Type: model.ChannelTypeGroup}
	otherPrivate := &model.Channel{Id: "other-private", Type: model.ChannelTypePrivate, TeamId: "team-a"}
	otherPublicSameTeam := &model.Channel{Id: "other-public", Type: model.ChannelTypeOpen, TeamId: "team-a"}
	otherPublicOtherTeam := &model.Channel{Id: "other-team-public", Type: model.ChannelTypeOpen, TeamId: "team-b"}
	sourceDM := &model.Channel{Id: "source-dm", Type: model.ChannelTypeDirect}
	sourceGM := &model.Channel{Id: "source-gm", Type: model.ChannelTypeGroup}
	idOnly := &model.Channel{Id: "unknown-channel"}
	emptyID := &model.Channel{Id: "", Type: model.ChannelTypePrivate, TeamId: "team-a"}

	cases := []struct {
		name            string
		dest            *model.Channel
		sources         []*model.Channel
		sourceParam     any
		extraRemote     bool
		origin          string
		wantAutoRun     bool
		wantAutoPublish bool
	}{
		{
			name:            "DM destination",
			dest:            destDM,
			sources:         []*model.Channel{otherPrivate},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: true,
		},
		{
			name:            "source is destination channel",
			dest:            destPublic,
			sources:         []*model.Channel{destPublic},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: true,
		},
		{
			name:            "source is destination private channel",
			dest:            destPrivate,
			sources:         []*model.Channel{destPrivate},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: true,
		},
		{
			name:            "source is public channel of destination team",
			dest:            destPublic,
			sources:         []*model.Channel{otherPublicSameTeam},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: true,
		},
		{
			name:            "source is other private channel",
			dest:            destPublic,
			sources:         []*model.Channel{otherPrivate},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "sources include a private channel other than destination",
			dest:            destPublic,
			sources:         []*model.Channel{otherPublicSameTeam, otherPrivate},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "source is public channel of another team",
			dest:            destPublic,
			sources:         []*model.Channel{otherPublicOtherTeam},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "source provenance is not determined",
			dest:            destPublic,
			sources:         nil,
			origin:          "https://mcp.example.com/mcp",
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "group message destination with other private source",
			dest:            destGM,
			sources:         []*model.Channel{otherPrivate},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "group message destination is the source",
			dest:            destGM,
			sources:         []*model.Channel{destGM},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: true,
		},
		{
			name:            "group message destination with public channel of a team",
			dest:            destGM,
			sources:         []*model.Channel{otherPublicSameTeam},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "source is a direct message",
			dest:            destPublic,
			sources:         []*model.Channel{sourceDM},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "source is a group message",
			dest:            destPublic,
			sources:         []*model.Channel{sourceGM},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "destination private and source is a different private channel",
			dest:            destPrivate,
			sources:         []*model.Channel{otherPrivate},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "source list is empty",
			dest:            destPublic,
			sources:         []*model.Channel{},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: true,
		},
		{
			name:            "source list key is absent for embedded tool",
			dest:            destPublic,
			sources:         nil,
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "source has only an id",
			dest:            destPublic,
			sources:         []*model.Channel{idOnly},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "source has an empty id",
			dest:            destPublic,
			sources:         []*model.Channel{emptyID},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "source entry is missing",
			dest:            destPublic,
			sources:         []*model.Channel{otherPublicSameTeam, nil},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name: "source list has an unexpected type",
			dest: destPublic,
			sourceParam: []any{
				map[string]any{"id": otherPublicSameTeam.Id, "type": string(model.ChannelTypeOpen), "team_id": otherPublicSameTeam.TeamId},
			},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "embedded and remote tools in the same turn",
			dest:            destPublic,
			sources:         []*model.Channel{otherPublicSameTeam},
			extraRemote:     true,
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
	}

	const remoteOrigin = "https://mcp.example.com/mcp"
	const remoteToolName = "remote_tool"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policies := mapPolicyChecker{
				tc.origin: {
					toolName: {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
				},
			}
			if tc.extraRemote {
				policies[remoteOrigin] = map[string]struct {
					policy  string
					enabled bool
				}{
					remoteToolName: {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
				}
			}
			c := &Conversations{toolPolicyChecker: policies}
			llmCtx := &llm.Context{
				Channel: tc.dest,
				Tools:   llm.NewToolStore(),
			}
			if tc.dest != nil && tc.dest.TeamId != "" {
				llmCtx.Team = &model.Team{Id: tc.dest.TeamId}
			}
			switch {
			case tc.sourceParam != nil:
				llmCtx.Parameters = map[string]any{sourceChannelsParam: tc.sourceParam}
			case tc.sources != nil:
				llmCtx.Parameters = map[string]any{sourceChannelsParam: tc.sources}
			}
			llmCtx.Tools.AddTools([]llm.Tool{{Name: toolName, ServerOrigin: tc.origin}})
			call := llm.ToolCall{Name: toolName, ServerOrigin: tc.origin}
			calls := []llm.ToolCall{call}
			if tc.extraRemote {
				llmCtx.Tools.AddTools([]llm.Tool{{Name: remoteToolName, ServerOrigin: remoteOrigin}})
				calls = append(calls, llm.ToolCall{Name: remoteToolName, ServerOrigin: remoteOrigin})
			}
			turns := []toolrunner.ToolTurn{{AssistantToolCalls: calls}}

			isDM := tc.dest != nil && tc.dest.Type == model.ChannelTypeDirect
			assert.Equal(t, tc.wantAutoRun, c.shouldAutoExecuteTool(llmCtx, isDM)(call),
				"tools still auto-run when policy allows")
			assert.Equal(t, tc.wantAutoPublish, c.allToolsAutoRunEverywhere(turns, llmCtx),
				"auto-publish requires every tool source to be readable by the destination audience")
		})
	}
}
