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
