// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package openai

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostsToChatCompletionMessages(t *testing.T) {
	tests := []struct {
		name  string
		posts []llm.Post
		check func(t *testing.T, messages []openai.ChatCompletionMessageParamUnion)
	}{
		{
			name: "basic conversation",
			posts: []llm.Post{
				{Role: llm.PostRoleSystem, Message: "You are a helpful assistant"},
				{Role: llm.PostRoleUser, Message: "Hello"},
				{Role: llm.PostRoleBot, Message: "Hi there!"},
			},
			check: func(t *testing.T, messages []openai.ChatCompletionMessageParamUnion) {
				require.Len(t, messages, 3)

				// Check system message
				assert.NotNil(t, messages[0].OfSystem)
				if messages[0].OfSystem != nil {
					// Content should contain the system message
					assert.NotNil(t, messages[0].OfSystem.Content)
				}

				// Check user message
				assert.NotNil(t, messages[1].OfUser)
				if messages[1].OfUser != nil {
					// Content should contain the user message
					assert.NotNil(t, messages[1].OfUser.Content)
				}

				// Check assistant message
				assert.NotNil(t, messages[2].OfAssistant)
			},
		},
		{
			name: "user message with images",
			posts: []llm.Post{
				{
					Role:    llm.PostRoleUser,
					Message: "Look at this image:",
					Files: []llm.File{
						{
							MimeType: "image/jpeg",
							Reader:   bytes.NewReader([]byte("fake-image-data")),
							Size:     15,
						},
						{
							MimeType: "image/png",
							Reader:   bytes.NewReader([]byte("fake-png-data")),
							Size:     13,
						},
					},
				},
			},
			check: func(t *testing.T, messages []openai.ChatCompletionMessageParamUnion) {
				require.Len(t, messages, 1)
				assert.NotNil(t, messages[0].OfUser)
				// The user message should have multipart content with text and images
				if messages[0].OfUser != nil {
					// Content should be an array type with text and image parts
					assert.NotNil(t, messages[0].OfUser.Content)
				}
			},
		},
		{
			name: "unsupported image type",
			posts: []llm.Post{
				{
					Role: llm.PostRoleUser,
					Files: []llm.File{
						{
							MimeType: "image/tiff",
							Reader:   bytes.NewReader([]byte("fake-tiff-data")),
							Size:     14,
						},
					},
				},
			},
			check: func(t *testing.T, messages []openai.ChatCompletionMessageParamUnion) {
				require.Len(t, messages, 1)
				assert.NotNil(t, messages[0].OfUser)
			},
		},
		{
			name: "oversized image",
			posts: []llm.Post{
				{
					Role:    llm.PostRoleUser,
					Message: "Check this huge image:",
					Files: []llm.File{
						{
							MimeType: "image/jpeg",
							Reader:   bytes.NewReader([]byte("fake-image-data")),
							Size:     OpenAIMaxImageSize + 1, // Over 20MB
						},
					},
				},
			},
			check: func(t *testing.T, messages []openai.ChatCompletionMessageParamUnion) {
				require.Len(t, messages, 1)
				assert.NotNil(t, messages[0].OfUser)
				// Should have a message about the image being too large
			},
		},
		{
			name: "assistant message with tool calls",
			posts: []llm.Post{
				{
					Role:    llm.PostRoleBot,
					Message: "I'll search for that",
					ToolUse: []llm.ToolCall{
						{
							ID:        "call_123",
							Name:      "search",
							Arguments: []byte(`{"query":"test"}`),
							Result:    "Found 3 results",
							Status:    llm.ToolCallStatusSuccess,
						},
					},
				},
			},
			check: func(t *testing.T, messages []openai.ChatCompletionMessageParamUnion) {
				// Should have assistant message with tool call and tool result message
				require.Len(t, messages, 2)

				// First message should be assistant with tool calls
				assert.NotNil(t, messages[0].OfAssistant)
				if messages[0].OfAssistant != nil {
					assert.NotEmpty(t, messages[0].OfAssistant.ToolCalls)
				}

				// Second message should be tool result
				assert.NotNil(t, messages[1].OfTool)
				if messages[1].OfTool != nil {
					// Content is wrapped in param.Opt, check the Value field
					assert.Equal(t, "Found 3 results", messages[1].OfTool.Content.OfString.Value)
				}
			},
		},
		{
			name: "multiple tool calls",
			posts: []llm.Post{
				{
					Role: llm.PostRoleBot,
					ToolUse: []llm.ToolCall{
						{
							ID:        "call_1",
							Name:      "search",
							Arguments: []byte(`{"query":"test1"}`),
							Result:    "Result 1",
							Status:    llm.ToolCallStatusSuccess,
						},
						{
							ID:        "call_2",
							Name:      "calculate",
							Arguments: []byte(`{"expression":"2+2"}`),
							Result:    "4",
							Status:    llm.ToolCallStatusSuccess,
						},
					},
				},
			},
			check: func(t *testing.T, messages []openai.ChatCompletionMessageParamUnion) {
				// Should have 1 assistant message with tool calls + 2 tool result messages
				require.Len(t, messages, 3)

				// First message should be assistant with tool calls
				assert.NotNil(t, messages[0].OfAssistant)
				if messages[0].OfAssistant != nil {
					assert.Len(t, messages[0].OfAssistant.ToolCalls, 2)
				}

				// Next two messages should be tool results
				assert.NotNil(t, messages[1].OfTool)
				assert.NotNil(t, messages[2].OfTool)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := postsToChatCompletionMessages(tt.posts)
			tt.check(t, messages)
		})
	}
}

func TestToolsToOpenAITools(t *testing.T) {
	tests := []struct {
		name     string
		tools    []llm.Tool
		expected int
		check    func(t *testing.T, result []openai.ChatCompletionToolUnionParam)
	}{
		{
			name: "single tool",
			tools: []llm.Tool{
				{
					Name:        "search",
					Description: "Search for information",
					Schema: &jsonschema.Schema{
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"query": {
								Type:        "string",
								Description: "The search query",
							},
						},
						Required: []string{"query"},
					},
				},
			},
			expected: 1,
			check: func(t *testing.T, result []openai.ChatCompletionToolUnionParam) {
				require.Len(t, result, 1)
				assert.NotNil(t, result[0].OfFunction)
				if result[0].OfFunction != nil {
					assert.Equal(t, "search", result[0].OfFunction.Function.Name)
					assert.Equal(t, "Search for information", result[0].OfFunction.Function.Description.Value)
				}
			},
		},
		{
			name: "multiple tools",
			tools: []llm.Tool{
				{
					Name:        "search",
					Description: "Search tool",
					Schema:      &jsonschema.Schema{Type: "object"},
				},
				{
					Name:        "calculate",
					Description: "Calculator tool",
					Schema:      &jsonschema.Schema{Type: "object"},
				},
			},
			expected: 2,
			check: func(t *testing.T, result []openai.ChatCompletionToolUnionParam) {
				require.Len(t, result, 2)
				assert.NotNil(t, result[0].OfFunction)
				assert.NotNil(t, result[1].OfFunction)
			},
		},
		{
			name:     "empty tools",
			tools:    []llm.Tool{},
			expected: 0,
			check: func(t *testing.T, result []openai.ChatCompletionToolUnionParam) {
				assert.Empty(t, result)
			},
		},
		{
			name: "tool with no parameters (like atlassianUserInfo)",
			tools: []llm.Tool{
				{
					Name:        "atlassianUserInfo",
					Description: "Get current user info from Atlassian",
					Schema: &jsonschema.Schema{
						Type:       "object",
						Properties: map[string]*jsonschema.Schema{},
					},
				},
			},
			expected: 1,
			check: func(t *testing.T, result []openai.ChatCompletionToolUnionParam) {
				require.Len(t, result, 1)
				assert.NotNil(t, result[0].OfFunction)
				if result[0].OfFunction != nil {
					assert.Equal(t, "atlassianUserInfo", result[0].OfFunction.Function.Name)
					assert.Equal(t, "Get current user info from Atlassian", result[0].OfFunction.Function.Description.Value)

					// Most importantly, check that Parameters is not nil and has the required structure
					params := result[0].OfFunction.Function.Parameters
					assert.NotNil(t, params)
					assert.Equal(t, "object", params["type"])
					props, ok := params["properties"].(map[string]any)
					assert.True(t, ok, "properties should be a map")
					assert.Empty(t, props, "properties should be empty for parameterless tool")
				}
			},
		},
		{
			name: "tool with nil schema",
			tools: []llm.Tool{
				{
					Name:        "simpleAction",
					Description: "Simple action with no parameters",
					Schema:      nil,
				},
			},
			expected: 1,
			check: func(t *testing.T, result []openai.ChatCompletionToolUnionParam) {
				require.Len(t, result, 1)
				assert.NotNil(t, result[0].OfFunction)
				if result[0].OfFunction != nil {
					// Even with nil schema, we should get valid parameters
					params := result[0].OfFunction.Function.Parameters
					assert.NotNil(t, params)
					assert.Equal(t, "object", params["type"])
					props, ok := params["properties"].(map[string]any)
					assert.True(t, ok, "properties should be a map")
					assert.Empty(t, props, "properties should be empty for nil schema")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toolsToOpenAITools(tt.tools)
			assert.Len(t, result, tt.expected)
			tt.check(t, result)
		})
	}
}

func TestSchemaToFunctionParameters(t *testing.T) {
	tests := []struct {
		name   string
		schema *jsonschema.Schema
		check  func(t *testing.T, result shared.FunctionParameters)
	}{
		{
			name: "simple object schema",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"query": {
						Type:        "string",
						Description: "Search query",
					},
					"limit": {
						Type:        "integer",
						Description: "Result limit",
					},
				},
				Required: []string{"query"},
			},
			check: func(t *testing.T, result shared.FunctionParameters) {
				assert.Equal(t, "object", result["type"])
				props, ok := result["properties"].(map[string]interface{})
				assert.True(t, ok)
				if ok {
					queryProp, ok := props["query"].(map[string]interface{})
					assert.True(t, ok)
					if ok {
						assert.Equal(t, "string", queryProp["type"])
						assert.Equal(t, "Search query", queryProp["description"])
					}
				}
				assert.NotNil(t, result["required"])
			},
		},
		{
			name:   "nil schema",
			schema: nil,
			check: func(t *testing.T, result shared.FunctionParameters) {
				// When schema is nil, we should return a basic object schema
				// with type="object" and empty properties to satisfy OpenAI API requirements
				assert.Equal(t, "object", result["type"])
				props, ok := result["properties"].(map[string]any)
				assert.True(t, ok)
				assert.Empty(t, props)
			},
		},
		{
			name: "empty properties schema (like atlassianUserInfo)",
			schema: &jsonschema.Schema{
				Type:       "object",
				Properties: map[string]*jsonschema.Schema{},
			},
			check: func(t *testing.T, result shared.FunctionParameters) {
				// Even with empty properties, we should have type="object" and properties={}
				assert.Equal(t, "object", result["type"])
				props, ok := result["properties"].(map[string]interface{})
				assert.True(t, ok)
				assert.Empty(t, props)
			},
		},
		{
			name: "nested object schema",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"user": {
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"name": {Type: "string"},
							"age":  {Type: "integer"},
						},
					},
				},
			},
			check: func(t *testing.T, result shared.FunctionParameters) {
				assert.Equal(t, "object", result["type"])
				props, ok := result["properties"].(map[string]interface{})
				assert.True(t, ok)
				if ok {
					userProp, ok := props["user"].(map[string]interface{})
					assert.True(t, ok)
					if ok {
						assert.Equal(t, "object", userProp["type"])
						userProps, ok := userProp["properties"].(map[string]interface{})
						assert.True(t, ok)
						if ok {
							assert.NotNil(t, userProps["name"])
							assert.NotNil(t, userProps["age"])
						}
					}
				}
			},
		},
		{
			name: "schema without type field",
			schema: &jsonschema.Schema{
				Properties: map[string]*jsonschema.Schema{
					"field1": {Type: "string"},
				},
			},
			check: func(t *testing.T, result shared.FunctionParameters) {
				// Should default to "object" type
				assert.Equal(t, "object", result["type"])
				assert.NotNil(t, result["properties"])
			},
		},
		{
			name: "schema with only required field",
			schema: &jsonschema.Schema{
				Required: []string{"field1"},
			},
			check: func(t *testing.T, result shared.FunctionParameters) {
				// Should have type and properties even if only required is specified
				assert.Equal(t, "object", result["type"])
				props, ok := result["properties"].(map[string]any)
				assert.True(t, ok)
				assert.Empty(t, props)
				assert.NotNil(t, result["required"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := schemaToFunctionParameters(tt.schema)
			tt.check(t, result)
		})
	}
}

func TestGetModelConstant(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected shared.ChatModel
	}{
		{
			name:     "gpt-4o model",
			model:    "gpt-4o",
			expected: shared.ChatModelGPT4o,
		},
		{
			name:     "gpt-4o-mini model",
			model:    "gpt-4o-mini",
			expected: shared.ChatModelGPT4oMini,
		},
		{
			name:     "gpt-4-turbo model",
			model:    "gpt-4-turbo",
			expected: shared.ChatModelGPT4Turbo,
		},
		{
			name:     "gpt-4 model",
			model:    "gpt-4",
			expected: shared.ChatModelGPT4,
		},
		{
			name:     "gpt-3.5-turbo model",
			model:    "gpt-3.5-turbo",
			expected: shared.ChatModelGPT3_5Turbo,
		},
		{
			name:     "o1-preview model",
			model:    "o1-preview",
			expected: shared.ChatModelO1Preview,
		},
		{
			name:     "o1-mini model",
			model:    "o1-mini",
			expected: shared.ChatModelO1Mini,
		},
		{
			name:     "custom model",
			model:    "custom-model-xyz",
			expected: shared.ChatModel("custom-model-xyz"),
		},
		{
			name:     "gpt-4-32k model (custom)",
			model:    "gpt-4-32k",
			expected: shared.ChatModel("gpt-4-32k"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getModelConstant(tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEmbeddingModelConstant(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected openai.EmbeddingModel
	}{
		{
			name:     "text-embedding-3-large",
			model:    "text-embedding-3-large",
			expected: openai.EmbeddingModelTextEmbedding3Large,
		},
		{
			name:     "text-embedding-3-small",
			model:    "text-embedding-3-small",
			expected: openai.EmbeddingModelTextEmbedding3Small,
		},
		{
			name:     "text-embedding-ada-002",
			model:    "text-embedding-ada-002",
			expected: openai.EmbeddingModelTextEmbeddingAda002,
		},
		{
			name:     "custom embedding model",
			model:    "custom-embedding-model",
			expected: openai.EmbeddingModel("custom-embedding-model"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getEmbeddingModelConstant(tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInputTokenLimit(t *testing.T) {
	tests := []struct {
		name          string
		config        Config
		expectedLimit int
	}{
		{
			name: "explicit input token limit",
			config: Config{
				InputTokenLimit: 50000,
				DefaultModel:    "gpt-4o",
			},
			expectedLimit: 50000,
		},
		{
			name: "gpt-4o model default",
			config: Config{
				DefaultModel: "gpt-4o",
			},
			expectedLimit: 128000,
		},
		{
			name: "o1-preview model default",
			config: Config{
				DefaultModel: "o1-preview",
			},
			expectedLimit: 128000,
		},
		{
			name: "gpt-4-turbo model default",
			config: Config{
				DefaultModel: "gpt-4-turbo",
			},
			expectedLimit: 128000,
		},
		{
			name: "gpt-4 model default",
			config: Config{
				DefaultModel: "gpt-4",
			},
			expectedLimit: 8192,
		},
		{
			name: "gpt-3.5-turbo model default",
			config: Config{
				DefaultModel: "gpt-3.5-turbo",
			},
			expectedLimit: 16385,
		},
		{
			name: "gpt-3.5-turbo-instruct model default",
			config: Config{
				DefaultModel: "gpt-3.5-turbo-instruct",
			},
			expectedLimit: 4096,
		},
		{
			name: "unknown model default",
			config: Config{
				DefaultModel: "unknown-model",
			},
			expectedLimit: 128000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &OpenAI{config: tt.config}
			result := o.InputTokenLimit()
			assert.Equal(t, tt.expectedLimit, result)
		})
	}
}

func TestCountTokens(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		minCount int
		maxCount int
	}{
		{
			name:     "empty string",
			text:     "",
			minCount: 0,
			maxCount: 0,
		},
		{
			name:     "single word",
			text:     "hello",
			minCount: 1,
			maxCount: 3,
		},
		{
			name:     "short sentence",
			text:     "The quick brown fox jumps over the lazy dog",
			minCount: 8,
			maxCount: 15,
		},
		{
			name:     "long text",
			text:     "This is a longer piece of text that contains multiple sentences. It should have a higher token count than the shorter examples. The token counting is an approximation, so we're testing within a reasonable range.",
			minCount: 30,
			maxCount: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &OpenAI{}
			result := o.CountTokens(tt.text)
			assert.GreaterOrEqual(t, result, tt.minCount)
			assert.LessOrEqual(t, result, tt.maxCount)
		})
	}
}

func TestDimensions(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected int
	}{
		{
			name: "custom dimensions",
			config: Config{
				EmbeddingDimensions: 1536,
			},
			expected: 1536,
		},
		{
			name: "large embedding dimensions",
			config: Config{
				EmbeddingDimensions: 3072,
			},
			expected: 3072,
		},
		{
			name:     "zero dimensions",
			config:   Config{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &OpenAI{config: tt.config}
			result := o.Dimensions()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToolBufferElement(t *testing.T) {
	t.Run("tool buffer accumulation", func(t *testing.T) {
		buffer := &ToolBufferElement{}

		// Test ID accumulation
		buffer.id.WriteString("call_")
		buffer.id.WriteString("123")
		assert.Equal(t, "call_123", buffer.id.String())

		// Test name accumulation
		buffer.name.WriteString("search")
		buffer.name.WriteString("_function")
		assert.Equal(t, "search_function", buffer.name.String())

		// Test arguments accumulation
		buffer.args.WriteString(`{"query":`)
		buffer.args.WriteString(`"test"}`)
		assert.Equal(t, `{"query":"test"}`, buffer.args.String())
	})
}

func TestReasoningEffortConfiguration(t *testing.T) {
	tests := []struct {
		name               string
		reasoningEnabled   bool
		reasoningEffort    string
		expectedEffort     shared.ReasoningEffort
		shouldSetReasoning bool
	}{
		{
			name:               "reasoning enabled with minimal effort",
			reasoningEnabled:   true,
			reasoningEffort:    "minimal",
			expectedEffort:     shared.ReasoningEffortMinimal,
			shouldSetReasoning: true,
		},
		{
			name:               "reasoning enabled with low effort",
			reasoningEnabled:   true,
			reasoningEffort:    "low",
			expectedEffort:     shared.ReasoningEffortLow,
			shouldSetReasoning: true,
		},
		{
			name:               "reasoning enabled with medium effort",
			reasoningEnabled:   true,
			reasoningEffort:    "medium",
			expectedEffort:     shared.ReasoningEffortMedium,
			shouldSetReasoning: true,
		},
		{
			name:               "reasoning enabled with high effort",
			reasoningEnabled:   true,
			reasoningEffort:    "high",
			expectedEffort:     shared.ReasoningEffortHigh,
			shouldSetReasoning: true,
		},
		{
			name:               "reasoning enabled with default (empty string defaults to medium)",
			reasoningEnabled:   true,
			reasoningEffort:    "",
			expectedEffort:     shared.ReasoningEffortMedium,
			shouldSetReasoning: true,
		},
		{
			name:               "reasoning enabled with invalid effort (defaults to medium)",
			reasoningEnabled:   true,
			reasoningEffort:    "invalid",
			expectedEffort:     shared.ReasoningEffortMedium,
			shouldSetReasoning: true,
		},
		{
			name:               "reasoning disabled",
			reasoningEnabled:   false,
			reasoningEffort:    "high",
			shouldSetReasoning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create an OpenAI instance with the test config
			oai := New(Config{
				APIKey:           "test-key",
				DefaultModel:     "gpt-4o",
				ReasoningEnabled: tt.reasoningEnabled,
				ReasoningEffort:  tt.reasoningEffort,
			}, &http.Client{})

			// Create test params
			chatParams := openai.ChatCompletionNewParams{
				Model:    shared.ChatModelGPT4o,
				Messages: []openai.ChatCompletionMessageParamUnion{},
			}

			// Call the actual function that handles reasoning configuration
			result := oai.convertToResponseParams(chatParams, &llm.Context{}, llm.LanguageModelConfig{
				Model:              "gpt-4o",
				MaxGeneratedTokens: 8192,
			})

			if !tt.shouldSetReasoning {
				// When reasoning is disabled, Reasoning should be empty
				assert.Equal(t, shared.ReasoningParam{}, result.Reasoning, "Reasoning should not be set when disabled")
				return
			}

			// When reasoning is enabled, verify the effort is set correctly
			assert.Equal(t, tt.expectedEffort, result.Reasoning.Effort, "Reasoning effort should match expected value")
			assert.Equal(t, shared.ReasoningSummaryAuto, result.Reasoning.Summary, "Reasoning summary should be set to auto")
		})
	}
}
