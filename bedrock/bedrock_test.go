// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bedrock

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

func TestIsValidImageType(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		expected bool
	}{
		{"JPEG", "image/jpeg", true},
		{"PNG", "image/png", true},
		{"GIF", "image/gif", true},
		{"WebP", "image/webp", true},
		{"Invalid", "image/bmp", false},
		{"Invalid", "text/plain", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidImageType(tt.mimeType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConversationToMessages(t *testing.T) {
	t.Run("system and user messages", func(t *testing.T) {
		posts := []llm.Post{
			{Role: llm.PostRoleSystem, Message: "You are a helpful assistant."},
			{Role: llm.PostRoleUser, Message: "Hello!"},
		}

		system, messages := conversationToMessages(posts)

		require.Len(t, system, 1)
		require.Len(t, messages, 1)

		// Check system message
		systemText, ok := system[0].(*types.SystemContentBlockMemberText)
		require.True(t, ok)
		assert.Equal(t, "You are a helpful assistant.", systemText.Value)

		// Check user message
		assert.Equal(t, types.ConversationRoleUser, messages[0].Role)
		require.Len(t, messages[0].Content, 1)
		contentText, ok := messages[0].Content[0].(*types.ContentBlockMemberText)
		require.True(t, ok)
		assert.Equal(t, "Hello!", contentText.Value)
	})

	t.Run("alternating user and assistant messages", func(t *testing.T) {
		posts := []llm.Post{
			{Role: llm.PostRoleUser, Message: "Hello!"},
			{Role: llm.PostRoleBot, Message: "Hi there!"},
			{Role: llm.PostRoleUser, Message: "How are you?"},
			{Role: llm.PostRoleBot, Message: "I'm doing well!"},
		}

		system, messages := conversationToMessages(posts)

		require.Len(t, system, 0)
		require.Len(t, messages, 4)

		assert.Equal(t, types.ConversationRoleUser, messages[0].Role)
		assert.Equal(t, types.ConversationRoleAssistant, messages[1].Role)
		assert.Equal(t, types.ConversationRoleUser, messages[2].Role)
		assert.Equal(t, types.ConversationRoleAssistant, messages[3].Role)
	})

	t.Run("consecutive same-role messages are merged", func(t *testing.T) {
		posts := []llm.Post{
			{Role: llm.PostRoleUser, Message: "Hello!"},
			{Role: llm.PostRoleUser, Message: "Anyone there?"},
		}

		system, messages := conversationToMessages(posts)

		require.Len(t, system, 0)
		require.Len(t, messages, 1)

		assert.Equal(t, types.ConversationRoleUser, messages[0].Role)
		require.Len(t, messages[0].Content, 2)
	})

	t.Run("user message with image", func(t *testing.T) {
		imageData := []byte("fake png data")
		posts := []llm.Post{
			{
				Role:    llm.PostRoleUser,
				Message: "What's in this image?",
				Files: []llm.File{
					{
						MimeType: "image/png",
						Reader:   strings.NewReader(string(imageData)),
					},
				},
			},
		}

		system, messages := conversationToMessages(posts)

		require.Len(t, system, 0)
		require.Len(t, messages, 1)
		assert.Equal(t, types.ConversationRoleUser, messages[0].Role)
		require.Len(t, messages[0].Content, 2) // text + image

		// Check text content
		textBlock, ok := messages[0].Content[0].(*types.ContentBlockMemberText)
		require.True(t, ok)
		assert.Equal(t, "What's in this image?", textBlock.Value)

		// Check image content
		imageBlock, ok := messages[0].Content[1].(*types.ContentBlockMemberImage)
		require.True(t, ok)
		assert.Equal(t, types.ImageFormatPng, imageBlock.Value.Format)
	})

	t.Run("user message with unsupported image type", func(t *testing.T) {
		posts := []llm.Post{
			{
				Role:    llm.PostRoleUser,
				Message: "Check this file",
				Files: []llm.File{
					{
						MimeType: "image/bmp",
						Reader:   strings.NewReader("fake bmp data"),
					},
				},
			},
		}

		system, messages := conversationToMessages(posts)

		require.Len(t, system, 0)
		require.Len(t, messages, 1)
		require.Len(t, messages[0].Content, 2) // text + unsupported message

		// Second block should be text indicating unsupported type
		textBlock, ok := messages[0].Content[1].(*types.ContentBlockMemberText)
		require.True(t, ok)
		assert.Contains(t, textBlock.Value, "Unsupported image type")
	})

	t.Run("tool use in assistant message", func(t *testing.T) {
		posts := []llm.Post{
			{Role: llm.PostRoleUser, Message: "What's the weather?"},
			{
				Role:    llm.PostRoleBot,
				Message: "Let me check that for you.",
				ToolUse: []llm.ToolCall{
					{
						ID:        "tool-1",
						Name:      "get_weather",
						Arguments: []byte(`{"location": "Boston"}`),
						Status:    llm.ToolCallStatusSuccess,
						Result:    "72°F and sunny",
					},
				},
			},
		}

		system, messages := conversationToMessages(posts)

		require.Len(t, system, 0)
		require.Len(t, messages, 3) // user, assistant with tool use, user with tool result

		// Check assistant message has text and tool use
		assert.Equal(t, types.ConversationRoleAssistant, messages[1].Role)
		require.Len(t, messages[1].Content, 2) // text + tool use

		// Check tool result message
		assert.Equal(t, types.ConversationRoleUser, messages[2].Role)
		require.Len(t, messages[2].Content, 1)
		toolResult, ok := messages[2].Content[0].(*types.ContentBlockMemberToolResult)
		require.True(t, ok)
		assert.Equal(t, "tool-1", aws.ToString(toolResult.Value.ToolUseId))
		assert.Equal(t, types.ToolResultStatusSuccess, toolResult.Value.Status)
	})
}

func TestConvertTools(t *testing.T) {
	t.Run("convert single tool", func(t *testing.T) {
		schema := &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"location": {
					Type:        "string",
					Description: "The city name",
				},
			},
			Required: []string{"location"},
		}

		tools := []llm.Tool{
			{
				Name:        "get_weather",
				Description: "Get the current weather",
				Schema:      schema,
			},
		}

		converted := convertTools(tools)

		require.Len(t, converted, 1)
		require.NotNil(t, converted[0])

		// Type assert to ToolMemberToolSpec
		toolSpec, ok := converted[0].(*types.ToolMemberToolSpec)
		require.True(t, ok)
		assert.Equal(t, "get_weather", aws.ToString(toolSpec.Value.Name))
		assert.Equal(t, "Get the current weather", aws.ToString(toolSpec.Value.Description))
		require.NotNil(t, toolSpec.Value.InputSchema)
	})

	t.Run("convert multiple tools", func(t *testing.T) {
		schema := &jsonschema.Schema{
			Type:       "object",
			Properties: map[string]*jsonschema.Schema{},
		}

		tools := []llm.Tool{
			{
				Name:        "tool1",
				Description: "First tool",
				Schema:      schema,
			},
			{
				Name:        "tool2",
				Description: "Second tool",
				Schema:      schema,
			},
		}

		converted := convertTools(tools)

		require.Len(t, converted, 2)

		toolSpec1, ok := converted[0].(*types.ToolMemberToolSpec)
		require.True(t, ok)
		assert.Equal(t, "tool1", aws.ToString(toolSpec1.Value.Name))

		toolSpec2, ok := converted[1].(*types.ToolMemberToolSpec)
		require.True(t, ok)
		assert.Equal(t, "tool2", aws.ToString(toolSpec2.Value.Name))
	})

	t.Run("empty tools array", func(t *testing.T) {
		tools := []llm.Tool{}
		converted := convertTools(tools)
		assert.Len(t, converted, 0)
	})
}

func TestGetDefaultConfig(t *testing.T) {
	b := &Bedrock{defaultModel: "test-model", outputTokenLimit: 4096}
	config := b.GetDefaultConfig()
	assert.Equal(t, "test-model", config.Model)
	assert.Equal(t, 4096, config.MaxGeneratedTokens)

	b2 := &Bedrock{defaultModel: "test-model", outputTokenLimit: 0}
	config2 := b2.GetDefaultConfig()
	assert.Equal(t, DefaultMaxTokens, config2.MaxGeneratedTokens)
}

func TestInputTokenLimit(t *testing.T) {
	// Custom limit takes precedence
	b := &Bedrock{inputTokenLimit: 150000}
	assert.Equal(t, 150000, b.InputTokenLimit())

	// Default limit when not configured
	b2 := &Bedrock{inputTokenLimit: 0}
	assert.Equal(t, 200000, b2.InputTokenLimit())
}

func TestCountTokens(t *testing.T) {
	b := &Bedrock{}

	// CountTokens uses: (len(text)/4.0 + len(Fields)/0.75) / 2.0
	assert.Equal(t, 0, b.CountTokens(""))
	assert.Equal(t, 2, b.CountTokens("Hello world"))
	assert.Equal(t, 12, b.CountTokens("This is a longer piece of text with more words"))
}

func TestExtractToolCallsFromBlocks(t *testing.T) {
	tests := []struct {
		name           string
		toolBlocks     map[int]*toolUseData
		expectedCalls  []llm.ToolCall
		expectedLength int
	}{
		{
			name:           "empty blocks returns empty slice",
			toolBlocks:     map[int]*toolUseData{},
			expectedCalls:  []llm.ToolCall{},
			expectedLength: 0,
		},
		{
			name: "single tool block extracts correctly",
			toolBlocks: map[int]*toolUseData{
				0: {
					id:   "tool-1",
					name: "get_weather",
					inputJSON: func() strings.Builder {
						var sb strings.Builder
						sb.WriteString(`{"location": "Boston"}`)
						return sb
					}(),
				},
			},
			expectedCalls: []llm.ToolCall{
				{
					ID:          "tool-1",
					Name:        "get_weather",
					Description: "",
					Arguments:   []byte(`{"location": "Boston"}`),
				},
			},
			expectedLength: 1,
		},
		{
			name: "multiple tool blocks extract correctly",
			toolBlocks: map[int]*toolUseData{
				0: {
					id:   "tool-1",
					name: "get_weather",
					inputJSON: func() strings.Builder {
						var sb strings.Builder
						sb.WriteString(`{"location": "Boston"}`)
						return sb
					}(),
				},
				1: {
					id:   "tool-2",
					name: "search_web",
					inputJSON: func() strings.Builder {
						var sb strings.Builder
						sb.WriteString(`{"query": "news"}`)
						return sb
					}(),
				},
			},
			expectedCalls: []llm.ToolCall{
				{
					ID:          "tool-1",
					Name:        "get_weather",
					Description: "",
					Arguments:   []byte(`{"location": "Boston"}`),
				},
				{
					ID:          "tool-2",
					Name:        "search_web",
					Description: "",
					Arguments:   []byte(`{"query": "news"}`),
				},
			},
			expectedLength: 2,
		},
		{
			name: "tool with complex JSON arguments",
			toolBlocks: map[int]*toolUseData{
				0: {
					id:   "tool-complex",
					name: "complex_tool",
					inputJSON: func() strings.Builder {
						var sb strings.Builder
						sb.WriteString(`{"nested": {"key": "value"}, "array": [1, 2, 3], "bool": true}`)
						return sb
					}(),
				},
			},
			expectedCalls: []llm.ToolCall{
				{
					ID:          "tool-complex",
					Name:        "complex_tool",
					Description: "",
					Arguments:   []byte(`{"nested": {"key": "value"}, "array": [1, 2, 3], "bool": true}`),
				},
			},
			expectedLength: 1,
		},
		{
			name: "tool with empty input gets default empty object",
			toolBlocks: map[int]*toolUseData{
				0: {
					id:        "tool-empty",
					name:      "no_args_tool",
					inputJSON: strings.Builder{}, // empty builder
				},
			},
			expectedCalls: []llm.ToolCall{
				{
					ID:          "tool-empty",
					Name:        "no_args_tool",
					Description: "",
					Arguments:   []byte(`{}`),
				},
			},
			expectedLength: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractToolCallsFromBlocks(tt.toolBlocks)
			require.Len(t, result, tt.expectedLength)

			for i, expected := range tt.expectedCalls {
				assert.Equal(t, expected.ID, result[i].ID)
				assert.Equal(t, expected.Name, result[i].Name)
				assert.Equal(t, expected.Description, result[i].Description)
				assert.JSONEq(t, string(expected.Arguments), string(result[i].Arguments))
			}
		})
	}
}

func TestBuildBedrockAssistantMessage(t *testing.T) {
	tests := []struct {
		name               string
		textContent        string
		toolBlocks         map[int]*toolUseData
		expectedContentLen int
		validateFn         func(t *testing.T, msg types.Message)
	}{
		{
			name:               "text only (no tool blocks)",
			textContent:        "Let me help you with that.",
			toolBlocks:         map[int]*toolUseData{},
			expectedContentLen: 1,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleAssistant, msg.Role)
				textBlock, ok := msg.Content[0].(*types.ContentBlockMemberText)
				require.True(t, ok)
				assert.Equal(t, "Let me help you with that.", textBlock.Value)
			},
		},
		{
			name:        "tool blocks only (no text)",
			textContent: "",
			toolBlocks: map[int]*toolUseData{
				0: {
					id:   "tool-1",
					name: "get_weather",
					inputJSON: func() strings.Builder {
						var sb strings.Builder
						sb.WriteString(`{"location": "Boston"}`)
						return sb
					}(),
				},
			},
			expectedContentLen: 1,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleAssistant, msg.Role)
				toolBlock, ok := msg.Content[0].(*types.ContentBlockMemberToolUse)
				require.True(t, ok)
				assert.Equal(t, "tool-1", aws.ToString(toolBlock.Value.ToolUseId))
				assert.Equal(t, "get_weather", aws.ToString(toolBlock.Value.Name))
			},
		},
		{
			name:        "both text and tool blocks",
			textContent: "Let me check the weather for you.",
			toolBlocks: map[int]*toolUseData{
				0: {
					id:   "tool-1",
					name: "get_weather",
					inputJSON: func() strings.Builder {
						var sb strings.Builder
						sb.WriteString(`{"location": "Boston"}`)
						return sb
					}(),
				},
			},
			expectedContentLen: 2,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleAssistant, msg.Role)
				require.Len(t, msg.Content, 2)

				// First block should be text
				textBlock, ok := msg.Content[0].(*types.ContentBlockMemberText)
				require.True(t, ok)
				assert.Equal(t, "Let me check the weather for you.", textBlock.Value)

				// Second block should be tool use
				toolBlock, ok := msg.Content[1].(*types.ContentBlockMemberToolUse)
				require.True(t, ok)
				assert.Equal(t, "tool-1", aws.ToString(toolBlock.Value.ToolUseId))
				assert.Equal(t, "get_weather", aws.ToString(toolBlock.Value.Name))
			},
		},
		{
			name:               "empty text and empty tool blocks",
			textContent:        "",
			toolBlocks:         map[int]*toolUseData{},
			expectedContentLen: 0,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleAssistant, msg.Role)
				assert.Len(t, msg.Content, 0)
			},
		},
		{
			name:        "multiple tool blocks",
			textContent: "I'll use multiple tools to help.",
			toolBlocks: map[int]*toolUseData{
				0: {
					id:   "tool-1",
					name: "get_weather",
					inputJSON: func() strings.Builder {
						var sb strings.Builder
						sb.WriteString(`{"location": "Boston"}`)
						return sb
					}(),
				},
				1: {
					id:   "tool-2",
					name: "search_web",
					inputJSON: func() strings.Builder {
						var sb strings.Builder
						sb.WriteString(`{"query": "Boston weather"}`)
						return sb
					}(),
				},
			},
			expectedContentLen: 3,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleAssistant, msg.Role)
				require.Len(t, msg.Content, 3)

				// First should be text
				textBlock, ok := msg.Content[0].(*types.ContentBlockMemberText)
				require.True(t, ok)
				assert.Equal(t, "I'll use multiple tools to help.", textBlock.Value)

				// Second and third should be tool use blocks
				toolBlock1, ok := msg.Content[1].(*types.ContentBlockMemberToolUse)
				require.True(t, ok)
				assert.Equal(t, "tool-1", aws.ToString(toolBlock1.Value.ToolUseId))

				toolBlock2, ok := msg.Content[2].(*types.ContentBlockMemberToolUse)
				require.True(t, ok)
				assert.Equal(t, "tool-2", aws.ToString(toolBlock2.Value.ToolUseId))
			},
		},
		{
			name:        "tool with invalid JSON gets empty object",
			textContent: "",
			toolBlocks: map[int]*toolUseData{
				0: {
					id:   "tool-bad",
					name: "bad_tool",
					inputJSON: func() strings.Builder {
						var sb strings.Builder
						sb.WriteString(`{invalid json}`)
						return sb
					}(),
				},
			},
			expectedContentLen: 1,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleAssistant, msg.Role)
				toolBlock, ok := msg.Content[0].(*types.ContentBlockMemberToolUse)
				require.True(t, ok)
				assert.Equal(t, "tool-bad", aws.ToString(toolBlock.Value.ToolUseId))
				assert.Equal(t, "bad_tool", aws.ToString(toolBlock.Value.Name))
				// Input should be an empty document
				require.NotNil(t, toolBlock.Value.Input)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildBedrockAssistantMessage(tt.textContent, tt.toolBlocks)
			assert.Len(t, result.Content, tt.expectedContentLen)
			tt.validateFn(t, result)
		})
	}
}

func TestBuildBedrockToolResultsMessage(t *testing.T) {
	tests := []struct {
		name               string
		results            []llm.AutoRunResult
		expectedContentLen int
		validateFn         func(t *testing.T, msg types.Message)
	}{
		{
			name: "single result",
			results: []llm.AutoRunResult{
				{
					ToolCallID: "tool-1",
					ToolName:   "get_weather",
					Result:     "72°F and sunny",
					IsError:    false,
				},
			},
			expectedContentLen: 1,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleUser, msg.Role)
				require.Len(t, msg.Content, 1)

				resultBlock, ok := msg.Content[0].(*types.ContentBlockMemberToolResult)
				require.True(t, ok)
				assert.Equal(t, "tool-1", aws.ToString(resultBlock.Value.ToolUseId))
				assert.Equal(t, types.ToolResultStatusSuccess, resultBlock.Value.Status)
				require.Len(t, resultBlock.Value.Content, 1)

				textContent, ok := resultBlock.Value.Content[0].(*types.ToolResultContentBlockMemberText)
				require.True(t, ok)
				assert.Equal(t, "72°F and sunny", textContent.Value)
			},
		},
		{
			name: "multiple results",
			results: []llm.AutoRunResult{
				{
					ToolCallID: "tool-1",
					ToolName:   "get_weather",
					Result:     "72°F and sunny",
					IsError:    false,
				},
				{
					ToolCallID: "tool-2",
					ToolName:   "search_web",
					Result:     "Found 5 results",
					IsError:    false,
				},
			},
			expectedContentLen: 2,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleUser, msg.Role)
				require.Len(t, msg.Content, 2)

				// Check first result
				resultBlock1, ok := msg.Content[0].(*types.ContentBlockMemberToolResult)
				require.True(t, ok)
				assert.Equal(t, "tool-1", aws.ToString(resultBlock1.Value.ToolUseId))
				assert.Equal(t, types.ToolResultStatusSuccess, resultBlock1.Value.Status)

				// Check second result
				resultBlock2, ok := msg.Content[1].(*types.ContentBlockMemberToolResult)
				require.True(t, ok)
				assert.Equal(t, "tool-2", aws.ToString(resultBlock2.Value.ToolUseId))
				assert.Equal(t, types.ToolResultStatusSuccess, resultBlock2.Value.Status)
			},
		},
		{
			name: "result with error status",
			results: []llm.AutoRunResult{
				{
					ToolCallID: "tool-error",
					ToolName:   "failing_tool",
					Result:     "Error: connection timeout",
					IsError:    true,
				},
			},
			expectedContentLen: 1,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleUser, msg.Role)
				require.Len(t, msg.Content, 1)

				resultBlock, ok := msg.Content[0].(*types.ContentBlockMemberToolResult)
				require.True(t, ok)
				assert.Equal(t, "tool-error", aws.ToString(resultBlock.Value.ToolUseId))
				assert.Equal(t, types.ToolResultStatusError, resultBlock.Value.Status)

				textContent, ok := resultBlock.Value.Content[0].(*types.ToolResultContentBlockMemberText)
				require.True(t, ok)
				assert.Equal(t, "Error: connection timeout", textContent.Value)
			},
		},
		{
			name:               "empty results",
			results:            []llm.AutoRunResult{},
			expectedContentLen: 0,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleUser, msg.Role)
				assert.Len(t, msg.Content, 0)
			},
		},
		{
			name: "mixed success and error results",
			results: []llm.AutoRunResult{
				{
					ToolCallID: "tool-1",
					ToolName:   "working_tool",
					Result:     "Success result",
					IsError:    false,
				},
				{
					ToolCallID: "tool-2",
					ToolName:   "failing_tool",
					Result:     "Error result",
					IsError:    true,
				},
				{
					ToolCallID: "tool-3",
					ToolName:   "another_working_tool",
					Result:     "Another success",
					IsError:    false,
				},
			},
			expectedContentLen: 3,
			validateFn: func(t *testing.T, msg types.Message) {
				assert.Equal(t, types.ConversationRoleUser, msg.Role)
				require.Len(t, msg.Content, 3)

				// First result should be success
				resultBlock1, ok := msg.Content[0].(*types.ContentBlockMemberToolResult)
				require.True(t, ok)
				assert.Equal(t, types.ToolResultStatusSuccess, resultBlock1.Value.Status)

				// Second result should be error
				resultBlock2, ok := msg.Content[1].(*types.ContentBlockMemberToolResult)
				require.True(t, ok)
				assert.Equal(t, types.ToolResultStatusError, resultBlock2.Value.Status)

				// Third result should be success
				resultBlock3, ok := msg.Content[2].(*types.ContentBlockMemberToolResult)
				require.True(t, ok)
				assert.Equal(t, types.ToolResultStatusSuccess, resultBlock3.Value.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildBedrockToolResultsMessage(tt.results)
			assert.Len(t, result.Content, tt.expectedContentLen)
			tt.validateFn(t, result)
		})
	}
}
