// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package anthropic

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	anthropicSDK "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

const (
	DefaultMaxTokens       = 8192
	MaxToolResolutionDepth = 10
)

type messageState struct {
	messages []anthropicSDK.MessageParam
	system   string
	output   chan<- llm.TextStreamEvent
	depth    int
	config   llm.LanguageModelConfig
	tools    []llm.Tool
	resolver func(name string, argsGetter llm.ToolArgumentGetter, context *llm.Context) (string, error)
	context  *llm.Context
}

type Anthropic struct {
	client             anthropicSDK.Client
	defaultModel       string
	inputTokenLimit    int
	outputTokenLimit   int
	enabledNativeTools []string
}

func New(llmService llm.ServiceConfig, httpClient *http.Client) *Anthropic {
	client := anthropicSDK.NewClient(
		option.WithAPIKey(llmService.APIKey),
		option.WithHTTPClient(httpClient),
	)

	return &Anthropic{
		client:             client,
		defaultModel:       llmService.DefaultModel,
		inputTokenLimit:    llmService.InputTokenLimit,
		outputTokenLimit:   llmService.OutputTokenLimit,
		enabledNativeTools: llmService.EnabledNativeTools,
	}
}

// isValidImageType checks if the MIME type is supported by the Anthropic API
func isValidImageType(mimeType string) bool {
	validTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/gif":  true,
		"image/webp": true,
	}
	return validTypes[mimeType]
}

// conversationToMessages creates a system prompt and a slice of input messages from conversation posts.
func conversationToMessages(posts []llm.Post) (string, []anthropicSDK.MessageParam) {
	systemMessage := ""
	messages := make([]anthropicSDK.MessageParam, 0, len(posts))

	var currentBlocks []anthropicSDK.ContentBlockParamUnion
	var currentRole anthropicSDK.MessageParamRole

	flushCurrentMessage := func() {
		if len(currentBlocks) > 0 {
			messages = append(messages, anthropicSDK.MessageParam{
				Role:    currentRole,
				Content: currentBlocks,
			})
			currentBlocks = nil
		}
	}

	for _, post := range posts {
		switch post.Role {
		case llm.PostRoleSystem:
			systemMessage += post.Message
			continue
		case llm.PostRoleBot:
			if currentRole != anthropicSDK.MessageParamRoleAssistant {
				flushCurrentMessage()
				currentRole = anthropicSDK.MessageParamRoleAssistant
			}
		case llm.PostRoleUser:
			if currentRole != anthropicSDK.MessageParamRoleUser {
				flushCurrentMessage()
				currentRole = anthropicSDK.MessageParamRoleUser
			}
		default:
			continue
		}

		if post.Message != "" {
			textBlock := anthropicSDK.NewTextBlock(post.Message)
			currentBlocks = append(currentBlocks, textBlock)
		}

		for _, file := range post.Files {
			if !isValidImageType(file.MimeType) {
				textBlock := anthropicSDK.NewTextBlock(fmt.Sprintf("[Unsupported image type: %s]", file.MimeType))
				currentBlocks = append(currentBlocks, textBlock)
				continue
			}

			data, err := io.ReadAll(file.Reader)
			if err != nil {
				textBlock := anthropicSDK.NewTextBlock("[Error reading image data]")
				currentBlocks = append(currentBlocks, textBlock)
				continue
			}

			encodedData := base64.StdEncoding.EncodeToString(data)
			imageBlock := anthropicSDK.NewImageBlockBase64(file.MimeType, encodedData)
			currentBlocks = append(currentBlocks, imageBlock)
		}

		if len(post.ToolUse) > 0 {
			for _, tool := range post.ToolUse {
				toolBlock := anthropicSDK.NewToolUseBlock(tool.ID, tool.Arguments, tool.Name)
				currentBlocks = append(currentBlocks, toolBlock)
			}

			resultBlocks := make([]anthropicSDK.ContentBlockParamUnion, 0, len(post.ToolUse))
			for _, tool := range post.ToolUse {
				isError := tool.Status != llm.ToolCallStatusSuccess
				toolResultBlock := anthropicSDK.NewToolResultBlock(tool.ID, tool.Result, isError)
				resultBlocks = append(resultBlocks, toolResultBlock)
			}

			if len(resultBlocks) > 0 {
				flushCurrentMessage()
				currentRole = anthropicSDK.MessageParamRoleUser
				currentBlocks = resultBlocks
				flushCurrentMessage()
			}
		}
	}

	flushCurrentMessage()
	return systemMessage, messages
}

func (a *Anthropic) GetDefaultConfig() llm.LanguageModelConfig {
	config := llm.LanguageModelConfig{
		Model: a.defaultModel,
	}
	if a.outputTokenLimit == 0 {
		config.MaxGeneratedTokens = DefaultMaxTokens
	} else {
		config.MaxGeneratedTokens = a.outputTokenLimit
	}
	return config
}

func (a *Anthropic) createConfig(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := a.GetDefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func (a *Anthropic) streamChatWithTools(state messageState) {
	if state.depth >= MaxToolResolutionDepth {
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: fmt.Errorf("max tool resolution depth (%d) exceeded", MaxToolResolutionDepth),
		}
		return
	}

	// Set up parameters for the Anthropic API
	params := anthropicSDK.MessageNewParams{
		Model:     anthropicSDK.Model(state.config.Model),
		MaxTokens: int64(state.config.MaxGeneratedTokens),
		Messages:  state.messages,
		System: []anthropicSDK.TextBlockParam{{
			Text: state.system,
		}},
		Tools: convertTools(state.tools),
	}

	// Add native tools if enabled
	if a.isNativeToolEnabled("web_search") {
		// Add web search as a native tool
		webSearchTool := anthropicSDK.WebSearchTool20250305Param{
			Name: "web_search",
			Type: "web_search_20250305",
		}
		params.Tools = append(params.Tools, anthropicSDK.ToolUnionParam{
			OfWebSearchTool20250305: &webSearchTool,
		})
	}

	// Enable thinking/reasoning for models that support it
	// We'll allocate a reasonable budget for thinking tokens (1/4 of max tokens, capped at 8192)
	thinkingBudget := int64(state.config.MaxGeneratedTokens / 4)
	if thinkingBudget > 8192 {
		thinkingBudget = 8192
	}
	if thinkingBudget < 1024 {
		thinkingBudget = 1024
	}

	params.Thinking = anthropicSDK.ThinkingConfigParamUnion{
		OfEnabled: &anthropicSDK.ThinkingConfigEnabledParam{
			Type:         "enabled",
			BudgetTokens: thinkingBudget,
		},
	}

	stream := a.client.Messages.NewStreaming(context.Background(), params)

	message := anthropicSDK.Message{}
	var thinkingBuffer strings.Builder
	var isThinkingComplete bool

	for stream.Next() {
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			state.output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: fmt.Errorf("error accumulating message: %w", err),
			}
			return
		}

		// Stream text and thinking content immediately
		switch eventVariant := event.AsAny().(type) { //nolint:gocritic
		case anthropicSDK.ContentBlockDeltaEvent:
			switch deltaVariant := eventVariant.Delta.AsAny().(type) { //nolint:gocritic
			case anthropicSDK.TextDelta:
				state.output <- llm.TextStreamEvent{
					Type:  llm.EventTypeText,
					Value: deltaVariant.Text,
				}
			case anthropicSDK.ThinkingDelta:
				// Accumulate thinking text
				thinkingBuffer.WriteString(deltaVariant.Thinking)
				// Stream thinking chunks as they arrive
				state.output <- llm.TextStreamEvent{
					Type:  llm.EventTypeReasoning,
					Value: deltaVariant.Thinking,
				}
			}

		case anthropicSDK.ContentBlockStopEvent:
			// Check if this is the end of a thinking block
			if thinkingBuffer.Len() > 0 && !isThinkingComplete {
				// Send the complete thinking/reasoning
				state.output <- llm.TextStreamEvent{
					Type:  llm.EventTypeReasoningEnd,
					Value: thinkingBuffer.String(),
				}
				isThinkingComplete = true
			}
		}
	}

	if err := stream.Err(); err != nil {
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: fmt.Errorf("error from anthropic stream: %w", err),
		}
		return
	}

	// If we haven't sent the complete thinking yet, send it now before processing tool calls
	if !isThinkingComplete && thinkingBuffer.Len() > 0 {
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeReasoningEnd,
			Value: thinkingBuffer.String(),
		}
	}

	// Check for tool usage in the message
	pendingToolCalls := make([]llm.ToolCall, 0, len(message.Content))
	for _, block := range message.Content {
		if block.Type == "tool_use" {
			pendingToolCalls = append(pendingToolCalls, llm.ToolCall{
				ID:          block.ID,
				Name:        block.Name,
				Description: "",
				Arguments:   block.Input,
			})
		}
	}

	// If tools were used, send tool calls event
	if len(pendingToolCalls) > 0 {
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeToolCalls,
			Value: pendingToolCalls,
		}
	}

	// Extract and send token usage data
	usage := llm.TokenUsage{
		InputTokens:  message.Usage.InputTokens,
		OutputTokens: message.Usage.OutputTokens,
	}
	state.output <- llm.TextStreamEvent{
		Type:  llm.EventTypeUsage,
		Value: usage,
	}

	// Send end event
	state.output <- llm.TextStreamEvent{
		Type:  llm.EventTypeEnd,
		Value: nil,
	}
}

func (a *Anthropic) ChatCompletion(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	eventStream := make(chan llm.TextStreamEvent)

	cfg := a.createConfig(opts)

	system, messages := conversationToMessages(request.Posts)

	initialState := messageState{
		messages: messages,
		system:   system,
		output:   eventStream,
		depth:    0,
		config:   cfg,
		context:  request.Context,
	}

	if request.Context.Tools != nil {
		initialState.tools = request.Context.Tools.GetTools()
		initialState.resolver = request.Context.Tools.ResolveTool
	}

	go func() {
		defer close(eventStream)
		a.streamChatWithTools(initialState)
	}()

	return &llm.TextStreamResult{Stream: eventStream}, nil
}

func (a *Anthropic) ChatCompletionNoStream(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	// This could perform better if we didn't use the streaming API here, but the complexity is not worth it.
	result, err := a.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (a *Anthropic) CountTokens(text string) int {
	return 0
}

// convertTools converts from llm.Tool to anthropicSDK.ToolUnionParam format
func convertTools(tools []llm.Tool) []anthropicSDK.ToolUnionParam {
	converted := make([]anthropicSDK.ToolUnionParam, len(tools))
	for i, tool := range tools {
		converted[i] = anthropicSDK.ToolUnionParam{
			OfTool: &anthropicSDK.ToolParam{
				Name:        tool.Name,
				Description: anthropicSDK.String(tool.Description),
				InputSchema: anthropicSDK.ToolInputSchemaParam{Properties: tool.Schema.Properties},
			},
		}
	}
	return converted
}

func (a *Anthropic) InputTokenLimit() int {
	if a.inputTokenLimit > 0 {
		return a.inputTokenLimit
	}
	return 100000
}

// isNativeToolEnabled checks if a specific native tool is enabled in the configuration
func (a *Anthropic) isNativeToolEnabled(toolName string) bool {
	for _, enabledTool := range a.enabledNativeTools {
		if enabledTool == toolName {
			return true
		}
	}
	return false
}
