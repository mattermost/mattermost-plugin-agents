// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import "encoding/json"

// Block type constants identify the type of content in a ContentBlock.
const (
	BlockTypeText        = "text"
	BlockTypeThinking    = "thinking"
	BlockTypeToolUse     = "tool_use"
	BlockTypeToolResult  = "tool_result"
	BlockTypeFile        = "file"
	BlockTypeImage       = "image"
	BlockTypeAnnotations = "annotations"
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
	Status       string          `json:"status,omitempty"`
	Shared       *bool           `json:"shared,omitempty"` // pointer to distinguish unset from false

	// ToolResult fields
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"` // tool_result or file content

	// File / Image fields
	Filename string `json:"filename,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileID   string `json:"file_id,omitempty"` // image blocks: references Mattermost file attachment

	// Annotations fields
	WebSearchContext *WebSearchContext `json:"web_search_context,omitempty"`
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

// BoolPtr returns a pointer to the given bool value.
func BoolPtr(b bool) *bool { return &b }

// FilterForNonRequester returns a new slice of content blocks with private
// tool data redacted. Tool use blocks with shared != true have their Input
// field set to nil. Tool result blocks with shared != true have their Content
// field set to empty string. All other block types pass through unchanged.
// The original slice and its elements are never mutated.
// Returns nil if the input is nil.
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
			}
		case BlockTypeToolResult:
			if block.Shared == nil || !*block.Shared {
				result[i].Content = ""
			}
		}
	}
	return result
}
