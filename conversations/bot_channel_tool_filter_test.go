// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/stretchr/testify/require"
)

type mapPolicyChecker map[string]map[string]struct {
	policy  string
	enabled bool
}

func (m mapPolicyChecker) GetToolPolicy(serverOrigin, toolName string) (string, bool) {
	byServer, ok := m[serverOrigin]
	if !ok {
		return mcp.ToolPolicyAsk, false
	}
	cfg, ok := byServer[toolName]
	if !ok {
		return mcp.ToolPolicyAsk, true
	}
	return cfg.policy, cfg.enabled
}

func TestApplyBotChannelAutoEverywhereToolFilter(t *testing.T) {
	origin := "https://mcp.example.com/mcp"
	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"everywhere_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
				"auto_run_tool":   {policy: mcp.ToolPolicyAutoRunInDM, enabled: true},
				"ask_tool":        {policy: mcp.ToolPolicyAsk, enabled: true},
			},
		},
	}

	llmContext := &llm.Context{
		Tools: llm.NewToolStore(nil, false),
	}
	llmContext.Tools.AddTools([]llm.Tool{
		{Name: "builtin", ServerOrigin: "", Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "everywhere_tool", ServerOrigin: origin, Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "auto_run_tool", ServerOrigin: origin, Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "ask_tool", ServerOrigin: origin, Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }},
	})

	c.applyBotChannelAutoEverywhereToolFilter(llmContext)

	tools := llmContext.Tools.GetTools()
	require.Len(t, tools, 1)
	require.Equal(t, "everywhere_tool", tools[0].Name)
	require.Len(t, llmContext.DisabledToolsInfo, 3)
}

func TestApplyToolAvailabilityBeforeBotChannelFilterPreservesDisabledToolsInfo(t *testing.T) {
	origin := "https://mcp.example.com/mcp"
	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"everywhere_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
				"ask_tool":        {policy: mcp.ToolPolicyAsk, enabled: true},
			},
		},
	}

	llmContext := &llm.Context{
		Tools: llm.NewToolStore(nil, false),
	}
	llmContext.Tools.AddTools([]llm.Tool{
		{Name: "builtin", Description: "builtin tool", ServerOrigin: "", Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "everywhere_tool", Description: "auto everywhere", ServerOrigin: origin, Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "ask_tool", Description: "needs approval", ServerOrigin: origin, Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }},
	})

	toolsDisabled := applyToolAvailability(llmContext, false, true)
	require.False(t, toolsDisabled)

	c.applyBotChannelAutoEverywhereToolFilter(llmContext)

	tools := llmContext.Tools.GetTools()
	require.Len(t, tools, 1)
	require.Equal(t, "everywhere_tool", tools[0].Name)

	disabledNames := make([]string, 0, len(llmContext.DisabledToolsInfo))
	for _, info := range llmContext.DisabledToolsInfo {
		disabledNames = append(disabledNames, info.Name)
	}
	require.ElementsMatch(t, []string{"builtin", "ask_tool"}, disabledNames)
}

func TestApplyBotChannelAutoEverywhereToolFilter_nilCheckerFailClosed(t *testing.T) {
	origin := "https://mcp.example.com/mcp"
	c := &Conversations{toolPolicyChecker: nil}

	llmContext := &llm.Context{
		Tools: llm.NewToolStore(nil, false),
	}
	llmContext.Tools.AddTools([]llm.Tool{
		{Name: "builtin", ServerOrigin: "", Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "mcp_tool", ServerOrigin: origin, Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }},
	})

	c.applyBotChannelAutoEverywhereToolFilter(llmContext)

	require.Empty(t, llmContext.Tools.GetTools())
	require.Len(t, llmContext.DisabledToolsInfo, 2)
}
