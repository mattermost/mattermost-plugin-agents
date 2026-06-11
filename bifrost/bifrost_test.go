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
	"strings"
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
		ProviderSettings: ProviderSettings{
			Provider:              schemas.Vertex,
			Region:                "us-west1",
			VertexProjectID:       "my-gcp-project",
			VertexProjectNumber:   "123456789012",
			VertexAuthCredentials: saJSON,
		},
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
		ProviderSettings: ProviderSettings{
			Provider:              schemas.Vertex,
			Region:                "europe-west1",
			VertexProjectID:       "adc-project",
			VertexProjectNumber:   "",
			VertexAuthCredentials: "",
		},
	}
	keysADC, err := adc.GetKeysForProvider(context.Background(), schemas.Vertex)
	require.NoError(t, err)
	require.Len(t, keysADC, 1)
	require.NotNil(t, keysADC[0].VertexKeyConfig)
	assert.Equal(t, "adc-project", keysADC[0].VertexKeyConfig.ProjectID.Val)
	assert.Equal(t, "", keysADC[0].VertexKeyConfig.AuthCredentials.Val)

	other := &providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.OpenAI,
		},
	}
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

// --- multiProviderAccount tests ---

func TestMultiProviderAccount_SingleProvider(t *testing.T) {
	acc := newMultiProviderAccount()
	acc.addProvider(&providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.OpenAI,
			APIKey:   "openai-key",
			APIURL:   "https://api.openai.com",
			OrgID:    "org-123",
		},
	})

	providers, err := acc.GetConfiguredProviders()
	require.NoError(t, err)
	assert.Equal(t, []schemas.ModelProvider{schemas.OpenAI}, providers)

	keys, err := acc.GetKeysForProvider(context.Background(), schemas.OpenAI)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, "openai-key", keys[0].Value.Val)

	cfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

func TestMultiProviderAccount_MultipleProviders(t *testing.T) {
	acc := newMultiProviderAccount()
	acc.addProvider(&providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.OpenAI,
			APIKey:   "openai-key",
		},
	})
	acc.addProvider(&providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.Anthropic,
			APIKey:   "anthropic-key",
		},
	})

	providers, err := acc.GetConfiguredProviders()
	require.NoError(t, err)
	assert.Equal(t, []schemas.ModelProvider{schemas.OpenAI, schemas.Anthropic}, providers)

	// Verify each provider returns correct keys
	openaiKeys, err := acc.GetKeysForProvider(context.Background(), schemas.OpenAI)
	require.NoError(t, err)
	require.Len(t, openaiKeys, 1)
	assert.Equal(t, "openai-key", openaiKeys[0].Value.Val)

	anthropicKeys, err := acc.GetKeysForProvider(context.Background(), schemas.Anthropic)
	require.NoError(t, err)
	require.Len(t, anthropicKeys, 1)
	assert.Equal(t, "anthropic-key", anthropicKeys[0].Value.Val)
}

func TestMultiProviderAccount_UnknownProvider(t *testing.T) {
	acc := newMultiProviderAccount()
	acc.addProvider(&providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.OpenAI,
			APIKey:   "openai-key",
		},
	})

	_, err := acc.GetKeysForProvider(context.Background(), schemas.Anthropic)
	assert.Error(t, err)

	_, err = acc.GetConfigForProvider(schemas.Anthropic)
	assert.Error(t, err)
}

func TestMultiProviderAccount_DuplicateProvider(t *testing.T) {
	acc := newMultiProviderAccount()
	acc.addProvider(&providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.OpenAI,
			APIKey:   "first-key",
		},
	})
	// Second add with same provider should be silently skipped (first wins)
	acc.addProvider(&providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.OpenAI,
			APIKey:   "second-key",
		},
	})

	providers, err := acc.GetConfiguredProviders()
	require.NoError(t, err)
	assert.Len(t, providers, 1)

	keys, err := acc.GetKeysForProvider(context.Background(), schemas.OpenAI)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, "first-key", keys[0].Value.Val)
}

func TestMultiProviderAccount_AzureKeyConfig(t *testing.T) {
	acc := newMultiProviderAccount()
	acc.addProvider(&providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.Azure,
			APIKey:   "azure-key",
			APIURL:   "https://myservice.openai.azure.com",
		},
	})

	keys, err := acc.GetKeysForProvider(context.Background(), schemas.Azure)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.NotNil(t, keys[0].AzureKeyConfig)
	assert.Equal(t, "https://myservice.openai.azure.com", keys[0].AzureKeyConfig.Endpoint.Val)
}

func TestMultiProviderAccount_BedrockKeyConfig(t *testing.T) {
	acc := newMultiProviderAccount()
	acc.addProvider(&providerAccount{
		ProviderSettings: ProviderSettings{
			Provider:           schemas.Bedrock,
			APIKey:             "bedrock-key",
			Region:             "us-east-1",
			AWSAccessKeyID:     "AKIA123",
			AWSSecretAccessKey: "secret123",
		},
	})

	keys, err := acc.GetKeysForProvider(context.Background(), schemas.Bedrock)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.NotNil(t, keys[0].BedrockKeyConfig)
	assert.Equal(t, "AKIA123", keys[0].BedrockKeyConfig.AccessKey.Val)
	assert.Equal(t, "secret123", keys[0].BedrockKeyConfig.SecretKey.Val)
	require.NotNil(t, keys[0].BedrockKeyConfig.Region)
	assert.Equal(t, "us-east-1", keys[0].BedrockKeyConfig.Region.Val)
}

// --- Fallback request building tests ---

// TestConvertToBifrostRequest_FallbacksAttached verifies both request builders
// (chat and Responses) attach the configured fallback chain to the outgoing
// request, and leave it nil when none are configured. Per-entry routing is built
// by New (covered above) and exercised end to end by the failover tests; here we
// only pin that whatever chain is configured is wired onto the request at all —
// a guard against a builder that silently drops fallbacks.
func TestConvertToBifrostRequest_FallbacksAttached(t *testing.T) {
	configured := []schemas.Fallback{
		{Provider: schemas.Anthropic, Model: "claude-sonnet-4-20250514"},
		{Provider: schemas.Bedrock, Model: "anthropic.claude-3-sonnet-20240229-v1:0"},
	}
	tests := []struct {
		name            string
		useResponsesAPI bool
		fallbacks       []schemas.Fallback
	}{
		{name: "chat, no fallbacks", useResponsesAPI: false, fallbacks: nil},
		{name: "chat, with fallbacks", useResponsesAPI: false, fallbacks: configured},
		{name: "responses, no fallbacks", useResponsesAPI: true, fallbacks: nil},
		{name: "responses, with fallbacks", useResponsesAPI: true, fallbacks: configured},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{
				provider:        schemas.OpenAI,
				defaultModel:    "gpt-4o",
				useResponsesAPI: tt.useResponsesAPI,
				fallbacks:       tt.fallbacks,
			}

			var got []schemas.Fallback
			if tt.useResponsesAPI {
				req, err := b.convertToBifrostResponsesRequest(llm.CompletionRequest{}, b.GetDefaultConfig())
				require.NoError(t, err)
				got = req.Fallbacks
			} else {
				got = b.convertToBifrostRequest(llm.CompletionRequest{}, b.GetDefaultConfig()).Fallbacks
			}

			// The request must carry exactly the configured chain (nil when none).
			assert.Equal(t, tt.fallbacks, got)
		})
	}
}

// --- NewFromServiceConfig with fallbacks tests ---

func TestNewFromServiceConfig_NoFallbacks(t *testing.T) {
	svc := llm.ServiceConfig{
		ID:           "svc-1",
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "key",
		DefaultModel: "gpt-4o",
	}
	bot := llm.BotConfig{
		ID:          "bot-1",
		Name:        "ai",
		DisplayName: "AI",
		ServiceID:   "svc-1",
	}

	llmInstance, err := NewFromServiceConfig(svc, bot, nil)
	require.NoError(t, err)
	require.NotNil(t, llmInstance)
	defer llmInstance.Shutdown()

	assert.Nil(t, llmInstance.fallbacks)
	assert.Equal(t, schemas.OpenAI, llmInstance.provider)
	assert.Equal(t, "gpt-4o", llmInstance.defaultModel)
}

func TestNewFromServiceConfig_WithFallbackServices(t *testing.T) {
	primarySvc := llm.ServiceConfig{
		ID:           "svc-openai",
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "openai-key",
		DefaultModel: "gpt-4o",
	}
	fallbackSvc := llm.ServiceConfig{
		ID:           "svc-anthropic",
		Type:         llm.ServiceTypeAnthropic,
		APIKey:       "anthropic-key",
		DefaultModel: "claude-sonnet-4-20250514",
	}
	bot := llm.BotConfig{
		ID:          "bot-1",
		Name:        "ai",
		DisplayName: "AI",
		ServiceID:   "svc-openai",
	}

	llmInstance, err := NewFromServiceConfig(primarySvc, bot, []llm.ServiceConfig{fallbackSvc})
	require.NoError(t, err)
	require.NotNil(t, llmInstance)
	defer llmInstance.Shutdown()

	require.Len(t, llmInstance.fallbacks, 1)
	assert.Equal(t, schemas.Anthropic, llmInstance.fallbacks[0].Provider)
	assert.Equal(t, "claude-sonnet-4-20250514", llmInstance.fallbacks[0].Model)
}

func TestNewFromServiceConfig_MultipleFallbacks(t *testing.T) {
	primarySvc := llm.ServiceConfig{
		ID:           "svc-openai",
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "openai-key",
		DefaultModel: "gpt-4o",
	}
	fallbackAnthropicSvc := llm.ServiceConfig{
		ID:           "svc-anthropic",
		Type:         llm.ServiceTypeAnthropic,
		APIKey:       "anthropic-key",
		DefaultModel: "claude-sonnet-4-20250514",
	}
	fallbackLocalSvc := llm.ServiceConfig{
		ID:           "svc-local",
		Type:         llm.ServiceTypeOpenAICompatible,
		APIURL:       "http://localhost:11434/v1",
		DefaultModel: "llama3",
	}
	bot := llm.BotConfig{
		ID:          "bot-1",
		Name:        "ai",
		DisplayName: "AI",
		ServiceID:   "svc-openai",
	}

	llmInstance, err := NewFromServiceConfig(primarySvc, bot, []llm.ServiceConfig{fallbackAnthropicSvc, fallbackLocalSvc})
	require.NoError(t, err)
	require.NotNil(t, llmInstance)
	defer llmInstance.Shutdown()

	require.Len(t, llmInstance.fallbacks, 2)
	// The Anthropic fallback has its own base provider type, so it keeps it.
	assert.Equal(t, schemas.Anthropic, llmInstance.fallbacks[0].Provider)
	assert.Equal(t, "claude-sonnet-4-20250514", llmInstance.fallbacks[0].Model)
	// The local OpenAI-compatible fallback maps to the OpenAI provider, which the
	// primary already occupies. It must therefore be registered under a slot
	// DISTINCT from the primary's base OpenAI slot so it keeps its own base
	// URL/key at fallback time (proven end to end by
	// TestNewFromServiceConfig_OpenAICompatibleFallbackRoutesToOwnEndpoint). We
	// assert distinctness rather than the internal custom-provider name string.
	assert.NotEqual(t, schemas.OpenAI, llmInstance.fallbacks[1].Provider,
		"a same-base fallback must not collide on the primary's provider slot")
	assert.NotEqual(t, llmInstance.fallbacks[0].Provider, llmInstance.fallbacks[1].Provider,
		"each fallback must occupy a distinct slot")
	assert.Equal(t, "llama3", llmInstance.fallbacks[1].Model)
}

// TestNewFromServiceConfig_ErrorsOnUnmappableFallbackInChain pins the contract
// that a fallback service which cannot be mapped to a Bifrost provider fails
// bot construction instead of being silently dropped: an admin must find out
// at setup that the configured fallback won't be there at failover time.
// ServiceTypeScale is the realistic trigger: it is a valid plugin service type
// (IsValidService accepts it, so ResolveFallbackChain keeps it in the chain)
// that the Bifrost adapter's MapServiceTypeToProvider does not support.
func TestNewFromServiceConfig_ErrorsOnUnmappableFallbackInChain(t *testing.T) {
	primary := llm.ServiceConfig{
		ID:           "svc-openai",
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "openai-key",
		DefaultModel: "gpt-4o",
	}
	anthropicFallback := llm.ServiceConfig{
		ID:           "svc-anthropic",
		Type:         llm.ServiceTypeAnthropic,
		APIKey:       "anthropic-key",
		DefaultModel: "claude-sonnet-4-20250514",
	}
	unmappableFallback := llm.ServiceConfig{
		ID:           "svc-scale",
		Type:         llm.ServiceTypeScale, // valid plugin type, unsupported by the Bifrost adapter
		APIKey:       "scale-key",
		APIURL:       "https://scale.example.com",
		DefaultModel: "scale-model",
	}
	bot := llm.BotConfig{ID: "bot-1", Name: "ai", DisplayName: "AI", ServiceID: "svc-openai"}

	_, err := NewFromServiceConfig(primary, bot,
		[]llm.ServiceConfig{anthropicFallback, unmappableFallback})
	require.Error(t, err, "an unmappable fallback must fail bot construction, not be silently dropped")
	assert.Contains(t, err.Error(), "svc-scale", "the error must name the offending fallback service")
}

func TestNewFromServiceConfig_BotModelOverrideDoesNotAffectFallback(t *testing.T) {
	primarySvc := llm.ServiceConfig{
		ID:           "svc-openai",
		Type:         llm.ServiceTypeOpenAI,
		APIKey:       "openai-key",
		DefaultModel: "gpt-4o",
	}
	fallbackSvc := llm.ServiceConfig{
		ID:           "svc-anthropic",
		Type:         llm.ServiceTypeAnthropic,
		APIKey:       "anthropic-key",
		DefaultModel: "claude-sonnet-4-20250514",
	}
	bot := llm.BotConfig{
		ID:          "bot-1",
		Name:        "ai",
		DisplayName: "AI",
		ServiceID:   "svc-openai",
		Model:       "gpt-4o-mini", // Bot overrides model
	}

	llmInstance, err := NewFromServiceConfig(primarySvc, bot, []llm.ServiceConfig{fallbackSvc})
	require.NoError(t, err)
	require.NotNil(t, llmInstance)
	defer llmInstance.Shutdown()

	// Primary model is overridden by bot config
	assert.Equal(t, "gpt-4o-mini", llmInstance.defaultModel)

	// Fallback model uses the fallback service's DefaultModel, not the bot override
	require.Len(t, llmInstance.fallbacks, 1)
	assert.Equal(t, "claude-sonnet-4-20250514", llmInstance.fallbacks[0].Model)
}

// chatCompletionSSE writes a minimal OpenAI-style streaming chat completion
// response carrying the given content. bifrost.LLM issues streaming requests
// under the hood even for ChatCompletionNoStream (see TestEnvProxyRouting).
func chatCompletionSSE(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":null}]}\n\n", content)
	fmt.Fprint(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// TestNewFromServiceConfig_OpenAICompatibleFallbackRoutesToOwnEndpoint is the
// PR's flagship DDIL scenario: a cloud OpenAI-compatible primary that falls back
// to a local OpenAI-compatible model (e.g. Ollama) when the cloud is unavailable.
//
// Both services map to schemas.OpenAI, so the request-level fallback must still
// reach the local service's OWN base URL and key. This test drives a real
// request through Bifrost on the chat-completions path: the cloud endpoint
// fails, and the local endpoint must receive the fallback request and serve it.
//
// Using OpenAI-compatible for both keeps the request on /v1/chat/completions so
// the assertion isolates fallback routing (the collision fix) from the separate
// question of whether a local model supports the Responses API.
func TestNewFromServiceConfig_OpenAICompatibleFallbackRoutesToOwnEndpoint(t *testing.T) {
	var cloudHits, localHits atomic.Int32
	var cloudAuth, localAuth atomic.Value // string Authorization header per endpoint

	cloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudHits.Add(1)
		cloudAuth.Store(r.Header.Get("Authorization"))
		// Primary provider is unavailable (DDIL): fail every request.
		http.Error(w, `{"error":{"message":"service unavailable"}}`, http.StatusInternalServerError)
	}))
	defer cloudServer.Close()

	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localHits.Add(1)
		localAuth.Store(r.Header.Get("Authorization"))
		chatCompletionSSE(w, "from-local")
	}))
	defer localServer.Close()

	primarySvc := llm.ServiceConfig{
		ID:                "cloud-openai",
		Type:              llm.ServiceTypeOpenAICompatible,
		APIKey:            "cloud-key",
		APIURL:            cloudServer.URL,
		DefaultModel:      "gpt-4o",
		FallbackServiceID: "local-ollama",
	}
	localSvc := llm.ServiceConfig{
		ID:           "local-ollama",
		Type:         llm.ServiceTypeOpenAICompatible,
		APIKey:       "local-key",
		APIURL:       localServer.URL,
		DefaultModel: "llama3",
	}
	bot := llm.BotConfig{ID: "bot-1", Name: "ai", DisplayName: "AI", ServiceID: "cloud-openai"}

	llmInstance, err := NewFromServiceConfig(primarySvc, bot, []llm.ServiceConfig{localSvc})
	require.NoError(t, err)
	defer llmInstance.Shutdown()

	result, err := llmInstance.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
	})

	require.NoError(t, err, "fallback to the local OpenAI-compatible model should succeed")
	assert.Equal(t, "from-local", result, "response must come from the local fallback model")

	// The failover must happen in order: the cloud primary is attempted (and
	// fails) before the local fallback serves the response. Asserting cloudHits
	// guards against a regression where the primary is silently never tried.
	assert.Positive(t, cloudHits.Load(), "cloud primary should have been attempted before falling back")
	assert.Positive(t, localHits.Load(), "local fallback endpoint should have received the fallback request")

	// Each service must authenticate with its OWN key on its OWN endpoint. The
	// fallback is registered as a Bifrost custom provider (it collides on the
	// OpenAI base slot with the primary); a custom provider that inherited the
	// primary's credentials — or sent none — would 401 in production. These
	// assertions pin that the fallback sends "local-key", not "cloud-key".
	assert.Equal(t, "Bearer cloud-key", cloudAuth.Load(), "primary must use its own API key")
	assert.Equal(t, "Bearer local-key", localAuth.Load(), "fallback must send its own API key, not the primary's")
}

// TestNewFromServiceConfig_ResponsesPrimaryDowngradesToChatForLocalFallback
// covers the full DDIL scenario: a direct-OpenAI primary (which always uses the
// Responses API) failing over to a local OpenAI-compatible model that only
// speaks /v1/chat/completions. The request type is fixed by the primary, so the
// fallback would otherwise receive /v1/responses and fail. Registering the local
// model as a chat-only custom provider makes Bifrost transparently downgrade the
// Responses request to chat completions for it.
func TestNewFromServiceConfig_ResponsesPrimaryDowngradesToChatForLocalFallback(t *testing.T) {
	var localChatHit, localResponsesHit, cloudHit atomic.Bool
	var cloudPath atomic.Value // request path the direct-OpenAI primary received

	cloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudHit.Store(true)
		cloudPath.Store(r.URL.Path)
		// Primary is unavailable (DDIL): fail every request.
		http.Error(w, `{"error":{"message":"service unavailable"}}`, http.StatusInternalServerError)
	}))
	defer cloudServer.Close()

	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/chat/completions"):
			localChatHit.Store(true)
			chatCompletionSSE(w, "from-local")
		case strings.Contains(r.URL.Path, "/responses"):
			// A real local server (Ollama/vLLM) does not implement this endpoint.
			localResponsesHit.Store(true)
			http.Error(w, "not found", http.StatusNotFound)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer localServer.Close()

	// Direct OpenAI primary always uses the Responses API.
	primarySvc := llm.ServiceConfig{
		ID:                "cloud-openai",
		Type:              llm.ServiceTypeOpenAI,
		APIKey:            "cloud-key",
		APIURL:            cloudServer.URL,
		DefaultModel:      "gpt-4o",
		FallbackServiceID: "local-ollama",
	}
	// Local OpenAI-compatible model that only implements /v1/chat/completions.
	localSvc := llm.ServiceConfig{
		ID:           "local-ollama",
		Type:         llm.ServiceTypeOpenAICompatible,
		APIURL:       localServer.URL,
		DefaultModel: "llama3",
	}
	bot := llm.BotConfig{ID: "bot-1", Name: "ai", DisplayName: "AI", ServiceID: "cloud-openai"}

	llmInstance, err := NewFromServiceConfig(primarySvc, bot, []llm.ServiceConfig{localSvc})
	require.NoError(t, err)
	defer llmInstance.Shutdown()

	result, err := llmInstance.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
	})

	require.NoError(t, err, "Responses-API primary should downgrade and succeed against the chat-only local fallback")
	assert.Equal(t, "from-local", result)
	assert.True(t, cloudHit.Load(), "cloud primary should have been attempted before falling back")
	// The primary must start on the Responses API; otherwise no downgrade is
	// exercised and the local /chat/completions hit below would prove nothing.
	require.Equal(t, "/v1/responses", cloudPath.Load(), "direct-OpenAI primary must use the Responses API")
	assert.True(t, localChatHit.Load(), "local model should have been called on /v1/chat/completions (downgraded)")
	assert.False(t, localResponsesHit.Load(), "local model must not be called on /v1/responses")
}

// TestNewFromServiceConfig_PrimarySuccessDoesNotInvokeFallback proves the
// fallback stays dormant on the happy path: when the primary serves a response,
// the fallback endpoint must never be contacted. A regression that invoked
// fallbacks unconditionally (rather than only on primary failure) would
// double-bill, add latency, and could even return the wrong model's output.
func TestNewFromServiceConfig_PrimarySuccessDoesNotInvokeFallback(t *testing.T) {
	var primaryHits, fallbackHits atomic.Int32

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		chatCompletionSSE(w, "from-primary")
	}))
	defer primaryServer.Close()

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits.Add(1)
		chatCompletionSSE(w, "from-fallback")
	}))
	defer fallbackServer.Close()

	primarySvc := llm.ServiceConfig{
		ID:                "cloud-openai",
		Type:              llm.ServiceTypeOpenAICompatible,
		APIKey:            "cloud-key",
		APIURL:            primaryServer.URL,
		DefaultModel:      "gpt-4o",
		FallbackServiceID: "local-ollama",
	}
	fallbackSvc := llm.ServiceConfig{
		ID:           "local-ollama",
		Type:         llm.ServiceTypeOpenAICompatible,
		APIURL:       fallbackServer.URL,
		DefaultModel: "llama3",
	}
	bot := llm.BotConfig{ID: "bot-1", Name: "ai", DisplayName: "AI", ServiceID: "cloud-openai"}

	llmInstance, err := NewFromServiceConfig(primarySvc, bot, []llm.ServiceConfig{fallbackSvc})
	require.NoError(t, err)
	defer llmInstance.Shutdown()

	result, err := llmInstance.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "from-primary", result, "a healthy primary must serve the response itself")
	assert.Positive(t, primaryHits.Load(), "primary should have been called")
	assert.Zero(t, fallbackHits.Load(), "fallback must not be contacted when the primary succeeds")
}

// TestNewFromServiceConfig_MultiHopFailoverReachesSecondFallback proves Bifrost
// walks PAST the first fallback when it also fails — the core DDIL case where a
// cloud primary AND a regional fallback are both down but a local model is up.
// The middle hop is an Anthropic service: a different base provider type with a
// different auth scheme (x-api-key) and request path (/v1/messages). It records
// its auth header and returns an error, which proves the cross-base fallback was
// routed to its OWN endpoint with its OWN credentials before traversal continued
// to the final OpenAI-compatible hop.
func TestNewFromServiceConfig_MultiHopFailoverReachesSecondFallback(t *testing.T) {
	var cloudHits, regionalHits, localHits atomic.Int32
	var regionalAuth atomic.Value            // x-api-key seen by the Anthropic hop
	var regionalPath, localPath atomic.Value // request paths each hop received

	cloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudHits.Add(1)
		http.Error(w, `{"error":{"message":"service unavailable"}}`, http.StatusInternalServerError)
	}))
	defer cloudServer.Close()

	regionalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		regionalHits.Add(1)
		regionalAuth.Store(r.Header.Get("x-api-key"))
		regionalPath.Store(r.URL.Path)
		http.Error(w, `{"type":"error","error":{"type":"overloaded_error"}}`, http.StatusServiceUnavailable)
	}))
	defer regionalServer.Close()

	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localHits.Add(1)
		localPath.Store(r.URL.Path)
		chatCompletionSSE(w, "from-local")
	}))
	defer localServer.Close()

	primarySvc := llm.ServiceConfig{
		ID:                "cloud-openai",
		Type:              llm.ServiceTypeOpenAICompatible,
		APIKey:            "cloud-key",
		APIURL:            cloudServer.URL,
		DefaultModel:      "gpt-4o",
		FallbackServiceID: "regional-anthropic",
	}
	regionalSvc := llm.ServiceConfig{
		ID:                "regional-anthropic",
		Type:              llm.ServiceTypeAnthropic,
		APIKey:            "anthropic-key",
		APIURL:            regionalServer.URL,
		DefaultModel:      "claude-sonnet-4-20250514",
		FallbackServiceID: "local-ollama",
	}
	localSvc := llm.ServiceConfig{
		ID:           "local-ollama",
		Type:         llm.ServiceTypeOpenAICompatible,
		APIURL:       localServer.URL,
		DefaultModel: "llama3",
	}
	bot := llm.BotConfig{ID: "bot-1", Name: "ai", DisplayName: "AI", ServiceID: "cloud-openai"}

	// fallbackServices is the chain in resolved order (primary→regional→local).
	llmInstance, err := NewFromServiceConfig(primarySvc, bot, []llm.ServiceConfig{regionalSvc, localSvc})
	require.NoError(t, err)
	defer llmInstance.Shutdown()

	result, err := llmInstance.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
	})

	require.NoError(t, err, "failover should continue past the failed regional fallback to the local model")
	assert.Equal(t, "from-local", result, "response must come from the second (local) fallback")
	assert.Positive(t, cloudHits.Load(), "primary should have been attempted")
	assert.Positive(t, regionalHits.Load(), "the first fallback should have been attempted before the second")
	assert.Positive(t, localHits.Load(), "the second fallback should have served the request")
	assert.Equal(t, "anthropic-key", regionalAuth.Load(), "the cross-base fallback must reach its own endpoint with its own key (x-api-key)")
	// Each hop must be routed to its provider's own API path, not merely its host.
	assert.Equal(t, "/v1/messages", regionalPath.Load(), "the Anthropic hop must use the Messages API path")
	assert.Equal(t, "/v1/chat/completions", localPath.Load(), "the final OpenAI-compatible hop must use the chat completions path")
}

func TestServiceConfigToFallbackEntry(t *testing.T) {
	tests := []struct {
		name             string
		svc              llm.ServiceConfig
		expectedProvider schemas.ModelProvider
		expectedModel    string
		expectedAPIURL   string
		expectedChatOnly bool
		expectError      bool
	}{
		{
			name: "OpenAI service",
			svc: llm.ServiceConfig{
				Type:         llm.ServiceTypeOpenAI,
				APIKey:       "key",
				DefaultModel: "gpt-4o",
			},
			expectedProvider: schemas.OpenAI,
			expectedModel:    "gpt-4o",
			// Direct OpenAI always uses the Responses API, so it is not chat-only.
			expectedChatOnly: false,
		},
		{
			name: "Anthropic service",
			svc: llm.ServiceConfig{
				Type:         llm.ServiceTypeAnthropic,
				APIKey:       "key",
				DefaultModel: "claude-sonnet-4-20250514",
			},
			expectedProvider: schemas.Anthropic,
			expectedModel:    "claude-sonnet-4-20250514",
			// Non-OpenAI base providers handle the Responses API natively; the
			// chat-only downgrade gate only applies to OpenAI-base endpoints.
			expectedChatOnly: false,
		},
		{
			name: "Cohere service gets default URL",
			svc: llm.ServiceConfig{
				Type:         llm.ServiceTypeCohere,
				APIKey:       "key",
				DefaultModel: "command-r-plus",
			},
			expectedProvider: schemas.Cohere,
			expectedModel:    "command-r-plus",
			expectedAPIURL:   "https://api.cohere.ai/compatibility/v1",
			expectedChatOnly: false,
		},
		{
			name: "Mistral service gets default URL",
			svc: llm.ServiceConfig{
				Type:         llm.ServiceTypeMistral,
				APIKey:       "key",
				DefaultModel: "mistral-large-latest",
			},
			expectedProvider: schemas.Mistral,
			expectedModel:    "mistral-large-latest",
			expectedAPIURL:   "https://api.mistral.ai/v1",
			expectedChatOnly: false,
		},
		{
			name: "OpenAI Compatible normalizes URL and is chat-only",
			svc: llm.ServiceConfig{
				Type:         llm.ServiceTypeOpenAICompatible,
				APIURL:       "http://localhost:11434/v1",
				DefaultModel: "llama3",
			},
			expectedProvider: schemas.OpenAI,
			expectedModel:    "llama3",
			expectedAPIURL:   "http://localhost:11434",
			// A local OpenAI-compatible endpoint that does not advertise the
			// Responses API is chat-only, so it must carry the downgrade gate.
			expectedChatOnly: true,
		},
		{
			name: "OpenAI Compatible with Responses API is not chat-only",
			svc: llm.ServiceConfig{
				Type:            llm.ServiceTypeOpenAICompatible,
				APIURL:          "http://localhost:8000/v1",
				DefaultModel:    "gpt-oss",
				UseResponsesAPI: true,
			},
			expectedProvider: schemas.OpenAI,
			expectedModel:    "gpt-oss",
			expectedAPIURL:   "http://localhost:8000",
			// It speaks the Responses API, so no downgrade gate is needed.
			expectedChatOnly: false,
		},
		{
			name: "unsupported service type",
			svc: llm.ServiceConfig{
				Type: "unknown-type",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := serviceConfigToFallbackEntry(tt.svc)
			if tt.expectError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedProvider, entry.Provider)
			assert.Equal(t, tt.expectedModel, entry.DefaultModel)
			assert.Equal(t, tt.expectedAPIURL, entry.APIURL)
			assert.Equal(t, tt.expectedChatOnly, entry.ChatOnly)
		})
	}
}

func TestServiceConfigToFallbackEntry_VertexCredsAndKeyless(t *testing.T) {
	t.Run("vertex carries project and credentials", func(t *testing.T) {
		entry, err := serviceConfigToFallbackEntry(llm.ServiceConfig{
			ID:                    "vertex-svc",
			Type:                  llm.ServiceTypeVertex,
			DefaultModel:          "gemini-1.5-pro",
			Region:                "us-central1",
			VertexProjectID:       "my-project",
			VertexProjectNumber:   "12345",
			VertexAuthCredentials: `{"type":"service_account"}`,
		})
		require.NoError(t, err)
		assert.Equal(t, "vertex-svc", entry.ID)
		assert.Equal(t, schemas.Vertex, entry.Provider)
		assert.Equal(t, "my-project", entry.VertexProjectID)
		assert.Equal(t, "12345", entry.VertexProjectNumber)
		assert.Equal(t, `{"type":"service_account"}`, entry.VertexAuthCredentials)
	})

	t.Run("keyless when OpenAI-compatible has no API key", func(t *testing.T) {
		entry, err := serviceConfigToFallbackEntry(llm.ServiceConfig{
			ID:           "local",
			Type:         llm.ServiceTypeOpenAICompatible,
			APIURL:       "http://localhost:11434/v1",
			DefaultModel: "llama3",
		})
		require.NoError(t, err)
		assert.True(t, entry.IsKeyLess)
	})

	t.Run("not keyless when an API key is set", func(t *testing.T) {
		entry, err := serviceConfigToFallbackEntry(llm.ServiceConfig{
			ID:           "local",
			Type:         llm.ServiceTypeOpenAICompatible,
			APIKey:       "secret",
			APIURL:       "http://localhost:11434/v1",
			DefaultModel: "llama3",
		})
		require.NoError(t, err)
		assert.False(t, entry.IsKeyLess)
	})
}

// TestMultiProviderAccount_CustomProviderKeepsDistinctSlot verifies that a
// service sharing a base provider type with the primary is registered under a
// distinct custom-provider name with its own base URL and a CustomProviderConfig
// — the account-level mechanism that makes same-base fallbacks routable.
func TestMultiProviderAccount_CustomProviderKeepsDistinctSlot(t *testing.T) {
	acc := newMultiProviderAccount()
	acc.addProvider(&providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.OpenAI,
			APIKey:   "cloud",
			APIURL:   "https://api.openai.com",
		},
	})
	customName := customProviderName(schemas.OpenAI, "local")
	acc.addProvider(&providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.OpenAI,
			APIURL:   "http://localhost:11434",
		},
		name:    customName,
		keyless: true,
	})

	providers, err := acc.GetConfiguredProviders()
	require.NoError(t, err)
	assert.ElementsMatch(t, []schemas.ModelProvider{schemas.OpenAI, customName}, providers)

	customCfg, err := acc.GetConfigForProvider(customName)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:11434", customCfg.NetworkConfig.BaseURL)
	require.NotNil(t, customCfg.CustomProviderConfig)
	assert.Equal(t, schemas.OpenAI, customCfg.CustomProviderConfig.BaseProviderType)
	assert.True(t, customCfg.CustomProviderConfig.IsKeyLess)

	// The standard primary slot keeps its own URL and is not a custom provider.
	primaryCfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	require.NoError(t, err)
	assert.Equal(t, "https://api.openai.com", primaryCfg.NetworkConfig.BaseURL)
	assert.Nil(t, primaryCfg.CustomProviderConfig)
}

// TestNewFromServiceConfig_UnsupportedSameProviderCollisionErrors verifies that
// a same-type fallback whose provider cannot be a custom-provider base type
// (e.g. Mistral) fails bot construction rather than being silently misrouted to
// the primary's endpoint or dropped.
func TestNewFromServiceConfig_UnsupportedSameProviderCollisionErrors(t *testing.T) {
	primary := llm.ServiceConfig{
		ID:           "mistral-1",
		Type:         llm.ServiceTypeMistral,
		APIKey:       "key1",
		DefaultModel: "mistral-large-latest",
	}
	fallback := llm.ServiceConfig{
		ID:           "mistral-2",
		Type:         llm.ServiceTypeMistral,
		APIKey:       "key2",
		DefaultModel: "mistral-small-latest",
	}

	_, err := NewFromServiceConfig(primary, llm.BotConfig{ServiceID: "mistral-1"}, []llm.ServiceConfig{fallback})
	require.Error(t, err, "a same-type Mistral fallback cannot be disambiguated and must fail setup")
	assert.Contains(t, err.Error(), "mistral-2", "the error must name the offending fallback service")
}

// TestNewFromServiceConfig_ChatOnlyFallbackGetsCustomProviderWithoutCollision
// verifies the full-coverage path: a local OpenAI-compatible fallback that does
// not use the Responses API is registered under a custom provider name even when
// it does NOT collide with the primary (here an Anthropic primary). This is what
// lets Bifrost attach the chat-only gate so a Responses primary can downgrade.
func TestNewFromServiceConfig_ChatOnlyFallbackGetsCustomProviderWithoutCollision(t *testing.T) {
	primary := llm.ServiceConfig{
		ID:           "anthropic-1",
		Type:         llm.ServiceTypeAnthropic,
		APIKey:       "key",
		DefaultModel: "claude-sonnet-4-20250514",
	}
	localSvc := llm.ServiceConfig{
		ID:           "local-ollama",
		Type:         llm.ServiceTypeOpenAICompatible,
		APIURL:       "http://localhost:11434/v1",
		DefaultModel: "llama3",
	}

	llmInstance, err := NewFromServiceConfig(primary, llm.BotConfig{ServiceID: "anthropic-1"}, []llm.ServiceConfig{localSvc})
	require.NoError(t, err)
	defer llmInstance.Shutdown()

	require.Len(t, llmInstance.fallbacks, 1)
	// Even though the OpenAI base slot is free (primary is Anthropic), the
	// chat-only local model must be registered under a slot distinct from the
	// bare OpenAI provider so the chat-only AllowedRequests gate can be attached
	// (see TestProviderAccount_ChatOnlyCustomConfig). Assert distinctness, not the
	// internal custom-provider name string.
	assert.NotEqual(t, schemas.OpenAI, llmInstance.fallbacks[0].Provider,
		"a chat-only fallback must get its own custom-provider slot to carry the downgrade gate")
}

// TestProviderAccount_ChatOnlyCustomConfig verifies that a chat-only custom
// provider declares chat-only AllowedRequests (the gate Bifrost uses to downgrade
// Responses → chat), while a non-chat-only custom provider leaves it unset.
func TestProviderAccount_ChatOnlyCustomConfig(t *testing.T) {
	chatOnly := &providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.OpenAI,
			APIURL:   "http://localhost:11434",
		},
		name:     customProviderName(schemas.OpenAI, "local"),
		chatOnly: true,
	}
	cfg, err := chatOnly.GetConfigForProvider(chatOnly.registeredName())
	require.NoError(t, err)
	require.NotNil(t, cfg.CustomProviderConfig)
	require.NotNil(t, cfg.CustomProviderConfig.AllowedRequests)
	assert.True(t, cfg.CustomProviderConfig.AllowedRequests.ChatCompletion)
	assert.True(t, cfg.CustomProviderConfig.AllowedRequests.ChatCompletionStream)
	assert.False(t, cfg.CustomProviderConfig.AllowedRequests.Responses)
	assert.False(t, cfg.CustomProviderConfig.AllowedRequests.ResponsesStream)

	// A custom provider that does support the Responses API leaves AllowedRequests
	// unset so all operations remain available.
	responsesCapable := &providerAccount{
		ProviderSettings: ProviderSettings{
			Provider: schemas.OpenAI,
			APIURL:   "https://api.example.com",
		},
		name: customProviderName(schemas.OpenAI, "other"),
	}
	cfg, err = responsesCapable.GetConfigForProvider(responsesCapable.registeredName())
	require.NoError(t, err)
	require.NotNil(t, cfg.CustomProviderConfig)
	assert.Nil(t, cfg.CustomProviderConfig.AllowedRequests)
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
		ProviderSettings: ProviderSettings{
			Provider:         schemas.OpenAI,
			APIKey:           "test-key",
			APIURL:           backend.URL,
			DefaultModel:     "gpt-4",
			StreamingTimeout: 10 * time.Second,
		},
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

func TestConvertChatUsage(t *testing.T) {
	tests := []struct {
		name  string
		usage *schemas.BifrostLLMUsage
		want  llm.TokenUsage
	}{
		{
			name:  "nil usage yields zero value",
			usage: nil,
			want:  llm.TokenUsage{},
		},
		{
			name: "nil detail and cost pointers stay zero",
			usage: &schemas.BifrostLLMUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
			},
			want: llm.TokenUsage{InputTokens: 100, OutputTokens: 50},
		},
		{
			name: "fully populated payload carries every field",
			usage: &schemas.BifrostLLMUsage{
				PromptTokens:     1200,
				CompletionTokens: 350,
				PromptTokensDetails: &schemas.ChatPromptTokensDetails{
					CachedReadTokens:  800,
					CachedWriteTokens: 100,
				},
				CompletionTokensDetails: &schemas.ChatCompletionTokensDetails{
					ReasoningTokens: 64,
				},
				Cost: &schemas.BifrostCost{TotalCost: 0.0123},
			},
			want: llm.TokenUsage{
				InputTokens:       1200,
				OutputTokens:      350,
				CachedReadTokens:  800,
				CachedWriteTokens: 100,
				ReasoningTokens:   64,
				Cost:              0.0123,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertChatUsage(tt.usage)
			assert.Equal(t, tt.want.InputTokens, got.InputTokens)
			assert.Equal(t, tt.want.OutputTokens, got.OutputTokens)
			assert.Equal(t, tt.want.CachedReadTokens, got.CachedReadTokens)
			assert.Equal(t, tt.want.CachedWriteTokens, got.CachedWriteTokens)
			assert.Equal(t, tt.want.ReasoningTokens, got.ReasoningTokens)
			assert.InDelta(t, tt.want.Cost, got.Cost, 1e-9)
		})
	}
}

func TestCountTokensReturnsCount(t *testing.T) {
	var backendHit atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHit.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens": 42}`))
	}))
	defer backend.Close()

	llmClient, err := New(Config{
		ProviderSettings: ProviderSettings{
			Provider:         schemas.Anthropic,
			APIKey:           "test-key",
			APIURL:           backend.URL,
			DefaultModel:     "claude-sonnet-4-5",
			StreamingTimeout: 10 * time.Second,
		},
	})
	require.NoError(t, err)
	defer llmClient.client.Shutdown()

	count, err := llmClient.CountTokens(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hello world"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 42, count)
	assert.True(t, backendHit.Load())
}

// TestCountTokensOmitsMaxOutputTokens pins the second body-shape fix for
// the count_tokens endpoint. OpenAI's Responses-API count endpoint rejects
// max_output_tokens with "Unknown parameter: 'max_output_tokens'." even
// though the streaming endpoint requires it. We strip the whole Params
// payload for the count call since none of those fields are needed for
// token math.
func TestCountTokensOmitsMaxOutputTokens(t *testing.T) {
	var recordedBody []byte
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens": 99}`))
	}))
	defer backend.Close()

	llmClient, err := New(Config{
		ProviderSettings: ProviderSettings{
			Provider:         schemas.OpenAI,
			APIKey:           "test-key",
			APIURL:           backend.URL,
			DefaultModel:     "gpt-5.4",
			StreamingTimeout: 10 * time.Second,
		},
		OutputTokenLimit: 8192, // produces MaxGeneratedTokens > 0 → MaxOutputTokens in the request
	})
	require.NoError(t, err)
	defer llmClient.client.Shutdown()

	count, err := llmClient.CountTokens(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hello world"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 99, count)
	require.NotEmpty(t, recordedBody)
	assert.NotContains(t, string(recordedBody), "max_output_tokens",
		"the count_tokens endpoint rejects max_output_tokens; "+
			"sending it falls us back to 'estimated' for any OpenAI bot")
}

// TestCountTokensOmitsNativeServerTools pins down the fix for the
// production failure surfaced via the /conversations/:id/context endpoint:
// Anthropic's count_tokens endpoint rejects any request that carries native
// server tools (web_search, file_search, code_interpreter), so those must be
// stripped before the count call. Function tool definitions are kept (see
// TestCountTokensKeepsFunctionTools) because they contribute to the count.
func TestCountTokensOmitsNativeServerTools(t *testing.T) {
	var recordedBody []byte
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens": 42}`))
	}))
	defer backend.Close()

	// Bot configured with native web_search — same shape that triggered
	// the production "Server tools are not supported in the count_tokens
	// endpoint" error.
	llmClient, err := New(Config{
		ProviderSettings: ProviderSettings{
			Provider:         schemas.Anthropic,
			APIKey:           "test-key",
			APIURL:           backend.URL,
			DefaultModel:     "claude-sonnet-4-6",
			StreamingTimeout: 10 * time.Second,
		},
		EnabledNativeTools: []string{"web_search"},
	})
	require.NoError(t, err)
	defer llmClient.client.Shutdown()

	count, err := llmClient.CountTokens(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hello world"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 42, count)

	require.NotEmpty(t, recordedBody, "backend must have received the count_tokens request")
	assert.NotContains(t, string(recordedBody), "web_search",
		"native server tools must be stripped before the count_tokens call — "+
			"Anthropic 400s the request otherwise and we fall back to estimated")
	assert.NotContains(t, string(recordedBody), "code_execution")
	assert.NotContains(t, string(recordedBody), "file_search")
}

// TestCountTokensKeepsFunctionTools pins the other half of the count_tokens
// body shape: function (custom) tool definitions are part of the prompt and
// consume input tokens, so they must reach the count endpoint. Dropping them
// undercounts every tools-enabled bot and surfaces a misleadingly low number.
func TestCountTokensKeepsFunctionTools(t *testing.T) {
	var recordedBody []byte
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens": 55}`))
	}))
	defer backend.Close()

	llmClient, err := New(Config{
		ProviderSettings: ProviderSettings{
			Provider:         schemas.Anthropic,
			APIKey:           "test-key",
			APIURL:           backend.URL,
			DefaultModel:     "claude-sonnet-4-6",
			StreamingTimeout: 10 * time.Second,
		},
	})
	require.NoError(t, err)
	defer llmClient.client.Shutdown()

	tools := llm.NewToolStore()
	tools.AddTools([]llm.Tool{
		{Name: "get_weather", Description: "Returns weather for a city", Schema: map[string]interface{}{"type": "object"}},
	})

	count, err := llmClient.CountTokens(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hello world"}},
		Context: &llm.Context{Tools: tools},
	})
	require.NoError(t, err)
	assert.Equal(t, 55, count)

	require.NotEmpty(t, recordedBody)
	assert.Contains(t, string(recordedBody), "get_weather",
		"function tool definitions contribute to the input-token count and must reach count_tokens")
}

func TestCountTokensUnsupportedProvider(t *testing.T) {
	var backendHit atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// Mistral doesn't implement count-tokens in Bifrost; it returns the
	// "unsupported_operation" error synchronously. CountTokens must classify
	// that as ErrUnsupportedTokenCount without contacting the backend.
	llmClient, err := New(Config{
		ProviderSettings: ProviderSettings{
			Provider:         schemas.Mistral,
			APIKey:           "test-key",
			APIURL:           backend.URL,
			DefaultModel:     "mistral-large-latest",
			StreamingTimeout: 10 * time.Second,
		},
	})
	require.NoError(t, err)
	defer llmClient.client.Shutdown()

	_, err = llmClient.CountTokens(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hello"}},
	})
	require.ErrorIs(t, err, llm.ErrUnsupportedTokenCount)
	assert.False(t, backendHit.Load(), "unsupported provider must not hit the backend")
}

func TestCountTokensScrubsAPIKeyFromError(t *testing.T) {
	const secret = "sk-real-secret-key-do-not-leak" // #nosec G101 -- fixture, not a real credential
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Anthropic-shaped error payload that echoes the configured key.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid key %s"}}`, secret)
	}))
	defer backend.Close()

	llmClient, err := New(Config{
		ProviderSettings: ProviderSettings{
			Provider:         schemas.Anthropic,
			APIKey:           secret,
			APIURL:           backend.URL,
			DefaultModel:     "claude-sonnet-4-5",
			StreamingTimeout: 10 * time.Second,
		},
	})
	require.NoError(t, err)
	defer llmClient.client.Shutdown()

	_, err = llmClient.CountTokens(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hello"}},
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret, "API key must be redacted from provider error messages")
}

// newSearchToolStore builds a one-tool store for tool_choice tests.
func newSearchToolStore() *llm.ToolStore {
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{
		Name:        "search_posts",
		Description: "search",
		Resolver:    func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil },
	}})
	return store
}

func TestConvertToBifrostRequestToolChoiceNone(t *testing.T) {
	noneStr := string(schemas.ChatToolChoiceTypeNone)

	tests := []struct {
		name           string
		toolsDisabled  bool
		toolUseHistory bool
		expectTools    bool
		expectChoice   *string
	}{
		{
			name:           "tools enabled — tools included, no tool_choice override",
			toolsDisabled:  false,
			toolUseHistory: false,
			expectTools:    true,
			expectChoice:   nil,
		},
		{
			name:           "tools disabled, no history — tools omitted, no tool_choice",
			toolsDisabled:  true,
			toolUseHistory: false,
			expectTools:    false,
			expectChoice:   nil,
		},
		{
			name:           "tools disabled with history tool_use — tools kept, tool_choice none",
			toolsDisabled:  true,
			toolUseHistory: true,
			expectTools:    true,
			expectChoice:   &noneStr,
		},
		{
			name:           "tools enabled with history tool_use — tools included, no override",
			toolsDisabled:  false,
			toolUseHistory: true,
			expectTools:    true,
			expectChoice:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{provider: schemas.Anthropic, defaultModel: "claude"}

			posts := []llm.Post{{Role: llm.PostRoleUser, Message: "find video info"}}
			if tt.toolUseHistory {
				posts = append(posts, llm.Post{
					Role:    llm.PostRoleBot,
					Message: "Let me search",
					ToolUse: []llm.ToolCall{{ID: "tc1", Name: "search_posts", Status: llm.ToolCallStatusAutoApproved}},
				})
			}
			req := llm.CompletionRequest{
				Posts:   posts,
				Context: &llm.Context{Tools: newSearchToolStore()},
			}
			cfg := llm.LanguageModelConfig{Model: "claude", ToolsDisabled: tt.toolsDisabled}

			got := b.convertToBifrostRequest(req, cfg)

			if tt.expectTools {
				require.NotEmpty(t, got.Params.Tools, "expected tools in request")
			} else {
				assert.Empty(t, got.Params.Tools, "expected no tools in request")
			}

			if tt.expectChoice == nil {
				assert.Nil(t, got.Params.ToolChoice, "expected no tool_choice override")
			} else {
				require.NotNil(t, got.Params.ToolChoice)
				require.NotNil(t, got.Params.ToolChoice.ChatToolChoiceStr)
				assert.Equal(t, *tt.expectChoice, *got.Params.ToolChoice.ChatToolChoiceStr)
			}
		})
	}
}

func TestConvertToBifrostResponsesRequestToolChoiceNone(t *testing.T) {
	noneStr := string(schemas.ResponsesToolChoiceTypeNone)

	tests := []struct {
		name            string
		toolsDisabled   bool
		toolUseHistory  bool
		expectFuncTools bool
		expectChoice    *string
	}{
		{
			name:            "tools enabled — function tools included, no tool_choice override",
			toolsDisabled:   false,
			toolUseHistory:  false,
			expectFuncTools: true,
			expectChoice:    nil,
		},
		{
			name:            "tools disabled, no history — function tools omitted, no tool_choice",
			toolsDisabled:   true,
			toolUseHistory:  false,
			expectFuncTools: false,
			expectChoice:    nil,
		},
		{
			name:            "tools disabled with history tool_use — function tools kept, tool_choice none",
			toolsDisabled:   true,
			toolUseHistory:  true,
			expectFuncTools: true,
			expectChoice:    &noneStr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{provider: schemas.OpenAI, defaultModel: "gpt-4", useResponsesAPI: true}

			posts := []llm.Post{{Role: llm.PostRoleUser, Message: "find video info"}}
			if tt.toolUseHistory {
				posts = append(posts, llm.Post{
					Role:    llm.PostRoleBot,
					Message: "Let me search",
					ToolUse: []llm.ToolCall{{ID: "tc1", Name: "search_posts", Status: llm.ToolCallStatusAutoApproved}},
				})
			}
			req := llm.CompletionRequest{
				Posts:   posts,
				Context: &llm.Context{Tools: newSearchToolStore()},
			}
			cfg := llm.LanguageModelConfig{Model: "gpt-4", ToolsDisabled: tt.toolsDisabled}

			got, err := b.convertToBifrostResponsesRequest(req, cfg)
			require.NoError(t, err)

			hasFunction := false
			for _, tool := range got.Params.Tools {
				if tool.Type == schemas.ResponsesToolTypeFunction {
					hasFunction = true
					break
				}
			}
			assert.Equal(t, tt.expectFuncTools, hasFunction, "function tool presence mismatch")

			if tt.expectChoice == nil {
				assert.Nil(t, got.Params.ToolChoice, "expected no tool_choice override")
			} else {
				require.NotNil(t, got.Params.ToolChoice)
				require.NotNil(t, got.Params.ToolChoice.ResponsesToolChoiceStr)
				assert.Equal(t, *tt.expectChoice, *got.Params.ToolChoice.ResponsesToolChoiceStr)
			}
		})
	}
}
