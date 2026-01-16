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

func isValidImageType(mimeType string) bool {
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// conversationToMessages creates a system prompt and a slice of input messages from conversation posts.
func conversationToMessages(posts []llm.Post) (string, []anthropicSDK.MessageParam) {
	var systemMessage string
	var messages []anthropicSDK.MessageParam
	var currentBlocks []anthropicSDK.ContentBlockParamUnion
	var currentRole anthropicSDK.MessageParamRole

	flushCurrentMessage := func() {
		if len(currentBlocks) == 0 {
			return
		}
		messages = append(messages, anthropicSDK.MessageParam{
			Role:    currentRole,
			Content: currentBlocks,
		})
		currentBlocks = nil
	}

	for _, post := range posts {
		newRole := postRoleToAnthropicRole(post.Role)
		if newRole == "" {
			if post.Role == llm.PostRoleSystem {
				systemMessage += post.Message
			}
			continue
		}

		if currentRole != newRole {
			flushCurrentMessage()
			currentRole = newRole
		}

		// Add thinking block first for assistant messages with tool use (required by Anthropic API)
		if post.Role == llm.PostRoleBot && len(post.ToolUse) > 0 && post.Reasoning != "" {
			currentBlocks = append(currentBlocks, anthropicSDK.NewThinkingBlock(post.ReasoningSignature, post.Reasoning))
		}

		if post.Message != "" {
			currentBlocks = append(currentBlocks, anthropicSDK.NewTextBlock(post.Message))
		}

		currentBlocks = append(currentBlocks, convertFilesToBlocks(post.Files)...)

		if len(post.ToolUse) > 0 {
			currentBlocks = append(currentBlocks, convertToolUseToBlocks(post.ToolUse)...)

			// Tool results must be in a separate user message
			flushCurrentMessage()
			currentRole = anthropicSDK.MessageParamRoleUser
			currentBlocks = convertToolResultsToBlocks(post.ToolUse)
			flushCurrentMessage()
		}
	}

	flushCurrentMessage()
	return systemMessage, messages
}

func postRoleToAnthropicRole(role llm.PostRole) anthropicSDK.MessageParamRole {
	switch role {
	case llm.PostRoleBot:
		return anthropicSDK.MessageParamRoleAssistant
	case llm.PostRoleUser:
		return anthropicSDK.MessageParamRoleUser
	default:
		return ""
	}
}

func convertFilesToBlocks(files []llm.File) []anthropicSDK.ContentBlockParamUnion {
	var blocks []anthropicSDK.ContentBlockParamUnion
	for _, file := range files {
		if !isValidImageType(file.MimeType) {
			blocks = append(blocks, anthropicSDK.NewTextBlock(fmt.Sprintf("[Unsupported image type: %s]", file.MimeType)))
			continue
		}

		data, err := io.ReadAll(file.Reader)
		if err != nil {
			blocks = append(blocks, anthropicSDK.NewTextBlock("[Error reading image data]"))
			continue
		}

		blocks = append(blocks, anthropicSDK.NewImageBlockBase64(file.MimeType, base64.StdEncoding.EncodeToString(data)))
	}
	return blocks
}

func convertToolUseToBlocks(toolCalls []llm.ToolCall) []anthropicSDK.ContentBlockParamUnion {
	blocks := make([]anthropicSDK.ContentBlockParamUnion, len(toolCalls))
	for i, tool := range toolCalls {
		blocks[i] = anthropicSDK.NewToolUseBlock(tool.ID, tool.Arguments, tool.Name)
	}
	return blocks
}

func convertToolResultsToBlocks(toolCalls []llm.ToolCall) []anthropicSDK.ContentBlockParamUnion {
	blocks := make([]anthropicSDK.ContentBlockParamUnion, len(toolCalls))
	for i, tool := range toolCalls {
		blocks[i] = anthropicSDK.NewToolResultBlock(tool.ID, tool.Result, tool.Status != llm.ToolCallStatusSuccess)
	}
	return blocks
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

func (a *Anthropic) buildAPIParams(state *messageState) anthropicSDK.MessageNewParams {
	params := anthropicSDK.MessageNewParams{
		Model:     anthropicSDK.Model(state.config.Model),
		MaxTokens: int64(state.config.MaxGeneratedTokens),
		Messages:  state.messages,
	}

	if !state.config.ToolsDisabled {
		params.Tools = convertTools(state.tools)

		if a.isNativeToolEnabled("web_search") {
			params.Tools = append(params.Tools, anthropicSDK.ToolUnionParam{
				OfWebSearchTool20250305: &anthropicSDK.WebSearchTool20250305Param{
					Name: "web_search",
					Type: "web_search_20250305",
				},
			})
		}
	}

	if state.system != "" {
		params.System = []anthropicSDK.TextBlockParam{{Text: state.system}}
	}

	if !state.config.ReasoningDisabled {
		if thinkingConfig, ok := a.calculateThinkingConfig(state.config.MaxGeneratedTokens); ok {
			params.Thinking = thinkingConfig
		}
	}

	return params
}

func (a *Anthropic) processStream(state *messageState, params anthropicSDK.MessageNewParams) streamResult {
	stream := a.client.Messages.NewStreaming(context.Background(), params)

	var message anthropicSDK.Message
	var thinkingBuffer, signatureBuffer strings.Builder
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

	if thinkingBuffer.Len() > 0 {
		state.output <- llm.TextStreamEvent{
			Type: llm.EventTypeReasoningEnd,
			Value: llm.ReasoningData{
				Text:      thinkingBuffer.String(),
				Signature: signatureBuffer.String(),
			},
		}
	}

	return streamResult{
		message:          message,
		pendingToolCalls: extractToolCalls(message),
	}
}

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

func extractToolCalls(message anthropicSDK.Message) []llm.ToolCall {
	var toolCalls []llm.ToolCall
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

func buildAssistantMessage(message anthropicSDK.Message) anthropicSDK.MessageParam {
	content := make([]anthropicSDK.ContentBlockParamUnion, 0, len(message.Content))

	for _, block := range message.Content {
		if converted := convertContentBlock(block); converted != nil {
			content = append(content, *converted)
		}
	}

	return anthropicSDK.MessageParam{
		Role:    anthropicSDK.MessageParamRoleAssistant,
		Content: content,
	}
}

func convertContentBlock(block anthropicSDK.ContentBlockUnion) *anthropicSDK.ContentBlockParamUnion {
	switch block.Type {
	case "text":
		if textBlock, ok := block.AsAny().(anthropicSDK.TextBlock); ok {
			result := anthropicSDK.NewTextBlock(textBlock.Text)
			return &result
		}
	case "tool_use":
		if toolBlock, ok := block.AsAny().(anthropicSDK.ToolUseBlock); ok {
			result := anthropicSDK.NewToolUseBlock(toolBlock.ID, toolBlock.Input, toolBlock.Name)
			return &result
		}
	case "thinking":
		if thinkingBlock, ok := block.AsAny().(anthropicSDK.ThinkingBlock); ok {
			result := anthropicSDK.NewThinkingBlock(thinkingBlock.Signature, thinkingBlock.Thinking)
			return &result
		}
	}
	return nil
}

func buildToolResultsMessage(results []llm.AutoRunResult) anthropicSDK.MessageParam {
	toolResults := make([]anthropicSDK.ContentBlockParamUnion, len(results))
	for i, result := range results {
		toolResults[i] = anthropicSDK.NewToolResultBlock(result.ToolCallID, result.Result, result.IsError)
	}
	return anthropicSDK.MessageParam{
		Role:    anthropicSDK.MessageParamRoleUser,
		Content: toolResults,
	}
}

func (a *Anthropic) emitPostStreamEvents(state *messageState, message anthropicSDK.Message) {
	if annotations := a.extractAnnotations(message); len(annotations) > 0 {
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeAnnotations,
			Value: annotations,
		}
	}

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

	for state.depth < MaxToolResolutionDepth {
		result := a.processStream(&state, a.buildAPIParams(&state))

		if result.err != nil {
			state.output <- llm.TextStreamEvent{Type: llm.EventTypeError, Value: result.err}
			return
		}

		if len(result.pendingToolCalls) > 0 && llm.ShouldAutoRunTools(result.pendingToolCalls, state.config.AutoRunTools) {
			state.messages = append(state.messages, buildAssistantMessage(result.message))

			toolResults := llm.ExecuteAutoRunTools(
				result.pendingToolCalls,
				state.resolver,
				state.context,
			)
			state.messages = append(state.messages, buildToolResultsMessage(toolResults))

			a.emitPostStreamEvents(&state, result.message)
			state.depth++
			continue
		}

		if len(result.pendingToolCalls) > 0 {
			state.output <- llm.TextStreamEvent{
				Type:  llm.EventTypeToolCalls,
				Value: result.pendingToolCalls,
			}
		}

		a.emitPostStreamEvents(&state, result.message)
		state.output <- llm.TextStreamEvent{Type: llm.EventTypeEnd, Value: nil}
		return
	}

	state.output <- llm.TextStreamEvent{
		Type:  llm.EventTypeError,
		Value: fmt.Errorf("max tool resolution depth (%d) exceeded", MaxToolResolutionDepth),
	}
}

func (a *Anthropic) extractAnnotations(message anthropicSDK.Message) []llm.Annotation {
	var annotations []llm.Annotation
	textPosition := 0
	citationIndex := 1

	for _, block := range message.Content {
		if block.Type != "text" {
			continue
		}

		textBlock, ok := block.AsAny().(anthropicSDK.TextBlock)
		if !ok {
			continue
		}

		startPos := textPosition
		endPos := textPosition + len(textBlock.Text)
		textPosition = endPos

		for _, citation := range textBlock.Citations {
			webSearchCitation, ok := citation.AsAny().(anthropicSDK.CitationsWebSearchResultLocation)
			if !ok {
				continue
			}

			annotations = append(annotations, llm.Annotation{
				Type:       llm.AnnotationTypeURLCitation,
				StartIndex: startPos,
				EndIndex:   endPos,
				URL:        webSearchCitation.URL,
				Title:      webSearchCitation.Title,
				CitedText:  webSearchCitation.CitedText,
				Index:      citationIndex,
			})
			citationIndex++
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

func convertTools(tools []llm.Tool) []anthropicSDK.ToolUnionParam {
	converted := make([]anthropicSDK.ToolUnionParam, len(tools))
	for i, tool := range tools {
		converted[i] = anthropicSDK.ToolUnionParam{
			OfTool: &anthropicSDK.ToolParam{
				Name:        tool.Name,
				Description: anthropicSDK.String(tool.Description),
				InputSchema: extractInputSchema(tool.Schema),
			},
		}
	}
	return converted
}

func extractInputSchema(schema interface{}) anthropicSDK.ToolInputSchemaParam {
	switch s := schema.(type) {
	case map[string]interface{}:
		if props, ok := s["properties"].(map[string]interface{}); ok {
			return anthropicSDK.ToolInputSchemaParam{Properties: props}
		}
	case *jsonschema.Schema:
		return anthropicSDK.ToolInputSchemaParam{Properties: s.Properties}
	}
	return anthropicSDK.ToolInputSchemaParam{}
}

func (a *Anthropic) InputTokenLimit() int {
	if a.inputTokenLimit > 0 {
		return a.inputTokenLimit
	}
	return 100000
}

func (a *Anthropic) isNativeToolEnabled(toolName string) bool {
	for _, enabledTool := range a.enabledNativeTools {
		if enabledTool == toolName {
			return true
		}
	}
	return false
}

// calculateThinkingConfig returns the thinking configuration if reasoning is enabled and valid.
func (a *Anthropic) calculateThinkingConfig(maxGeneratedTokens int) (anthropicSDK.ThinkingConfigParamUnion, bool) {
	if !a.reasoningEnabled {
		return anthropicSDK.ThinkingConfigParamUnion{}, false
	}

	budget := a.calculateThinkingBudget(maxGeneratedTokens)

	// Anthropic requires thinking budget to be less than max_tokens
	if budget >= int64(maxGeneratedTokens) {
		return anthropicSDK.ThinkingConfigParamUnion{}, false
	}

	return anthropicSDK.ThinkingConfigParamUnion{
		OfEnabled: &anthropicSDK.ThinkingConfigEnabledParam{
			Type:         "enabled",
			BudgetTokens: budget,
		},
	}, true
}

func (a *Anthropic) calculateThinkingBudget(maxGeneratedTokens int) int64 {
	const minBudget, maxBudget = 1024, 8192

	if a.thinkingBudget > 0 {
		return max(int64(a.thinkingBudget), minBudget)
	}

	budget := int64(maxGeneratedTokens / 4)
	return max(min(budget, maxBudget), minBudget)
}

func FetchModels(apiKey string, httpClient *http.Client) ([]llm.ModelInfo, error) {
	client := anthropicSDK.NewClient(
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(httpClient),
	)

	autoPager := client.Models.ListAutoPaging(context.Background(), anthropicSDK.ModelListParams{})

	var models []llm.ModelInfo
	for autoPager.Next() {
		model := autoPager.Current()
		models = append(models, llm.ModelInfo{
			ID:          model.ID,
			DisplayName: model.DisplayName,
		})
	}

	if err := autoPager.Err(); err != nil {
		return nil, err
	}

	return models, nil
}
