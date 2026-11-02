// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const DefaultMaxFileSize = int64(5 * 1024 * 1024)

// InlineFileMaxBytes is the upper bound on readable text that gets inlined
// directly into a turn. Files at or below this size are cheap enough to inline
// (and save the LLM a tool round trip); larger files are surfaced as metadata
// only and fetched on demand via the read_file tool. ~8 KiB ≈ 2000 tokens.
const InlineFileMaxBytes = int64(8 * 1024)

// UnsharedToolResultRedaction replaces tool_result content the requester
// decided not to share, preserving the tool_use/tool_result pairing required
// by LLM providers. Unshared tool_use arguments are redacted alongside it.
const UnsharedToolResultRedaction = "[arguments and result not shared by user]"

// PendingShareToolResultRedaction replaces tool_result content whose share
// decision the requester has not made yet.
const PendingShareToolResultRedaction = "[result pending the user's decision to share it]"

// NotExecutedToolResult is replayed as the result of a tool call that was
// still pending or accepted when the turn ended, so the model does not read
// it as a call that ran and returned nothing.
const NotExecutedToolResult = "Not executed: the user stopped the response or moved on without approving this tool call."

// unsharedToolUseArgumentsRedaction replaces tool_use arguments the requester
// has not shared. Empty JSON keeps the call well-formed for providers that
// require a JSON object while stripping any sensitive parameter values.
var unsharedToolUseArgumentsRedaction = json.RawMessage("{}")

// PostConversionOptions configures how BlocksToPost converts content blocks.
type PostConversionOptions struct {
	RedactUnshared bool
	MMClient       mmapi.Client
	// UserID is the user whose channel read access gates every file and image
	// block. An empty UserID withholds all of them.
	UserID       string
	EnableVision bool
	MaxFileSize  int64
	ToolStore    *llm.ToolStore
}

// canReadFileChannel reports whether opts.UserID may read the channel the file
// was posted in. Attachments can come from a channel other than the
// conversation's own (e.g. a meeting's captions file), so access is re-checked
// on every replay.
func canReadFileChannel(opts PostConversionOptions, fileInfo *model.FileInfo) bool {
	if opts.UserID == "" || fileInfo.ChannelId == "" {
		return false
	}
	return opts.MMClient.HasPermissionToChannel(opts.UserID, fileInfo.ChannelId, model.PermissionReadChannel)
}

// BlocksToPost converts a slice of content blocks and a role string into an llm.Post.
// When opts.RedactUnshared is true, tool_result content whose Shared flag is not
// true is replaced with UnsharedToolResultRedaction (or
// PendingShareToolResultRedaction before the share decision), and tool_use arguments
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
	var notices []string
	resultIDs := make(map[string]bool)

	for _, block := range blocks {
		switch block.Type {
		case BlockTypeText:
			textParts = append(textParts, block.Text)
			if block.Text != "" {
				post.AssistantSegments = append(post.AssistantSegments, llm.TurnSegment{
					Kind: llm.TurnSegmentText,
					Text: block.Text,
				})
			}

		case BlockTypeThinking:
			// Last thinking block wins. Persisted thinking is cleared by the
			// request builder because signed provider blocks cannot be rebuilt
			// safely from normalized storage.
			post.Reasoning = block.Text
			post.ReasoningSignature = block.Signature

		case BlockTypeServerToolUse:
			if block.ServerTool == nil || block.ServerTool.ID == "" {
				continue
			}
			activity := block.ServerTool.Clone()
			post.ServerTools = append(post.ServerTools, activity)
			post.AssistantSegments = append(post.AssistantSegments, llm.TurnSegment{
				Kind:         llm.TurnSegmentServerTool,
				ServerToolID: activity.ID,
			})

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
				Title:        block.Title,
				Description:  block.Description,
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
				if block.DecidedAt == nil {
					content = PendingShareToolResultRedaction
				}
			}
			resultIDs[block.ToolUseID] = true
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
			if opts.MMClient == nil {
				notices = append(notices, format.UnavailableAttachment("", format.AttachmentLoadFailed))
				continue
			}
			// Fetched even with vision off: it re-checks the requester's access
			// before the placeholder names the file.
			fileInfo, err := opts.MMClient.GetFileInfo(block.FileID)
			if err != nil || fileInfo == nil {
				notices = append(notices, attachmentLoadNotice(opts.MMClient, block, err, "failed to get file info for image attachment"))
				continue
			}
			if !opts.EnableVision {
				notices = append(notices, format.UnavailableAttachment(fileInfo.Name, format.AttachmentImageInputDisabled))
				continue
			}
			if !canReadFileChannel(opts, fileInfo) {
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
				notices = append(notices, attachmentLoadNotice(opts.MMClient, block, err, "failed to get file for image attachment"))
				continue
			}
			data, err := io.ReadAll(reader)
			if closeErr := reader.Close(); closeErr != nil {
				opts.MMClient.LogError("failed to close image attachment reader", "error", closeErr)
			}
			if err != nil {
				notices = append(notices, attachmentLoadNotice(opts.MMClient, block, err, "failed to read image attachment"))
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
				notices = append(notices, format.UnavailableAttachment("", format.AttachmentLoadFailed))
				continue
			}
			fileInfo, err := opts.MMClient.GetFileInfo(block.FileID)
			if err != nil || fileInfo == nil {
				notices = append(notices, attachmentLoadNotice(opts.MMClient, block, err, "failed to get file info for file attachment"))
				continue
			}
			if !canReadFileChannel(opts, fileInfo) {
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
					notices = append(notices, attachmentLoadNotice(opts.MMClient, block, err, "failed to get file for file attachment"))
					continue
				}
				body, err := io.ReadAll(io.LimitReader(reader, effectiveMax))
				if closeErr := reader.Close(); closeErr != nil {
					opts.MMClient.LogError("failed to close file attachment reader", "error", closeErr)
				}
				if err != nil {
					notices = append(notices, attachmentLoadNotice(opts.MMClient, block, err, "failed to read file content"))
					continue
				}
				content = string(body)
			}

			fileContents = append(fileContents, "File Name: "+fileInfo.Name+"\nContent: "+content)

		case BlockTypeAnnotations:
			// Not mapped to llm.Post
		}
	}

	for i := range post.ToolUse {
		tc := &post.ToolUse[i]
		if resultIDs[tc.ID] {
			continue
		}
		if tc.Status == llm.ToolCallStatusPending || tc.Status == llm.ToolCallStatusAccepted {
			tc.Result = NotExecutedToolResult
			tc.Status = llm.ToolCallStatusRejected
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
	if len(notices) > 0 {
		if post.Message != "" {
			post.Message += "\n"
		}
		post.Message += strings.Join(notices, "\n")
	}
	if len(post.ServerTools) == 0 {
		post.AssistantSegments = nil
	}

	return post
}

// attachmentLoadNotice logs a failed attachment read and returns its
// placeholder. A forbidden file is an expected access denial: it is neither
// logged nor named.
func attachmentLoadNotice(mmClient mmapi.Client, block ContentBlock, err error, logMsg string) string {
	if errors.Is(err, mmapi.ErrFileActionForbidden) {
		return format.UnavailableAttachment("", format.AttachmentForbidden)
	}
	if err != nil {
		mmClient.LogError(logMsg, "error", err)
	}
	return format.UnavailableAttachment(block.Filename, format.AttachmentLoadFailed)
}

func enrichToolCallFromStore(toolCall *llm.ToolCall, toolStore *llm.ToolStore) {
	llm.EnrichToolCall(toolCall, toolStore, llm.EnrichToolCallOptions{
		OverwriteDescription: true,
		BareNameFallback:     true,
	})
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
