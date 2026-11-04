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
	"github.com/mattermost/mattermost/server/public/model"
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
		{name: "channel rounds of meta tools only", rounds: [][]llm.ToolCall{{meta}, {meta}}, want: true},
		{name: "channel round with an auto_run_everywhere tool of undetermined provenance", rounds: [][]llm.ToolCall{{meta}, {everywhere, meta}}, want: false},
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

// TestAllToolsAutoRunEverywhere_RespectsDestinationAudience checks that the
// channel answer is posted only when every executed tool reported provenance
// the destination audience can read. Tools still auto-run when policy allows.
// A false result withholds that answer until a share decision.
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
		noGuests        bool
		extraTool       string
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
			noGuests:        true,
			wantAutoRun:     true,
			wantAutoPublish: true,
		},
		{
			name:            "public channel of destination team when a guest may be present",
			dest:            destPublic,
			sources:         []*model.Channel{otherPublicSameTeam},
			origin:          mcp.EmbeddedClientKey,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "destination channel is missing",
			dest:            nil,
			sources:         []*model.Channel{otherPublicSameTeam},
			origin:          mcp.EmbeddedClientKey,
			noGuests:        true,
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "executed tool did not report provenance",
			dest:            destPublic,
			sources:         []*model.Channel{otherPublicSameTeam},
			origin:          mcp.EmbeddedClientKey,
			noGuests:        true,
			extraTool:       "get_channel_info",
			wantAutoRun:     true,
			wantAutoPublish: false,
		},
		{
			name:            "executed tool is outside the provenance allowlist",
			dest:            destPublic,
			sources:         []*model.Channel{otherPublicSameTeam},
			origin:          mcp.EmbeddedClientKey,
			noGuests:        true,
			extraTool:       "create_post",
			wantAutoRun:     true,
			wantAutoPublish: false,
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
			c := &Conversations{toolPolicyChecker: policies, licenseChecker: enterprisetest.CheckerAt(enterprise.LevelEnterprise)}
			llmCtx := &llm.Context{
				Channel:                tc.dest,
				Tools:                  llm.NewToolStore(),
				DestinationHasNoGuests: tc.noGuests,
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
			if llmCtx.Parameters != nil {
				llmCtx.Parameters[reportedSourceToolsParam] = map[string]bool{toolName: true}
			}
			llmCtx.Tools.AddTools([]llm.Tool{{Name: toolName, ServerOrigin: tc.origin}})
			call := llm.ToolCall{Name: toolName, ServerOrigin: tc.origin}
			calls := []llm.ToolCall{call}
			if tc.extraTool != "" {
				policies[tc.origin][tc.extraTool] = struct {
					policy  string
					enabled bool
				}{policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}
				llmCtx.Tools.AddTools([]llm.Tool{{Name: tc.extraTool, ServerOrigin: tc.origin}})
				calls = append(calls, llm.ToolCall{Name: tc.extraTool, ServerOrigin: tc.origin})
			}
			if tc.extraRemote {
				llmCtx.Tools.AddTools([]llm.Tool{{Name: remoteToolName, ServerOrigin: remoteOrigin}})
				calls = append(calls, llm.ToolCall{Name: remoteToolName, ServerOrigin: remoteOrigin})
			}
			turns := []toolrunner.ToolTurn{{AssistantToolCalls: calls}}

			isDM := tc.dest != nil && tc.dest.Type == model.ChannelTypeDirect
			assert.Equal(t, tc.wantAutoRun, testTurnPolicy(c, isDM).autoExecute(llmCtx)(call),
				"tools still auto-run when policy allows")
			assert.Equal(t, tc.wantAutoPublish, c.allToolsAutoRunEverywhere(turns, llmCtx),
				"the channel answer is posted only when provenance is covered")
			if !isDM {
				assert.Equal(t, tc.wantAutoPublish && tc.wantAutoRun, testTurnPolicy(c, false).sharedForTurns(turns, llmCtx),
					"tool results are written shared only when the answer is publishable")
				llmCtx.HoldChannelAnswer = func(calls []llm.ToolCall) bool {
					return !c.allToolsAutoRunEverywhere([]toolrunner.ToolTurn{{AssistantToolCalls: calls}}, llmCtx)
				}
				assert.Equal(t, !tc.wantAutoPublish, llmCtx.HoldChannelAnswer(calls),
					"an uncovered answer is withheld until a share decision")
			}
		})
	}
}

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
	llmCtx := &llm.Context{Channel: &model.Channel{Id: "dm", Type: model.ChannelTypeDirect}, Tools: llm.NewToolStore()}
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
	llmCtx := &llm.Context{Channel: &model.Channel{Id: "dm", Type: model.ChannelTypeDirect}, Tools: store}

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
	llmCtx := &llm.Context{Channel: &model.Channel{Id: "dm", Type: model.ChannelTypeDirect}, Tools: llm.NewToolStore()}
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
	llmCtx := &llm.Context{Channel: &model.Channel{Id: "dm", Type: model.ChannelTypeDirect}, Tools: llm.NewToolStore()}
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
	llmCtx := &llm.Context{Channel: &model.Channel{Id: "dm", Type: model.ChannelTypeDirect}, Tools: llm.NewToolStore()}
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
