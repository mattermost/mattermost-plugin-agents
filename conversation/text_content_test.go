// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTextContent(t *testing.T) {
	tests := []struct {
		name     string
		blocks   []ContentBlock
		expected string
	}{
		{
			name:     "nil blocks",
			blocks:   nil,
			expected: "",
		},
		{
			name:     "empty slice",
			blocks:   []ContentBlock{},
			expected: "",
		},
		{
			name: "single text block",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "hello"},
			},
			expected: "hello",
		},
		{
			name: "several text blocks concatenate in order",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "hello "},
				{Type: BlockTypeText, Text: "world"},
			},
			expected: "hello world",
		},
		{
			name: "thinking text is not part of the text content",
			blocks: []ContentBlock{
				{Type: BlockTypeThinking, Text: "reasoning"},
				{Type: BlockTypeText, Text: "answer"},
			},
			expected: "answer",
		},
		{
			name: "no text blocks",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search"},
				{Type: BlockTypeImage, FileID: "file1"},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, TextContent(tt.blocks))
		})
	}
}

func TestWithTextContent(t *testing.T) {
	tests := []struct {
		name     string
		blocks   []ContentBlock
		text     string
		validate func(t *testing.T, original, result []ContentBlock)
	}{
		{
			name:   "nil blocks and empty text",
			blocks: nil,
			text:   "",
			validate: func(t *testing.T, _, result []ContentBlock) {
				assert.Nil(t, result)
			},
		},
		{
			name:   "nil blocks and non-empty text",
			blocks: nil,
			text:   "replacement",
			validate: func(t *testing.T, _, result []ContentBlock) {
				require.Len(t, result, 1)
				assert.Equal(t, BlockTypeText, result[0].Type)
				assert.Equal(t, "replacement", TextContent(result))
			},
		},
		{
			name:   "empty slice and empty text",
			blocks: []ContentBlock{},
			text:   "",
			validate: func(t *testing.T, _, result []ContentBlock) {
				assert.Empty(t, TextContent(result))
			},
		},
		{
			name: "several text blocks collapse into the first",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "first "},
				{Type: BlockTypeText, Text: "second"},
			},
			text: "replacement",
			validate: func(t *testing.T, _, result []ContentBlock) {
				assert.Equal(t, "replacement", TextContent(result))
				require.Len(t, result, 2)
				assert.Equal(t, "replacement", result[0].Text)
				assert.Empty(t, result[1].Text)
			},
		},
		{
			name: "citations are dropped with the text they index into",
			blocks: []ContentBlock{
				{
					Type:      BlockTypeText,
					Text:      "cited answer",
					Citations: []Citation{{Type: "url_citation", URL: "https://example.com", StartIndex: 0, EndIndex: 5}},
				},
			},
			text: "",
			validate: func(t *testing.T, _, result []ContentBlock) {
				require.Len(t, result, 1)
				assert.Empty(t, result[0].Text)
				assert.Empty(t, result[0].Citations)
			},
		},
		{
			name: "blocks of other types are carried over untouched",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "question"},
				{Type: BlockTypeImage, FileID: "file1", Filename: "shot.png", MimeType: "image/png"},
				{Type: BlockTypeThinking, Text: "reasoning"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"x"}`)},
			},
			text: "",
			validate: func(t *testing.T, _, result []ContentBlock) {
				require.Len(t, result, 4)
				assert.Empty(t, result[0].Text)
				assert.Equal(t, "file1", result[1].FileID)
				assert.Equal(t, "shot.png", result[1].Filename)
				assert.Equal(t, "reasoning", result[2].Text)
				assert.Equal(t, "search", result[3].Name)
				assert.JSONEq(t, `{"q":"x"}`, string(result[3].Input))
			},
		},
		{
			name: "a turn with no text block gains one only for non-empty text",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search"},
			},
			text: "",
			validate: func(t *testing.T, _, result []ContentBlock) {
				require.Len(t, result, 1)
				assert.Equal(t, BlockTypeToolUse, result[0].Type)
			},
		},
		{
			name: "the original blocks are left as they were",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "question"},
			},
			text: "replacement",
			validate: func(t *testing.T, original, result []ContentBlock) {
				assert.Equal(t, "question", original[0].Text)
				assert.Equal(t, "replacement", TextContent(result))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WithTextContent(tt.blocks, tt.text)
			assert.Equal(t, tt.text, TextContent(result),
				"the text content of the result is the text that was written")
			tt.validate(t, tt.blocks, result)
		})
	}
}

// TestUnmarshalBlocksRoundTripsStoredContent covers the stored shapes the turn
// content column can hold.
func TestUnmarshalBlocksRoundTripsStoredContent(t *testing.T) {
	tests := []struct {
		name      string
		raw       json.RawMessage
		expectErr bool
		expected  []ContentBlock
	}{
		{
			name:     "empty content",
			raw:      json.RawMessage(""),
			expected: nil,
		},
		{
			name:     "json null",
			raw:      json.RawMessage("null"),
			expected: nil,
		},
		{
			name:     "empty array",
			raw:      json.RawMessage("[]"),
			expected: []ContentBlock{},
		},
		{
			name:      "malformed json",
			raw:       json.RawMessage("{not json"),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, err := UnmarshalBlocks(tt.raw)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, blocks)
			assert.Empty(t, TextContent(blocks))
		})
	}
}
