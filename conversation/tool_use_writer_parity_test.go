// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/require"
)

// persistedToolUseFields are the tool_use ContentBlock JSON tags that the
// approval-path writers must emit so a tool call renders identically live and
// after reload. This is the conversation-side half of the tool-call parity
// drift-guard: the live-path writer (streaming.buildContentBlocks), the generic
// PostToBlocks converter, and the redaction parity are asserted in
// streaming/tool_call_parity_test.go, which cannot reach the unexported
// toolUseBlocks writer used for auto-run tool rounds. Keep this list in sync
// with the persisted==true rows of that package's policy table.
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
	blocks := toolUseBlocks("", llm.ReasoningData{}, []llm.ToolCall{parityToolCall()}, true)
	require.Len(t, blocks, 1)
	require.Equal(t, BlockTypeToolUse, blocks[0].Type)

	m := toolUseBlockJSONMap(t, blocks[0])
	for _, field := range persistedToolUseFields {
		require.Containsf(t, m, field, "toolUseBlocks dropped persisted tool_use field %q", field)
		require.NotEmptyf(t, m[field], "toolUseBlocks emitted an empty persisted tool_use field %q", field)
	}
}

// TestPostToBlocksPersistsPolicyFields asserts the generic Post->blocks
// converter carries the same tool identity/metadata. It intentionally omits
// user_interaction: PostToBlocks converts completed posts, where the pending
// interaction kind is not meaningful (it is set by the approval-path writers).
func TestPostToBlocksPersistsPolicyFields(t *testing.T) {
	post := llm.Post{Role: llm.PostRoleBot, ToolUse: []llm.ToolCall{parityToolCall()}}
	blocks := PostToBlocks(post, true)
	require.GreaterOrEqual(t, len(blocks), 1)
	require.Equal(t, BlockTypeToolUse, blocks[0].Type)

	m := toolUseBlockJSONMap(t, blocks[0])
	for _, field := range persistedToolUseFields {
		if field == "user_interaction" {
			continue
		}
		require.Containsf(t, m, field, "PostToBlocks dropped persisted tool_use field %q", field)
		require.NotEmptyf(t, m[field], "PostToBlocks emitted an empty persisted tool_use field %q", field)
	}
}
