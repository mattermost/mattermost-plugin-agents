// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBlocksToPost(t *testing.T) {
	tests := []struct {
		name     string
		blocks   []ContentBlock
		role     string
		expected llm.Post
	}{
		{
			name:     "text blocks to message",
			blocks:   []ContentBlock{{Type: BlockTypeText, Text: "Hello"}, {Type: BlockTypeText, Text: "World"}},
			role:     "user",
			expected: llm.Post{Role: llm.PostRoleUser, Message: "Hello\nWorld"},
		},
		{
			name: "tool_use blocks to ToolUse",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", ServerOrigin: "https://mcp.example.com", Input: json.RawMessage(`{"q":"test"}`), Status: StatusSuccess, Shared: BoolPtr(true)},
			},
			role: "assistant",
			expected: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "search", ServerOrigin: "https://mcp.example.com", Arguments: json.RawMessage(`{"q":"test"}`), Status: llm.ToolCallStatusSuccess},
				},
			},
		},
		{
			name: "thinking block to reasoning",
			blocks: []ContentBlock{
				{Type: BlockTypeThinking, Text: "Let me think...", Signature: "sig123"},
			},
			role: "assistant",
			expected: llm.Post{
				Role:               llm.PostRoleBot,
				Reasoning:          "Let me think...",
				ReasoningSignature: "sig123",
			},
		},
		{
			name: "mixed block types in single turn",
			blocks: []ContentBlock{
				{Type: BlockTypeThinking, Text: "thinking...", Signature: "sig"},
				{Type: BlockTypeText, Text: "Here is the answer"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "weather", Input: json.RawMessage(`{}`), Status: StatusSuccess},
			},
			role: "assistant",
			expected: llm.Post{
				Role:               llm.PostRoleBot,
				Message:            "Here is the answer",
				Reasoning:          "thinking...",
				ReasoningSignature: "sig",
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "weather", Arguments: json.RawMessage(`{}`), Status: llm.ToolCallStatusSuccess},
				},
			},
		},
		{
			name:   "tool_result role",
			blocks: []ContentBlock{{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "result data", Status: StatusSuccess}},
			role:   "tool_result",
			expected: llm.Post{
				Role: llm.PostRoleUser,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Result: "result data", Status: llm.ToolCallStatusSuccess},
				},
			},
		},
		{
			name:     "empty blocks",
			blocks:   []ContentBlock{},
			role:     "user",
			expected: llm.Post{Role: llm.PostRoleUser},
		},
		{
			name: "multiple thinking blocks uses last one",
			blocks: []ContentBlock{
				{Type: BlockTypeThinking, Text: "first thought", Signature: "sig1"},
				{Type: BlockTypeThinking, Text: "second thought", Signature: "sig2"},
			},
			role: "assistant",
			expected: llm.Post{
				Role:               llm.PostRoleBot,
				Reasoning:          "second thought",
				ReasoningSignature: "sig2",
			},
		},
		// File and image blocks are no longer "skipped"; they lazy-resolve
		// through mmClient. See TestBlocksToPost_LazyResolvesAttachments
		// for the new behavior. This case is deliberately removed because
		// its old expectation (no Files entry) only held while attachments
		// were broken.
		{
			name: "annotations blocks are skipped",
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "answer"},
				{Type: BlockTypeAnnotations, WebSearchContext: &WebSearchContext{Count: 1}},
			},
			role: "assistant",
			expected: llm.Post{
				Role:    llm.PostRoleBot,
				Message: "answer",
			},
		},
		{
			name: "tool_result merges into matching tool_use entry",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", ServerOrigin: "https://mcp.example.com", Input: json.RawMessage(`{"q":"test"}`), Status: StatusSuccess},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "found it", Status: StatusSuccess},
			},
			role: "assistant",
			expected: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "search", ServerOrigin: "https://mcp.example.com", Arguments: json.RawMessage(`{"q":"test"}`), Result: "found it", Status: llm.ToolCallStatusSuccess},
				},
			},
		},
		{
			name: "tool_result without matching tool_use creates standalone entry",
			blocks: []ContentBlock{
				{Type: BlockTypeToolResult, ToolUseID: "tc_orphan", Content: "orphan result", Status: StatusSuccess},
			},
			role: "assistant",
			expected: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc_orphan", Result: "orphan result", Status: llm.ToolCallStatusSuccess},
				},
			},
		},
		{
			name: "multiple tool_use and tool_result blocks merge correctly",
			blocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"a"}`), Status: StatusSuccess},
				{Type: BlockTypeToolUse, ID: "tc2", Name: "weather", Input: json.RawMessage(`{"city":"NYC"}`), Status: StatusSuccess},
				{Type: BlockTypeToolResult, ToolUseID: "tc2", Content: "72F sunny", Status: StatusSuccess},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "found it", Status: StatusSuccess},
			},
			role: "assistant",
			expected: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{"q":"a"}`), Result: "found it", Status: llm.ToolCallStatusSuccess},
					{ID: "tc2", Name: "weather", Arguments: json.RawMessage(`{"city":"NYC"}`), Result: "72F sunny", Status: llm.ToolCallStatusSuccess},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BlocksToPost(tt.blocks, tt.role, PostConversionOptions{})
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBlocksToPost_RedactUnshared(t *testing.T) {
	blocks := []ContentBlock{
		{Type: BlockTypeToolUse, ID: "t-shared", Name: "search", Input: json.RawMessage(`{"q":"public"}`), Status: StatusSuccess, Shared: BoolPtr(true)},
		{Type: BlockTypeToolResult, ToolUseID: "t-shared", Content: "PUBLIC", Status: StatusSuccess, Shared: BoolPtr(true)},
		{
			Type:        BlockTypeToolUse,
			ID:          "t-private",
			Name:        "read_dm",
			Input:       json.RawMessage(`{"channel":"secret-dm"}`),
			MCPBareName: "read_dm",
			Status:      StatusSuccess,
			Shared:      BoolPtr(false),
		},
		{Type: BlockTypeToolResult, ToolUseID: "t-private", Content: "SECRET", Status: StatusSuccess, Shared: BoolPtr(false)},
		{Type: BlockTypeToolUse, ID: "t-nilshared", Name: "foo", Input: json.RawMessage(`{"token":"xyz"}`), Status: StatusSuccess},
		{Type: BlockTypeToolResult, ToolUseID: "t-nilshared", Content: "ALSO SECRET", Status: StatusSuccess},
	}

	t.Run("redactUnshared=false", func(t *testing.T) {
		got := BlocksToPost(blocks, "assistant", PostConversionOptions{})
		require.Len(t, got.ToolUse, 3)
		results := map[string]string{}
		args := map[string]string{}
		for _, tc := range got.ToolUse {
			results[tc.ID] = tc.Result
			args[tc.ID] = string(tc.Arguments)
		}
		assert.Equal(t, "PUBLIC", results["t-shared"])
		assert.Equal(t, "SECRET", results["t-private"])
		assert.Equal(t, "ALSO SECRET", results["t-nilshared"])
		assert.JSONEq(t, `{"q":"public"}`, args["t-shared"])
		assert.JSONEq(t, `{"channel":"secret-dm"}`, args["t-private"])
		assert.JSONEq(t, `{"token":"xyz"}`, args["t-nilshared"])
	})

	t.Run("redactUnshared=true", func(t *testing.T) {
		got := BlocksToPost(blocks, "assistant", PostConversionOptions{RedactUnshared: true})
		require.Len(t, got.ToolUse, 3)
		results := map[string]string{}
		args := map[string]string{}
		for _, tc := range got.ToolUse {
			results[tc.ID] = tc.Result
			args[tc.ID] = string(tc.Arguments)
		}
		assert.Equal(t, "PUBLIC", results["t-shared"])
		assert.Equal(t, UnsharedToolResultRedaction, results["t-private"])
		assert.Equal(t, UnsharedToolResultRedaction, results["t-nilshared"])
		// Shared tool_use arguments pass through; unshared and nil-shared
		// arguments are redacted to empty JSON so the LLM cannot paraphrase
		// private tool parameters into a channel-visible reply.
		assert.JSONEq(t, `{"q":"public"}`, args["t-shared"])
		assert.JSONEq(t, `{}`, args["t-private"])
		assert.JSONEq(t, `{}`, args["t-nilshared"])
		for _, tc := range got.ToolUse {
			if tc.ID == "t-private" {
				assert.Nil(t, tc.Schema)
				assert.Empty(t, tc.MCPBareName)
				assert.Empty(t, tc.Description)
			}
		}
	})
}

func TestPostToBlocksPreservesToolIdentityMetadata(t *testing.T) {
	post := llm.Post{
		Role: llm.PostRoleBot,
		ToolUse: []llm.ToolCall{{
			ID:           "tc1",
			Name:         "jira__get_issue",
			Description:  "Get a Jira issue",
			ServerOrigin: "https://jira.example.com",
			Arguments:    json.RawMessage(`{"key":"MM-1"}`),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": map[string]any{"type": "string"},
				},
			},
			MCPBareName: "get_issue",
			Status:      llm.ToolCallStatusPending,
		}},
	}

	blocks := PostToBlocks(post, false)

	require.Len(t, blocks, 1)
	assert.Equal(t, BlockTypeToolUse, blocks[0].Type)
	assert.Equal(t, "jira__get_issue", blocks[0].Name)
	assert.Equal(t, "https://jira.example.com", blocks[0].ServerOrigin)
	assert.Equal(t, "get_issue", blocks[0].MCPBareName)

	data, err := json.Marshal(blocks[0])
	require.NoError(t, err)
	assert.NotContains(t, string(data), "input_schema")
	assert.NotContains(t, string(data), "tool_description")
}

func TestBlocksToPostRehydratesToolCatalogMetadata(t *testing.T) {
	// Persisted block omits the bare name on purpose: rehydration must derive
	// it from the namespaced catalog entry, not echo a value the test pre-set.
	blocks := []ContentBlock{{
		Type:         BlockTypeToolUse,
		ID:           "tc1",
		Name:         "jira__get_issue",
		ServerOrigin: "https://jira.example.com",
		Input:        json.RawMessage(`{"key":"MM-1"}`),
		Status:       StatusPending,
		Shared:       BoolPtr(true),
	}}
	toolStore := llm.NewToolStore()
	schema := json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}}}`)
	toolStore.AddTools([]llm.Tool{{
		Name:         "jira__get_issue",
		Description:  "Get a Jira issue",
		Schema:       schema,
		ServerOrigin: "https://jira.example.com",
	}})

	post := BlocksToPost(blocks, "assistant", PostConversionOptions{ToolStore: toolStore})

	require.Len(t, post.ToolUse, 1)
	toolCall := post.ToolUse[0]
	assert.Equal(t, "tc1", toolCall.ID)
	assert.Equal(t, "jira__get_issue", toolCall.Name)
	assert.Equal(t, "https://jira.example.com", toolCall.ServerOrigin)
	assert.Equal(t, "get_issue", toolCall.MCPBareName)
	assert.Equal(t, "Get a Jira issue", toolCall.Description)
	require.IsType(t, json.RawMessage{}, toolCall.Schema)
	assert.JSONEq(t, `{"type":"object","properties":{"key":{"type":"string"}}}`, string(toolCall.Schema.(json.RawMessage)))
}

func TestPostToBlocks(t *testing.T) {
	tests := []struct {
		name     string
		post     llm.Post
		shared   bool
		expected []ContentBlock
	}{
		{
			name:     "message only",
			post:     llm.Post{Role: llm.PostRoleUser, Message: "Hello"},
			shared:   true,
			expected: []ContentBlock{{Type: BlockTypeText, Text: "Hello"}},
		},
		{
			name:   "reasoning produces thinking block",
			post:   llm.Post{Role: llm.PostRoleBot, Reasoning: "thinking...", ReasoningSignature: "sig"},
			shared: true,
			expected: []ContentBlock{
				{Type: BlockTypeThinking, Text: "thinking...", Signature: "sig"},
			},
		},
		{
			name: "tool use produces tool_use blocks",
			post: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{"q":"test"}`), Status: llm.ToolCallStatusSuccess, ServerOrigin: "https://mcp.example.com"},
				},
			},
			shared: false,
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", ServerOrigin: "https://mcp.example.com", Input: json.RawMessage(`{"q":"test"}`), Status: StatusSuccess, Shared: BoolPtr(false)},
			},
		},
		{
			name: "resolved tool use produces both tool_use and tool_result",
			post: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{}`), Result: "found it", Status: llm.ToolCallStatusSuccess},
				},
			},
			shared: true,
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "found it", Status: StatusSuccess, Shared: BoolPtr(true)},
			},
		},
		{
			name: "full assistant post with reasoning text and tools",
			post: llm.Post{
				Role:               llm.PostRoleBot,
				Message:            "Here is the answer",
				Reasoning:          "Let me think",
				ReasoningSignature: "sig",
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "tool", Arguments: json.RawMessage(`{}`), Status: llm.ToolCallStatusPending},
				},
			},
			shared: false,
			expected: []ContentBlock{
				{Type: BlockTypeThinking, Text: "Let me think", Signature: "sig"},
				{Type: BlockTypeText, Text: "Here is the answer"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "tool", Input: json.RawMessage(`{}`), Status: StatusPending, Shared: BoolPtr(false)},
			},
		},
		{
			name:     "empty post produces no blocks",
			post:     llm.Post{Role: llm.PostRoleUser},
			shared:   true,
			expected: nil,
		},
		{
			name: "multiple tool calls with results interleaved",
			post: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "tool1", Arguments: json.RawMessage(`{}`), Result: "r1", Status: llm.ToolCallStatusSuccess},
					{ID: "tc2", Name: "tool2", Arguments: json.RawMessage(`{}`), Status: llm.ToolCallStatusPending},
				},
			},
			shared: true,
			expected: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "tc1", Name: "tool1", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "r1", Status: StatusSuccess, Shared: BoolPtr(true)},
				{Type: BlockTypeToolUse, ID: "tc2", Name: "tool2", Input: json.RawMessage(`{}`), Status: StatusPending, Shared: BoolPtr(true)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PostToBlocks(tt.post, tt.shared)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoleMapping(t *testing.T) {
	tests := []struct {
		roleStr  string
		expected llm.PostRole
	}{
		{"user", llm.PostRoleUser},
		{"assistant", llm.PostRoleBot},
		{"tool_result", llm.PostRoleUser},
		{"system", llm.PostRoleSystem},
		{"unknown", llm.PostRoleUser},
	}

	for _, tt := range tests {
		t.Run(tt.roleStr, func(t *testing.T) {
			assert.Equal(t, tt.expected, RoleFromString(tt.roleStr))
		})
	}
}

func TestStatusConversion(t *testing.T) {
	tests := []struct {
		str    string
		status llm.ToolCallStatus
	}{
		{StatusPending, llm.ToolCallStatusPending},
		{StatusAccepted, llm.ToolCallStatusAccepted},
		{StatusRejected, llm.ToolCallStatusRejected},
		{StatusError, llm.ToolCallStatusError},
		{StatusSuccess, llm.ToolCallStatusSuccess},
		{StatusAutoApproved, llm.ToolCallStatusAutoApproved},
	}

	for _, tt := range tests {
		t.Run(tt.str, func(t *testing.T) {
			assert.Equal(t, tt.status, StatusFromString(tt.str))
			assert.Equal(t, tt.str, StatusToString(tt.status))
		})
	}
}

func TestStatusFromStringDefault(t *testing.T) {
	assert.Equal(t, llm.ToolCallStatusPending, StatusFromString("bogus_status"))
}

func TestStatusToStringDefault(t *testing.T) {
	assert.Equal(t, StatusPending, StatusToString(llm.ToolCallStatus(999)))
}

func TestRoleToString(t *testing.T) {
	tests := []struct {
		role     llm.PostRole
		expected string
	}{
		{llm.PostRoleUser, "user"},
		{llm.PostRoleBot, "assistant"},
		{llm.PostRoleSystem, "system"},
		{llm.PostRole(999), "user"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, RoleToString(tt.role))
		})
	}
}

func TestPostToBlocksToPostRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		post   llm.Post
		shared bool
	}{
		{
			name:   "message only",
			post:   llm.Post{Role: llm.PostRoleBot, Message: "Hello world"},
			shared: true,
		},
		{
			name: "reasoning and message",
			post: llm.Post{
				Role:               llm.PostRoleBot,
				Message:            "The answer is 42",
				Reasoning:          "Let me think about this",
				ReasoningSignature: "sig_abc",
			},
			shared: true,
		},
		{
			name: "tool use with result",
			post: llm.Post{
				Role:    llm.PostRoleBot,
				Message: "Here are the results",
				ToolUse: []llm.ToolCall{
					{
						ID:           "tc1",
						Name:         "search",
						ServerOrigin: "https://mcp.example.com",
						Arguments:    json.RawMessage(`{"q":"test"}`),
						Result:       "found it",
						Status:       llm.ToolCallStatusSuccess,
					},
				},
			},
			shared: false,
		},
		{
			name: "multiple tools mixed resolved and unresolved",
			post: llm.Post{
				Role: llm.PostRoleBot,
				ToolUse: []llm.ToolCall{
					{ID: "tc1", Name: "tool1", Arguments: json.RawMessage(`{}`), Result: "r1", Status: llm.ToolCallStatusSuccess},
					{ID: "tc2", Name: "tool2", Arguments: json.RawMessage(`{"x":1}`), Status: llm.ToolCallStatusPending},
				},
			},
			shared: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := PostToBlocks(tt.post, tt.shared)
			role := RoleToString(tt.post.Role)
			roundTripped := BlocksToPost(blocks, role, PostConversionOptions{})

			assert.Equal(t, tt.post.Role, roundTripped.Role)
			assert.Equal(t, tt.post.Message, roundTripped.Message)
			assert.Equal(t, tt.post.Reasoning, roundTripped.Reasoning)
			assert.Equal(t, tt.post.ReasoningSignature, roundTripped.ReasoningSignature)
			assert.Equal(t, len(tt.post.ToolUse), len(roundTripped.ToolUse))
			for i := range tt.post.ToolUse {
				assert.Equal(t, tt.post.ToolUse[i].ID, roundTripped.ToolUse[i].ID)
				assert.Equal(t, tt.post.ToolUse[i].Name, roundTripped.ToolUse[i].Name)
				assert.Equal(t, tt.post.ToolUse[i].ServerOrigin, roundTripped.ToolUse[i].ServerOrigin)
				assert.JSONEq(t, string(tt.post.ToolUse[i].Arguments), string(roundTripped.ToolUse[i].Arguments))
				assert.Equal(t, tt.post.ToolUse[i].Result, roundTripped.ToolUse[i].Result)
				assert.Equal(t, tt.post.ToolUse[i].Status, roundTripped.ToolUse[i].Status)
			}
		})
	}
}

// fakeReadCloser wraps a strings.Reader as io.ReadCloser so the mock GetFile
// return type matches mmapi.Client.GetFile.
type fakeReadCloser struct {
	io.Reader
}

func (fakeReadCloser) Close() error { return nil }

func newFakeReadCloser(s string) io.ReadCloser {
	return fakeReadCloser{Reader: strings.NewReader(s)}
}

// TestBlocksToPost_LazyResolvesAttachments encodes the new desired behavior:
// file and image content blocks are lazy-resolved through mmClient at the
// moment we build the LLM request, instead of being inlined when the user
// turn was first written.
func TestBlocksToPost_LazyResolvesAttachments(t *testing.T) {
	bigContent := strings.Repeat("x", int(InlineFileMaxBytes)+1)
	boundaryBody := strings.Repeat("A", int(InlineFileMaxBytes))

	tests := []struct {
		name         string
		role         string // defaults to "user"
		enableVision bool
		nilClient    bool
		setup        func(m *mmapimocks.MockClient)
		blocks       []ContentBlock
		assert       func(t *testing.T, m *mmapimocks.MockClient, post llm.Post)
	}{
		{
			name:         "image block with EnableVision=true populates Files",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "img1").Return(&model.FileInfo{
					Id: "img1", Name: "shot.png", MimeType: "image/png", Size: 1234,
				}, nil)
				m.On("GetFile", "img1").Return(newFakeReadCloser("PNGDATA"), nil)
			},
			blocks: []ContentBlock{
				{Type: BlockTypeImage, FileID: "img1", Filename: "shot.png", MimeType: "image/png"},
			},
			assert: func(t *testing.T, _ *mmapimocks.MockClient, post llm.Post) {
				require.Len(t, post.Files, 1, "an image block with vision enabled must produce exactly one entry in Post.Files")
				assert.Equal(t, "image/png", post.Files[0].MimeType)
				assert.Equal(t, int64(1234), post.Files[0].Size)
				assert.Equal(t, []byte("PNGDATA"), post.Files[0].Data)
				require.NotNil(t, post.Files[0].Reader)

				// Read the bytes back and pin them to what the mock returned.
				// Catches a wrong-FileID-resolution bug (e.g. iterating wrong
				// FileID, returning a stale reader, etc.).
				bytesBack, readErr := io.ReadAll(post.Files[0].Reader)
				require.NoError(t, readErr)
				assert.Equal(t, "PNGDATA", string(bytesBack),
					"the Reader bound to Post.Files must yield the bytes that mmClient.GetFile returned for that exact FileID")
			},
		},
		{
			name:         "image block with EnableVision=false is skipped",
			enableVision: false,
			// No expectations: GetFile/GetFileInfo must NOT be called when
			// vision is off. mockery will fail the test if either runs.
			blocks: []ContentBlock{
				{Type: BlockTypeImage, FileID: "img1", Filename: "shot.png", MimeType: "image/png"},
			},
			assert: func(t *testing.T, _ *mmapimocks.MockClient, post llm.Post) {
				assert.Empty(t, post.Files, "image block must be silently dropped when vision is disabled")
			},
		},
		{
			name:         "unsupported image MIME is passed through without reading blob",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "img1").Return(&model.FileInfo{
					Id: "img1", Name: "vector.svg", MimeType: "image/svg+xml", Size: 1234,
				}, nil)
			},
			blocks: []ContentBlock{
				{Type: BlockTypeImage, FileID: "img1", Filename: "vector.svg", MimeType: "image/svg+xml"},
			},
			assert: func(t *testing.T, m *mmapimocks.MockClient, post llm.Post) {
				require.Len(t, post.Files, 1)
				m.AssertNotCalled(t, "GetFile", "img1")
				assert.Equal(t, "image/svg+xml", post.Files[0].MimeType)
				assert.Empty(t, post.Files[0].Data)
				assert.Nil(t, post.Files[0].Reader)
			},
		},
		{
			name:         "text/plain file block reads content via GetFile and appends Attached File Contents",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "doc1").Return(&model.FileInfo{
					Id: "doc1", Name: "foo.txt", MimeType: "text/plain", Size: 11,
				}, nil)
				m.On("GetFile", "doc1").Return(newFakeReadCloser("hello world"), nil)
			},
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "look at this"},
				{Type: BlockTypeFile, FileID: "doc1", Filename: "foo.txt", MimeType: "text/plain"},
			},
			assert: func(t *testing.T, _ *mmapimocks.MockClient, post llm.Post) {
				assert.Empty(t, post.Files, "non-image text file must NOT go through Post.Files")
				assert.Contains(t, post.Message, "look at this")
				assert.Contains(t, post.Message, "\nAttached File Contents:\nFile Name: foo.txt\nContent: hello world")
			},
		},
		{
			name:         "file block uses pre-extracted FileInfo.Content without GetFile",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "doc1").Return(&model.FileInfo{
					Id: "doc1", Name: "doc.pdf", MimeType: "application/pdf", Content: "pre-extracted content",
				}, nil)
				// No GetFile expectation — mockery fails if it is called.
			},
			blocks: []ContentBlock{
				{Type: BlockTypeFile, FileID: "doc1", Filename: "doc.pdf", MimeType: "application/pdf"},
			},
			assert: func(t *testing.T, _ *mmapimocks.MockClient, post llm.Post) {
				assert.Contains(t, post.Message, "File Name: doc.pdf")
				assert.Contains(t, post.Message, "Content: pre-extracted content")
			},
		},
		{
			name:         "large text file is surfaced as a descriptor without reading it",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "doc1").Return(&model.FileInfo{
					Id: "doc1", Name: "bigdoc.txt", MimeType: "text/plain", Size: InlineFileMaxBytes + 1,
				}, nil)
				// No GetFile expectation — the inline-vs-descriptor decision is
				// made from metadata alone, so a large file is never downloaded.
			},
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "see attached"},
				{Type: BlockTypeFile, FileID: "doc1", Filename: "bigdoc.txt", MimeType: "text/plain"},
			},
			assert: func(t *testing.T, m *mmapimocks.MockClient, post llm.Post) {
				// Pin the full assembled suffix: the header sentence the read_file
				// tool relies on, the per-entry header, and every descriptor field.
				expected := "see attached\n" +
					"Attached files (call the read_file tool with the File ID to read their contents):\n" +
					fmt.Sprintf("**Attached File 1**:\nName: bigdoc.txt\nFile ID: doc1\nType: text/plain\nSize: %d bytes", InlineFileMaxBytes+1)
				assert.Equal(t, expected, post.Message)
				m.AssertNotCalled(t, "GetFile", "doc1")
			},
		},
		{
			name:         "large pre-extracted content is surfaced as a descriptor",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "doc1").Return(&model.FileInfo{
					Id: "doc1", Name: "huge.pdf", MimeType: "application/pdf", Content: bigContent,
				}, nil)
				// No GetFile expectation — pre-extracted content is measured directly.
			},
			blocks: []ContentBlock{
				{Type: BlockTypeFile, FileID: "doc1", Filename: "huge.pdf", MimeType: "application/pdf"},
			},
			assert: func(t *testing.T, _ *mmapimocks.MockClient, post llm.Post) {
				assert.Contains(t, post.Message, "File ID: doc1")
				assert.NotContains(t, post.Message, bigContent,
					"the descriptor must not inline the extracted content")
				assert.NotContains(t, post.Message, "Attached File Contents:",
					"a partial inline of the extracted content would still be a regression")
			},
		},
		{
			name:         "binary file with no extractable text is surfaced as a descriptor without GetFile",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "doc1").Return(&model.FileInfo{
					Id: "doc1", Name: "archive.zip", MimeType: "application/zip",
				}, nil)
				// No GetFile expectation — a binary with no extractable text still
				// gets a metadata descriptor so the LLM is aware of it.
			},
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "see attached"},
				{Type: BlockTypeFile, FileID: "doc1", Filename: "archive.zip", MimeType: "application/zip"},
			},
			assert: func(t *testing.T, m *mmapimocks.MockClient, post llm.Post) {
				assert.Contains(t, post.Message, "see attached")
				assert.Contains(t, post.Message, "Name: archive.zip")
				assert.Contains(t, post.Message, "File ID: doc1")
				m.AssertNotCalled(t, "GetFile", "doc1")
			},
		},
		{
			name:         "multiple mixed blocks: text + image + text/plain file",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "img1").Return(&model.FileInfo{
					Id: "img1", Name: "shot.png", MimeType: "image/png", Size: 500,
				}, nil)
				m.On("GetFile", "img1").Return(newFakeReadCloser("PNGDATA"), nil)
				m.On("GetFileInfo", "doc1").Return(&model.FileInfo{
					Id: "doc1", Name: "foo.txt", MimeType: "text/plain", Size: 5,
				}, nil)
				m.On("GetFile", "doc1").Return(newFakeReadCloser("hello"), nil)
			},
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "the text"},
				{Type: BlockTypeImage, FileID: "img1", Filename: "shot.png", MimeType: "image/png"},
				{Type: BlockTypeFile, FileID: "doc1", Filename: "foo.txt", MimeType: "text/plain"},
			},
			assert: func(t *testing.T, _ *mmapimocks.MockClient, post llm.Post) {
				require.Len(t, post.Files, 1, "exactly the image attachment should populate Post.Files")
				assert.Equal(t, "image/png", post.Files[0].MimeType)
				assert.Contains(t, post.Message, "the text")
				assert.Contains(t, post.Message, "Attached File Contents:")
				assert.Contains(t, post.Message, "File Name: foo.txt")
				assert.Contains(t, post.Message, "Content: hello")

				// Ordering contract: original user text precedes the
				// "Attached File Contents:" suffix. Without this ordering,
				// the LLM would see file content inserted before the user's
				// own message.
				idxText := strings.Index(post.Message, "the text")
				idxAttach := strings.Index(post.Message, "Attached File Contents:")
				require.NotEqual(t, -1, idxText)
				require.NotEqual(t, -1, idxAttach)
				assert.Less(t, idxText, idxAttach,
					"user text must appear in the message before the Attached File Contents suffix")
			},
		},
		{
			name:         "multiple text/plain files appear in input order in the message",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "doc-a").Return(&model.FileInfo{
					Id: "doc-a", Name: "a.txt", MimeType: "text/plain", Size: 5,
				}, nil)
				m.On("GetFile", "doc-a").Return(newFakeReadCloser("AAAAA"), nil)
				m.On("GetFileInfo", "doc-b").Return(&model.FileInfo{
					Id: "doc-b", Name: "b.txt", MimeType: "text/plain", Size: 5,
				}, nil)
				m.On("GetFile", "doc-b").Return(newFakeReadCloser("BBBBB"), nil)
			},
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "two attachments"},
				{Type: BlockTypeFile, FileID: "doc-a", Filename: "a.txt", MimeType: "text/plain"},
				{Type: BlockTypeFile, FileID: "doc-b", Filename: "b.txt", MimeType: "text/plain"},
			},
			assert: func(t *testing.T, _ *mmapimocks.MockClient, post llm.Post) {
				idxA := strings.Index(post.Message, "File Name: a.txt")
				idxB := strings.Index(post.Message, "File Name: b.txt")
				require.NotEqual(t, -1, idxA, "a.txt must appear in the message")
				require.NotEqual(t, -1, idxB, "b.txt must appear in the message")
				assert.Less(t, idxA, idxB,
					"file blocks must appear in input order in the Attached File Contents suffix")
			},
		},
		{
			name:         "GetFile error on image is logged and skipped, conversation continues",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "img1").Return(&model.FileInfo{
					Id: "img1", Name: "broken.png", MimeType: "image/png",
				}, nil)
				m.On("GetFile", "img1").Return(nil, errors.New("file store offline"))
				// LogError must actually be invoked — without .Maybe() this fails
				// if production silently swallows the error (the broken behavior).
				m.On("LogError", mock.Anything, mock.Anything).Return()
			},
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "still here"},
				{Type: BlockTypeImage, FileID: "img1", Filename: "broken.png", MimeType: "image/png"},
			},
			assert: func(t *testing.T, m *mmapimocks.MockClient, post llm.Post) {
				// Pin that GetFile was actually attempted — production simply
				// dropping the image without trying to fetch it would still leave
				// post.Files empty, so the previous broken behavior would pass
				// without this AssertCalled.
				m.AssertCalled(t, "GetFile", "img1")
				assert.Empty(t, post.Files, "GetFile errors must drop the entry, not abort the request")
				assert.Equal(t, "still here", post.Message,
					"text content must still appear when an image attachment fails to load")
			},
		},
		{
			// Pin the inline-vs-descriptor boundary by name: a file whose readable
			// size is at or below InlineFileMaxBytes is inlined; one byte over
			// becomes a descriptor (covered by the large-text-file case above).
			name:         "text file at exactly InlineFileMaxBytes is inlined",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "doc1").Return(&model.FileInfo{
					Id: "doc1", Name: "boundary.txt", MimeType: "text/plain", Size: int64(len(boundaryBody)),
				}, nil)
				m.On("GetFile", "doc1").Return(newFakeReadCloser(boundaryBody), nil)
			},
			blocks: []ContentBlock{
				{Type: BlockTypeFile, FileID: "doc1", Filename: "boundary.txt", MimeType: "text/plain"},
			},
			assert: func(t *testing.T, _ *mmapimocks.MockClient, post llm.Post) {
				assert.Contains(t, post.Message, "Attached File Contents:")
				assert.Contains(t, post.Message, "File Name: boundary.txt")
				assert.NotContains(t, post.Message, "read_file",
					"a file at the inline threshold must be inlined, not described")
			},
		},
		{
			// Documents the zero-config safety guarantee: a turn with only
			// text/thinking/tool blocks must not panic when mmClient is nil.
			name:      "nil mmClient is safe when no file or image blocks are present",
			role:      "assistant",
			nilClient: true,
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "hello"},
				{Type: BlockTypeThinking, Text: "reason", Signature: "sig"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(true)},
			},
			assert: func(t *testing.T, _ *mmapimocks.MockClient, post llm.Post) {
				assert.Equal(t, "hello", post.Message)
				assert.Equal(t, "reason", post.Reasoning)
				require.Len(t, post.ToolUse, 1)
				assert.Equal(t, "tc1", post.ToolUse[0].ID)
			},
		},
		{
			// Dispatch must be by block.Type, not by MimeType — a malformed
			// block (Type=file but MimeType=image/png) must not be sent to the
			// LLM as an image. With empty FileInfo.Content and a non-text MIME
			// there is no extractable text, so the block is surfaced as a
			// metadata descriptor and GetFile must NOT be called.
			name:         "BlockTypeFile with image/* MIME goes through file path, not image path",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "doc1").Return(&model.FileInfo{
					Id: "doc1", Name: "weird.png", MimeType: "image/png",
				}, nil)
				// No GetFile expectation — mockery fails the test if production
				// fetches bytes anyway (which would happen if dispatch went by
				// MimeType into the image branch).
			},
			blocks: []ContentBlock{
				{Type: BlockTypeText, Text: "see attached"},
				{Type: BlockTypeFile, FileID: "doc1", Filename: "weird.png", MimeType: "image/png"},
			},
			assert: func(t *testing.T, m *mmapimocks.MockClient, post llm.Post) {
				m.AssertNotCalled(t, "GetFile", "doc1")
				assert.Empty(t, post.Files,
					"a BlockTypeFile must never populate Post.Files even when its MimeType says image/*")
				assert.Contains(t, post.Message, "see attached")
				assert.Contains(t, post.Message, "File ID: doc1",
					"a file block with no extractable text is surfaced as a metadata descriptor")
			},
		},
		{
			name:         "FileInfo.Content non-empty wins over GetFile (explicit AssertNotCalled)",
			enableVision: true,
			setup: func(m *mmapimocks.MockClient) {
				m.On("GetFileInfo", "doc1").Return(&model.FileInfo{
					Id: "doc1", Name: "extracted.pdf", MimeType: "application/pdf",
					Content: "server-extracted text",
				}, nil)
				// No GetFile expectation: when fileInfo.Content is non-empty,
				// the byte fetch must be skipped entirely.
			},
			blocks: []ContentBlock{
				{Type: BlockTypeFile, FileID: "doc1", Filename: "extracted.pdf", MimeType: "application/pdf"},
			},
			assert: func(t *testing.T, m *mmapimocks.MockClient, post llm.Post) {
				m.AssertNotCalled(t, "GetFile", "doc1",
					"pre-extracted FileInfo.Content must short-circuit GetFile to avoid a redundant blob read")
				assert.Contains(t, post.Message, "Content: server-extracted text")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role := tt.role
			if role == "" {
				role = "user"
			}

			if tt.nilClient {
				post := BlocksToPost(tt.blocks, role, PostConversionOptions{EnableVision: tt.enableVision})
				tt.assert(t, nil, post)
				return
			}

			mmClient := mmapimocks.NewMockClient(t)
			if tt.setup != nil {
				tt.setup(mmClient)
			}

			post := BlocksToPost(tt.blocks, role, PostConversionOptions{MMClient: mmClient, EnableVision: tt.enableVision})
			tt.assert(t, mmClient, post)
		})
	}
}
