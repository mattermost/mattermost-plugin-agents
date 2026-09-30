// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// Block type constants identify the type of content in a ContentBlock.
const (
	BlockTypeText        = "text"
	BlockTypeThinking    = "thinking"
	BlockTypeToolUse     = "tool_use"
	BlockTypeToolResult  = "tool_result"
	BlockTypeFile        = "file"
	BlockTypeImage       = "image"
	BlockTypeAnnotations = "annotations"
	// BlockTypeServerToolUse is provider-executed activity. Payload matches
	// the live websocket event so persisted rounds render the same way.
	BlockTypeServerToolUse = "server_tool_use"
)

// Tool call status string constants for JSON/JSONB representation.
const (
	StatusPending      = "pending"
	StatusAccepted     = "accepted"
	StatusRejected     = "rejected"
	StatusError        = "error"
	StatusSuccess      = "success"
	StatusAutoApproved = "auto_approved"
)

// ContentBlock is a flat struct representing any content block type.
// The Type field discriminates which fields are meaningful.
// Uses omitempty on all optional fields so JSON output only includes relevant fields.
type ContentBlock struct {
	Type string `json:"type"`

	// Text / Thinking fields
	Text      string     `json:"text,omitempty"`
	Signature string     `json:"signature,omitempty"` // thinking blocks only
	Citations []Citation `json:"citations,omitempty"` // text blocks only

	// ToolUse fields
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	ServerOrigin string          `json:"server_origin,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	MCPBareName  string          `json:"mcp_bare_name,omitempty"`
	Status       string          `json:"status,omitempty"`
	Shared       *bool           `json:"shared,omitempty"` // pointer to distinguish unset from false

	// Title and Description mirror llm.ToolCall so a reloaded conversation
	// renders the same tool identity the live websocket event showed. Both
	// are visible to non-requesters like Name. Description is not rendered
	// anywhere yet.
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`

	// UserInteraction is the persisted form of llm.Tool.UserInteraction.
	UserInteraction string `json:"user_interaction,omitempty"`

	// WouldAutoExecute marks any pending tool_use block that passed the
	// auto-execution policy (see llm.ToolCall.WouldAutoExecute).
	WouldAutoExecute bool `json:"would_auto_execute,omitempty"`

	// DecidedAt (tool_result blocks) records when the share/keep-private
	// decision was made — either by the user clicking Share or Keep Private
	// in a channel, or implicitly at creation time (DMs, rejected tools,
	// auto_run_everywhere results). A nil value means the result still
	// needs a user decision; any non-nil value means the decision is final
	// and no further approval UI should appear. This distinguishes the
	// "undecided" and "decided to keep private" states, which both present
	// Shared=false but require opposite UI behavior.
	DecidedAt *int64 `json:"decided_at,omitempty"`

	// ToolResult fields
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"` // tool_result or file content

	// File / Image fields
	Filename string `json:"filename,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileID   string `json:"file_id,omitempty"` // image blocks: references Mattermost file attachment

	// Annotations fields
	WebSearchContext *WebSearchContext `json:"web_search_context,omitempty"`

	// ServerTool is streamed activity. No approval flow; shares post-text
	// visibility, so FilterForNonRequester does not redact it.
	ServerTool *llm.ServerToolUse `json:"server_tool,omitempty"`
}

// Citation represents an inline citation in a text block.
type Citation struct {
	Type       string `json:"type"`
	URL        string `json:"url,omitempty"`
	Title      string `json:"title,omitempty"`
	StartIndex int    `json:"start_index"`
	EndIndex   int    `json:"end_index"`
}

// WebSearchContext holds web search metadata for annotations blocks.
type WebSearchContext struct {
	Results         json.RawMessage `json:"results"`
	ExecutedQueries json.RawMessage `json:"executed_queries"`
	Count           int             `json:"count"`
}

// FilterForNonRequester returns a new slice of content blocks with private
// tool data redacted. Tool use blocks with shared != true have Input and
// MCPBareName cleared; tool result blocks with shared != true have Content
// cleared. Tool identity (Name, Title, Description, ServerOrigin) stays
// visible, mirroring redactToolCalls on the live path so both paths render
// identically. The original slice is never mutated; nil in, nil out.
func FilterForNonRequester(blocks []ContentBlock) []ContentBlock {
	if blocks == nil {
		return nil
	}
	result := make([]ContentBlock, len(blocks))
	for i, block := range blocks {
		result[i] = block

		switch block.Type {
		case BlockTypeToolUse:
			if block.Shared == nil || !*block.Shared {
				result[i].Input = nil
				result[i].MCPBareName = ""
			}
		case BlockTypeToolResult:
			if block.Shared == nil || !*block.Shared {
				result[i].Content = ""
			}
		}
	}
	return result
}

// TextContent returns the concatenated text of every text block.
func TextContent(blocks []ContentBlock) string {
	var text strings.Builder
	for _, block := range blocks {
		if block.Type == BlockTypeText {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// WithTextContent returns a new slice of content blocks whose entire text
// content is text: the first text block carries it and every other text block
// is emptied, so TextContent of the result equals text. Blocks that hold no
// text block gain a leading one unless text is empty. Citations index into the
// replaced text and are dropped with it. Blocks of every other type are copied
// unchanged. The original slice is never mutated; nil in, nil out for empty
// text.
func WithTextContent(blocks []ContentBlock, text string) []ContentBlock {
	if blocks == nil && text == "" {
		return nil
	}
	result := make([]ContentBlock, 0, len(blocks)+1)
	placed := false
	for _, block := range blocks {
		if block.Type == BlockTypeText {
			block.Text = ""
			block.Citations = nil
			if !placed {
				block.Text = text
				placed = true
			}
		}
		result = append(result, block)
	}
	if !placed && text != "" {
		result = append([]ContentBlock{{Type: BlockTypeText, Text: text}}, result...)
	}
	return result
}

// SanitizeForDisplay returns a new slice of content blocks with LLM-generated
// and MCP-server-supplied string fields sanitized against Unicode bidi/spoofing
// attacks: Input, Title, and Description on tool_use blocks, Content on
// tool_result blocks. Title/Description are already sanitized at capture; this
// is defense in depth that also covers older persisted turns. The original
// slice is never mutated; nil in, nil out.
func SanitizeForDisplay(blocks []ContentBlock) []ContentBlock {
	if blocks == nil {
		return nil
	}
	result := make([]ContentBlock, len(blocks))
	for i, block := range blocks {
		result[i] = block

		switch block.Type {
		case BlockTypeToolUse:
			if len(block.Input) > 0 {
				result[i].Input = json.RawMessage(llm.SanitizeNonPrintableChars(string(block.Input)))
			}
			if block.Title != "" {
				result[i].Title = llm.SanitizeNonPrintableChars(block.Title)
			}
			if block.Description != "" {
				result[i].Description = llm.SanitizeNonPrintableChars(block.Description)
			}
		case BlockTypeToolResult:
			if block.Content != "" {
				result[i].Content = llm.SanitizeNonPrintableChars(block.Content)
			}
		case BlockTypeServerToolUse:
			if block.ServerTool != nil {
				st := block.ServerTool.Clone()
				st.ProviderRoute = ""
				st.Sanitize()
				result[i].ServerTool = &st
			}
		}
	}
	return result
}
