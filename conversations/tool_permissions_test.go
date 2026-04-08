// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
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
}

func toolNames(store *llm.ToolStore) []string {
	var out []string
	for _, t := range store.GetTools() {
		out = append(out, t.Name)
	}
	return out
}
