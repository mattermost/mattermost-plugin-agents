// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

// textBlocks creates content blocks from a plain text message.
func textBlocks(message string) []ContentBlock {
	if message == "" {
		return nil
	}
	return []ContentBlock{{Type: BlockTypeText, Text: message}}
}

// marshalBlocks serializes content blocks to JSON for store.Turn.Content.
func marshalBlocks(blocks []ContentBlock) (json.RawMessage, error) {
	if blocks == nil {
		blocks = []ContentBlock{}
	}
	return json.Marshal(blocks)
}

// unmarshalBlocks deserializes JSON content from store.Turn.Content.
func unmarshalBlocks(raw json.RawMessage) ([]ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// toolUseBlocks builds assistant-side content blocks from ToolRunner output.
// Tool calls must carry their resolved status (AutoApproved / Error) — the
// toolrunner stores resolved tool calls on ToolTurn.AssistantToolCalls after
// execution, so this helper just forwards tc.Status verbatim.
func toolUseBlocks(
	message string,
	reasoning llm.ReasoningData,
	toolCalls []llm.ToolCall,
	shared bool,
) []ContentBlock {
	var blocks []ContentBlock

	if reasoning.Text != "" {
		blocks = append(blocks, ContentBlock{
			Type:      BlockTypeThinking,
			Text:      reasoning.Text,
			Signature: reasoning.Signature,
		})
	}

	if message != "" {
		blocks = append(blocks, ContentBlock{
			Type: BlockTypeText,
			Text: message,
		})
	}

	for _, tc := range toolCalls {
		blocks = append(blocks, ContentBlock{
			Type:         BlockTypeToolUse,
			ID:           tc.ID,
			Name:         tc.Name,
			ServerOrigin: tc.ServerOrigin,
			Input:        tc.Arguments,
			Status:       StatusToString(tc.Status),
			Shared:       BoolPtr(shared),
		})
	}

	return blocks
}

// toolResultBlocks builds tool_result-side content blocks from ToolRunner output.
// Auto-executed tool rounds are terminal: there is no share/keep-private step,
// so stamp DecidedAt at creation time to reflect that no further approval UI
// is needed. DMs inherit the same treatment (shared=true, decided).
func toolResultBlocks(results []toolrunner.ToolResult, shared bool) []ContentBlock {
	now := model.GetMillis()
	blocks := make([]ContentBlock, len(results))
	for i, tr := range results {
		status := StatusSuccess
		if tr.IsError {
			status = StatusError
		}
		blocks[i] = ContentBlock{
			Type:      BlockTypeToolResult,
			ToolUseID: tr.ToolCallID,
			Content:   tr.Result,
			Status:    status,
			Shared:    BoolPtr(shared),
			DecidedAt: Int64Ptr(now),
		}
	}
	return blocks
}
