// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
			name:     "custom model passes through as-is",
			model:    "custom-model-xyz",
			expected: shared.ChatModel("custom-model-xyz"),
		},
		{
			name:     "unlisted model passes through as-is",
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
			name:     "custom embedding model passes through as-is",
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

func TestCreateEmbedding(t *testing.T) {
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		text           string
		contextFunc    func() (context.Context, context.CancelFunc)
		expectError    bool
		errorContains  string
		expectedLength int
	}{
		{
			name: "successful embedding creation",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				response := `{
					"object": "list",
					"data": [
						{
							"object": "embedding",
							"index": 0,
							"embedding": [0.1, 0.2, 0.3, 0.4, 0.5]
						}
					],
					"model": "text-embedding-3-large",
					"usage": {"prompt_tokens": 5, "total_tokens": 5}
				}`
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(response))
			},
			text:           "test text",
			contextFunc:    func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			expectError:    false,
			expectedLength: 5,
		},
		{
			name: "API rate limiting error (429)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error": {"message": "Rate limit exceeded", "type": "rate_limit_error"}}`))
			},
			text:          "test text",
			contextFunc:   func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			expectError:   true,
			errorContains: "failed to create embedding",
		},
		{
			name: "API invalid API key error (401)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error": {"message": "Invalid API key", "type": "invalid_request_error"}}`))
			},
			text:          "test text",
			contextFunc:   func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			expectError:   true,
			errorContains: "failed to create embedding",
		},
		{
			name: "API server error (500)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error": {"message": "Internal server error", "type": "server_error"}}`))
			},
			text:          "test text",
			contextFunc:   func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			expectError:   true,
			errorContains: "failed to create embedding",
		},
		{
			name: "empty response from API (len(resp.Data) == 0)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				response := `{
					"object": "list",
					"data": [],
					"model": "text-embedding-3-large",
					"usage": {"prompt_tokens": 0, "total_tokens": 0}
				}`
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(response))
			},
			text:          "test text",
			contextFunc:   func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			expectError:   true,
			errorContains: "no embedding data returned",
		},
		{
			name: "context cancellation",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// Simulate slow response - check if context is done
				select {
				case <-r.Context().Done():
					// Context was canceled
					return
				case <-time.After(5 * time.Second):
					// This should not happen in tests
					w.WriteHeader(http.StatusOK)
				}
			},
			text: "test text",
			contextFunc: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				// Cancel immediately
				cancel()
				return ctx, cancel
			},
			expectError:   true,
			errorContains: "", // Error message varies based on implementation
		},
		{
			name: "network timeout",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// Simulate very slow response
				time.Sleep(2 * time.Second)
				w.WriteHeader(http.StatusOK)
			},
			text: "test text",
			contextFunc: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 50*time.Millisecond)
			},
			expectError:   true,
			errorContains: "", // Error message varies
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			client := NewCompatibleEmbeddings(Config{
				APIKey:              "test-key",
				APIURL:              server.URL,
				EmbeddingModel:      "text-embedding-3-large",
				EmbeddingDimensions: 5,
			}, server.Client())

			ctx, cancel := tt.contextFunc()
			defer cancel()

			result, err := client.CreateEmbedding(ctx, tt.text)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Len(t, result, tt.expectedLength)
				// Verify float32 conversion
				assert.Equal(t, float32(0.1), result[0])
				assert.Equal(t, float32(0.5), result[4])
			}
		})
	}
}

func TestBatchCreateEmbeddings(t *testing.T) {
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		texts          []string
		contextFunc    func() (context.Context, context.CancelFunc)
		expectError    bool
		errorContains  string
		expectedCount  int
		expectedLength int
	}{
		{
			name: "successful batch embedding creation",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				response := `{
					"object": "list",
					"data": [
						{"object": "embedding", "index": 0, "embedding": [0.1, 0.2, 0.3]},
						{"object": "embedding", "index": 1, "embedding": [0.4, 0.5, 0.6]},
						{"object": "embedding", "index": 2, "embedding": [0.7, 0.8, 0.9]}
					],
					"model": "text-embedding-3-large",
					"usage": {"prompt_tokens": 15, "total_tokens": 15}
				}`
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(response))
			},
			texts:          []string{"text1", "text2", "text3"},
			contextFunc:    func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			expectError:    false,
			expectedCount:  3,
			expectedLength: 3,
		},
		{
			name: "API error during batch call",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": {"message": "Invalid request", "type": "invalid_request_error"}}`))
			},
			texts:         []string{"text1", "text2"},
			contextFunc:   func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			expectError:   true,
			errorContains: "failed to create embeddings batch",
		},
		{
			name: "large batch handling - verifies request body contains array",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// Read and verify the request body contains an array of strings
				var requestBody struct {
					Input []string `json:"input"`
					Model string   `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				// Verify we received all texts
				if len(requestBody.Input) != 100 {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error": {"message": "Expected 100 texts"}}`))
					return
				}

				// Build response with 100 embeddings
				var data []string
				for i := 0; i < 100; i++ {
					data = append(data, fmt.Sprintf(`{"object": "embedding", "index": %d, "embedding": [0.%d]}`, i, i%10))
				}

				w.Header().Set("Content-Type", "application/json")
				response := fmt.Sprintf(`{
					"object": "list",
					"data": [%s],
					"model": "text-embedding-3-large",
					"usage": {"prompt_tokens": 1000, "total_tokens": 1000}
				}`, strings.Join(data, ","))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(response))
			},
			texts: func() []string {
				texts := make([]string, 100)
				for i := range texts {
					texts[i] = fmt.Sprintf("text %d", i)
				}
				return texts
			}(),
			contextFunc:    func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			expectError:    false,
			expectedCount:  100,
			expectedLength: 1,
		},
		{
			name: "empty texts array input",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// Note: The OpenAI API would normally return an error for empty input,
				// but the SDK may handle this differently
				w.Header().Set("Content-Type", "application/json")
				response := `{
					"object": "list",
					"data": [],
					"model": "text-embedding-3-large",
					"usage": {"prompt_tokens": 0, "total_tokens": 0}
				}`
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(response))
			},
			texts:          []string{},
			contextFunc:    func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			expectError:    false,
			expectedCount:  0,
			expectedLength: 0,
		},
		{
			name: "context cancellation",
			handler: func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(5 * time.Second):
					w.WriteHeader(http.StatusOK)
				}
			},
			texts: []string{"text1", "text2"},
			contextFunc: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Cancel immediately
				return ctx, cancel
			},
			expectError: true,
		},
		{
			name: "response with mismatched count vs input - fewer embeddings returned",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// API returns fewer embeddings than requested
				w.Header().Set("Content-Type", "application/json")
				response := `{
					"object": "list",
					"data": [
						{"object": "embedding", "index": 0, "embedding": [0.1, 0.2, 0.3]}
					],
					"model": "text-embedding-3-large",
					"usage": {"prompt_tokens": 5, "total_tokens": 5}
				}`
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(response))
			},
			texts:       []string{"text1", "text2", "text3"},
			contextFunc: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			// The code now validates that response count matches input count
			expectError:   true,
			errorContains: "embedding count mismatch",
		},
		{
			name: "response with mismatched count vs input - more embeddings returned",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// API returns more embeddings than requested (unusual but possible)
				w.Header().Set("Content-Type", "application/json")
				response := `{
					"object": "list",
					"data": [
						{"object": "embedding", "index": 0, "embedding": [0.1, 0.2, 0.3]},
						{"object": "embedding", "index": 1, "embedding": [0.4, 0.5, 0.6]},
						{"object": "embedding", "index": 2, "embedding": [0.7, 0.8, 0.9]},
						{"object": "embedding", "index": 3, "embedding": [1.0, 1.1, 1.2]}
					],
					"model": "text-embedding-3-large",
					"usage": {"prompt_tokens": 10, "total_tokens": 10}
				}`
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(response))
			},
			texts:       []string{"text1", "text2"},
			contextFunc: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			// The code now validates that response count matches input count
			expectError:   true,
			errorContains: "embedding count mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			client := NewCompatibleEmbeddings(Config{
				APIKey:              "test-key",
				APIURL:              server.URL,
				EmbeddingModel:      "text-embedding-3-large",
				EmbeddingDimensions: 3,
			}, server.Client())

			ctx, cancel := tt.contextFunc()
			defer cancel()

			result, err := client.BatchCreateEmbeddings(ctx, tt.texts)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Len(t, result, tt.expectedCount)
				if tt.expectedCount > 0 && tt.expectedLength > 0 {
					assert.Len(t, result[0], tt.expectedLength)
				}
			}
		})
	}
}

func TestNewEmbeddingsDefaults(t *testing.T) {
	tests := []struct {
		name                    string
		config                  Config
		expectedModel           string
		expectedDimensions      int
		useCompatibleEmbeddings bool
	}{
		{
			name: "NewEmbeddings sets defaults when model is empty",
			config: Config{
				APIKey:         "test-key",
				EmbeddingModel: "",
			},
			expectedModel:           "text-embedding-3-large",
			expectedDimensions:      3072,
			useCompatibleEmbeddings: false,
		},
		{
			name: "NewEmbeddings preserves custom model when set",
			config: Config{
				APIKey:              "test-key",
				EmbeddingModel:      "text-embedding-3-small",
				EmbeddingDimensions: 1536,
			},
			expectedModel:           "text-embedding-3-small",
			expectedDimensions:      1536,
			useCompatibleEmbeddings: false,
		},
		{
			name: "NewCompatibleEmbeddings sets defaults when model is empty",
			config: Config{
				APIKey:         "test-key",
				APIURL:         "https://api.example.com/v1",
				EmbeddingModel: "",
			},
			expectedModel:           "text-embedding-3-large",
			expectedDimensions:      3072,
			useCompatibleEmbeddings: true,
		},
		{
			name: "NewCompatibleEmbeddings preserves custom model when set",
			config: Config{
				APIKey:              "test-key",
				APIURL:              "https://api.example.com/v1",
				EmbeddingModel:      "custom-embedding-model",
				EmbeddingDimensions: 768,
			},
			expectedModel:           "custom-embedding-model",
			expectedDimensions:      768,
			useCompatibleEmbeddings: true,
		},
		{
			name: "NewEmbeddings with model set but dimensions unset keeps dimensions at 0",
			config: Config{
				APIKey:              "test-key",
				EmbeddingModel:      "text-embedding-ada-002",
				EmbeddingDimensions: 0,
			},
			// Note: When model is set, the defaults are NOT applied
			// This is expected behavior - only empty model triggers defaults
			expectedModel:           "text-embedding-ada-002",
			expectedDimensions:      0,
			useCompatibleEmbeddings: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var client *OpenAI
			if tt.useCompatibleEmbeddings {
				client = NewCompatibleEmbeddings(tt.config, http.DefaultClient)
			} else {
				client = NewEmbeddings(tt.config, http.DefaultClient)
			}

			assert.Equal(t, tt.expectedModel, client.config.EmbeddingModel)
			assert.Equal(t, tt.expectedDimensions, client.config.EmbeddingDimensions)
			assert.Equal(t, tt.expectedDimensions, client.Dimensions())
		})
	}
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
			name:               "reasoning enabled with none effort",
			reasoningEnabled:   true,
			reasoningEffort:    "none",
			expectedEffort:     shared.ReasoningEffort("none"),
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
			name:               "reasoning enabled with xhigh effort",
			reasoningEnabled:   true,
			reasoningEffort:    "xhigh",
			expectedEffort:     shared.ReasoningEffort("xhigh"),
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
