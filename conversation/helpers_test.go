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
			blocks := toolUseBlocks("", llm.ReasoningData{}, nil, nil, tt.toolCalls, true)
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
	blocks := toolUseBlocks("", llm.ReasoningData{}, nil, nil, []llm.ToolCall{{
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

// TestToolUseBlocksIncludesServerToolActivity pins the fix for server-tool
// activity being lost from intermediate tool rounds: a round that mixed
// provider-executed tools with client tool calls must persist the activity
// as server_tool_use blocks placed before the round's text.
func TestToolUseBlocksIncludesServerToolActivity(t *testing.T) {
	serverTools := []llm.ServerToolUse{
		{ID: "srv1", Tool: llm.NativeToolWebSearch, Status: llm.ServerToolStatusSuccess, Query: "release notes"},
		{ID: "srv2", Tool: llm.NativeToolCodeInterpreter, Status: llm.ServerToolStatusSuccess, SubTool: "bash", Command: "ls"},
	}
	blocks := toolUseBlocks("Checking the channel too.", llm.ReasoningData{}, serverTools, nil, []llm.ToolCall{{
		ID:     "tc1",
		Name:   "read_channel",
		Status: llm.ToolCallStatusAutoApproved,
	}}, true)

	require.Len(t, blocks, 4)
	require.Equal(t, BlockTypeServerToolUse, blocks[0].Type)
	require.NotNil(t, blocks[0].ServerTool)
	assert.Equal(t, "srv1", blocks[0].ServerTool.ID)
	require.Equal(t, BlockTypeServerToolUse, blocks[1].Type)
	require.NotNil(t, blocks[1].ServerTool)
	assert.Equal(t, "srv2", blocks[1].ServerTool.ID)
	assert.Equal(t, BlockTypeText, blocks[2].Type, "server tool activity must precede the text block")
	assert.Equal(t, BlockTypeToolUse, blocks[3].Type)
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

// TestToolUseBlocksUsesRecordedOrder pins that a persisted tool round renders in
// the order the provider streamed it. Tool rounds go through this path rather
// than the streaming accumulator, so without the recorded order the same
// grouped-by-kind reordering shows up on every round that called a tool.
func TestToolUseBlocksUsesRecordedOrder(t *testing.T) {
	serverTools := []llm.ServerToolUse{
		{ID: "srv1", Tool: llm.NativeToolCodeInterpreter, SubTool: "bash", Command: "ls"},
		{ID: "srv2", Tool: llm.NativeToolCodeInterpreter, SubTool: "python", Command: "open(f)"},
	}
	segments := []llm.TurnSegment{
		{Kind: llm.TurnSegmentText, Text: "First I'll look."},
		{Kind: llm.TurnSegmentServerTool, ServerToolID: "srv1"},
		{Kind: llm.TurnSegmentText, Text: "Now the file."},
		{Kind: llm.TurnSegmentServerTool, ServerToolID: "srv2"},
	}

	blocks := toolUseBlocks("First I'll look.Now the file.", llm.ReasoningData{}, serverTools, segments,
		[]llm.ToolCall{{ID: "tc1", Name: "CreateFile", Status: llm.ToolCallStatusAutoApproved}}, true)

	require.Len(t, blocks, 5)
	assert.Equal(t, BlockTypeText, blocks[0].Type)
	assert.Equal(t, "First I'll look.", blocks[0].Text)
	assert.Equal(t, BlockTypeServerToolUse, blocks[1].Type)
	assert.Equal(t, "srv1", blocks[1].ServerTool.ID)
	assert.Equal(t, BlockTypeText, blocks[2].Type)
	assert.Equal(t, "Now the file.", blocks[2].Text)
	assert.Equal(t, BlockTypeServerToolUse, blocks[3].Type)
	assert.Equal(t, "srv2", blocks[3].ServerTool.ID)
	assert.Equal(t, BlockTypeToolUse, blocks[4].Type)
}

// TestSequenceBlocksDropsUnknownActivity pins that a segment whose activity is
// missing from the snapshot is skipped: the webapp has no card to render for an
// invocation with no payload.
func TestSequenceBlocksDropsUnknownActivity(t *testing.T) {
	blocks := SequenceBlocks([]llm.TurnSegment{
		{Kind: llm.TurnSegmentServerTool, ServerToolID: "gone"},
		{Kind: llm.TurnSegmentText, Text: "kept"},
		{Kind: llm.TurnSegmentText, Text: ""},
		{Kind: llm.TurnSegmentThinking, Text: ""},
	}, nil)

	require.Len(t, blocks, 1)
	assert.Equal(t, BlockTypeText, blocks[0].Type)
	assert.Equal(t, "kept", blocks[0].Text)
}
