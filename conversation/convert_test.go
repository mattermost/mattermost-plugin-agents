// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"errors"
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
			result := BlocksToPost(tt.blocks, tt.role, false, nil, false, 0)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBlocksToPost_RedactUnshared(t *testing.T) {
	blocks := []ContentBlock{
		{Type: BlockTypeToolUse, ID: "t-shared", Name: "search", Input: json.RawMessage(`{"q":"public"}`), Status: StatusSuccess, Shared: BoolPtr(true)},
		{Type: BlockTypeToolResult, ToolUseID: "t-shared", Content: "PUBLIC", Status: StatusSuccess, Shared: BoolPtr(true)},
		{Type: BlockTypeToolUse, ID: "t-private", Name: "read_dm", Input: json.RawMessage(`{"channel":"secret-dm"}`), Status: StatusSuccess, Shared: BoolPtr(false)},
		{Type: BlockTypeToolResult, ToolUseID: "t-private", Content: "SECRET", Status: StatusSuccess, Shared: BoolPtr(false)},
		{Type: BlockTypeToolUse, ID: "t-nilshared", Name: "foo", Input: json.RawMessage(`{"token":"xyz"}`), Status: StatusSuccess},
		{Type: BlockTypeToolResult, ToolUseID: "t-nilshared", Content: "ALSO SECRET", Status: StatusSuccess},
	}

	t.Run("redactUnshared=false", func(t *testing.T) {
		got := BlocksToPost(blocks, "assistant", false, nil, false, 0)
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
		got := BlocksToPost(blocks, "assistant", true, nil, false, 0)
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
	})
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
			roundTripped := BlocksToPost(blocks, role, false, nil, false, 0)

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
	t.Run("image block with EnableVision=true populates Files", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "img1").Return(&model.FileInfo{
			Id:       "img1",
			Name:     "shot.png",
			MimeType: "image/png",
			Size:     1234,
		}, nil)
		mmClient.On("GetFile", "img1").Return(newFakeReadCloser("PNGDATA"), nil)

		blocks := []ContentBlock{
			{Type: BlockTypeImage, FileID: "img1", Filename: "shot.png", MimeType: "image/png"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		require.Len(t, post.Files, 1, "an image block with vision enabled must produce exactly one entry in Post.Files")
		assert.Equal(t, "image/png", post.Files[0].MimeType)
		assert.Equal(t, int64(1234), post.Files[0].Size)
		require.NotNil(t, post.Files[0].Reader)

		// Read the bytes back and pin them to what the mock returned.
		// Catches a wrong-FileID-resolution bug (e.g. iterating wrong
		// FileID, returning a stale reader, etc.).
		bytesBack, readErr := io.ReadAll(post.Files[0].Reader)
		require.NoError(t, readErr)
		assert.Equal(t, "PNGDATA", string(bytesBack),
			"the Reader bound to Post.Files must yield the bytes that mmClient.GetFile returned for that exact FileID")
	})

	t.Run("image block with EnableVision=false is skipped", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)
		// No expectations: GetFile/GetFileInfo must NOT be called when
		// vision is off. mockery will fail the test if either runs.

		blocks := []ContentBlock{
			{Type: BlockTypeImage, FileID: "img1", Filename: "shot.png", MimeType: "image/png"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, false, 0)

		assert.Empty(t, post.Files, "image block must be silently dropped when vision is disabled")
	})

	t.Run("text/plain file block reads content via GetFile and appends Attached File Contents", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id:       "doc1",
			Name:     "foo.txt",
			MimeType: "text/plain",
			Size:     11,
		}, nil)
		mmClient.On("GetFile", "doc1").Return(newFakeReadCloser("hello world"), nil)

		blocks := []ContentBlock{
			{Type: BlockTypeText, Text: "look at this"},
			{Type: BlockTypeFile, FileID: "doc1", Filename: "foo.txt", MimeType: "text/plain"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		assert.Empty(t, post.Files, "non-image text file must NOT go through Post.Files")
		assert.Contains(t, post.Message, "look at this")
		assert.Contains(t, post.Message, "\nAttached File Contents:\nFile Name: foo.txt\nContent: hello world")
	})

	t.Run("file block uses pre-extracted FileInfo.Content without GetFile", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id:       "doc1",
			Name:     "doc.pdf",
			MimeType: "application/pdf",
			Content:  "pre-extracted content",
		}, nil)
		// No GetFile expectation — mockery fails if it is called.

		blocks := []ContentBlock{
			{Type: BlockTypeFile, FileID: "doc1", Filename: "doc.pdf", MimeType: "application/pdf"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		assert.Contains(t, post.Message, "File Name: doc.pdf")
		assert.Contains(t, post.Message, "Content: pre-extracted content")
	})

	t.Run("text/plain file at exactly maxFileSize gets truncation marker", func(t *testing.T) {
		const maxBytes = int64(8)
		body := "ABCDEFGH" // exactly 8 bytes

		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id:       "doc1",
			Name:     "big.txt",
			MimeType: "text/plain",
			Size:     int64(len(body)),
		}, nil)
		mmClient.On("GetFile", "doc1").Return(newFakeReadCloser(body), nil)

		blocks := []ContentBlock{
			{Type: BlockTypeFile, FileID: "doc1", Filename: "big.txt", MimeType: "text/plain"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, maxBytes)

		assert.Contains(t, post.Message, "... (content truncated due to size limit)",
			"reading exactly maxFileSize bytes must append the truncation marker so the LLM knows the content was cut")
	})

	t.Run("non-text non-image MIME with empty Content is skipped without GetFile", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id:       "doc1",
			Name:     "binary.pdf",
			MimeType: "application/pdf",
		}, nil)
		// No GetFile expectation — mockery fails if it is called.

		blocks := []ContentBlock{
			{Type: BlockTypeText, Text: "see attached"},
			{Type: BlockTypeFile, FileID: "doc1", Filename: "binary.pdf", MimeType: "application/pdf"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		assert.Equal(t, "see attached", post.Message,
			"non-text non-image attachments without pre-extracted Content must be silently skipped")
	})

	t.Run("multiple mixed blocks: text + image + text/plain file", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "img1").Return(&model.FileInfo{
			Id:       "img1",
			Name:     "shot.png",
			MimeType: "image/png",
			Size:     500,
		}, nil)
		mmClient.On("GetFile", "img1").Return(newFakeReadCloser("PNGDATA"), nil)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id:       "doc1",
			Name:     "foo.txt",
			MimeType: "text/plain",
			Size:     5,
		}, nil)
		mmClient.On("GetFile", "doc1").Return(newFakeReadCloser("hello"), nil)

		blocks := []ContentBlock{
			{Type: BlockTypeText, Text: "the text"},
			{Type: BlockTypeImage, FileID: "img1", Filename: "shot.png", MimeType: "image/png"},
			{Type: BlockTypeFile, FileID: "doc1", Filename: "foo.txt", MimeType: "text/plain"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

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
	})

	t.Run("multiple text/plain files appear in input order in the message", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc-a").Return(&model.FileInfo{
			Id: "doc-a", Name: "a.txt", MimeType: "text/plain", Size: 5,
		}, nil)
		mmClient.On("GetFile", "doc-a").Return(newFakeReadCloser("AAAAA"), nil)
		mmClient.On("GetFileInfo", "doc-b").Return(&model.FileInfo{
			Id: "doc-b", Name: "b.txt", MimeType: "text/plain", Size: 5,
		}, nil)
		mmClient.On("GetFile", "doc-b").Return(newFakeReadCloser("BBBBB"), nil)

		blocks := []ContentBlock{
			{Type: BlockTypeText, Text: "two attachments"},
			{Type: BlockTypeFile, FileID: "doc-a", Filename: "a.txt", MimeType: "text/plain"},
			{Type: BlockTypeFile, FileID: "doc-b", Filename: "b.txt", MimeType: "text/plain"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		idxA := strings.Index(post.Message, "File Name: a.txt")
		idxB := strings.Index(post.Message, "File Name: b.txt")
		require.NotEqual(t, -1, idxA, "a.txt must appear in the message")
		require.NotEqual(t, -1, idxB, "b.txt must appear in the message")
		assert.Less(t, idxA, idxB,
			"file blocks must appear in input order in the Attached File Contents suffix")
	})

	t.Run("GetFile error on image is logged and skipped, conversation continues", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "img1").Return(&model.FileInfo{
			Id:       "img1",
			Name:     "broken.png",
			MimeType: "image/png",
		}, nil)
		mmClient.On("GetFile", "img1").Return(nil, errors.New("file store offline"))
		// LogError must actually be invoked — without .Maybe() this fails
		// if production silently swallows the error (the broken behavior).
		mmClient.On("LogError", mock.Anything, mock.Anything).Return()

		blocks := []ContentBlock{
			{Type: BlockTypeText, Text: "still here"},
			{Type: BlockTypeImage, FileID: "img1", Filename: "broken.png", MimeType: "image/png"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		// Pin that GetFile was actually attempted — production simply
		// dropping the image without trying to fetch it would still leave
		// post.Files empty, so the previous broken behavior would pass
		// without this AssertCalled.
		mmClient.AssertCalled(t, "GetFile", "img1")
		assert.Empty(t, post.Files, "GetFile errors must drop the entry, not abort the request")
		assert.Equal(t, "still here", post.Message,
			"text content must still appear when an image attachment fails to load")
	})

	// The maxFileSize=0 → DefaultMaxFileSize boundary trio. We pin the
	// constant by name so this test has to be updated alongside any change
	// to DefaultMaxFileSize.
	t.Run("maxFileSize=0 default: payload of DefaultMaxFileSize-1 bytes has no truncation marker", func(t *testing.T) {
		body := strings.Repeat("A", int(DefaultMaxFileSize-1))

		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id: "doc1", Name: "under.txt", MimeType: "text/plain", Size: int64(len(body)),
		}, nil)
		mmClient.On("GetFile", "doc1").Return(newFakeReadCloser(body), nil)

		blocks := []ContentBlock{
			{Type: BlockTypeFile, FileID: "doc1", Filename: "under.txt", MimeType: "text/plain"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		assert.Contains(t, post.Message, "File Name: under.txt")
		assert.NotContains(t, post.Message, "content truncated",
			"a payload of DefaultMaxFileSize-1 bytes must NOT be marked truncated")
	})

	t.Run("maxFileSize=0 default: payload of exactly DefaultMaxFileSize bytes gets truncation marker", func(t *testing.T) {
		body := strings.Repeat("A", int(DefaultMaxFileSize))

		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id: "doc1", Name: "boundary.txt", MimeType: "text/plain", Size: int64(len(body)),
		}, nil)
		mmClient.On("GetFile", "doc1").Return(newFakeReadCloser(body), nil)

		blocks := []ContentBlock{
			{Type: BlockTypeFile, FileID: "doc1", Filename: "boundary.txt", MimeType: "text/plain"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		assert.Contains(t, post.Message, "... (content truncated due to size limit)",
			"reading exactly DefaultMaxFileSize bytes must append the truncation marker (LimitReader saw the cap)")
	})

	t.Run("maxFileSize=0 default: payload of DefaultMaxFileSize+1 bytes gets truncation marker", func(t *testing.T) {
		body := strings.Repeat("A", int(DefaultMaxFileSize+1))

		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id: "doc1", Name: "over.txt", MimeType: "text/plain", Size: int64(len(body)),
		}, nil)
		mmClient.On("GetFile", "doc1").Return(newFakeReadCloser(body), nil)

		blocks := []ContentBlock{
			{Type: BlockTypeFile, FileID: "doc1", Filename: "over.txt", MimeType: "text/plain"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		assert.Contains(t, post.Message, "... (content truncated due to size limit)",
			"a payload above DefaultMaxFileSize must be truncated with the marker")
	})

	t.Run("nil mmClient is safe when no file or image blocks are present", func(t *testing.T) {
		// Documents the zero-config safety guarantee: a turn with only
		// text/thinking/tool blocks must not panic when mmClient is nil.
		blocks := []ContentBlock{
			{Type: BlockTypeText, Text: "hello"},
			{Type: BlockTypeThinking, Text: "reason", Signature: "sig"},
			{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{}`), Status: StatusSuccess, Shared: BoolPtr(true)},
		}

		require.NotPanics(t, func() {
			post := BlocksToPost(blocks, "assistant", false, nil, false, 0)
			assert.Equal(t, "hello", post.Message)
			assert.Equal(t, "reason", post.Reasoning)
			require.Len(t, post.ToolUse, 1)
			assert.Equal(t, "tc1", post.ToolUse[0].ID)
		})
	})

	t.Run("BlockTypeFile with image/* MIME goes through file path, not image path", func(t *testing.T) {
		// Dispatch must be by block.Type, not by MimeType — a malformed
		// block (Type=file but MimeType=image/png) must not be sent to
		// the LLM as an image. With empty FileInfo.Content and a
		// non-text MIME, the file-text path skips the block, so GetFile
		// must NOT be called.
		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id: "doc1", Name: "weird.png", MimeType: "image/png",
		}, nil)
		// No GetFile expectation — mockery fails the test if production
		// fetches bytes anyway (which would happen if dispatch went by
		// MimeType into the image branch).

		blocks := []ContentBlock{
			{Type: BlockTypeText, Text: "see attached"},
			{Type: BlockTypeFile, FileID: "doc1", Filename: "weird.png", MimeType: "image/png"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		mmClient.AssertNotCalled(t, "GetFile", "doc1")
		assert.Empty(t, post.Files,
			"a BlockTypeFile must never populate Post.Files even when its MimeType says image/*")
		assert.Equal(t, "see attached", post.Message,
			"a non-text non-image file block with empty Content must be silently skipped")
	})

	t.Run("FileInfo.Content non-empty wins over GetFile (explicit AssertNotCalled)", func(t *testing.T) {
		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id: "doc1", Name: "extracted.pdf", MimeType: "application/pdf",
			Content: "server-extracted text",
		}, nil)
		// No GetFile expectation: when fileInfo.Content is non-empty,
		// the byte fetch must be skipped entirely.

		blocks := []ContentBlock{
			{Type: BlockTypeFile, FileID: "doc1", Filename: "extracted.pdf", MimeType: "application/pdf"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, 0)

		mmClient.AssertNotCalled(t, "GetFile", "doc1",
			"pre-extracted FileInfo.Content must short-circuit GetFile to avoid a redundant blob read")
		assert.Contains(t, post.Message, "Content: server-extracted text")
	})

	t.Run("pre-extracted FileInfo.Content larger than maxFileSize gets truncation marker", func(t *testing.T) {
		// Mattermost's server-side text extraction is itself bounded, but
		// a per-bot MaxFileSize lower than the server's cap could be
		// silently violated by the pre-extracted-content shortcut. Cap it
		// the same way the GetFile branch does.
		const maxBytes = int64(8)
		oversized := strings.Repeat("X", int(maxBytes)+1) // 9 bytes vs maxBytes=8

		mmClient := mmapimocks.NewMockClient(t)
		mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
			Id: "doc1", Name: "huge.pdf", MimeType: "application/pdf",
			Content: oversized,
		}, nil)
		// GetFile must NOT be called — pre-extracted content path still
		// short-circuits the byte fetch even when the cap clips it.

		blocks := []ContentBlock{
			{Type: BlockTypeFile, FileID: "doc1", Filename: "huge.pdf", MimeType: "application/pdf"},
		}

		post := BlocksToPost(blocks, "user", false, mmClient, true, maxBytes)

		mmClient.AssertNotCalled(t, "GetFile", "doc1",
			"the pre-extracted-content cap must not trigger a GetFile fetch")
		assert.Contains(t, post.Message, "... (content truncated due to size limit)",
			"FileInfo.Content longer than effectiveMax must be truncated with the marker")
	})
}
