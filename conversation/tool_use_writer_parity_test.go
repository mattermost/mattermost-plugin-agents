// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/require"
)

// persistedToolUseFields are the tool_use ContentBlock JSON tags the writers
// in this package must emit so a tool call renders identically live and after
// reload. Keep in sync with the persisted fields in
// streaming/tool_call_parity_test.go, which covers the live-path writer and
// redaction parity but cannot reach the unexported toolUseBlocks.
var persistedToolUseFields = []string{
	"id",
	"name",
	"server_origin",
	"input",
	"mcp_bare_name",
	"status",
	"title",
	"description",
	"user_interaction",
}

func parityToolCall() llm.ToolCall {
	return llm.ToolCall{
		ID:              "tc-1",
		Name:            "mattermost__create_post",
		Description:     "Create a post",
		Title:           "Create Post",
		Arguments:       json.RawMessage(`{"channel_id":"c1"}`),
		Status:          llm.ToolCallStatusSuccess,
		MCPBareName:     "create_post",
		UserInteraction: llm.UserInteractionSelect,
		ServerOrigin:    "embedded://mattermost",
	}
}

func toolUseBlockJSONMap(t *testing.T, block ContentBlock) map[string]any {
	t.Helper()
	data, err := json.Marshal(block)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// TestToolUseBlocksPersistsPolicyFields asserts the auto-run writer
// (toolUseBlocks) emits every persisted tool_use identity/metadata field for a
// fully-populated call, so an auto-run round renders the same as a live one.
func TestToolUseBlocksPersistsPolicyFields(t *testing.T) {
	blocks := toolUseBlocks("", llm.ReasoningData{}, nil, nil, []llm.ToolCall{parityToolCall()}, true)
	require.Len(t, blocks, 1)
	require.Equal(t, BlockTypeToolUse, blocks[0].Type)

	m := toolUseBlockJSONMap(t, blocks[0])
	for _, field := range persistedToolUseFields {
		t.Run(field, func(t *testing.T) {
			require.Contains(t, m, field, "toolUseBlocks dropped the field")
			require.NotEmpty(t, m[field], "toolUseBlocks emitted an empty field")
		})
	}
}
