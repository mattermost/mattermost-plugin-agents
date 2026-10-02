// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

// textBlocks creates content blocks from a plain text message.
func textBlocks(message string) []ContentBlock {
	if message == "" {
		return nil
	}
	return []ContentBlock{{Type: BlockTypeText, Text: message}}
}

// userBlocksWithAttachments emits a text block followed by image/file blocks
// for each fileID. A failed GetFileInfo is logged and the bad ID is skipped
// so one deleted or unreadable attachment does not poison the whole turn.
func userBlocksWithAttachments(message string, fileIDs []string, mmClient mmapi.Client) []ContentBlock {
	blocks := textBlocks(message)
	if mmClient == nil {
		return blocks
	}
	for _, fileID := range fileIDs {
		fileInfo, err := mmClient.GetFileInfo(fileID)
		if err != nil {
			if !errors.Is(err, mmapi.ErrFileActionForbidden) {
				mmClient.LogError("failed to get file info for user attachment", "error", err, "file_id", fileID)
			}
			continue
		}
		if strings.HasPrefix(fileInfo.MimeType, "image/") {
			blocks = append(blocks, ContentBlock{
				Type:     BlockTypeImage,
				FileID:   fileID,
				Filename: fileInfo.Name,
				MimeType: fileInfo.MimeType,
			})
		} else {
			blocks = append(blocks, ContentBlock{
				Type:     BlockTypeFile,
				FileID:   fileID,
				Filename: fileInfo.Name,
				MimeType: fileInfo.MimeType,
			})
		}
	}
	return blocks
}

// marshalBlocks serializes content blocks to JSON for store.Turn.Content.
func marshalBlocks(blocks []ContentBlock) (json.RawMessage, error) {
	if blocks == nil {
		blocks = []ContentBlock{}
	}
	return json.Marshal(blocks)
}

// UnmarshalBlocks deserializes JSON content from store.Turn.Content.
// Empty content yields nil blocks with no error.
func UnmarshalBlocks(raw json.RawMessage) ([]ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// SequenceBlocks renders segments in arrival order, resolving server_tool
// ids against the snapshot. Missing activity is dropped, not rendered empty.
func SequenceBlocks(segments []llm.TurnSegment, serverTools []llm.ServerToolUse) []ContentBlock {
	byID := make(map[string]*llm.ServerToolUse, len(serverTools))
	for i := range serverTools {
		byID[serverTools[i].ID] = &serverTools[i]
	}

	blocks := make([]ContentBlock, 0, len(segments))
	for _, segment := range segments {
		switch segment.Kind {
		case llm.TurnSegmentText:
			if segment.Text == "" {
				continue
			}
			blocks = append(blocks, ContentBlock{Type: BlockTypeText, Text: segment.Text})
		case llm.TurnSegmentThinking:
			if segment.Text == "" {
				continue
			}
			blocks = append(blocks, ContentBlock{
				Type:      BlockTypeThinking,
				Text:      segment.Text,
				Signature: segment.Signature,
			})
		case llm.TurnSegmentServerTool:
			use, ok := byID[segment.ServerToolID]
			if !ok {
				continue
			}
			activity := use.Clone()
			blocks = append(blocks, ContentBlock{
				Type:       BlockTypeServerToolUse,
				ServerTool: &activity,
			})
		}
	}
	return blocks
}

// toolUseBlocks builds assistant-side content blocks. Tool calls keep their
// resolved status. Empty segments fall back to reasoning → activity → text.
func toolUseBlocks(
	message string,
	reasoning llm.ReasoningData,
	serverTools []llm.ServerToolUse,
	segments []llm.TurnSegment,
	toolCalls []llm.ToolCall,
	shared bool,
) []ContentBlock {
	var blocks []ContentBlock

	if len(segments) > 0 {
		blocks = append(blocks, SequenceBlocks(segments, serverTools)...)
	} else {
		if reasoning.Text != "" {
			blocks = append(blocks, ContentBlock{
				Type:      BlockTypeThinking,
				Text:      reasoning.Text,
				Signature: reasoning.Signature,
			})
		}

		for i := range serverTools {
			serverTool := serverTools[i].Clone()
			blocks = append(blocks, ContentBlock{
				Type:       BlockTypeServerToolUse,
				ServerTool: &serverTool,
			})
		}

		if message != "" {
			blocks = append(blocks, ContentBlock{
				Type: BlockTypeText,
				Text: message,
			})
		}
	}

	// Tool use ends an assistant turn, so calls always come last.
	for _, tc := range toolCalls {
		blocks = append(blocks, ContentBlock{
			Type:             BlockTypeToolUse,
			ID:               tc.ID,
			Name:             tc.Name,
			ServerOrigin:     tc.ServerOrigin,
			Input:            tc.Arguments,
			MCPBareName:      tc.MCPBareName,
			Status:           StatusToString(tc.Status),
			Shared:           new(shared),
			UserInteraction:  tc.UserInteraction,
			WouldAutoExecute: tc.WouldAutoExecute,
			Title:            tc.Title,
			Description:      tc.Description,
		})
	}

	return blocks
}

func heldAnswerBlock(tt toolrunner.ToolTurn) *ContentBlock {
	if tt.HeldAnswer == "" && len(tt.HeldSources) == 0 && len(tt.HeldAnnotations) == 0 {
		return nil
	}
	return &ContentBlock{
		Type:           BlockTypeText,
		Text:           tt.HeldAnswer,
		RequesterOnly:  true,
		Citations:      citationsFromAnnotations(tt.HeldAnnotations),
		SourceChannels: sourceChannelsFromHeld(tt.HeldSources),
	}
}

func citationsFromAnnotations(annotations []llm.Annotation) []Citation {
	if len(annotations) == 0 {
		return nil
	}
	out := make([]Citation, 0, len(annotations))
	for _, ann := range annotations {
		out = append(out, Citation{
			Type:        string(ann.Type),
			URL:         ann.URL,
			Title:       ann.Title,
			StartIndex:  ann.StartIndex,
			EndIndex:    ann.EndIndex,
			ChannelID:   ann.ChannelID,
			ChannelName: ann.ChannelName,
			Private:     ann.Private,
		})
	}
	return out
}

func sourceChannelsFromHeld(sources []toolrunner.HeldSource) []SourceChannel {
	if len(sources) == 0 {
		return nil
	}
	out := make([]SourceChannel, 0, len(sources))
	for _, src := range sources {
		out = append(out, SourceChannel{
			ID:          src.ID,
			Name:        src.Name,
			DisplayName: src.DisplayName,
			Type:        src.Type,
			TeamID:      src.TeamID,
			Private:     src.Private,
		})
	}
	return out
}

// toolResultBlocks builds tool_result-side content blocks from ToolRunner output.
// A round that can be posted now is terminal: stamp DecidedAt so no share
// step remains. A round that cannot be posted leaves DecidedAt nil so the
// requester gets one share decision before any channel answer.
func toolResultBlocks(results []toolrunner.ToolResult, shared bool) []ContentBlock {
	now := model.GetMillis()
	blocks := make([]ContentBlock, len(results))
	for i, tr := range results {
		status := StatusSuccess
		if tr.IsError {
			status = StatusError
		}
		block := ContentBlock{
			Type:      BlockTypeToolResult,
			ToolUseID: tr.ToolCallID,
			Content:   tr.Result,
			Status:    status,
			Shared:    new(shared),
		}
		if shared {
			block.DecidedAt = new(now)
		} else {
			block.AudienceReview = true
		}
		blocks[i] = block
	}
	return blocks
}
