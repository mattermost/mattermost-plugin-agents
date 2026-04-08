// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
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
func toolUseBlocks(
	message string,
	reasoning llm.ReasoningData,
	toolCalls []llm.ToolCall,
	results []toolrunner.ToolResult,
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

	for i, tc := range toolCalls {
		status := StatusSuccess
		if i < len(results) && results[i].IsError {
			status = StatusError
		}
		blocks = append(blocks, ContentBlock{
			Type:   BlockTypeToolUse,
			ID:     tc.ID,
			Name:   tc.Name,
			Input:  tc.Arguments,
			Status: status,
			Shared: BoolPtr(shared),
		})
	}

	return blocks
}

// toolResultBlocks builds tool_result-side content blocks from ToolRunner output.
func toolResultBlocks(results []toolrunner.ToolResult, shared bool) []ContentBlock {
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
		}
	}
	return blocks
}
