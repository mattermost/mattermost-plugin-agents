// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolUseBlocksStatuses verifies that tool_use blocks written via
// toolUseBlocks are tagged auto_approved for successful rounds. WriteToolTurns
// is only invoked after the toolrunner has auto-executed every tool call in a
// round, so the persisted status must reflect that — otherwise the UI cannot
// distinguish auto-executed tools from user-approved ones and drops the
// "Auto-approved" badge on DM follow-ups.
func TestToolUseBlocksStatuses(t *testing.T) {
	tests := []struct {
		name       string
		toolCalls  []llm.ToolCall
		results    []toolrunner.ToolResult
		wantStatus []string
	}{
		{
			name: "successful auto-executed round tagged auto_approved",
			toolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "read_channel"},
			},
			results: []toolrunner.ToolResult{
				{ToolCallID: "tc1", Name: "read_channel", Result: "ok", IsError: false},
			},
			wantStatus: []string{StatusAutoApproved},
		},
		{
			name: "errored tool call tagged error",
			toolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "read_channel"},
			},
			results: []toolrunner.ToolResult{
				{ToolCallID: "tc1", Name: "read_channel", Result: "boom", IsError: true},
			},
			wantStatus: []string{StatusError},
		},
		{
			name: "mixed success and error in one round",
			toolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "read_channel"},
				{ID: "tc2", Name: "get_channel_info"},
			},
			results: []toolrunner.ToolResult{
				{ToolCallID: "tc1", Result: "ok"},
				{ToolCallID: "tc2", Result: "boom", IsError: true},
			},
			wantStatus: []string{StatusAutoApproved, StatusError},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := toolUseBlocks("", llm.ReasoningData{}, tt.toolCalls, tt.results, true)
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
