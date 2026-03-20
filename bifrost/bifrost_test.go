// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

func TestCalculateThinkingBudget(t *testing.T) {
	tests := []struct {
		name               string
		thinkingBudget     int
		maxGeneratedTokens int
		expected           int
	}{
		{
			name:               "default with maxTokens 8192",
			thinkingBudget:     0,
			maxGeneratedTokens: 8192,
			expected:           2048,
		},
		{
			name:               "default with maxTokens 32768 caps at 8192",
			thinkingBudget:     0,
			maxGeneratedTokens: 32768,
			expected:           8192,
		},
		{
			name:               "default with maxTokens 2048 enforces min 1024",
			thinkingBudget:     0,
			maxGeneratedTokens: 2048,
			expected:           1024,
		},
		{
			name:               "custom budget 4096",
			thinkingBudget:     4096,
			maxGeneratedTokens: 8192,
			expected:           4096,
		},
		{
			name:               "custom budget below min enforces 1024",
			thinkingBudget:     500,
			maxGeneratedTokens: 8192,
			expected:           1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{thinkingBudget: tt.thinkingBudget}
			result := b.calculateThinkingBudget(tt.maxGeneratedTokens)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildChatReasoning(t *testing.T) {
	tests := []struct {
		name             string
		provider         schemas.ModelProvider
		reasoningEnabled bool
		thinkingBudget   int
		reasoningEffort  string
		cfg              llm.LanguageModelConfig
		expectNil        bool
		checkMaxTokens   *int
		checkEffort      *string
	}{
		{
			name:             "Anthropic uses MaxTokens",
			provider:         schemas.Anthropic,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkMaxTokens:   Ptr(2048),
		},
		{
			name:             "non-Anthropic uses Effort",
			provider:         schemas.OpenAI,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkEffort:      Ptr("medium"),
		},
		{
			name:             "non-Anthropic with custom effort",
			provider:         schemas.OpenAI,
			reasoningEnabled: true,
			reasoningEffort:  "high",
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkEffort:      Ptr("high"),
		},
		{
			name:             "ReasoningDisabled returns nil",
			provider:         schemas.Anthropic,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192, ReasoningDisabled: true},
			expectNil:        true,
		},
		{
			name:             "reasoning not enabled returns nil",
			provider:         schemas.Anthropic,
			reasoningEnabled: false,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			expectNil:        true,
		},
		{
			name:             "budget >= maxTokens returns nil",
			provider:         schemas.Anthropic,
			reasoningEnabled: true,
			thinkingBudget:   8192,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			expectNil:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{
				provider:         tt.provider,
				reasoningEnabled: tt.reasoningEnabled,
				thinkingBudget:   tt.thinkingBudget,
				reasoningEffort:  tt.reasoningEffort,
			}
			result := b.buildChatReasoning(tt.cfg)
			if tt.expectNil {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			if tt.checkMaxTokens != nil {
				require.NotNil(t, result.MaxTokens)
				assert.Equal(t, *tt.checkMaxTokens, *result.MaxTokens)
			}
			if tt.checkEffort != nil {
				require.NotNil(t, result.Effort)
				assert.Equal(t, *tt.checkEffort, *result.Effort)
			}
		})
	}
}

func TestShouldUseResponsesAPI(t *testing.T) {
	tests := []struct {
		name               string
		enabledNativeTools []string
		useResponsesAPI    bool
		cfg                llm.LanguageModelConfig
		expected           bool
	}{
		{
			name:               "native tools configured returns true",
			enabledNativeTools: []string{"web_search"},
			expected:           true,
		},
		{
			name:               "NativeWebSearchAllowed with web_search enabled returns true",
			enabledNativeTools: []string{"web_search"},
			cfg:                llm.LanguageModelConfig{NativeWebSearchAllowed: true},
			expected:           true,
		},
		{
			name:               "NativeWebSearchAllowed without web_search in tools returns true",
			enabledNativeTools: nil,
			cfg:                llm.LanguageModelConfig{NativeWebSearchAllowed: true},
			expected:           true,
		},
		{
			name:               "nothing configured returns false",
			enabledNativeTools: nil,
			cfg:                llm.LanguageModelConfig{},
			expected:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{
				enabledNativeTools: tt.enabledNativeTools,
				useResponsesAPI:    tt.useResponsesAPI,
			}
			result := b.shouldUseResponsesAPI(tt.cfg)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertMessagesReasoning(t *testing.T) {
	tests := []struct {
		name                string
		posts               []llm.Post
		expectReasoningLen  int
		expectToolCallsLen  int
		expectReasoningText string
		expectReasoningSig  string
		checkMessageIndex   int
		provider            schemas.ModelProvider
	}{
		{
			name: "bot post with Reasoning populates ReasoningDetails",
			posts: []llm.Post{
				{
					Role:               llm.PostRoleBot,
					Message:            "hello",
					Reasoning:          "I thought about it",
					ReasoningSignature: "sig123",
				},
			},
			expectReasoningLen:  1,
			expectReasoningText: "I thought about it",
			expectReasoningSig:  "sig123",
			checkMessageIndex:   0,
			provider:            schemas.OpenAI,
		},
		{
			name: "bot post without Reasoning has no ReasoningDetails",
			posts: []llm.Post{
				{
					Role:    llm.PostRoleBot,
					Message: "hello",
				},
			},
			expectReasoningLen: 0,
			checkMessageIndex:  0,
			provider:           schemas.OpenAI,
		},
		{
			name: "bot post with both Reasoning and ToolUse",
			posts: []llm.Post{
				{
					Role:               llm.PostRoleBot,
					Message:            "using tool",
					Reasoning:          "I need to use a tool",
					ReasoningSignature: "sig456",
					ToolUse: []llm.ToolCall{
						{
							ID:        "call1",
							Name:      "test_tool",
							Arguments: []byte(`{"key":"value"}`),
							Result:    "tool result",
						},
					},
				},
			},
			expectReasoningLen:  1,
			expectToolCallsLen:  1,
			expectReasoningText: "I need to use a tool",
			expectReasoningSig:  "sig456",
			checkMessageIndex:   0,
			provider:            schemas.OpenAI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{provider: tt.provider}
			messages := b.convertMessages(tt.posts)
			require.True(t, len(messages) > tt.checkMessageIndex)

			msg := messages[tt.checkMessageIndex]
			if tt.expectReasoningLen == 0 {
				if msg.ChatAssistantMessage == nil {
					return
				}
				assert.Empty(t, msg.ReasoningDetails)
				return
			}

			require.NotNil(t, msg.ChatAssistantMessage)
			assert.Len(t, msg.ReasoningDetails, tt.expectReasoningLen)
			if tt.expectReasoningLen > 0 {
				rd := msg.ReasoningDetails[0]
				assert.Equal(t, schemas.BifrostReasoningDetailsTypeText, rd.Type)
				require.NotNil(t, rd.Text)
				assert.Equal(t, tt.expectReasoningText, *rd.Text)
				require.NotNil(t, rd.Signature)
				assert.Equal(t, tt.expectReasoningSig, *rd.Signature)
			}
			if tt.expectToolCallsLen > 0 {
				assert.Len(t, msg.ToolCalls, tt.expectToolCallsLen)
			}
		})
	}
}

func TestMergeConsecutiveSameRoleMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []schemas.ChatMessage
		expected int // expected number of messages after merge
		validate func(t *testing.T, merged []schemas.ChatMessage)
	}{
		{
			name:     "empty input",
			messages: nil,
			expected: 0,
		},
		{
			name: "single message unchanged",
			messages: []schemas.ChatMessage{
				{
					Role:    schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("hello")},
				},
			},
			expected: 1,
		},
		{
			name: "consecutive user messages merged",
			messages: []schemas.ChatMessage{
				{
					Role:    schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("msg1")},
				},
				{
					Role:    schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("msg2")},
				},
			},
			expected: 1,
			validate: func(t *testing.T, merged []schemas.ChatMessage) {
				require.Len(t, merged[0].Content.ContentBlocks, 2)
				assert.Equal(t, "msg1", *merged[0].Content.ContentBlocks[0].Text)
				assert.Equal(t, "msg2", *merged[0].Content.ContentBlocks[1].Text)
			},
		},
		{
			name: "consecutive assistant messages merged with tool calls combined",
			messages: []schemas.ChatMessage{
				{
					Role:    schemas.ChatMessageRoleAssistant,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("resp1")},
					ChatAssistantMessage: &schemas.ChatAssistantMessage{
						ToolCalls: []schemas.ChatAssistantMessageToolCall{
							{ID: Ptr("tc1"), Function: schemas.ChatAssistantMessageToolCallFunction{Name: Ptr("tool1")}},
						},
					},
				},
				{
					Role:    schemas.ChatMessageRoleAssistant,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("resp2")},
					ChatAssistantMessage: &schemas.ChatAssistantMessage{
						ToolCalls: []schemas.ChatAssistantMessageToolCall{
							{ID: Ptr("tc2"), Function: schemas.ChatAssistantMessageToolCallFunction{Name: Ptr("tool2")}},
						},
					},
				},
			},
			expected: 1,
			validate: func(t *testing.T, merged []schemas.ChatMessage) {
				require.Len(t, merged[0].Content.ContentBlocks, 2)
				require.NotNil(t, merged[0].ChatAssistantMessage)
				assert.Len(t, merged[0].ToolCalls, 2)
			},
		},
		{
			name: "different roles not merged",
			messages: []schemas.ChatMessage{
				{
					Role:    schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("question")},
				},
				{
					Role:    schemas.ChatMessageRoleAssistant,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("answer")},
				},
			},
			expected: 2,
		},
		{
			name: "tool messages never merged",
			messages: []schemas.ChatMessage{
				{
					Role:    schemas.ChatMessageRoleTool,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("result1")},
					ChatToolMessage: &schemas.ChatToolMessage{
						ToolCallID: Ptr("tc1"),
					},
				},
				{
					Role:    schemas.ChatMessageRoleTool,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("result2")},
					ChatToolMessage: &schemas.ChatToolMessage{
						ToolCallID: Ptr("tc2"),
					},
				},
			},
			expected: 2,
		},
		{
			name: "system messages not merged with user messages",
			messages: []schemas.ChatMessage{
				{
					Role:    schemas.ChatMessageRoleSystem,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("system prompt")},
				},
				{
					Role:    schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{ContentStr: Ptr("hello")},
				},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{provider: schemas.Anthropic}
			merged := b.mergeConsecutiveSameRoleMessages(tt.messages)
			assert.Len(t, merged, tt.expected)
			if tt.validate != nil {
				tt.validate(t, merged)
			}
		})
	}
}

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		provider schemas.ModelProvider
		apiURL   string
		expected string
	}{
		{
			name:     "strips /v1 suffix from OpenAI URL",
			provider: schemas.OpenAI,
			apiURL:   "https://api.openai.com/v1",
			expected: "https://api.openai.com",
		},
		{
			name:     "strips /v1/ suffix from OpenAI URL",
			provider: schemas.OpenAI,
			apiURL:   "https://api.openai.com/v1/",
			expected: "https://api.openai.com",
		},
		{
			name:     "no change for URL without /v1",
			provider: schemas.OpenAI,
			apiURL:   "http://localhost:8080",
			expected: "http://localhost:8080",
		},
		{
			name:     "no change for empty URL",
			provider: schemas.OpenAI,
			apiURL:   "",
			expected: "",
		},
		{
			name:     "preserves /v1 in non-suffix position",
			provider: schemas.OpenAI,
			apiURL:   "http://localhost:8080/v1/proxy",
			expected: "http://localhost:8080/v1/proxy",
		},
		{
			name:     "no change for Anthropic provider",
			provider: schemas.Anthropic,
			apiURL:   "https://api.anthropic.com/v1",
			expected: "https://api.anthropic.com/v1",
		},
		{
			name:     "strips from custom OpenAI-compatible URL",
			provider: schemas.OpenAI,
			apiURL:   "http://myserver:11434/v1",
			expected: "http://myserver:11434",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeOpenAIBaseURL(tt.provider, tt.apiURL)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }

func TestConvertBifrostAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		ann      *schemas.ResponsesOutputMessageContentTextAnnotation
		index    int
		expected *llm.Annotation
	}{
		{
			name:     "nil annotation",
			ann:      nil,
			index:    1,
			expected: nil,
		},
		{
			name: "non-url_citation type is ignored",
			ann: &schemas.ResponsesOutputMessageContentTextAnnotation{
				Type: "file_citation",
			},
			index:    1,
			expected: nil,
		},
		{
			name: "OpenAI fields used when present",
			ann: &schemas.ResponsesOutputMessageContentTextAnnotation{
				Type:       "url_citation",
				StartIndex: intPtr(10),
				EndIndex:   intPtr(50),
				URL:        strPtr("https://example.com"),
				Title:      strPtr("Example"),
				Text:       strPtr("cited text"),
			},
			index: 1,
			expected: &llm.Annotation{
				Type:       llm.AnnotationTypeURLCitation,
				StartIndex: 10,
				EndIndex:   50,
				URL:        "https://example.com",
				Title:      "Example",
				CitedText:  "cited text",
				Index:      1,
			},
		},
		{
			name: "nil StartIndex and EndIndex default to zero",
			ann: &schemas.ResponsesOutputMessageContentTextAnnotation{
				Type:  "url_citation",
				URL:   strPtr("https://anthropic.com"),
				Title: strPtr("Anthropic"),
			},
			index: 3,
			expected: &llm.Annotation{
				Type:  llm.AnnotationTypeURLCitation,
				URL:   "https://anthropic.com",
				Title: "Anthropic",
				Index: 3,
			},
		},
		{
			name: "all position fields nil defaults to zero",
			ann: &schemas.ResponsesOutputMessageContentTextAnnotation{
				Type: "url_citation",
				URL:  strPtr("https://example.com"),
			},
			index: 1,
			expected: &llm.Annotation{
				Type:  llm.AnnotationTypeURLCitation,
				URL:   "https://example.com",
				Index: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertBifrostAnnotation(tt.ann, tt.index)
			if tt.expected == nil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, *tt.expected, *result)
			}
		})
	}
}

type testStructuredOutput struct {
	Name  string `json:"name"`
	Score int    `json:"score"`
}

func TestConvertToBifrostRequestStructuredOutput(t *testing.T) {
	tests := []struct {
		name             string
		jsonOutputFormat bool
		expectFormat     bool
	}{
		{
			name:             "with JSON output format sets ResponseFormat",
			jsonOutputFormat: true,
			expectFormat:     true,
		},
		{
			name:             "without JSON output format leaves ResponseFormat nil",
			jsonOutputFormat: false,
			expectFormat:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{
				provider:     schemas.OpenAI,
				defaultModel: "gpt-4",
			}
			cfg := llm.LanguageModelConfig{
				Model:              "gpt-4",
				MaxGeneratedTokens: 1000,
			}
			if tt.jsonOutputFormat {
				cfg.JSONOutputFormat = llm.NewJSONSchemaFromStruct[testStructuredOutput]()
			}

			req := b.convertToBifrostRequest(llm.CompletionRequest{}, cfg)

			if tt.expectFormat {
				require.NotNil(t, req.Params.ResponseFormat)
				// Verify the structure
				data, err := json.Marshal(*req.Params.ResponseFormat)
				require.NoError(t, err)
				var format map[string]interface{}
				require.NoError(t, json.Unmarshal(data, &format))
				assert.Equal(t, "json_schema", format["type"])
				jsonSchema, ok := format["json_schema"].(map[string]interface{})
				require.True(t, ok)
				assert.Equal(t, "response", jsonSchema["name"])
				assert.Equal(t, true, jsonSchema["strict"])
				assert.NotNil(t, jsonSchema["schema"])
			} else {
				assert.Nil(t, req.Params.ResponseFormat)
			}
		})
	}
}

func TestConvertToBifrostResponsesRequestStructuredOutput(t *testing.T) {
	tests := []struct {
		name             string
		jsonOutputFormat bool
		expectFormat     bool
	}{
		{
			name:             "with JSON output format sets Text config",
			jsonOutputFormat: true,
			expectFormat:     true,
		},
		{
			name:             "without JSON output format leaves Text nil",
			jsonOutputFormat: false,
			expectFormat:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{
				provider:        schemas.OpenAI,
				defaultModel:    "gpt-4",
				useResponsesAPI: true,
			}
			cfg := llm.LanguageModelConfig{
				Model:              "gpt-4",
				MaxGeneratedTokens: 1000,
			}
			if tt.jsonOutputFormat {
				cfg.JSONOutputFormat = llm.NewJSONSchemaFromStruct[testStructuredOutput]()
			}

			req := b.convertToBifrostResponsesRequest(llm.CompletionRequest{}, cfg)

			if tt.expectFormat {
				require.NotNil(t, req.Params.Text)
				require.NotNil(t, req.Params.Text.Format)
				assert.Equal(t, "json_schema", req.Params.Text.Format.Type)
				assert.Equal(t, "response", *req.Params.Text.Format.Name)
				assert.Equal(t, true, *req.Params.Text.Format.Strict)
				require.NotNil(t, req.Params.Text.Format.JSONSchema)
				require.NotNil(t, req.Params.Text.Format.JSONSchema.Schema)
			} else {
				assert.Nil(t, req.Params.Text)
			}
		})
	}
}
