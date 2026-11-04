// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/stretchr/testify/assert"
)

// policyTestConversations is a Conversations licensed for remote MCP, so the
// policy decisions below are not masked by the license gate.
func policyTestConversations(checker mcp.ToolPolicyChecker) *Conversations {
	return &Conversations{toolPolicyChecker: checker, licenseChecker: enterprisetest.CheckerAt(enterprise.LevelEnterprise)}
}

// testTurnPolicy is the policy of a turn with tools enabled in a DM or a
// channel.
func testTurnPolicy(c *Conversations, isDM bool) *turnPolicy {
	return &turnPolicy{c: c, isDM: isDM, toolsEnabled: true}
}

// testDMTurnPolicy is the policy resolveTurnPolicy gives a DM turn with bot,
// for tests that exercise the tool loop rather than policy resolution.
func testDMTurnPolicy(c *Conversations, bot *bots.Bot) *turnPolicy {
	return &turnPolicy{
		c:            c,
		bot:          bot,
		isDM:         true,
		toolsEnabled: true,
		interactive:  true,
	}
}

// TestAutoExecute captures the channel-vs-DM policy matrix. In
// DMs, both auto_run and auto_run_everywhere bypass approval. In channels,
// only auto_run_everywhere bypasses approval — auto_run tools fall through
// to the manual approve → share flow so the channel-visible follow-up
// cannot reveal unshared tool output.
func TestAutoExecute(t *testing.T) {
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
			c := policyTestConversations(mapPolicyChecker{
				origin: {
					toolName: {policy: tc.policy, enabled: tc.enabled},
				},
			})
			llmCtx := &llm.Context{Tools: llm.NewToolStore()}
			llmCtx.Tools.AddTools([]llm.Tool{{Name: toolName, ServerOrigin: origin}})
			callback := testTurnPolicy(c, tc.isDM).autoExecute(llmCtx)
			got := callback(llm.ToolCall{Name: toolName, ServerOrigin: origin})
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAutoExecute_MetaToolsAutoExecute(t *testing.T) {
	c := policyTestConversations(mapPolicyChecker{})
	store := llm.NewToolStore()
	store.AddTools(mcp.NewMetaTools(nil))
	llmCtx := &llm.Context{Tools: store}

	for _, isDM := range []bool{true, false} {
		got := testTurnPolicy(c, isDM).autoExecute(llmCtx)(llm.ToolCall{Name: mcp.LoadToolName})
		assert.True(t, got, "isDM=%v", isDM)
	}

	got := testTurnPolicy(c, true).autoExecute(llmCtx)(llm.ToolCall{Name: mcp.LoadToolName, ServerOrigin: "https://mcp.example.com/mcp"})
	assert.False(t, got)
}

func TestAutoExecute_NamespacedToolUsesBarePolicy(t *testing.T) {
	const origin = "https://mcp.example.com/mcp"

	c := policyTestConversations(mapPolicyChecker{
		origin: {
			"example_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
		},
	})
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: "example__example_tool", ServerOrigin: origin}})

	got := testTurnPolicy(c, false).autoExecute(llmCtx)(llm.ToolCall{Name: "example__example_tool"})

	assert.True(t, got)
}

// TestAutoExecute_ConfigPolicyChecker runs the production
// config-backed checker against runtime tool names as the MCP client builds
// them. Plugin tools carry a "{pluginID}__" prefix from pluginmcp, so their
// configured names contain the namespace separator themselves.
func TestAutoExecute_ConfigPolicyChecker(t *testing.T) {
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
			c := policyTestConversations(checker)
			runtimeName := llm.NamespaceMCPToolName(tc.serverSlug, tc.configuredName)
			llmCtx := &llm.Context{Tools: llm.NewToolStore()}
			llmCtx.Tools.AddTools([]llm.Tool{{Name: runtimeName, ServerOrigin: tc.origin}})
			call := llm.ToolCall{Name: runtimeName, ServerOrigin: tc.origin}

			assert.Equal(t, tc.wantDM, testTurnPolicy(c, true).autoExecute(llmCtx)(call), "DM")
			assert.Equal(t, tc.wantChannel, testTurnPolicy(c, false).autoExecute(llmCtx)(call), "channel")
		})
	}
}

func TestAutoExecuteMetaToolsBypassPolicy(t *testing.T) {
	c := policyTestConversations(mapPolicyChecker{})

	assert.True(t, testTurnPolicy(c, true).autoExecute(nil)(llm.ToolCall{Name: mcp.SearchToolsName}))
	assert.True(t, testTurnPolicy(c, false).autoExecute(nil)(llm.ToolCall{Name: mcp.LoadToolName}))
}

func TestAutoExecuteMetaToolDoesNotAuthorizeBusinessTool(t *testing.T) {
	c := policyTestConversations(mapPolicyChecker{})

	assert.False(t, testTurnPolicy(c, true).autoExecute(nil)(llm.ToolCall{Name: "jira__get_issue"}))
}

// TestAutoExecute_AutoExecuteBuiltIn pins that auto-execute
// built-ins (e.g. CreateFile) bypass approval in both DMs and channels, while
// an MCP tool carrying the flag must still go through policy and therefore
// fails closed here.
func TestAutoExecute_AutoExecuteBuiltIn(t *testing.T) {
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
			c := policyTestConversations(mapPolicyChecker{})
			llmCtx := &llm.Context{Tools: llm.NewToolStore()}
			llmCtx.Tools.AddTools([]llm.Tool{{Name: "CreateFile", ServerOrigin: tc.origin, AutoExecute: true}})

			got := testTurnPolicy(c, tc.isDM).autoExecute(llmCtx)(llm.ToolCall{Name: "CreateFile", ServerOrigin: tc.origin})

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAutoExecute_AutoExecuteBuiltInSkipsPolicyLookup(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAsk, enabled: true}
	c := policyTestConversations(checker)
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: "CreateFile", AutoExecute: true}})

	got := testTurnPolicy(c, false).autoExecute(llmCtx)(llm.ToolCall{Name: "CreateFile"})

	assert.True(t, got)
	assert.Zero(t, checker.calls)
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

func TestAutoExecute_UnknownToolSkipsPolicyLookup(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := policyTestConversations(checker)
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}

	got := testTurnPolicy(c, true).autoExecute(llmCtx)(llm.ToolCall{Name: "unknown_tool", ServerOrigin: "https://mcp.example.com"})

	assert.False(t, got)
	assert.Zero(t, checker.calls)
}

func TestAutoExecute_KnownToolUsesPolicy(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunInDM, enabled: true}
	c := policyTestConversations(checker)
	const origin = "https://mcp.example.com"
	const toolName = "known_tool"
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: toolName, ServerOrigin: origin}})

	got := testTurnPolicy(c, true).autoExecute(llmCtx)(llm.ToolCall{Name: toolName})

	assert.True(t, got)
	assert.Equal(t, 1, checker.calls)
}

func TestAutoExecuteDenormalizesNamespacedTool(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := policyTestConversations(checker)
	const origin = "https://mcp.atlassian.com"
	const runtimeToolName = "jira__get_issue"
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{{Name: runtimeToolName, ServerOrigin: origin}})

	got := testTurnPolicy(c, false).autoExecute(llmCtx)(llm.ToolCall{Name: runtimeToolName})

	assert.True(t, got)
	assert.Equal(t, 1, checker.calls)
	assert.Equal(t, "get_issue", checker.lastToolName)
}

func TestAutoExecuteFailsClosedOnAmbiguousBareName(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := policyTestConversations(checker)
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{
		{Name: "jira__get_issue", ServerOrigin: "https://jira.example.com"},
		{Name: "github__get_issue", ServerOrigin: "https://github.example.com"},
	})

	got := testTurnPolicy(c, false).autoExecute(llmCtx)(llm.ToolCall{Name: "get_issue"})

	assert.False(t, got)
	assert.Zero(t, checker.calls)
}

func TestAutoExecuteUsesServerOriginToDisambiguateBareName(t *testing.T) {
	checker := &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
	c := policyTestConversations(checker)
	const origin = "https://github.example.com"
	llmCtx := &llm.Context{Tools: llm.NewToolStore()}
	llmCtx.Tools.AddTools([]llm.Tool{
		{Name: "jira__get_issue", ServerOrigin: "https://jira.example.com"},
		{Name: "github__get_issue", ServerOrigin: origin},
	})

	got := testTurnPolicy(c, false).autoExecute(llmCtx)(llm.ToolCall{Name: "get_issue", ServerOrigin: origin})

	assert.True(t, got)
	assert.Equal(t, 1, checker.calls)
	assert.Equal(t, origin, checker.lastOrigin)
	assert.Equal(t, "get_issue", checker.lastToolName)
}

// TestSharedForTurns pins when rounds the turn ran automatically skip the
// share step: always in a DM, and in a channel only when every call is one
// the policy auto-runs there.
func TestSharedForTurns(t *testing.T) {
	const origin = "https://mcp.atlassian.com"
	c := policyTestConversations(mapPolicyChecker{origin: {
		"everywhere_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
		"dm_tool":         {policy: mcp.ToolPolicyAutoRunInDM, enabled: true},
	}})
	store := llm.NewToolStore()
	store.AddTools(mcp.NewMetaTools(nil))
	store.AddTools([]llm.Tool{{Name: "jira__everywhere_tool", ServerOrigin: origin}, {Name: "jira__dm_tool", ServerOrigin: origin}})
	llmCtx := &llm.Context{Tools: store}
	everywhere := llm.ToolCall{Name: "jira__everywhere_tool", ServerOrigin: origin}
	dmOnly := llm.ToolCall{Name: "jira__dm_tool", ServerOrigin: origin}
	meta := llm.ToolCall{Name: mcp.SearchToolsName}

	cases := []struct {
		name   string
		isDM   bool
		rounds [][]llm.ToolCall
		want   bool
	}{
		{name: "DM round with a DM-only tool", isDM: true, rounds: [][]llm.ToolCall{{dmOnly}}, want: true},
		{name: "channel rounds of meta and auto_run_everywhere tools", rounds: [][]llm.ToolCall{{meta}, {everywhere, meta}}, want: true},
		{name: "channel round with a DM-only tool", rounds: [][]llm.ToolCall{{meta}, {dmOnly}}, want: false},
		{name: "channel turn without tool calls", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			turns := make([]toolrunner.ToolTurn, 0, len(tc.rounds))
			for _, calls := range tc.rounds {
				turns = append(turns, toolrunner.ToolTurn{AssistantToolCalls: calls})
			}
			assert.Equal(t, tc.want, testTurnPolicy(c, tc.isDM).sharedForTurns(turns, llmCtx))
		})
	}
}

// TestAutoExecuteRequiresRemoteMCPLicense pins that a tool from a remote or
// plugin MCP server never runs without approval below Enterprise, whatever
// its policy, while built-in and embedded Mattermost tools still follow
// policy.
func TestAutoExecuteRequiresRemoteMCPLicense(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		level  enterprise.Level
		want   bool
	}{
		{name: "remote MCP below Enterprise", origin: "https://mcp.example.com", level: enterprise.LevelProfessional, want: false},
		{name: "plugin MCP below Enterprise", origin: "plugin://com.example.demo", level: enterprise.LevelProfessional, want: false},
		{name: "embedded MCP below Enterprise", origin: mcp.EmbeddedClientKey, level: enterprise.LevelUnlicensed, want: true},
		{name: "remote MCP at Enterprise", origin: "https://mcp.example.com", level: enterprise.LevelEnterprise, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Conversations{
				toolPolicyChecker: &countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
				licenseChecker:    enterprisetest.CheckerAt(tc.level),
			}
			llmCtx := &llm.Context{Tools: llm.NewToolStore()}
			llmCtx.Tools.AddTools([]llm.Tool{{Name: "get_issue", ServerOrigin: tc.origin}})

			for _, isDM := range []bool{true, false} {
				got := testTurnPolicy(c, isDM).autoExecute(llmCtx)(llm.ToolCall{Name: "get_issue", ServerOrigin: tc.origin})
				assert.Equal(t, tc.want, got, "isDM=%v", isDM)
			}
		})
	}
}

// TestAutoExecuteNeverRunsWhenToolsAreDisabled pins that a turn without tools
// runs nothing, even tools that never need approval.
func TestAutoExecuteNeverRunsWhenToolsAreDisabled(t *testing.T) {
	c := policyTestConversations(&countingPolicyChecker{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true})
	store := llm.NewToolStore()
	store.AddTools(mcp.NewMetaTools(nil))
	store.AddTools([]llm.Tool{{Name: "CreateFile", AutoExecute: true}, {Name: "get_issue", ServerOrigin: "https://mcp.example.com"}})
	llmCtx := &llm.Context{Tools: store}
	p := &turnPolicy{c: c}

	for _, call := range []llm.ToolCall{{Name: mcp.LoadToolName}, {Name: "CreateFile"}, {Name: "get_issue", ServerOrigin: "https://mcp.example.com"}} {
		assert.False(t, p.autoExecute(llmCtx)(call), call.Name)
	}
}
