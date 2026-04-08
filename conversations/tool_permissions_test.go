// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/stretchr/testify/require"
)

func TestFilterAutomatedInvokerTools(t *testing.T) {
	const origin = "https://example.com/mcp"

	checker := streaming.ToolPolicyFunc(func(serverBaseURL, toolName string) (string, bool) {
		switch toolName {
		case "ask_tool":
			return mcp.ToolPolicyAsk, true
		case "auto_run_tool":
			return mcp.ToolPolicyAutoRun, true
		case "everywhere_tool":
			return mcp.ToolPolicyAutoRunEverywhere, true
		default:
			return mcp.ToolPolicyAsk, true
		}
	})

	t.Run("no-op when not automated", func(t *testing.T) {
		store := llm.NewToolStore(nil, false)
		store.AddTools([]llm.Tool{
			{Name: "ask_tool", ServerOrigin: origin},
		})
		filterAutomatedInvokerTools(store, false, false, checker)
		require.Len(t, store.GetTools(), 1)
	})

	t.Run("channel keeps only auto_run_everywhere", func(t *testing.T) {
		store := llm.NewToolStore(nil, false)
		store.AddTools([]llm.Tool{
			{Name: "ask_tool", ServerOrigin: origin},
			{Name: "auto_run_tool", ServerOrigin: origin},
			{Name: "everywhere_tool", ServerOrigin: origin},
		})
		filterAutomatedInvokerTools(store, true, false, checker)
		names := toolNames(store)
		require.ElementsMatch(t, []string{"everywhere_tool"}, names)
	})

	t.Run("DM keeps auto_run and auto_run_everywhere", func(t *testing.T) {
		store := llm.NewToolStore(nil, false)
		store.AddTools([]llm.Tool{
			{Name: "ask_tool", ServerOrigin: origin},
			{Name: "auto_run_tool", ServerOrigin: origin},
			{Name: "everywhere_tool", ServerOrigin: origin},
		})
		filterAutomatedInvokerTools(store, true, true, checker)
		names := toolNames(store)
		require.ElementsMatch(t, []string{"auto_run_tool", "everywhere_tool"}, names)
	})

	t.Run("policy disabled in tool config removes tool for automated invokers", func(t *testing.T) {
		disabledChecker := streaming.ToolPolicyFunc(func(serverBaseURL, toolName string) (string, bool) {
			switch toolName {
			case "everywhere_on":
				return mcp.ToolPolicyAutoRunEverywhere, true
			case "everywhere_off":
				return mcp.ToolPolicyAutoRunEverywhere, false
			case "auto_on":
				return mcp.ToolPolicyAutoRun, true
			default:
				return mcp.ToolPolicyAsk, true
			}
		})
		store := llm.NewToolStore(nil, false)
		store.AddTools([]llm.Tool{
			{Name: "everywhere_on", ServerOrigin: origin},
			{Name: "everywhere_off", ServerOrigin: origin},
			{Name: "auto_on", ServerOrigin: origin},
		})
		filterAutomatedInvokerTools(store, true, false, disabledChecker)
		require.ElementsMatch(t, []string{"everywhere_on"}, toolNames(store))

		store2 := llm.NewToolStore(nil, false)
		store2.AddTools([]llm.Tool{
			{Name: "everywhere_on", ServerOrigin: origin},
			{Name: "everywhere_off", ServerOrigin: origin},
			{Name: "auto_on", ServerOrigin: origin},
		})
		filterAutomatedInvokerTools(store2, true, true, disabledChecker)
		require.ElementsMatch(t, []string{"everywhere_on", "auto_on"}, toolNames(store2))
	})

	t.Run("built-in tools with empty ServerOrigin are never removed", func(t *testing.T) {
		productionLikeChecker := streaming.ToolPolicyFunc(func(serverBaseURL, toolName string) (string, bool) {
			if serverBaseURL == mcp.EmbeddedClientKey {
				return mcp.ToolPolicyAutoRunEverywhere, true
			}
			return "ask", false
		})
		store := llm.NewToolStore(nil, false)
		store.AddTools([]llm.Tool{
			{Name: "native_builtin", ServerOrigin: ""},
			{Name: "remote_ask", ServerOrigin: origin},
		})
		filterAutomatedInvokerTools(store, true, false, productionLikeChecker)
		names := toolNames(store)
		require.ElementsMatch(t, []string{"native_builtin"}, names)
	})
}

func toolNames(store *llm.ToolStore) []string {
	var out []string
	for _, t := range store.GetTools() {
		out = append(out, t.Name)
	}
	return out
}
