// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// BlocksToPost converts a slice of content blocks and a role string into an llm.Post.
// This is used when reading turns from the database to build a CompletionRequest.
func BlocksToPost(blocks []ContentBlock, role string) llm.Post {
	post := llm.Post{
		Role: RoleFromString(role),
	}

	var textParts []string

	for _, block := range blocks {
		switch block.Type {
		case BlockTypeText:
			textParts = append(textParts, block.Text)

		case BlockTypeThinking:
			// Last thinking block wins
			post.Reasoning = block.Text
			post.ReasoningSignature = block.Signature

		case BlockTypeToolUse:
			post.ToolUse = append(post.ToolUse, llm.ToolCall{
				ID:           block.ID,
				Name:         block.Name,
				ServerOrigin: block.ServerOrigin,
				Arguments:    block.Input,
				Status:       StatusFromString(block.Status),
			})

		case BlockTypeToolResult:
			merged := false
			for i := range post.ToolUse {
				if post.ToolUse[i].ID == block.ToolUseID {
					post.ToolUse[i].Result = block.Content
					merged = true
					break
				}
			}
			if !merged {
				post.ToolUse = append(post.ToolUse, llm.ToolCall{
					ID:     block.ToolUseID,
					Result: block.Content,
					Status: StatusFromString(block.Status),
				})
			}

		case BlockTypeFile, BlockTypeImage, BlockTypeAnnotations:
			// Not mapped to llm.Post
		}
	}

	if len(textParts) > 0 {
		post.Message = strings.Join(textParts, "\n")
	}

	return post
}

// PostToBlocks converts an llm.Post into a slice of content blocks.
// This is used when writing turns to the database from stream events or the current llm.Post model.
// The shared parameter controls whether tool blocks get shared=true or shared=false.
func PostToBlocks(post llm.Post, shared bool) []ContentBlock {
	var blocks []ContentBlock

	// 1. Thinking block (if Reasoning is non-empty)
	if post.Reasoning != "" {
		blocks = append(blocks, ContentBlock{
			Type:      BlockTypeThinking,
			Text:      post.Reasoning,
			Signature: post.ReasoningSignature,
		})
	}

	// 2. Text block (if Message is non-empty)
	if post.Message != "" {
		blocks = append(blocks, ContentBlock{
			Type: BlockTypeText,
			Text: post.Message,
		})
	}

	// 3. For each ToolUse: a tool_use block, optionally followed by a tool_result block
	for _, tc := range post.ToolUse {
		blocks = append(blocks, ContentBlock{
			Type:         BlockTypeToolUse,
			ID:           tc.ID,
			Name:         tc.Name,
			ServerOrigin: tc.ServerOrigin,
			Input:        tc.Arguments,
			Status:       StatusToString(tc.Status),
			Shared:       BoolPtr(shared),
		})

		if tc.Result != "" {
			blocks = append(blocks, ContentBlock{
				Type:      BlockTypeToolResult,
				ToolUseID: tc.ID,
				Content:   tc.Result,
				Status:    StatusToString(tc.Status),
				Shared:    BoolPtr(shared),
			})
		}
	}

	return blocks
}

// RoleFromString converts a turn role string to an llm.PostRole.
func RoleFromString(role string) llm.PostRole {
	switch role {
	case "user":
		return llm.PostRoleUser
	case "assistant":
		return llm.PostRoleBot
	case "tool_result":
		return llm.PostRoleUser
	case "system":
		return llm.PostRoleSystem
	default:
		return llm.PostRoleUser
	}
}

// RoleToString converts an llm.PostRole to a turn role string.
func RoleToString(role llm.PostRole) string {
	switch role {
	case llm.PostRoleUser:
		return "user"
	case llm.PostRoleBot:
		return "assistant"
	case llm.PostRoleSystem:
		return "system"
	default:
		return "user"
	}
}

// StatusFromString converts a status string to an llm.ToolCallStatus.
func StatusFromString(s string) llm.ToolCallStatus {
	switch s {
	case StatusPending:
		return llm.ToolCallStatusPending
	case StatusAccepted:
		return llm.ToolCallStatusAccepted
	case StatusRejected:
		return llm.ToolCallStatusRejected
	case StatusError:
		return llm.ToolCallStatusError
	case StatusSuccess:
		return llm.ToolCallStatusSuccess
	case StatusAutoApproved:
		return llm.ToolCallStatusAutoApproved
	default:
		return llm.ToolCallStatusPending
	}
}

// StatusToString converts an llm.ToolCallStatus to a status string.
func StatusToString(s llm.ToolCallStatus) string {
	switch s {
	case llm.ToolCallStatusPending:
		return StatusPending
	case llm.ToolCallStatusAccepted:
		return StatusAccepted
	case llm.ToolCallStatusRejected:
		return StatusRejected
	case llm.ToolCallStatusError:
		return StatusError
	case llm.ToolCallStatusSuccess:
		return StatusSuccess
	case llm.ToolCallStatusAutoApproved:
		return StatusAutoApproved
	default:
		return StatusPending
	}
}
