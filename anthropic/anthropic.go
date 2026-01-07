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
	"github.com/google/jsonschema-go/jsonschema"

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
	reasoningEnabled   bool
	thinkingBudget     int
}

func New(llmService llm.ServiceConfig, botConfig llm.BotConfig, httpClient *http.Client) *Anthropic {
	client := anthropicSDK.NewClient(
		option.WithAPIKey(llmService.APIKey),
		option.WithHTTPClient(httpClient),
	)

	return &Anthropic{
		client:             client,
		defaultModel:       llmService.DefaultModel,
		inputTokenLimit:    llmService.InputTokenLimit,
		outputTokenLimit:   llmService.OutputTokenLimit,
		enabledNativeTools: botConfig.EnabledNativeTools,
		reasoningEnabled:   botConfig.ReasoningEnabled,
		thinkingBudget:     botConfig.ThinkingBudget,
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

		// For assistant messages with tool use, add thinking block first if present
		// This is required by the Anthropic API when thinking is enabled
		if post.Role == llm.PostRoleBot && len(post.ToolUse) > 0 && post.Reasoning != "" {
			// Use the preserved signature from the original thinking block
			// The signature is an opaque verification field that must be passed back unmodified
			thinkingBlock := anthropicSDK.NewThinkingBlock(post.ReasoningSignature, post.Reasoning)
			currentBlocks = append(currentBlocks, thinkingBlock)
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

// streamResult holds the accumulated result from processing a stream
type streamResult struct {
	message          anthropicSDK.Message
	pendingToolCalls []llm.ToolCall
	err              error
}

// buildAPIParams constructs the Anthropic API parameters from the current state
func (a *Anthropic) buildAPIParams(state *messageState) anthropicSDK.MessageNewParams {
	params := anthropicSDK.MessageNewParams{
		Model:     anthropicSDK.Model(state.config.Model),
		MaxTokens: int64(state.config.MaxGeneratedTokens),
		Messages:  state.messages,
	}

	// Only add tools if not explicitly disabled
	if !state.config.ToolsDisabled {
		params.Tools = convertTools(state.tools)
	}

	// Only include system message if it's non-empty
	if state.system != "" {
		params.System = []anthropicSDK.TextBlockParam{{
			Text: state.system,
		}}
	}

	// Add native tools if not explicitly disabled
	if !state.config.ToolsDisabled && a.isNativeToolEnabled("web_search") {
		webSearchTool := anthropicSDK.WebSearchTool20250305Param{
			Name: "web_search",
			Type: "web_search_20250305",
		}
		params.Tools = append(params.Tools, anthropicSDK.ToolUnionParam{
			OfWebSearchTool20250305: &webSearchTool,
		})
	}

	// Enable thinking/reasoning for models that support it (unless explicitly disabled)
	if !state.config.ReasoningDisabled {
		if thinkingConfig, ok := a.calculateThinkingConfig(state.config.MaxGeneratedTokens); ok {
			params.Thinking = thinkingConfig
		}
	}

	return params
}

// processStream handles the streaming loop and returns the accumulated result
func (a *Anthropic) processStream(state *messageState, params anthropicSDK.MessageNewParams) streamResult {
	stream := a.client.Messages.NewStreaming(context.Background(), params)

	message := anthropicSDK.Message{}
	var thinkingBuffer strings.Builder
	var signatureBuffer strings.Builder
	var currentBlockIsThinking bool

	for stream.Next() {
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			return streamResult{err: fmt.Errorf("error accumulating message: %w", err)}
		}

		a.handleStreamEvent(state, event, &thinkingBuffer, &signatureBuffer, &currentBlockIsThinking)
	}

	if err := stream.Err(); err != nil {
		return streamResult{err: fmt.Errorf("error from anthropic stream: %w", err)}
	}

	// Flush any remaining thinking content
	if thinkingBuffer.Len() > 0 {
		state.output <- llm.TextStreamEvent{
			Type: llm.EventTypeReasoningEnd,
			Value: llm.ReasoningData{
				Text:      thinkingBuffer.String(),
				Signature: signatureBuffer.String(),
			},
		}
	}

	// Extract pending tool calls from the message
	pendingToolCalls := extractToolCalls(message)

	return streamResult{
		message:          message,
		pendingToolCalls: pendingToolCalls,
	}
}

// handleStreamEvent processes individual stream events and emits appropriate output events
func (a *Anthropic) handleStreamEvent(
	state *messageState,
	event anthropicSDK.MessageStreamEventUnion,
	thinkingBuffer, signatureBuffer *strings.Builder,
	currentBlockIsThinking *bool,
) {
	switch eventVariant := event.AsAny().(type) { //nolint:gocritic
	case anthropicSDK.ContentBlockStartEvent:
		*currentBlockIsThinking = eventVariant.ContentBlock.Type == "thinking"

	case anthropicSDK.ContentBlockDeltaEvent:
		switch deltaVariant := eventVariant.Delta.AsAny().(type) { //nolint:gocritic
		case anthropicSDK.TextDelta:
			state.output <- llm.TextStreamEvent{
				Type:  llm.EventTypeText,
				Value: deltaVariant.Text,
			}
		case anthropicSDK.ThinkingDelta:
			thinkingBuffer.WriteString(deltaVariant.Thinking)
			state.output <- llm.TextStreamEvent{
				Type:  llm.EventTypeReasoning,
				Value: deltaVariant.Thinking,
			}
		case anthropicSDK.SignatureDelta:
			signatureBuffer.WriteString(deltaVariant.Signature)
		}

	case anthropicSDK.ContentBlockStopEvent:
		if *currentBlockIsThinking && thinkingBuffer.Len() > 0 {
			state.output <- llm.TextStreamEvent{
				Type: llm.EventTypeReasoningEnd,
				Value: llm.ReasoningData{
					Text:      thinkingBuffer.String(),
					Signature: signatureBuffer.String(),
				},
			}
			thinkingBuffer.Reset()
			signatureBuffer.Reset()
			*currentBlockIsThinking = false
		}
	}
}

// extractToolCalls extracts tool call information from the message content
func extractToolCalls(message anthropicSDK.Message) []llm.ToolCall {
	toolCalls := make([]llm.ToolCall, 0, len(message.Content))
	for _, block := range message.Content {
		if block.Type == "tool_use" {
			toolCalls = append(toolCalls, llm.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}
	return toolCalls
}

// buildAssistantMessage converts the message content blocks to MessageParam format
func buildAssistantMessage(message anthropicSDK.Message) anthropicSDK.MessageParam {
	assistantContent := make([]anthropicSDK.ContentBlockParamUnion, 0, len(message.Content))
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			if textBlock, ok := block.AsAny().(anthropicSDK.TextBlock); ok {
				assistantContent = append(assistantContent, anthropicSDK.NewTextBlock(textBlock.Text))
			}
		case "tool_use":
			if toolBlock, ok := block.AsAny().(anthropicSDK.ToolUseBlock); ok {
				assistantContent = append(assistantContent, anthropicSDK.NewToolUseBlock(toolBlock.ID, toolBlock.Input, toolBlock.Name))
			}
		case "thinking":
			if thinkingBlock, ok := block.AsAny().(anthropicSDK.ThinkingBlock); ok {
				assistantContent = append(assistantContent, anthropicSDK.NewThinkingBlock(thinkingBlock.Signature, thinkingBlock.Thinking))
			}
		}
	}
	return anthropicSDK.MessageParam{
		Role:    anthropicSDK.MessageParamRoleAssistant,
		Content: assistantContent,
	}
}

// buildToolResultsMessage creates a user message containing tool execution results
func buildToolResultsMessage(results []llm.AutoRunResult) anthropicSDK.MessageParam {
	toolResults := make([]anthropicSDK.ContentBlockParamUnion, 0, len(results))
	for _, result := range results {
		toolResults = append(toolResults, anthropicSDK.NewToolResultBlock(result.ToolCallID, result.Result, result.IsError))
	}
	return anthropicSDK.MessageParam{
		Role:    anthropicSDK.MessageParamRoleUser,
		Content: toolResults,
	}
}

// emitPostStreamEvents sends annotations and usage events after stream processing
func (a *Anthropic) emitPostStreamEvents(state *messageState, message anthropicSDK.Message) {
	// Extract and emit annotations/citations
	if annotations := a.extractAnnotations(message); len(annotations) > 0 {
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeAnnotations,
			Value: annotations,
		}
	}

	// Emit token usage data
	state.output <- llm.TextStreamEvent{
		Type: llm.EventTypeUsage,
		Value: llm.TokenUsage{
			InputTokens:  message.Usage.InputTokens,
			OutputTokens: message.Usage.OutputTokens,
		},
	}
}

func (a *Anthropic) streamChatWithTools(initialState messageState) {
	state := initialState

	for {
		if state.depth >= MaxToolResolutionDepth {
			state.output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: fmt.Errorf("max tool resolution depth (%d) exceeded", MaxToolResolutionDepth),
			}
			return
		}

		params := a.buildAPIParams(&state)
		result := a.processStream(&state, params)

		if result.err != nil {
			state.output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: result.err,
			}
			return
		}

		// Handle tool calls if present
		if len(result.pendingToolCalls) > 0 {
			if llm.ShouldAutoRunTools(result.pendingToolCalls, state.config.AutoRunTools) {
				// Auto-run tools: execute and continue the loop
				state.messages = append(state.messages, buildAssistantMessage(result.message))

				toolResults := llm.ExecuteAutoRunTools(
					result.pendingToolCalls,
					state.config.AutoRunTools,
					state.resolver,
					state.context,
				)
				state.messages = append(state.messages, buildToolResultsMessage(toolResults))

				a.emitPostStreamEvents(&state, result.message)
				state.depth++
				continue
			}

			// Manual approval needed - emit tool calls event
			state.output <- llm.TextStreamEvent{
				Type:  llm.EventTypeToolCalls,
				Value: result.pendingToolCalls,
			}
		}

		a.emitPostStreamEvents(&state, result.message)

		// Send end event
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeEnd,
			Value: nil,
		}
		return
	}
}

// extractAnnotations extracts citations from Anthropic's message content blocks
func (a *Anthropic) extractAnnotations(message anthropicSDK.Message) []llm.Annotation {
	var annotations []llm.Annotation

	// Track text position as we build the complete message
	type textBlockInfo struct {
		startPos  int
		endPos    int
		text      string
		citations []anthropicSDK.TextCitationUnion
	}
	var textBlocks []textBlockInfo
	var completeText strings.Builder

	// First pass: build complete text and track block positions
	for _, block := range message.Content {
		if block.Type == "text" {
			blockVariant := block.AsAny()
			if textBlock, ok := blockVariant.(anthropicSDK.TextBlock); ok {
				startPos := completeText.Len()
				completeText.WriteString(textBlock.Text)
				endPos := completeText.Len()

				textBlocks = append(textBlocks, textBlockInfo{
					startPos:  startPos,
					endPos:    endPos,
					text:      textBlock.Text,
					citations: textBlock.Citations,
				})
			}
		}
	}

	citationIndex := 1

	// Second pass: extract citations from text blocks
	for _, blockInfo := range textBlocks {
		if len(blockInfo.citations) > 0 {
			for _, citation := range blockInfo.citations {
				citationVariant := citation.AsAny()
				if webSearchCitation, ok := citationVariant.(anthropicSDK.CitationsWebSearchResultLocation); ok {
					// Annotate the entire text block that contains the citation
					// This is appropriate since citations in Anthropic are associated with text blocks
					annotations = append(annotations, llm.Annotation{
						Type:       llm.AnnotationTypeURLCitation,
						StartIndex: blockInfo.startPos,
						EndIndex:   blockInfo.endPos,
						URL:        webSearchCitation.URL,
						Title:      webSearchCitation.Title,
						CitedText:  webSearchCitation.CitedText,
						Index:      citationIndex,
					})
					citationIndex++
				}
			}
		}
	}

	return annotations
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
		// Convert schema to the format Anthropic expects
		inputSchema := anthropicSDK.ToolInputSchemaParam{}
		if schema, ok := tool.Schema.(map[string]interface{}); ok {
			if props, ok := schema["properties"].(map[string]interface{}); ok {
				inputSchema.Properties = props
			}
		} else if schema, ok := tool.Schema.(*jsonschema.Schema); ok {
			inputSchema.Properties = schema.Properties
		}

		converted[i] = anthropicSDK.ToolUnionParam{
			OfTool: &anthropicSDK.ToolParam{
				Name:        tool.Name,
				Description: anthropicSDK.String(tool.Description),
				InputSchema: inputSchema,
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

// calculateThinkingConfig calculates the thinking configuration based on bot config and max tokens.
// Returns the thinking config and a boolean indicating whether thinking should be enabled.
func (a *Anthropic) calculateThinkingConfig(maxGeneratedTokens int) (anthropicSDK.ThinkingConfigParamUnion, bool) {
	// Check if reasoning is enabled for this bot
	if !a.reasoningEnabled {
		return anthropicSDK.ThinkingConfigParamUnion{}, false
	}

	// Calculate thinking budget
	var thinkingBudget int64
	if a.thinkingBudget > 0 {
		// Use configured budget
		thinkingBudget = int64(a.thinkingBudget)
	} else {
		// Use default: 1/4 of max tokens, capped at 8192
		thinkingBudget = int64(maxGeneratedTokens / 4)
		if thinkingBudget > 8192 {
			thinkingBudget = 8192
		}
	}

	// Ensure minimum budget of 1024 tokens
	if thinkingBudget < 1024 {
		thinkingBudget = 1024
	}

	// Anthropic requires a minimum thinking budget of 1024 tokens
	// If the thinking budget is more than the max_tokens, Anthropic will return an error.
	if thinkingBudget >= int64(maxGeneratedTokens) {
		return anthropicSDK.ThinkingConfigParamUnion{}, false
	}

	config := anthropicSDK.ThinkingConfigParamUnion{
		OfEnabled: &anthropicSDK.ThinkingConfigEnabledParam{
			Type:         "enabled",
			BudgetTokens: thinkingBudget,
		},
	}

	return config, true
}

// FetchModels retrieves the list of available models from the Anthropic API
func FetchModels(apiKey string, httpClient *http.Client) ([]llm.ModelInfo, error) {
	client := anthropicSDK.NewClient(
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(httpClient),
	)

	// Use AutoPaging to automatically handle pagination
	autoPager := client.Models.ListAutoPaging(context.Background(), anthropicSDK.ModelListParams{})

	var models []llm.ModelInfo

	// Iterate through all pages
	for autoPager.Next() {
		model := autoPager.Current()
		models = append(models, llm.ModelInfo{
			ID:          model.ID,
			DisplayName: model.DisplayName,
		})
	}

	// Check if there was an error during iteration
	if err := autoPager.Err(); err != nil {
		return nil, err
	}

	return models, nil
}
