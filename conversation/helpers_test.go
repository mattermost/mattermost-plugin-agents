// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolUseBlocksStatuses verifies that toolUseBlocks forwards the resolved
// tool-call status (AutoApproved / Error) from the input tool calls without
// re-deriving it. The toolrunner is responsible for storing resolved status on
// ToolTurn.AssistantToolCalls; this helper just translates.
func TestToolUseBlocksStatuses(t *testing.T) {
	tests := []struct {
		name       string
		toolCalls  []llm.ToolCall
		wantStatus []string
	}{
		{
			name: "auto-approved tool tagged auto_approved",
			toolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "read_channel", Status: llm.ToolCallStatusAutoApproved},
			},
			wantStatus: []string{StatusAutoApproved},
		},
		{
			name: "errored tool call tagged error",
			toolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "read_channel", Status: llm.ToolCallStatusError},
			},
			wantStatus: []string{StatusError},
		},
		{
			name: "mixed statuses passed through independently",
			toolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "read_channel", Status: llm.ToolCallStatusAutoApproved},
				{ID: "tc2", Name: "get_channel_info", Status: llm.ToolCallStatusError},
			},
			wantStatus: []string{StatusAutoApproved, StatusError},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := toolUseBlocks("", llm.ReasoningData{}, tt.toolCalls, true)
			var got []string
			for _, b := range blocks {
				if b.Type == BlockTypeToolUse {
					got = append(got, b.Status)
				}
			}
			assert.Equal(t, tt.wantStatus, got)
		})
	}
}

func TestToolUseBlocksPreservesApprovalMetadata(t *testing.T) {
	blocks := toolUseBlocks("", llm.ReasoningData{}, []llm.ToolCall{{
		ID:           "tc1",
		Name:         "jira__get_issue",
		Description:  "Get a Jira issue",
		ServerOrigin: "https://jira.example.com",
		Arguments:    json.RawMessage(`{"key":"MM-1"}`),
		Schema:       json.RawMessage(`{"type":"object"}`),
		MCPBareName:  "get_issue",
		Status:       llm.ToolCallStatusPending,
	}}, false)

	require.Len(t, blocks, 1)
	assert.Equal(t, BlockTypeToolUse, blocks[0].Type)
	assert.Equal(t, "jira__get_issue", blocks[0].Name)
	assert.Equal(t, "https://jira.example.com", blocks[0].ServerOrigin)
	assert.Equal(t, "get_issue", blocks[0].MCPBareName)
}

func TestUnmarshalBlocks(t *testing.T) {
	tests := []struct {
		name           string
		raw            json.RawMessage
		expectedBlocks []ContentBlock
		expectErr      bool
	}{
		{
			name:           "nil RawMessage returns nil",
			raw:            nil,
			expectedBlocks: nil,
			expectErr:      false,
		},
		{
			name:           "empty RawMessage returns nil",
			raw:            json.RawMessage{},
			expectedBlocks: nil,
			expectErr:      false,
		},
		{
			name:           "empty JSON array returns empty slice",
			raw:            json.RawMessage(`[]`),
			expectedBlocks: []ContentBlock{},
			expectErr:      false,
		},
		{
			name: "valid blocks JSON",
			raw:  json.RawMessage(`[{"type":"text","text":"hello"}]`),
			expectedBlocks: []ContentBlock{
				{Type: BlockTypeText, Text: "hello"},
			},
			expectErr: false,
		},
		{
			name:      "invalid JSON returns error",
			raw:       json.RawMessage(`{not json`),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, err := unmarshalBlocks(tt.raw)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedBlocks, blocks)
		})
	}
}
