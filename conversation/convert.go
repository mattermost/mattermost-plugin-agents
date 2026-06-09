// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
)

const DefaultMaxFileSize = int64(5 * 1024 * 1024)

// InlineFileMaxBytes is the upper bound on readable text that gets inlined
// directly into a turn. Files at or below this size are cheap enough to inline
// (and save the LLM a tool round trip); larger files are surfaced as metadata
// only and fetched on demand via the read_file tool. ~8 KiB ≈ 2000 tokens.
const InlineFileMaxBytes = int64(8 * 1024)

// UnsharedToolResultRedaction replaces tool_result content the requester has
// not shared, preserving the tool_use/tool_result pairing required by LLM
// providers.
const UnsharedToolResultRedaction = "[result not shared by user]"

// unsharedToolUseArgumentsRedaction replaces tool_use arguments the requester
// has not shared. Empty JSON keeps the call well-formed for providers that
// require a JSON object while stripping any sensitive parameter values.
var unsharedToolUseArgumentsRedaction = json.RawMessage("{}")

// PostConversionOptions configures how BlocksToPost converts content blocks.
type PostConversionOptions struct {
	RedactUnshared bool
	MMClient       mmapi.Client
	EnableVision   bool
	MaxFileSize    int64
	ToolStore      *llm.ToolStore
}

// BlocksToPost converts a slice of content blocks and a role string into an llm.Post.
// When opts.RedactUnshared is true, tool_result content whose Shared flag is not
// true is replaced with UnsharedToolResultRedaction, and tool_use arguments
// whose Shared flag is not true are replaced with an empty JSON object so the
// LLM cannot paraphrase private tool parameters into a channel-visible reply.
func BlocksToPost(
	blocks []ContentBlock,
	role string,
	opts PostConversionOptions,
) llm.Post {
	post := llm.Post{
		Role: RoleFromString(role),
	}

	effectiveMax := opts.MaxFileSize
	if effectiveMax <= 0 {
		effectiveMax = DefaultMaxFileSize
	}

	var textParts []string
	var fileContents []string
	var descriptors []string

	for _, block := range blocks {
		switch block.Type {
		case BlockTypeText:
			textParts = append(textParts, block.Text)

		case BlockTypeThinking:
			// Last thinking block wins
			post.Reasoning = block.Text
			post.ReasoningSignature = block.Signature

		case BlockTypeToolUse:
			arguments := block.Input
			redactToolUse := opts.RedactUnshared && (block.Shared == nil || !*block.Shared)
			if redactToolUse {
				arguments = unsharedToolUseArgumentsRedaction
			}
			toolCall := llm.ToolCall{
				ID:           block.ID,
				Name:         block.Name,
				ServerOrigin: block.ServerOrigin,
				Arguments:    arguments,
				MCPBareName:  block.MCPBareName,
				Status:       StatusFromString(block.Status),
			}
			if redactToolUse {
				toolCall.MCPBareName = ""
			} else {
				enrichToolCallFromStore(&toolCall, opts.ToolStore)
			}
			post.ToolUse = append(post.ToolUse, toolCall)

		case BlockTypeToolResult:
			content := block.Content
			if opts.RedactUnshared && (block.Shared == nil || !*block.Shared) {
				content = UnsharedToolResultRedaction
			}
			merged := false
			for i := range post.ToolUse {
				if post.ToolUse[i].ID == block.ToolUseID {
					post.ToolUse[i].Result = content
					merged = true
					break
				}
			}
			if !merged {
				post.ToolUse = append(post.ToolUse, llm.ToolCall{
					ID:     block.ToolUseID,
					Result: content,
					Status: StatusFromString(block.Status),
				})
			}

		case BlockTypeImage:
			if !opts.EnableVision {
				continue
			}
			if opts.MMClient == nil {
				continue
			}
			fileInfo, err := opts.MMClient.GetFileInfo(block.FileID)
			if err != nil {
				opts.MMClient.LogError("failed to get file info for image attachment", "error", err)
				continue
			}
			if !llm.IsSupportedImageMimeType(fileInfo.MimeType) {
				post.Files = append(post.Files, llm.File{
					MimeType: fileInfo.MimeType,
					Size:     fileInfo.Size,
				})
				continue
			}
			reader, err := opts.MMClient.GetFile(block.FileID)
			if err != nil {
				opts.MMClient.LogError("failed to get file for image attachment", "error", err)
				continue
			}
			data, err := io.ReadAll(reader)
			if closeErr := reader.Close(); closeErr != nil {
				opts.MMClient.LogError("failed to close image attachment reader", "error", closeErr)
			}
			if err != nil {
				opts.MMClient.LogError("failed to read image attachment", "error", err)
				continue
			}
			post.Files = append(post.Files, llm.File{
				MimeType: fileInfo.MimeType,
				Size:     fileInfo.Size,
				Data:     data,
				Reader:   bytes.NewReader(data),
			})

		case BlockTypeFile:
			if opts.MMClient == nil {
				continue
			}
			fileInfo, err := opts.MMClient.GetFileInfo(block.FileID)
			if err != nil {
				opts.MMClient.LogError("failed to get file info for file attachment", "error", err)
				continue
			}

			// Decide inline vs descriptor from metadata alone (no download):
			// readable text within InlineFileMaxBytes is inlined; larger files
			// and binaries with no extractable text become a read-on-demand
			// descriptor.
			extracted := strings.TrimSpace(fileInfo.Content)
			isText := strings.HasPrefix(fileInfo.MimeType, "text/")
			inlineSize := int64(-1) // -1 means no readable text → descriptor only
			switch {
			case extracted != "":
				inlineSize = int64(len(extracted))
			case isText:
				inlineSize = fileInfo.Size
			}

			if inlineSize < 0 || inlineSize > InlineFileMaxBytes {
				var b strings.Builder
				format.WriteFileDescriptor(&b, format.FileDescriptorEntry{
					Number:   len(descriptors) + 1,
					FileInfo: fileInfo,
				})
				descriptors = append(descriptors, strings.TrimRight(b.String(), "\n"))
				continue
			}

			content := extracted
			if content == "" {
				reader, err := opts.MMClient.GetFile(block.FileID)
				if err != nil {
					opts.MMClient.LogError("failed to get file for file attachment", "error", err)
					continue
				}
				body, err := io.ReadAll(io.LimitReader(reader, effectiveMax))
				if closeErr := reader.Close(); closeErr != nil {
					opts.MMClient.LogError("failed to close file attachment reader", "error", closeErr)
				}
				if err != nil {
					opts.MMClient.LogError("failed to read file content", "error", err)
					continue
				}
				content = string(body)
			}

			fileContents = append(fileContents, "File Name: "+fileInfo.Name+"\nContent: "+content)

		case BlockTypeAnnotations:
			// Not mapped to llm.Post
		}
	}

	if len(textParts) > 0 {
		post.Message = strings.Join(textParts, "\n")
	}
	if len(fileContents) > 0 {
		post.Message += "\nAttached File Contents:\n" + strings.Join(fileContents, "\n\n")
	}
	if len(descriptors) > 0 {
		post.Message += "\nAttached files (call the read_file tool with the File ID to read their contents):\n" + strings.Join(descriptors, "\n\n")
	}

	return post
}

func enrichToolCallFromStore(toolCall *llm.ToolCall, toolStore *llm.ToolStore) {
	llm.EnrichToolCall(toolCall, toolStore, llm.EnrichToolCallOptions{
		OverwriteDescription: true,
		BareNameFallback:     true,
	})
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
			MCPBareName:  tc.MCPBareName,
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
