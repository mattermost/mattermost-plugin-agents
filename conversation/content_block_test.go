// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentBlockMarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		block    ContentBlock
		expected string
	}{
		{
			name:     "text block",
			block:    ContentBlock{Type: BlockTypeText, Text: "Hello world"},
			expected: `{"type":"text","text":"Hello world"}`,
		},
		{
			name: "text block with citations",
			block: ContentBlock{
				Type: BlockTypeText,
				Text: "According to recent results, the answer is 42.",
				Citations: []Citation{
					{Type: "url_citation", URL: "https://example.com", Title: "Source", StartIndex: 0, EndIndex: 42},
				},
			},
			expected: `{"type":"text","text":"According to recent results, the answer is 42.","citations":[{"type":"url_citation","url":"https://example.com","title":"Source","start_index":0,"end_index":42}]}`,
		},
		{
			name: "thinking block with signature",
			block: ContentBlock{
				Type:      BlockTypeThinking,
				Text:      "Let me think about this...",
				Signature: "sig_abc123",
			},
			expected: `{"type":"thinking","text":"Let me think about this...","signature":"sig_abc123"}`,
		},
		{
			name: "tool_use block",
			block: ContentBlock{
				Type:         BlockTypeToolUse,
				ID:           "tc_01",
				Name:         "get_weather",
				ServerOrigin: "https://mcp.example.com",
				Input:        json.RawMessage(`{"city":"NYC"}`),
				Status:       StatusSuccess,
				Shared:       BoolPtr(true),
			},
			expected: `{"type":"tool_use","id":"tc_01","name":"get_weather","server_origin":"https://mcp.example.com","input":{"city":"NYC"},"status":"success","shared":true}`,
		},
		{
			name: "tool_result block",
			block: ContentBlock{
				Type:      BlockTypeToolResult,
				ToolUseID: "tc_01",
				Content:   "72F, sunny",
				Status:    StatusSuccess,
				Shared:    BoolPtr(true),
			},
			expected: `{"type":"tool_result","tool_use_id":"tc_01","content":"72F, sunny","status":"success","shared":true}`,
		},
		{
			name: "file block",
			block: ContentBlock{
				Type:     BlockTypeFile,
				Filename: "report.txt",
				MimeType: "text/plain",
				Content:  "file contents here",
			},
			expected: `{"type":"file","content":"file contents here","filename":"report.txt","mime_type":"text/plain"}`,
		},
		{
			name: "image block",
			block: ContentBlock{
				Type:     BlockTypeImage,
				Filename: "screenshot.png",
				MimeType: "image/png",
				FileID:   "abc123",
			},
			expected: `{"type":"image","filename":"screenshot.png","mime_type":"image/png","file_id":"abc123"}`,
		},
		{
			name: "annotations block",
			block: ContentBlock{
				Type: BlockTypeAnnotations,
				WebSearchContext: &WebSearchContext{
					Results:         json.RawMessage(`[{"url":"https://example.com"}]`),
					ExecutedQueries: json.RawMessage(`["weather NYC"]`),
					Count:           1,
				},
			},
			expected: `{"type":"annotations","web_search_context":{"results":[{"url":"https://example.com"}],"executed_queries":["weather NYC"],"count":1}}`,
		},
		{
			name: "tool_use block with shared false",
			block: ContentBlock{
				Type:   BlockTypeToolUse,
				ID:     "tc_02",
				Name:   "read_file",
				Input:  json.RawMessage(`{"path":"/etc/passwd"}`),
				Status: StatusPending,
				Shared: BoolPtr(false),
			},
			expected: `{"type":"tool_use","id":"tc_02","name":"read_file","input":{"path":"/etc/passwd"},"status":"pending","shared":false}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.block)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(data))

			var roundTripped ContentBlock
			err = json.Unmarshal(data, &roundTripped)
			require.NoError(t, err)
			assert.Equal(t, tt.block, roundTripped)
		})
	}
}

func TestContentBlockSliceRoundTrip(t *testing.T) {
	blocks := []ContentBlock{
		{Type: BlockTypeThinking, Text: "thinking...", Signature: "sig"},
		{Type: BlockTypeText, Text: "Hello"},
		{Type: BlockTypeToolUse, ID: "tc_01", Name: "search", Input: json.RawMessage(`{}`), Status: StatusPending, Shared: BoolPtr(false)},
		{Type: BlockTypeToolResult, ToolUseID: "tc_01", Content: "result", Status: StatusSuccess, Shared: BoolPtr(true)},
		{Type: BlockTypeFile, Filename: "f.txt", MimeType: "text/plain", Content: "data"},
		{Type: BlockTypeImage, Filename: "img.png", MimeType: "image/png", FileID: "file1"},
		{Type: BlockTypeAnnotations, WebSearchContext: &WebSearchContext{Count: 3, Results: json.RawMessage(`[]`), ExecutedQueries: json.RawMessage(`[]`)}},
	}

	data, err := json.Marshal(blocks)
	require.NoError(t, err)

	var roundTripped []ContentBlock
	err = json.Unmarshal(data, &roundTripped)
	require.NoError(t, err)
	assert.Equal(t, blocks, roundTripped)
}

func TestContentBlockUnknownTypePreserved(t *testing.T) {
	input := `{"type":"future_block","text":"some data"}`
	var block ContentBlock
	err := json.Unmarshal([]byte(input), &block)
	require.NoError(t, err)
	assert.Equal(t, "future_block", block.Type)
	assert.Equal(t, "some data", block.Text)

	data, err := json.Marshal(block)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"future_block","text":"some data"}`, string(data))
}

func TestFilterForNonRequester(t *testing.T) {
	tests := []struct {
		name     string
		blocks   []ContentBlock
		expected []ContentBlock
	}{
		{
			name: "strips input from tool_use where shared is nil",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"secret"}`), Status: StatusSuccess},
			},
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: nil, Status: StatusSuccess},
			},
		},
		{
			name: "strips input from tool_use where shared is false",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"secret"}`), Status: StatusSuccess, Shared: BoolPtr(false)},
			},
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: nil, Status: StatusSuccess, Shared: BoolPtr(false)},
			},
		},
		{
			name: "strips content from tool_result where shared is nil",
			blocks: []ContentBlock{
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "sensitive data", Status: StatusSuccess},
			},
			expected: []ContentBlock{
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "", Status: StatusSuccess},
			},
		},
		{
			name: "strips content from tool_result where shared is false",
			blocks: []ContentBlock{
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "sensitive data", Status: StatusSuccess, Shared: BoolPtr(false)},
			},
			expected: []ContentBlock{
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "", Status: StatusSuccess, Shared: BoolPtr(false)},
			},
		},
		{
			name: "leaves shared=true tool blocks untouched",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"query"}`), Status: StatusSuccess, Shared: BoolPtr(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "public result", Status: StatusSuccess, Shared: BoolPtr(true)},
			},
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"query"}`), Status: StatusSuccess, Shared: BoolPtr(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "public result", Status: StatusSuccess, Shared: BoolPtr(true)},
			},
		},
		{
			name: "leaves text thinking file image annotations untouched",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "hello"},
				{Type: BlockTypeThinking, Text: "thinking", Signature: "sig"},
				{Type: BlockTypeFile, Filename: "f.txt", Content: "data"},
				{Type: BlockTypeImage, FileID: "img1"},
				{Type: BlockTypeAnnotations, WebSearchContext: &WebSearchContext{Count: 1}},
			},
			expected: []ContentBlock{
				{Type: BlockTypeText, Text: "hello"},
				{Type: BlockTypeThinking, Text: "thinking", Signature: "sig"},
				{Type: BlockTypeFile, Filename: "f.txt", Content: "data"},
				{Type: BlockTypeImage, FileID: "img1"},
				{Type: BlockTypeAnnotations, WebSearchContext: &WebSearchContext{Count: 1}},
			},
		},
		{
			name: "mixed blocks only private tool blocks are redacted",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "response"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "tool", Input: json.RawMessage(`{"x":1}`), Status: StatusSuccess, Shared: BoolPtr(false)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "secret", Status: StatusSuccess, Shared: BoolPtr(false)},
				{Type: BlockTypeToolUse, ID: "tc2", Name: "tool2", Input: json.RawMessage(`{"y":2}`), Status: StatusSuccess, Shared: BoolPtr(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc2", Content: "public", Status: StatusSuccess, Shared: BoolPtr(true)},
			},
			expected: []ContentBlock{
				{Type: BlockTypeText, Text: "response"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "tool", Input: nil, Status: StatusSuccess, Shared: BoolPtr(false)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "", Status: StatusSuccess, Shared: BoolPtr(false)},
				{Type: BlockTypeToolUse, ID: "tc2", Name: "tool2", Input: json.RawMessage(`{"y":2}`), Status: StatusSuccess, Shared: BoolPtr(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc2", Content: "public", Status: StatusSuccess, Shared: BoolPtr(true)},
			},
		},
		{
			name:     "empty slice returns empty slice",
			blocks:   []ContentBlock{},
			expected: []ContentBlock{},
		},
		{
			name:     "nil input returns nil",
			blocks:   nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterForNonRequester(tt.blocks)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFilterForNonRequesterDoesNotMutateOriginal(t *testing.T) {
	original := []ContentBlock{
		{Type: BlockTypeToolUse, ID: "tc1", Input: json.RawMessage(`{"secret":"val"}`), Status: StatusSuccess, Shared: BoolPtr(false)},
		{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "secret result", Status: StatusSuccess, Shared: BoolPtr(false)},
	}

	originalInputCopy := make(json.RawMessage, len(original[0].Input))
	copy(originalInputCopy, original[0].Input)
	originalContentCopy := original[1].Content

	_ = FilterForNonRequester(original)

	assert.Equal(t, originalInputCopy, original[0].Input)
	assert.Equal(t, originalContentCopy, original[1].Content)
}
