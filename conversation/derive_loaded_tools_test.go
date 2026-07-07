// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/stretchr/testify/require"
)

func assistantTurn(t *testing.T, seq int, blocks ...ContentBlock) store.Turn {
	t.Helper()
	content, err := json.Marshal(blocks)
	require.NoError(t, err)
	return store.Turn{
		ConversationID: "conv",
		Role:           "assistant",
		Content:        content,
		Sequence:       seq,
	}
}

func resultTurn(t *testing.T, seq int, blocks ...ContentBlock) store.Turn {
	t.Helper()
	content, err := json.Marshal(blocks)
	require.NoError(t, err)
	return store.Turn{
		ConversationID: "conv",
		Role:           "tool_result",
		Content:        content,
		Sequence:       seq,
	}
}

func loadToolUseBlock(toolUseID, namespacedName string) ContentBlock {
	input, _ := json.Marshal(map[string]string{"name": namespacedName})
	return ContentBlock{
		Type:  BlockTypeToolUse,
		ID:    toolUseID,
		Name:  mcp.LoadToolName,
		Input: input,
	}
}

func loadToolResultBlock(toolUseID, status string, payload mcp.LoadToolResult) ContentBlock {
	content, _ := json.Marshal(payload)
	return ContentBlock{
		Type:      BlockTypeToolResult,
		ToolUseID: toolUseID,
		Status:    status,
		Content:   string(content),
	}
}

func successfulLoadResultBlock(toolUseID, namespacedName string) ContentBlock {
	return loadToolResultBlock(toolUseID, StatusSuccess, mcp.LoadToolResult{
		Loaded: true,
		Name:   namespacedName,
		Schema: map[string]any{"type": "object"},
	})
}

func TestDeriveLoadedMCPTools(t *testing.T) {
	tests := []struct {
		name  string
		turns []store.Turn
		want  []string
	}{
		{
			name: "single successful load",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 2, successfulLoadResultBlock("a", "jira__get_issue")),
			},
			want: []string{"jira__get_issue"},
		},
		{
			name: "same tool loaded twice returns one name, first-load order",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 2, successfulLoadResultBlock("a", "jira__get_issue")),
				assistantTurn(t, 3, loadToolUseBlock("b", "jira__get_issue")),
				resultTurn(t, 4, successfulLoadResultBlock("b", "jira__get_issue")),
			},
			want: []string{"jira__get_issue"},
		},
		{
			name: "two distinct tools in first-load order",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 2, successfulLoadResultBlock("a", "jira__get_issue")),
				assistantTurn(t, 3, loadToolUseBlock("b", "github__search")),
				resultTurn(t, 4, successfulLoadResultBlock("b", "github__search")),
			},
			want: []string{"jira__get_issue", "github__search"},
		},
		{
			name: "two distinct tools reverse seed reverses output",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "github__search")),
				resultTurn(t, 2, successfulLoadResultBlock("a", "github__search")),
				assistantTurn(t, 3, loadToolUseBlock("b", "jira__get_issue")),
				resultTurn(t, 4, successfulLoadResultBlock("b", "jira__get_issue")),
			},
			want: []string{"github__search", "jira__get_issue"},
		},
		{
			name: "failed status is omitted",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 2, loadToolResultBlock("a", StatusError, mcp.LoadToolResult{
					Loaded: true,
					Name:   "jira__get_issue",
				})),
			},
			want: nil,
		},
		{
			name: "loaded false in payload omitted",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 2, loadToolResultBlock("a", StatusSuccess, mcp.LoadToolResult{
					Loaded: false,
					Error:  "miss",
				})),
			},
			want: nil,
		},
		{
			name: "empty name in payload omitted",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 2, loadToolResultBlock("a", StatusSuccess, mcp.LoadToolResult{
					Loaded: true,
					Name:   "",
				})),
			},
			want: nil,
		},
		{
			name: "malformed result JSON omitted",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 2, ContentBlock{
					Type:      BlockTypeToolResult,
					ToolUseID: "a",
					Status:    StatusSuccess,
					Content:   "not json",
				}),
			},
			want: nil,
		},
		{
			name: "orphan tool_result with no matching tool_use omitted",
			turns: []store.Turn{
				resultTurn(t, 1, successfulLoadResultBlock("ghost", "jira__get_issue")),
			},
			want: nil,
		},
		{
			name: "non-load_tool tool_use ignored even with success result",
			turns: []store.Turn{
				assistantTurn(t, 1, ContentBlock{
					Type: BlockTypeToolUse,
					ID:   "a",
					Name: "jira__get_issue",
				}),
				resultTurn(t, 2, successfulLoadResultBlock("a", "jira__get_issue")),
			},
			want: nil,
		},
		{
			name: "tool_use with empty ID is ignored",
			turns: []store.Turn{
				assistantTurn(t, 1, ContentBlock{
					Type: BlockTypeToolUse,
					ID:   "",
					Name: mcp.LoadToolName,
				}),
				resultTurn(t, 2, successfulLoadResultBlock("a", "jira__get_issue")),
			},
			want: nil,
		},
		{
			name: "tool_result with empty ToolUseID is ignored",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 2, ContentBlock{
					Type:      BlockTypeToolResult,
					ToolUseID: "",
					Status:    StatusSuccess,
					Content:   mustMarshalLoadResult(t, mcp.LoadToolResult{Loaded: true, Name: "jira__get_issue"}),
				}),
			},
			want: nil,
		},
		{
			name:  "nil input returns nil",
			turns: nil,
			want:  nil,
		},
		{
			name:  "empty input returns nil",
			turns: []store.Turn{},
			want:  nil,
		},
		{
			name: "turn with malformed top-level Content is silently skipped",
			turns: []store.Turn{
				{ConversationID: "conv", Role: "assistant", Content: json.RawMessage("not json"), Sequence: 1},
				assistantTurn(t, 2, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 3, successfulLoadResultBlock("a", "jira__get_issue")),
			},
			want: []string{"jira__get_issue"},
		},
		{
			name: "pair survives intervening unrelated turns",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				assistantTurn(t, 2, ContentBlock{Type: BlockTypeText, Text: "thinking"}),
				resultTurn(t, 3, successfulLoadResultBlock("a", "jira__get_issue")),
			},
			want: []string{"jira__get_issue"},
		},
		{
			name: "mixed batch: one successful, one failed load",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 2, successfulLoadResultBlock("a", "jira__get_issue")),
				assistantTurn(t, 3, loadToolUseBlock("b", "jira__transition_issue")),
				resultTurn(t, 4, loadToolResultBlock("b", StatusSuccess, mcp.LoadToolResult{
					Loaded: false,
					Error:  "miss",
				})),
			},
			want: []string{"jira__get_issue"},
		},
		{
			name: "auto_approved status on tool_result is NOT treated as success",
			turns: []store.Turn{
				assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
				resultTurn(t, 2, loadToolResultBlock("a", StatusAutoApproved, mcp.LoadToolResult{
					Loaded: true,
					Name:   "jira__get_issue",
				})),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveLoadedMCPTools(tt.turns)
			if tt.want == nil {
				require.Nil(t, got)
				return
			}
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRestoreLoadedMCPToolsFromTurns(t *testing.T) {
	toolStore := llm.NewNoTools()
	toolStore.SetUnloadedMCPTools([]llm.Tool{
		{Name: "jira__get_issue"},
		{Name: "github__search"},
	})
	turns := []store.Turn{
		assistantTurn(t, 1, loadToolUseBlock("a", "jira__get_issue")),
		resultTurn(t, 2, successfulLoadResultBlock("a", "jira__get_issue")),
	}

	loaded := RestoreLoadedMCPToolsFromTurns(toolStore, turns)

	require.Equal(t, []llm.Tool{{Name: "jira__get_issue"}}, loaded)
	require.NotNil(t, toolStore.GetTool("jira__get_issue"))
	require.Nil(t, toolStore.GetTool("github__search"))
}

func mustMarshalLoadResult(t *testing.T, result mcp.LoadToolResult) string {
	t.Helper()
	b, err := json.Marshal(result)
	require.NoError(t, err)
	return string(b)
}
