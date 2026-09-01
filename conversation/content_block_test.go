// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
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
				Shared:       new(true),
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
				Shared:    new(true),
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
				Shared: new(false),
			},
			expected: `{"type":"tool_use","id":"tc_02","name":"read_file","input":{"path":"/etc/passwd"},"status":"pending","shared":false}`,
		},
		{
			name: "tool_use block with title/description/mcp_bare_name",
			block: ContentBlock{
				Type:         BlockTypeToolUse,
				ID:           "tc_03",
				Name:         "mattermost__create_post",
				ServerOrigin: "embedded://mattermost",
				MCPBareName:  "create_post",
				Title:        "Create Post",
				Description:  "Create a new post in Mattermost.",
				Input:        json.RawMessage(`{"channel_id":"c1"}`),
				Status:       StatusPending,
				Shared:       new(true),
			},
			expected: `{"type":"tool_use","id":"tc_03","name":"mattermost__create_post","server_origin":"embedded://mattermost","input":{"channel_id":"c1"},"mcp_bare_name":"create_post","status":"pending","shared":true,"title":"Create Post","description":"Create a new post in Mattermost."}`,
		},
		{
			name: "server_tool_use block",
			block: ContentBlock{
				Type: BlockTypeServerToolUse,
				ServerTool: &llm.ServerToolUse{
					ID:      "srvtoolu_01",
					Tool:    llm.NativeToolCodeInterpreter,
					Status:  llm.ServerToolStatusSuccess,
					SubTool: "bash",
					Command: "ls",
					Output:  "file.txt\n",
				},
			},
			expected: `{"type":"server_tool_use","server_tool":{"id":"srvtoolu_01","tool":"code_interpreter","status":"success","sub_tool":"bash","command":"ls","output":"file.txt\n"}}`,
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
		{Type: BlockTypeToolUse, ID: "tc_01", Name: "search", Input: json.RawMessage(`{}`), Status: StatusPending, Shared: new(false)},
		{Type: BlockTypeToolResult, ToolUseID: "tc_01", Content: "result", Status: StatusSuccess, Shared: new(true)},
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

func TestContentBlockToolUseWithApprovalMetadataRoundTrip(t *testing.T) {
	block := ContentBlock{
		Type:         BlockTypeToolUse,
		ID:           "tc_approval",
		Name:         "jira__get_issue",
		ServerOrigin: "https://jira.example.com",
		Input:        json.RawMessage(`{"key":"MM-1"}`),
		MCPBareName:  "get_issue",
		Status:       StatusPending,
		Shared:       new(false),
	}

	data, err := json.Marshal(block)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"type": "tool_use",
		"id": "tc_approval",
		"name": "jira__get_issue",
		"server_origin": "https://jira.example.com",
		"input": {"key": "MM-1"},
		"mcp_bare_name": "get_issue",
		"status": "pending",
		"shared": false
	}`, string(data))

	var roundTripped ContentBlock
	require.NoError(t, json.Unmarshal(data, &roundTripped))
	assert.Equal(t, block, roundTripped)
}

func TestFilterForNonRequesterRedactsApprovalMetadata(t *testing.T) {
	blocks := []ContentBlock{{
		Type:        BlockTypeToolUse,
		ID:          "tc_private",
		Name:        "jira__get_issue",
		Title:       "Get Issue",
		Description: "Get a Jira issue",
		Input:       json.RawMessage(`{"key":"MM-1"}`),
		MCPBareName: "get_issue",
		Status:      StatusPending,
		Shared:      new(false),
	}}

	result := FilterForNonRequester(blocks)

	require.Len(t, result, 1)
	assert.Nil(t, result[0].Input)
	assert.Empty(t, result[0].MCPBareName)
	// Tool identity stays visible to non-requesters, matching redactToolCalls.
	assert.Equal(t, "Get Issue", result[0].Title)
	assert.Equal(t, "Get a Jira issue", result[0].Description)
	assert.Equal(t, "jira__get_issue", result[0].Name)
	assert.NotNil(t, blocks[0].Input, "original block must not be mutated")
	assert.Equal(t, "get_issue", blocks[0].MCPBareName, "original block must not be mutated")
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
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"secret"}`), Status: StatusSuccess, Shared: new(false)},
			},
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: nil, Status: StatusSuccess, Shared: new(false)},
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
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "sensitive data", Status: StatusSuccess, Shared: new(false)},
			},
			expected: []ContentBlock{
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "", Status: StatusSuccess, Shared: new(false)},
			},
		},
		{
			name: "leaves shared=true tool blocks untouched",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"query"}`), Status: StatusSuccess, Shared: new(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "public result", Status: StatusSuccess, Shared: new(true)},
			},
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"query"}`), Status: StatusSuccess, Shared: new(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "public result", Status: StatusSuccess, Shared: new(true)},
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
			// Server tools run provider-side with no approval flow; their
			// activity shares the post text's visibility and is never redacted.
			name: "leaves server_tool_use untouched",
			blocks: []ContentBlock{
				{Type: BlockTypeServerToolUse, ServerTool: &llm.ServerToolUse{
					ID: "srv1", Tool: llm.NativeToolWebSearch, Status: llm.ServerToolStatusSuccess, Query: "release notes",
				}},
			},
			expected: []ContentBlock{
				{Type: BlockTypeServerToolUse, ServerTool: &llm.ServerToolUse{
					ID: "srv1", Tool: llm.NativeToolWebSearch, Status: llm.ServerToolStatusSuccess, Query: "release notes",
				}},
			},
		},
		{
			name: "mixed blocks only private tool blocks are redacted",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "response"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "tool", Input: json.RawMessage(`{"x":1}`), Status: StatusSuccess, Shared: new(false)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "secret", Status: StatusSuccess, Shared: new(false)},
				{Type: BlockTypeToolUse, ID: "tc2", Name: "tool2", Input: json.RawMessage(`{"y":2}`), Status: StatusSuccess, Shared: new(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc2", Content: "public", Status: StatusSuccess, Shared: new(true)},
			},
			expected: []ContentBlock{
				{Type: BlockTypeText, Text: "response"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "tool", Input: nil, Status: StatusSuccess, Shared: new(false)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "", Status: StatusSuccess, Shared: new(false)},
				{Type: BlockTypeToolUse, ID: "tc2", Name: "tool2", Input: json.RawMessage(`{"y":2}`), Status: StatusSuccess, Shared: new(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc2", Content: "public", Status: StatusSuccess, Shared: new(true)},
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

// TestSanitizeForDisplayServerTool verifies server-tool activity strings are
// escaped against Unicode bidi/spoofing attacks (web content and sandbox
// output are attacker-influenced) and that the original blocks are not mutated.
func TestSanitizeForDisplayServerTool(t *testing.T) {
	bidi := "safe\u202Eevil"
	blocks := []ContentBlock{{
		Type: BlockTypeServerToolUse,
		ServerTool: &llm.ServerToolUse{
			ID:      "srv1",
			Tool:    llm.NativeToolCodeInterpreter,
			Status:  llm.ServerToolStatusSuccess,
			Command: bidi,
			Output:  bidi,
		},
	}}

	result := SanitizeForDisplay(blocks)

	require.Len(t, result, 1)
	require.NotNil(t, result[0].ServerTool)
	assert.NotContains(t, result[0].ServerTool.Command, "\u202E")
	assert.NotContains(t, result[0].ServerTool.Output, "\u202E")
	assert.Contains(t, result[0].ServerTool.Command, "safe")
	assert.Equal(t, bidi, blocks[0].ServerTool.Command, "original block must not be mutated")
	assert.Equal(t, bidi, blocks[0].ServerTool.Output, "original block must not be mutated")
}

func TestFilterForNonRequesterDoesNotMutateOriginal(t *testing.T) {
	original := []ContentBlock{
		{Type: BlockTypeToolUse, ID: "tc1", Input: json.RawMessage(`{"secret":"val"}`), Status: StatusSuccess, Shared: new(false)},
		{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "secret result", Status: StatusSuccess, Shared: new(false)},
	}

	originalInputCopy := make(json.RawMessage, len(original[0].Input))
	copy(originalInputCopy, original[0].Input)
	originalContentCopy := original[1].Content

	_ = FilterForNonRequester(original)

	assert.Equal(t, originalInputCopy, original[0].Input)
	assert.Equal(t, originalContentCopy, original[1].Content)
}

func TestSanitizeForDisplaySanitizesTitleAndDescription(t *testing.T) {
	// U+202E (right-to-left override) is a classic bidi spoofing character.
	blocks := []ContentBlock{{
		Type:        BlockTypeToolUse,
		ID:          "tc1",
		Name:        "jira__get_issue",
		Title:       "Get\u202eIssue",
		Description: "Get\u202ean issue",
		Input:       json.RawMessage("{\"key\":\"MM\u202e-1\"}"),
		Status:      StatusPending,
	}}

	result := SanitizeForDisplay(blocks)

	require.Len(t, result, 1)
	assert.Equal(t, "Get[U+202E]Issue", result[0].Title)
	assert.Equal(t, "Get[U+202E]an issue", result[0].Description)
	assert.Contains(t, string(result[0].Input), "[U+202E]")

	// Original is not mutated.
	assert.Equal(t, "Get\u202eIssue", blocks[0].Title)
	assert.Equal(t, "Get\u202ean issue", blocks[0].Description)
}

func TestSanitizeForDisplayLeavesCleanTitleAndDescription(t *testing.T) {
	blocks := []ContentBlock{{
		Type:        BlockTypeToolUse,
		ID:          "tc1",
		Name:        "jira__get_issue",
		Title:       "Get Issue",
		Description: "Get a Jira issue",
	}}

	result := SanitizeForDisplay(blocks)

	require.Len(t, result, 1)
	assert.Equal(t, "Get Issue", result[0].Title)
	assert.Equal(t, "Get a Jira issue", result[0].Description)
}
