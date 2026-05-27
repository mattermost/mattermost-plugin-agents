// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/llm"
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
			name:             "OpenAI on chat path returns nil (Responses API handles reasoning)",
			provider:         schemas.OpenAI,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			expectNil:        true,
		},
		{
			name:             "Mistral returns nil",
			provider:         schemas.Mistral,
			reasoningEnabled: true,
			reasoningEffort:  "high",
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			expectNil:        true,
		},
		{
			name:             "Cohere returns nil",
			provider:         schemas.Cohere,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			expectNil:        true,
		},
		{
			name:             "Bedrock returns nil",
			provider:         schemas.Bedrock,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			expectNil:        true,
		},
		{
			name:             "Azure returns nil",
			provider:         schemas.Azure,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			expectNil:        true,
		},
		{
			name:             "Gemini with effort only falls back to effort",
			provider:         schemas.Gemini,
			reasoningEnabled: true,
			reasoningEffort:  "high",
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkEffort:      Ptr("high"),
		},
		{
			name:             "Gemini with thinking budget prefers MaxTokens",
			provider:         schemas.Gemini,
			reasoningEnabled: true,
			thinkingBudget:   4096,
			reasoningEffort:  "high",
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkMaxTokens:   Ptr(4096),
		},
		{
			name:             "Gemini default effort when nothing set",
			provider:         schemas.Gemini,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkEffort:      Ptr("medium"),
		},
		{
			name:             "Vertex with thinking budget prefers MaxTokens",
			provider:         schemas.Vertex,
			reasoningEnabled: true,
			thinkingBudget:   2000,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkMaxTokens:   Ptr(2000),
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

func TestConvertMessagesReasoningDetails(t *testing.T) {
	tests := []struct {
		name              string
		provider          schemas.ModelProvider
		posts             []llm.Post
		expectedLen       int
		expectedReasoning string
		expectedSignature string
	}{
		{
			name:     "skips unsigned reasoning for Anthropic",
			provider: schemas.Anthropic,
			posts: []llm.Post{{
				Role:               llm.PostRoleBot,
				Message:            "partial response",
				Reasoning:          "partial thinking captured before stream error",
				ReasoningSignature: "",
			}},
			expectedLen: 1,
		},
		{
			name:     "preserves unsigned reasoning for non-Anthropic",
			provider: schemas.OpenAI,
			posts: []llm.Post{{
				Role:               llm.PostRoleBot,
				Message:            "partial response",
				Reasoning:          "partial thinking",
				ReasoningSignature: "",
			}},
			expectedLen:       1,
			expectedReasoning: "partial thinking",
		},
		{
			name:     "includes signed reasoning",
			provider: schemas.Anthropic,
			posts: []llm.Post{{
				Role:               llm.PostRoleBot,
				Message:            "response",
				Reasoning:          "thinking",
				ReasoningSignature: "sig123",
			}},
			expectedLen:       1,
			expectedReasoning: "thinking",
			expectedSignature: "sig123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{provider: tt.provider}

			messages := b.convertMessages(tt.posts)

			require.Len(t, messages, tt.expectedLen)
			if tt.expectedReasoning == "" {
				assert.Nil(t, messages[0].ChatAssistantMessage)
				return
			}
			require.Len(t, messages[0].ReasoningDetails, 1)
			assert.Equal(t, tt.expectedReasoning, *messages[0].ReasoningDetails[0].Text)
			assert.Equal(t, tt.expectedSignature, *messages[0].ReasoningDetails[0].Signature)
		})
	}
}

func TestCreateMultimodalContentUsesReusableFileData(t *testing.T) {
	b := &LLM{}
	imageData := []byte("PNGDATA")
	post := llm.Post{
		Role:    llm.PostRoleUser,
		Message: "look at this",
		Files: []llm.File{{
			MimeType: "image/png",
			Size:     int64(len(imageData)),
			Data:     imageData,
			Reader:   bytes.NewReader(imageData),
		}},
	}

	first := b.createMultimodalContent(post)
	second := b.createMultimodalContent(post)

	require.Len(t, first, 2)
	require.Len(t, second, 2)
	require.NotNil(t, first[1].ImageURLStruct)
	require.NotNil(t, second[1].ImageURLStruct)
	assert.Equal(t, first[1].ImageURLStruct.URL, second[1].ImageURLStruct.URL)
	assert.Contains(t, second[1].ImageURLStruct.URL, "UE5HREFUQQ==")
}

// TestConvertToBifrostRequestOpus47Reasoning verifies that when our
// convertToBifrostRequest is fed through bifrost's Anthropic provider for
// Claude Opus 4.7, the resulting upstream request uses thinking.type:"adaptive".
// Opus 4.7 dropped support for thinking.type:"enabled"; sending the legacy
// shape produces an API error: `"thinking.type.enabled" is not supported for
// this model. Use "thinking.type.adaptive" and "output_config.effort"`.
func TestConvertToBifrostRequestOpus47Reasoning(t *testing.T) {
	b := &LLM{
		provider:         schemas.Anthropic,
		reasoningEnabled: true,
	}
	request := llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleUser, Message: "think"},
		},
	}
	cfg := llm.LanguageModelConfig{
		Model:              "claude-opus-4-7-20260401",
		MaxGeneratedTokens: 8192,
	}

	bifrostReq := b.convertToBifrostRequest(request, cfg)

	ctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()
	result, err := anthropic.ToAnthropicChatRequest(ctx, bifrostReq)
	require.NoError(t, err)
	require.NotNil(t, result.Thinking)

	assert.Equal(t, "adaptive", result.Thinking.Type,
		"Opus 4.7 must use thinking.type:adaptive; thinking.type:enabled is rejected by the API")
	assert.Nil(t, result.Thinking.BudgetTokens,
		"Opus 4.7 does not accept budget_tokens alongside adaptive thinking")
}

func TestBuildResponsesReasoning(t *testing.T) {
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
		checkSummary     *string
	}{
		{
			name:             "Gemini with effort only sets effort and summary",
			provider:         schemas.Gemini,
			reasoningEnabled: true,
			reasoningEffort:  "high",
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkEffort:      Ptr("high"),
			checkSummary:     Ptr("auto"),
		},
		{
			name:             "Gemini with thinking budget prefers max_tokens and summary",
			provider:         schemas.Gemini,
			reasoningEnabled: true,
			thinkingBudget:   4096,
			reasoningEffort:  "high",
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkMaxTokens:   Ptr(4096),
			checkSummary:     Ptr("auto"),
		},
		{
			name:             "Gemini default effort when nothing set",
			provider:         schemas.Gemini,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkEffort:      Ptr("medium"),
			checkSummary:     Ptr("auto"),
		},
		{
			name:             "Vertex with thinking budget",
			provider:         schemas.Vertex,
			reasoningEnabled: true,
			thinkingBudget:   2000,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkMaxTokens:   Ptr(2000),
			checkSummary:     Ptr("auto"),
		},
		{
			name:             "Anthropic uses MaxTokens, no summary",
			provider:         schemas.Anthropic,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkMaxTokens:   Ptr(2048),
		},
		{
			name:             "OpenAI uses Effort with summary",
			provider:         schemas.OpenAI,
			reasoningEnabled: true,
			reasoningEffort:  "high",
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkEffort:      Ptr("high"),
			checkSummary:     Ptr("auto"),
		},
		{
			name:             "Azure uses Effort with summary",
			provider:         schemas.Azure,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			checkEffort:      Ptr("medium"),
			checkSummary:     Ptr("auto"),
		},
		{
			name:             "Mistral returns nil (no reasoning_effort support)",
			provider:         schemas.Mistral,
			reasoningEnabled: true,
			reasoningEffort:  "high",
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			expectNil:        true,
		},
		{
			name:             "Cohere returns nil",
			provider:         schemas.Cohere,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			expectNil:        true,
		},
		{
			name:             "Bedrock returns nil",
			provider:         schemas.Bedrock,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192},
			expectNil:        true,
		},
		{
			name:             "ReasoningDisabled returns nil",
			provider:         schemas.Gemini,
			reasoningEnabled: true,
			cfg:              llm.LanguageModelConfig{MaxGeneratedTokens: 8192, ReasoningDisabled: true},
			expectNil:        true,
		},
		{
			name:             "reasoning not enabled returns nil",
			provider:         schemas.Gemini,
			reasoningEnabled: false,
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
			result := b.buildResponsesReasoning(tt.cfg)
			if tt.expectNil {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			if tt.checkMaxTokens != nil {
				require.NotNil(t, result.MaxTokens)
				assert.Equal(t, *tt.checkMaxTokens, *result.MaxTokens)
			} else {
				assert.Nil(t, result.MaxTokens)
			}
			if tt.checkEffort != nil {
				require.NotNil(t, result.Effort)
				assert.Equal(t, *tt.checkEffort, *result.Effort)
			} else {
				assert.Nil(t, result.Effort)
			}
			if tt.checkSummary != nil {
				require.NotNil(t, result.Summary)
				assert.Equal(t, *tt.checkSummary, *result.Summary)
			}
		})
	}
}

func TestGetKeysForProviderVertex(t *testing.T) {
	saJSON := `{"type":"service_account","project_id":"x"}`
	acc := &providerAccount{
		provider:              schemas.Vertex,
		region:                "us-west1",
		vertexProjectID:       "my-gcp-project",
		vertexProjectNumber:   "123456789012",
		vertexAuthCredentials: saJSON,
	}

	keys, err := acc.GetKeysForProvider(context.Background(), schemas.Vertex)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.NotNil(t, keys[0].VertexKeyConfig)
	vc := keys[0].VertexKeyConfig
	assert.Equal(t, "my-gcp-project", vc.ProjectID.Val)
	assert.Equal(t, "123456789012", vc.ProjectNumber.Val)
	assert.Equal(t, "us-west1", vc.Region.Val)
	assert.Equal(t, saJSON, vc.AuthCredentials.Val)

	adc := &providerAccount{
		provider:              schemas.Vertex,
		region:                "europe-west1",
		vertexProjectID:       "adc-project",
		vertexProjectNumber:   "",
		vertexAuthCredentials: "",
	}
	keysADC, err := adc.GetKeysForProvider(context.Background(), schemas.Vertex)
	require.NoError(t, err)
	require.Len(t, keysADC, 1)
	require.NotNil(t, keysADC[0].VertexKeyConfig)
	assert.Equal(t, "adc-project", keysADC[0].VertexKeyConfig.ProjectID.Val)
	assert.Equal(t, "", keysADC[0].VertexKeyConfig.AuthCredentials.Val)

	other := &providerAccount{provider: schemas.OpenAI}
	_, err = other.GetKeysForProvider(context.Background(), schemas.Vertex)
	require.Error(t, err)
}

func TestShouldUseResponsesAPI(t *testing.T) {
	tests := []struct {
		name               string
		provider           schemas.ModelProvider
		enabledNativeTools []string
		useResponsesAPI    bool
		cfg                llm.LanguageModelConfig
		expected           bool
	}{
		{
			name:               "native tools configured returns true",
			provider:           schemas.OpenAI,
			enabledNativeTools: []string{"web_search"},
			expected:           true,
		},
		{
			name:               "NativeWebSearchAllowed with web_search enabled returns true",
			provider:           schemas.OpenAI,
			enabledNativeTools: []string{"web_search"},
			cfg:                llm.LanguageModelConfig{NativeWebSearchAllowed: true},
			expected:           true,
		},
		{
			name:               "NativeWebSearchAllowed without web_search in tools returns true",
			provider:           schemas.OpenAI,
			enabledNativeTools: nil,
			cfg:                llm.LanguageModelConfig{NativeWebSearchAllowed: true},
			expected:           true,
		},
		{
			name:               "explicit responses API flag wins for direct OpenAI",
			provider:           schemas.OpenAI,
			useResponsesAPI:    true,
			enabledNativeTools: nil,
			cfg:                llm.LanguageModelConfig{},
			expected:           true,
		},
		{
			name:               "unsupported provider ignores native tools",
			provider:           schemas.Bedrock,
			enabledNativeTools: []string{"web_search"},
			cfg:                llm.LanguageModelConfig{},
			expected:           false,
		},
		{
			name:               "Gemini with native tools auto-enables Responses API",
			provider:           schemas.Gemini,
			enabledNativeTools: []string{"web_search"},
			cfg:                llm.LanguageModelConfig{},
			expected:           true,
		},
		{
			name:               "Vertex with native web search allowed auto-enables Responses API",
			provider:           schemas.Vertex,
			enabledNativeTools: nil,
			cfg:                llm.LanguageModelConfig{NativeWebSearchAllowed: true},
			expected:           true,
		},
		{
			name:               "nothing configured returns false",
			provider:           schemas.OpenAI,
			enabledNativeTools: nil,
			cfg:                llm.LanguageModelConfig{},
			expected:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{
				provider:           tt.provider,
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

// TestConvertMessagesEmptyToolResult verifies that a tool with an empty
// Result is substituted with a placeholder so the Anthropic API does not
// reject the message with "text content blocks must be non-empty".
func TestConvertMessagesEmptyToolResult(t *testing.T) {
	b := &LLM{provider: schemas.OpenAI}
	posts := []llm.Post{{
		Role:    llm.PostRoleBot,
		Message: "",
		ToolUse: []llm.ToolCall{{
			ID:        "call1",
			Name:      "search",
			Arguments: []byte(`{}`),
			Result:    "",
		}},
	}}
	messages := b.convertMessages(posts)
	var toolMsg *schemas.ChatMessage
	for i := range messages {
		if messages[i].Role == schemas.ChatMessageRoleTool {
			toolMsg = &messages[i]
			break
		}
	}
	require.NotNil(t, toolMsg, "expected a tool-role message for the tool result")
	require.NotNil(t, toolMsg.Content)
	require.NotNil(t, toolMsg.Content.ContentStr)
	assert.NotEmpty(t, *toolMsg.Content.ContentStr, "empty tool result must be replaced so providers do not reject the message")
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

func TestAppendFirstWebSearchFallbackSource(t *testing.T) {
	sourceTitle := "Example Source"
	item := &schemas.ResponsesMessage{
		Type: schemas.Ptr(schemas.ResponsesMessageTypeWebSearchCall),
		ResponsesToolMessage: &schemas.ResponsesToolMessage{
			Action: &schemas.ResponsesToolMessageActionStruct{
				ResponsesWebSearchToolCallAction: &schemas.ResponsesWebSearchToolCallAction{
					Sources: []schemas.ResponsesWebSearchToolCallActionSearchSource{
						{Type: "url", URL: "https://example.com/one", Title: &sourceTitle},
						{Type: "url", URL: "https://example.com/two"},
					},
				},
			},
		},
	}

	sources := appendFirstWebSearchFallbackSource(nil, item)
	require.Len(t, sources, 2)
	assert.Equal(t, webSearchFallbackSource{
		URL:   "https://example.com/one",
		Title: "Example Source",
	}, sources[0])
	assert.Equal(t, webSearchFallbackSource{
		URL:   "https://example.com/two",
		Title: "",
	}, sources[1])

	sources = appendFirstWebSearchFallbackSource(sources, item)
	require.Len(t, sources, 2)
	assert.Equal(t, []webSearchFallbackSource{
		{
			URL:   "https://example.com/one",
			Title: "Example Source",
		},
		{
			URL:   "https://example.com/two",
			Title: "",
		},
	}, sources)
}

func TestBuildFallbackAnnotations(t *testing.T) {
	annotations := buildFallbackAnnotations([]webSearchFallbackSource{
		{URL: "https://example.com/one", Title: "One"},
		{URL: "https://example.com/two", Title: "Two"},
	}, 42)

	require.Len(t, annotations, 2)
	assert.Equal(t, llm.Annotation{
		Type:       llm.AnnotationTypeURLCitation,
		StartIndex: 42,
		EndIndex:   42,
		URL:        "https://example.com/one",
		Title:      "One",
		Index:      1,
	}, annotations[0])
	assert.Equal(t, llm.Annotation{
		Type:       llm.AnnotationTypeURLCitation,
		StartIndex: 42,
		EndIndex:   42,
		URL:        "https://example.com/two",
		Title:      "Two",
		Index:      2,
	}, annotations[1])
}

func TestApplyPendingAnnotationPositions(t *testing.T) {
	annotations := []llm.Annotation{
		{Type: llm.AnnotationTypeURLCitation, StartIndex: 0, EndIndex: 0, URL: "https://example.com/one", Index: 1},
		{Type: llm.AnnotationTypeURLCitation, StartIndex: 5, EndIndex: 10, URL: "https://example.com/two", Index: 2},
	}

	applyPendingAnnotationPositions(annotations, []pendingAnnotationPosition{
		{index: 0, missingStart: true, missingEnd: true},
		{index: 1, missingEnd: true},
	}, 20, 42)

	assert.Equal(t, 20, annotations[0].StartIndex)
	assert.Equal(t, 42, annotations[0].EndIndex)
	assert.Equal(t, 5, annotations[1].StartIndex)
	assert.Equal(t, 42, annotations[1].EndIndex)
}

func TestPendingAnnotationPositionsWithoutContentIndex(t *testing.T) {
	pending := map[int][]pendingAnnotationPosition{}
	annotations := []llm.Annotation{
		{Type: llm.AnnotationTypeURLCitation, StartIndex: 0, EndIndex: 0, URL: "https://example.com", Index: 1},
	}

	pending[missingContentIndex] = append(pending[missingContentIndex], pendingAnnotationPosition{
		index:        0,
		missingStart: true,
		missingEnd:   true,
	})
	flushPendingAnnotationPositions(annotations, pending, missingContentIndex, 0, 42)

	assert.Empty(t, pending)
	assert.Equal(t, 0, annotations[0].StartIndex)
	assert.Equal(t, 42, annotations[0].EndIndex)
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

			req, err := b.convertToBifrostResponsesRequest(llm.CompletionRequest{}, cfg)
			require.NoError(t, err)

			if tt.expectFormat {
				require.NotNil(t, req.Params.Text)
				require.NotNil(t, req.Params.Text.Format)
				assert.Equal(t, "json_schema", req.Params.Text.Format.Type)
				assert.Equal(t, "response", *req.Params.Text.Format.Name)
				assert.Equal(t, true, *req.Params.Text.Format.Strict)
				require.NotNil(t, req.Params.Text.Format.JSONSchema)
				assert.Nil(t, req.Params.Text.Format.JSONSchema.Schema)
				require.NotNil(t, req.Params.Text.Format.JSONSchema.Type)
				assert.Equal(t, "object", *req.Params.Text.Format.JSONSchema.Type)
				require.NotNil(t, req.Params.Text.Format.JSONSchema.Properties)
				assert.Len(t, *req.Params.Text.Format.JSONSchema.Properties, 2)
				assert.ElementsMatch(t, []string{"name", "score"}, req.Params.Text.Format.JSONSchema.Required)
				require.NotNil(t, req.Params.Text.Format.JSONSchema.AdditionalProperties)
				require.NotNil(t, req.Params.Text.Format.JSONSchema.AdditionalProperties.AdditionalPropertiesBool)
				assert.False(t, *req.Params.Text.Format.JSONSchema.AdditionalProperties.AdditionalPropertiesBool)
			} else {
				assert.Nil(t, req.Params.Text)
			}
		})
	}
}

func TestConvertToBifrostResponsesRequestStructuredOutputStringEnum(t *testing.T) {
	b := &LLM{
		provider:        schemas.OpenAI,
		defaultModel:    "gpt-4",
		useResponsesAPI: true,
	}

	cfg := llm.LanguageModelConfig{
		Model:              "gpt-4",
		MaxGeneratedTokens: 1000,
		JSONOutputFormat: &jsonschema.Schema{
			Type: "string",
			Enum: []any{"open", "closed"},
		},
	}

	req, err := b.convertToBifrostResponsesRequest(llm.CompletionRequest{}, cfg)
	require.NoError(t, err)
	require.NotNil(t, req.Params.Text)
	require.NotNil(t, req.Params.Text.Format)
	require.NotNil(t, req.Params.Text.Format.JSONSchema)
	assert.Equal(t, []string{"open", "closed"}, req.Params.Text.Format.JSONSchema.Enum)
}

func TestConvertToBifrostResponsesRequestStructuredOutputRejectsNonStringEnum(t *testing.T) {
	b := &LLM{
		provider:        schemas.OpenAI,
		defaultModel:    "gpt-4",
		useResponsesAPI: true,
	}

	cfg := llm.LanguageModelConfig{
		Model:              "gpt-4",
		MaxGeneratedTokens: 1000,
		JSONOutputFormat: &jsonschema.Schema{
			Type: "integer",
			Enum: []any{1, 2},
		},
	}

	_, err := b.convertToBifrostResponsesRequest(llm.CompletionRequest{}, cfg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "enum[0] must be a string")
}

func TestConvertToBifrostResponsesRequestStructuredOutputMultiTypeArray(t *testing.T) {
	b := &LLM{
		provider:        schemas.OpenAI,
		defaultModel:    "gpt-4",
		useResponsesAPI: true,
	}

	cfg := llm.LanguageModelConfig{
		Model:              "gpt-4",
		MaxGeneratedTokens: 1000,
		JSONOutputFormat: &jsonschema.Schema{
			Types: []string{"string", "null"},
		},
	}

	req, err := b.convertToBifrostResponsesRequest(llm.CompletionRequest{}, cfg)
	require.NoError(t, err)
	require.NotNil(t, req.Params.Text.Format.JSONSchema)
	js := req.Params.Text.Format.JSONSchema
	assert.Nil(t, js.Type)
	require.Len(t, js.AnyOf, 2)
	assert.Equal(t, map[string]any{"type": "string"}, js.AnyOf[0])
	assert.Equal(t, map[string]any{"type": "null"}, js.AnyOf[1])
}

func TestConvertToBifrostResponsesRequestStructuredOutputTopLevelAnyOf(t *testing.T) {
	b := &LLM{
		provider:        schemas.OpenAI,
		defaultModel:    "gpt-4",
		useResponsesAPI: true,
	}

	cfg := llm.LanguageModelConfig{
		Model:              "gpt-4",
		MaxGeneratedTokens: 1000,
		JSONOutputFormat: &jsonschema.Schema{
			AnyOf: []*jsonschema.Schema{
				{Type: "string"},
				{Type: "number"},
			},
		},
	}

	req, err := b.convertToBifrostResponsesRequest(llm.CompletionRequest{}, cfg)
	require.NoError(t, err)
	require.NotNil(t, req.Params.Text.Format.JSONSchema)
	js := req.Params.Text.Format.JSONSchema
	require.Len(t, js.AnyOf, 2)
	assert.Equal(t, map[string]any{"type": "string"}, js.AnyOf[0])
	assert.Equal(t, map[string]any{"type": "number"}, js.AnyOf[1])
}

func TestChatCompletionNoStreamReturnsErrorForUnsupportedResponsesSchema(t *testing.T) {
	b := &LLM{
		provider:        schemas.OpenAI,
		defaultModel:    "gpt-4",
		useResponsesAPI: true,
	}

	_, err := b.ChatCompletionNoStream(
		context.Background(),
		llm.CompletionRequest{},
		func(cfg *llm.LanguageModelConfig) {
			cfg.Model = "gpt-4"
			cfg.JSONOutputFormat = &jsonschema.Schema{
				Type: "integer",
				Enum: []any{1, 2},
			}
		},
	)
	require.Error(t, err)
	assert.ErrorContains(t, err, "enum[0] must be a string")
}

func TestEnvProxyRouting(t *testing.T) {
	// Backend: fake OpenAI API that returns a valid SSE streaming response.
	var backendHit atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHit.Store(true)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer backend.Close()

	// Proxy: minimal HTTP CONNECT proxy that tunnels TCP to the backend.
	var proxyHit atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "expected CONNECT", http.StatusMethodNotAllowed)
			return
		}
		proxyHit.Store(true)

		targetConn, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer targetConn.Close()

		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking not supported", http.StatusInternalServerError)
			return
		}
		clientConn, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer clientConn.Close()

		_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(targetConn, clientConn)
			close(done)
		}()
		_, _ = io.Copy(clientConn, targetConn)
		<-done
	}))
	defer proxy.Close()

	// Point HTTP_PROXY and HTTPS_PROXY at our test proxy before Bifrost
	// initializes its dialer. Both are needed so fasthttpproxy's fast path
	// activates (it only proxies unconditionally when the two values match).
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)

	llmClient, err := New(Config{
		Provider:         schemas.OpenAI,
		APIKey:           "test-key",
		APIURL:           backend.URL,
		DefaultModel:     "gpt-4",
		StreamingTimeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer llmClient.client.Shutdown()

	result, err := llmClient.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "hello", result)
	assert.True(t, proxyHit.Load(), "request should have been tunneled through the proxy")
	assert.True(t, backendHit.Load(), "request should have reached the backend")
}
