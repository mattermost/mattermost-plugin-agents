// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/subtitles"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/azure"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/packages/param"
	"github.com/openai/openai-go/v2/packages/ssestream"
	"github.com/openai/openai-go/v2/responses"
	"github.com/openai/openai-go/v2/shared"
)

type Config struct {
	APIKey               string        `json:"apiKey"`
	APIURL               string        `json:"apiURL"`
	OrgID                string        `json:"orgID"`
	DefaultModel         string        `json:"defaultModel"`
	InputTokenLimit      int           `json:"inputTokenLimit"`
	OutputTokenLimit     int           `json:"outputTokenLimit"`
	StreamingTimeout     time.Duration `json:"streamingTimeout"`
	SendUserID           bool          `json:"sendUserID"`
	EmbeddingModel       string        `json:"embeddingModel"`
	EmbeddingDimensions  int           `json:"embeddingDimensions"`
	UseResponsesAPI      bool          `json:"useResponsesAPI"`
	EnabledNativeTools   []string      `json:"enabledNativeTools"`
	ReasoningEnabled     bool          `json:"reasoningEnabled"`
	ReasoningEffort      string        `json:"reasoningEffort"`
	DisableStreamOptions bool          `json:"disableStreamOptions"` // For OpenAI-compatible APIs that don't support stream_options
	UseMaxTokens         bool          `json:"useMaxTokens"`         // Use max_tokens instead of max_completion_tokens for compatible APIs
}

type OpenAI struct {
	client openai.Client
	config Config
}

const (
	MaxFunctionCalls   = 10
	OpenAIMaxImageSize = 20 * 1024 * 1024 // 20 MB
)

var ErrStreamingTimeout = errors.New("timeout streaming")

func NewAzure(config Config, httpClient *http.Client) *OpenAI {
	opts := []option.RequestOption{
		azure.WithEndpoint(strings.TrimSuffix(config.APIURL, "/"), "2025-04-01-preview"),
		azure.WithAPIKey(config.APIKey),
		option.WithHTTPClient(httpClient),
	}

	client := openai.NewClient(opts...)

	return &OpenAI{
		client: client,
		config: config,
	}
}

func NewCompatible(config Config, httpClient *http.Client) *OpenAI {
	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithHTTPClient(httpClient),
		option.WithBaseURL(strings.TrimSuffix(config.APIURL, "/")),
	}

	client := openai.NewClient(opts...)

	return &OpenAI{
		client: client,
		config: config,
	}
}

func New(config Config, httpClient *http.Client) *OpenAI {
	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithHTTPClient(httpClient),
	}

	if config.OrgID != "" {
		opts = append(opts, option.WithOrganization(config.OrgID))
	}

	client := openai.NewClient(opts...)

	return &OpenAI{
		client: client,
		config: config,
	}
}

// NewEmbeddings creates a new OpenAI client configured only for embeddings functionality
func NewEmbeddings(config Config, httpClient *http.Client) *OpenAI {
	if config.EmbeddingModel == "" {
		config.EmbeddingModel = openai.EmbeddingModelTextEmbedding3Large
		config.EmbeddingDimensions = 3072
	}

	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithHTTPClient(httpClient),
	}

	client := openai.NewClient(opts...)

	return &OpenAI{
		client: client,
		config: config,
	}
}

// NewCompatibleEmbeddings creates a new OpenAI client configured only for embeddings functionality
func NewCompatibleEmbeddings(config Config, httpClient *http.Client) *OpenAI {
	if config.EmbeddingModel == "" {
		config.EmbeddingModel = openai.EmbeddingModelTextEmbedding3Large
		config.EmbeddingDimensions = 3072
	}

	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithHTTPClient(httpClient),
		option.WithBaseURL(strings.TrimSuffix(config.APIURL, "/")),
	}

	client := openai.NewClient(opts...)

	return &OpenAI{
		client: client,
		config: config,
	}
}

func modifyCompletionRequestWithRequest(params openai.ChatCompletionNewParams, internalRequest llm.CompletionRequest, cfg llm.LanguageModelConfig) openai.ChatCompletionNewParams {
	params.Messages = postsToChatCompletionMessages(internalRequest.Posts)
	// Only add tools if not explicitly disabled
	if !cfg.ToolsDisabled && internalRequest.Context.Tools != nil {
		params.Tools = toolsToOpenAITools(internalRequest.Context.Tools.GetTools())
	}

	return params
}

// schemaToFunctionParameters converts a jsonschema.Schema to shared.FunctionParameters
func schemaToFunctionParameters(schema any) shared.FunctionParameters {
	// Default schema that satisfies OpenAI's requirements
	defaultSchema := shared.FunctionParameters{
		"type":       "object",
		"properties": map[string]any{},
	}

	if schema == nil {
		return defaultSchema
	}

	// If it's already a map, use it directly
	if schemaMap, ok := schema.(map[string]interface{}); ok {
		result := schemaMap
		// Ensure the result has the required fields for OpenAI
		if _, hasType := result["type"]; !hasType {
			result["type"] = "object"
		}
		if _, hasProps := result["properties"]; !hasProps {
			result["properties"] = map[string]any{}
		}
		return result
	}

	// Convert the schema to a map by marshaling and unmarshaling
	data, err := json.Marshal(schema)
	if err != nil {
		return defaultSchema
	}

	var result shared.FunctionParameters
	if err := json.Unmarshal(data, &result); err != nil {
		return defaultSchema
	}

	// Ensure the result has the required fields for OpenAI
	// OpenAI requires "type" and "properties" to be present, even if properties is empty
	// This is because OpenAI's FunctionDefinitionParam has `omitzero` on the `Parameters` field
	if result == nil {
		return defaultSchema
	}
	if _, hasType := result["type"]; !hasType {
		result["type"] = "object"
	}
	if _, hasProps := result["properties"]; !hasProps {
		result["properties"] = map[string]any{}
	}

	return result
}

func toolsToOpenAITools(tools []llm.Tool) []openai.ChatCompletionToolUnionParam {
	result := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		result = append(result, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        tool.Name,
			Description: openai.String(tool.Description),
			Parameters:  schemaToFunctionParameters(tool.Schema),
		}))
	}

	return result
}

func postsToChatCompletionMessages(posts []llm.Post) []openai.ChatCompletionMessageParamUnion {
	result := make([]openai.ChatCompletionMessageParamUnion, 0, len(posts))

	for _, post := range posts {
		switch post.Role {
		case llm.PostRoleSystem:
			result = append(result, openai.SystemMessage(post.Message))
		case llm.PostRoleBot:
			// Assistant message - if it has tool calls, we need to construct it differently
			if len(post.ToolUse) > 0 {
				// For messages with tool calls, we need to build it manually
				toolCalls := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(post.ToolUse))
				for _, tool := range post.ToolUse {
					// Create function tool call
					toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: tool.ID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tool.Name,
								Arguments: string(tool.Arguments),
							},
						},
					})
				}

				// Create assistant message with tool calls
				msgParam := openai.ChatCompletionAssistantMessageParam{}

				// Only set content if it's not empty
				if post.Message != "" {
					msgParam.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: openai.String(post.Message),
					}
				}

				msgParam.ToolCalls = toolCalls

				result = append(result, openai.ChatCompletionMessageParamUnion{
					OfAssistant: &msgParam,
				})

				// Add tool results as separate messages
				for _, tool := range post.ToolUse {
					result = append(result, openai.ToolMessage(tool.Result, tool.ID))
				}
			} else {
				// Simple assistant message
				result = append(result, openai.AssistantMessage(post.Message))
			}
		case llm.PostRoleUser:
			// User message
			if len(post.Files) > 0 {
				// Create multipart content for images
				parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(post.Files)+1)

				if post.Message != "" {
					parts = append(parts, openai.TextContentPart(post.Message))
				}

				for _, file := range post.Files {
					if file.MimeType != "image/png" &&
						file.MimeType != "image/jpeg" &&
						file.MimeType != "image/gif" &&
						file.MimeType != "image/webp" {
						parts = append(parts, openai.TextContentPart("User submitted image was not a supported format. Tell the user this."))
						continue
					}
					if file.Size > OpenAIMaxImageSize {
						parts = append(parts, openai.TextContentPart("User submitted an image larger than 20MB. Tell the user this."))
						continue
					}
					fileBytes, err := io.ReadAll(file.Reader)
					if err != nil {
						continue
					}
					imageEncoded := base64.StdEncoding.EncodeToString(fileBytes)
					encodedString := fmt.Sprintf("data:"+file.MimeType+";base64,%s", imageEncoded)
					parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
						URL:    encodedString,
						Detail: "auto",
					}))
				}

				// Create a user message with multipart content
				result = append(result, openai.UserMessage(parts))
			} else {
				result = append(result, openai.UserMessage(post.Message))
			}
		}
	}

	return result
}

type ToolBufferElement struct {
	id   strings.Builder
	name strings.Builder
	args strings.Builder
}

// collectToolCalls converts buffered tool elements to llm.ToolCall slice
func collectToolCalls(buffer map[int]*ToolBufferElement) []llm.ToolCall {
	result := make([]llm.ToolCall, 0, len(buffer))
	for _, tool := range buffer {
		if tool == nil {
			continue
		}
		name := tool.name.String()
		if name == "" {
			continue
		}
		result = append(result, llm.ToolCall{
			ID:        tool.id.String(),
			Name:      name,
			Arguments: []byte(tool.args.String()),
		})
	}
	return result
}

// buildToolCallsMessageParam creates OpenAI message params for tool calls
func buildToolCallsMessageParam(toolCalls []llm.ToolCall) openai.ChatCompletionMessageParamUnion {
	params := make([]openai.ChatCompletionMessageToolCallUnionParam, len(toolCalls))
	for i, tc := range toolCalls {
		params[i] = openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: tc.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			},
		}
	}
	return openai.ChatCompletionMessageParamUnion{
		OfAssistant: &openai.ChatCompletionAssistantMessageParam{
			ToolCalls: params,
		},
	}
}

// appendToolResultMessages adds tool execution results to the message history
func appendToolResultMessages(
	messages []openai.ChatCompletionMessageParamUnion,
	results []llm.AutoRunResult,
) []openai.ChatCompletionMessageParamUnion {
	for _, result := range results {
		messages = append(messages, openai.ToolMessage(result.Result, result.ToolCallID))
	}
	return messages
}

// handleAutoRunTools processes auto-run tools and updates the message history.
// Returns true if tools were auto-run and the loop should continue.
func (s *OpenAI) handleAutoRunTools(
	messages *[]openai.ChatCompletionMessageParamUnion,
	pendingToolCalls []llm.ToolCall,
	cfg llm.LanguageModelConfig,
	llmContext *llm.Context,
	output chan<- llm.TextStreamEvent,
) bool {
	if !llm.ShouldAutoRunTools(pendingToolCalls, cfg.AutoRunTools) {
		// Manual approval needed
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeToolCalls,
			Value: pendingToolCalls,
		}
		return false
	}

	// Check recursion depth
	numFunctionCalls := 0
	for i := len(*messages) - 1; i >= 0; i-- {
		if (*messages)[i].OfTool != nil {
			numFunctionCalls++
		} else {
			break
		}
	}
	if numFunctionCalls > MaxFunctionCalls {
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: errors.New("too many function calls"),
		}
		return false
	}

	// Add assistant message with tool calls
	*messages = append(*messages, buildToolCallsMessageParam(pendingToolCalls))

	// Execute tools and add results
	results := llm.ExecuteAutoRunTools(
		pendingToolCalls,
		llmContext.Tools.ResolveTool,
		llmContext,
	)
	*messages = appendToolResultMessages(*messages, results)

	return true
}

func (s *OpenAI) streamResultToChannels(params openai.ChatCompletionNewParams, llmContext *llm.Context, cfg llm.LanguageModelConfig, output chan<- llm.TextStreamEvent) {
	// Route to Responses API or Completions API based on configuration
	if s.config.UseResponsesAPI {
		s.streamResponsesAPIToChannels(params, llmContext, cfg, output)
	} else {
		s.streamCompletionsAPIToChannels(params, llmContext, cfg, output)
	}
}

// streamCompletionsAPIToChannels uses the original Completions API for streaming
func (s *OpenAI) streamCompletionsAPIToChannels(initialParams openai.ChatCompletionNewParams, llmContext *llm.Context, cfg llm.LanguageModelConfig, output chan<- llm.TextStreamEvent) {
	params := initialParams

	for {
		ctx, cancel := context.WithCancelCause(context.Background())

		watchdog, watchdogDone := s.startWatchdog(ctx, cancel)
		stream := s.client.Chat.Completions.NewStreaming(ctx, params)

		var toolsBuffer map[int]*ToolBufferElement
		shouldContinue := false

		for stream.Next() {
			chunk := stream.Current()
			watchdog <- struct{}{}

			// Emit usage data if available
			if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				output <- llm.TextStreamEvent{
					Type: llm.EventTypeUsage,
					Value: llm.TokenUsage{
						InputTokens:  chunk.Usage.PromptTokens,
						OutputTokens: chunk.Usage.CompletionTokens,
					},
				}
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			delta := choice.Delta

			// Buffer tool calls
			if len(delta.ToolCalls) > 0 {
				toolsBuffer = s.bufferToolCalls(toolsBuffer, delta.ToolCalls)
			}

			if delta.Content != "" {
				output <- llm.TextStreamEvent{
					Type:  llm.EventTypeText,
					Value: delta.Content,
				}
			}

			// Handle finish reasons
			switch choice.FinishReason {
			case "stop":
				continue
			case "tool_calls":
				pendingToolCalls := collectToolCalls(toolsBuffer)
				shouldContinue = s.handleAutoRunTools(&params.Messages, pendingToolCalls, cfg, llmContext, output)

				stream.Close()
				cancel(nil)
				<-watchdogDone

				if shouldContinue {
					break
				}
				return
			case "":
				// Not done yet
			default:
				stream.Close()
				cancel(nil)
				<-watchdogDone
				return
			}

			if shouldContinue {
				break
			}
		}

		if !shouldContinue {
			s.handleStreamEnd(ctx, stream, cancel, watchdogDone, output)
			return
		}
	}
}

// startWatchdog creates and starts a watchdog goroutine that cancels the context on timeout
func (s *OpenAI) startWatchdog(ctx context.Context, cancel context.CancelCauseFunc) (chan<- struct{}, <-chan struct{}) {
	watchdog := make(chan struct{})
	watchdogDone := make(chan struct{})

	go func() {
		defer close(watchdogDone)
		timer := time.NewTimer(s.config.StreamingTimeout)
		defer timer.Stop()

		for {
			select {
			case <-timer.C:
				cancel(ErrStreamingTimeout)
				return
			case <-ctx.Done():
				return
			case <-watchdog:
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(s.config.StreamingTimeout)
			}
		}
	}()

	return watchdog, watchdogDone
}

// bufferToolCalls accumulates tool call data from streaming chunks
func (s *OpenAI) bufferToolCalls(buffer map[int]*ToolBufferElement, toolCalls []openai.ChatCompletionChunkChoiceDeltaToolCall) map[int]*ToolBufferElement {
	if buffer == nil {
		buffer = make(map[int]*ToolBufferElement)
	}

	for _, toolCall := range toolCalls {
		idx := int(toolCall.Index)
		if buffer[idx] == nil {
			buffer[idx] = &ToolBufferElement{}
		}

		if toolCall.ID != "" {
			buffer[idx].id.WriteString(toolCall.ID)
		}
		if toolCall.Function.Name != "" {
			buffer[idx].name.WriteString(toolCall.Function.Name)
		}
		if toolCall.Function.Arguments != "" {
			buffer[idx].args.WriteString(toolCall.Function.Arguments)
		}
	}

	return buffer
}

// handleStreamEnd handles stream cleanup and error reporting
func (s *OpenAI) handleStreamEnd(ctx context.Context, stream *ssestream.Stream[openai.ChatCompletionChunk], cancel context.CancelCauseFunc, watchdogDone <-chan struct{}, output chan<- llm.TextStreamEvent) {
	if err := stream.Err(); err != nil {
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: ctxErr,
			}
		} else {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: err,
			}
		}
	}

	stream.Close()
	cancel(nil)
	<-watchdogDone

	output <- llm.TextStreamEvent{
		Type:  llm.EventTypeEnd,
		Value: nil,
	}
}

// responsesStreamState holds state accumulated during Responses API streaming
type responsesStreamState struct {
	toolsBuffer            map[int]*ToolBufferElement
	currentToolIndex       int
	reasoningSummaryBuffer strings.Builder
	reasoningComplete      bool
	annotations            []llm.Annotation
	fullMessageText        strings.Builder
}

// ensureToolBuffer initializes the tools buffer if needed and returns the element at the given index
func (s *responsesStreamState) ensureToolBuffer(idx int) *ToolBufferElement {
	if s.toolsBuffer == nil {
		s.toolsBuffer = make(map[int]*ToolBufferElement)
	}
	if s.toolsBuffer[idx] == nil {
		s.toolsBuffer[idx] = &ToolBufferElement{}
	}
	return s.toolsBuffer[idx]
}

// streamResponsesAPIToChannels uses the new Responses API for streaming
func (s *OpenAI) streamResponsesAPIToChannels(initialParams openai.ChatCompletionNewParams, llmContext *llm.Context, cfg llm.LanguageModelConfig, output chan<- llm.TextStreamEvent) {
	params := initialParams

	for {
		ctx, cancel := context.WithCancelCause(context.Background())
		watchdog, watchdogDone := s.startWatchdog(ctx, cancel)

		responseParams := s.convertToResponseParams(params, llmContext, cfg)
		stream := s.client.Responses.NewStreaming(ctx, responseParams)

		state := &responsesStreamState{}
		shouldContinue := false

		for stream.Next() {
			event := stream.Current()
			watchdog <- struct{}{}

			action := s.handleResponsesEvent(event, state, &params, cfg, llmContext, output)

			switch action {
			case responsesActionContinue:
				continue
			case responsesActionBreakLoop:
				shouldContinue = true
			case responsesActionReturn:
				stream.Close()
				cancel(nil)
				<-watchdogDone
				return
			case responsesActionBreakAndReturn:
				stream.Close()
				cancel(nil)
				<-watchdogDone

				if shouldContinue {
					break
				}
				return
			}

			if shouldContinue {
				break
			}
		}

		if !shouldContinue {
			s.handleResponsesStreamEnd(ctx, stream, cancel, watchdogDone, output)
			return
		}
	}
}

type responsesAction int

const (
	responsesActionNone responsesAction = iota
	responsesActionContinue
	responsesActionBreakLoop
	responsesActionReturn
	responsesActionBreakAndReturn
)

// handleResponsesEvent processes a single Responses API event and returns the action to take
func (s *OpenAI) handleResponsesEvent(
	event responses.ResponseStreamEventUnion,
	state *responsesStreamState,
	params *openai.ChatCompletionNewParams,
	cfg llm.LanguageModelConfig,
	llmContext *llm.Context,
	output chan<- llm.TextStreamEvent,
) responsesAction {
	switch event.Type {
	// No-action events
	case "response.created", "response.in_progress",
		"response.web_search_call.searching", "response.web_search_call.in_progress", "response.web_search_call.completed",
		"response.content_part.added", "response.reasoning_summary_part.added",
		"response.reasoning_summary_text.done", "response.reasoning_summary_part.done":
		return responsesActionContinue

	case "response.output_text.delta":
		s.handleTextDelta(event, state, output)

	case "response.content_part.done":
		s.extractAnnotationsFromPart(event, state)

	case "response.function_call_arguments.delta":
		s.bufferResponsesToolArgs(event, state)

	case "response.output_item.added":
		s.handleOutputItemAdded(event, state)

	case "response.function_call_arguments.done":
		s.handleFunctionCallDone(event, state)

	case "response.output_item.done":
		s.handleOutputItemDone(event, state)

	case "response.reasoning_summary_text.delta":
		s.handleReasoningDelta(event, state, output)

	case "response.output_text.done":
		s.emitAnnotationsIfPresent(state, output)

	case "response.completed":
		return s.handleResponseCompleted(event, state, params, cfg, llmContext, output)

	case "response.incomplete":
		s.emitUsageIfPresent(event.Response.Usage, output)
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: errors.New("response incomplete: max tokens reached before completion"),
		}
		return responsesActionReturn

	case "error":
		s.handleResponseError(event, output)
		return responsesActionReturn
	}

	return responsesActionNone
}

// handleResponseCompleted handles the response.completed event
func (s *OpenAI) handleResponseCompleted(
	event responses.ResponseStreamEventUnion,
	state *responsesStreamState,
	params *openai.ChatCompletionNewParams,
	cfg llm.LanguageModelConfig,
	llmContext *llm.Context,
	output chan<- llm.TextStreamEvent,
) responsesAction {
	sendReasoningEnd := func() {
		if !state.reasoningComplete && state.reasoningSummaryBuffer.Len() > 0 {
			output <- llm.TextStreamEvent{
				Type: llm.EventTypeReasoningEnd,
				Value: llm.ReasoningData{
					Text: state.reasoningSummaryBuffer.String(),
				},
			}
		}
	}

	if len(state.annotations) > 0 {
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeAnnotations,
			Value: state.annotations,
		}
	}

	s.emitUsageIfPresent(event.Response.Usage, output)

	if len(state.toolsBuffer) > 0 {
		pendingToolCalls := collectToolCalls(state.toolsBuffer)

		if s.handleAutoRunTools(&params.Messages, pendingToolCalls, cfg, llmContext, output) {
			return responsesActionBreakLoop
		}

		// Manual approval path
		sendReasoningEnd()
		return responsesActionBreakAndReturn
	}

	// No tools - complete the response
	sendReasoningEnd()
	output <- llm.TextStreamEvent{
		Type:  llm.EventTypeEnd,
		Value: nil,
	}
	return responsesActionReturn
}

// extractAnnotationsFromPart extracts URL citations from a content part
func (s *OpenAI) extractAnnotationsFromPart(event responses.ResponseStreamEventUnion, state *responsesStreamState) {
	if event.Part.Type != "output_text" || len(event.Part.Annotations) == 0 {
		return
	}

	for _, ann := range event.Part.Annotations {
		if ann.Type == "url_citation" {
			state.annotations = append(state.annotations, llm.Annotation{
				Type:       llm.AnnotationTypeURLCitation,
				StartIndex: int(ann.StartIndex),
				EndIndex:   int(ann.EndIndex),
				URL:        ann.URL,
				Title:      ann.Title,
				Index:      len(state.annotations) + 1,
			})
		}
	}
}

// bufferResponsesToolArgs buffers function call arguments from Responses API
func (s *OpenAI) bufferResponsesToolArgs(event responses.ResponseStreamEventUnion, state *responsesStreamState) {
	idx := state.currentToolIndex
	if event.OutputIndex > 0 {
		idx = int(event.OutputIndex)
	}

	toolBuffer := state.ensureToolBuffer(idx)
	if event.Delta != "" {
		toolBuffer.args.WriteString(event.Delta)
	}
	state.currentToolIndex = idx
}

// handleOutputItemAdded handles new output items (including function calls)
func (s *OpenAI) handleOutputItemAdded(event responses.ResponseStreamEventUnion, state *responsesStreamState) {
	if event.Item.Type != "function_call" {
		return
	}

	state.currentToolIndex = int(event.OutputIndex)
	toolBuffer := state.ensureToolBuffer(state.currentToolIndex)

	if event.Item.CallID != "" {
		toolBuffer.id.WriteString(event.Item.CallID)
	} else if event.Item.ID != "" {
		toolBuffer.id.WriteString(event.Item.ID)
	}
	if event.Item.Name != "" {
		toolBuffer.name.WriteString(event.Item.Name)
	}
}

// handleOutputItemDone handles completed output items
func (s *OpenAI) handleOutputItemDone(event responses.ResponseStreamEventUnion, state *responsesStreamState) {
	if event.Item.Type != "function_call" || state.toolsBuffer[state.currentToolIndex] == nil {
		return
	}

	if event.Item.Name != "" && state.toolsBuffer[state.currentToolIndex].name.Len() == 0 {
		state.toolsBuffer[state.currentToolIndex].name.WriteString(event.Item.Name)
	}
	if event.Item.CallID != "" && state.toolsBuffer[state.currentToolIndex].id.Len() == 0 {
		state.toolsBuffer[state.currentToolIndex].id.WriteString(event.Item.CallID)
	}
}

// handleTextDelta handles text output deltas
func (s *OpenAI) handleTextDelta(event responses.ResponseStreamEventUnion, state *responsesStreamState, output chan<- llm.TextStreamEvent) {
	if event.Delta == "" {
		return
	}
	state.fullMessageText.WriteString(event.Delta)
	output <- llm.TextStreamEvent{
		Type:  llm.EventTypeText,
		Value: event.Delta,
	}
}

// handleReasoningDelta handles reasoning summary text deltas
func (s *OpenAI) handleReasoningDelta(event responses.ResponseStreamEventUnion, state *responsesStreamState, output chan<- llm.TextStreamEvent) {
	if event.Delta == "" {
		return
	}
	state.reasoningSummaryBuffer.WriteString(event.Delta)
	output <- llm.TextStreamEvent{
		Type:  llm.EventTypeReasoning,
		Value: event.Delta,
	}
}

// handleFunctionCallDone handles completed function call arguments
func (s *OpenAI) handleFunctionCallDone(event responses.ResponseStreamEventUnion, state *responsesStreamState) {
	if event.Arguments == "" || state.toolsBuffer[state.currentToolIndex] == nil {
		return
	}
	if state.toolsBuffer[state.currentToolIndex].args.Len() == 0 {
		state.toolsBuffer[state.currentToolIndex].args.WriteString(event.Arguments)
	}
}

// emitAnnotationsIfPresent sends accumulated annotations to the output channel
func (s *OpenAI) emitAnnotationsIfPresent(state *responsesStreamState, output chan<- llm.TextStreamEvent) {
	if len(state.annotations) == 0 {
		return
	}
	output <- llm.TextStreamEvent{
		Type:  llm.EventTypeAnnotations,
		Value: state.annotations,
	}
	state.annotations = nil
}

// handleResponseError handles error events from the Responses API
func (s *OpenAI) handleResponseError(event responses.ResponseStreamEventUnion, output chan<- llm.TextStreamEvent) {
	errorMsg := "Unknown error from Responses API"
	if event.Message != "" {
		errorMsg = event.Message
	}
	output <- llm.TextStreamEvent{
		Type:  llm.EventTypeError,
		Value: errors.New(errorMsg),
	}
}

// emitUsageIfPresent emits a usage event if tokens were used
func (s *OpenAI) emitUsageIfPresent(usage responses.ResponseUsage, output chan<- llm.TextStreamEvent) {
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		output <- llm.TextStreamEvent{
			Type: llm.EventTypeUsage,
			Value: llm.TokenUsage{
				InputTokens:  usage.InputTokens,
				OutputTokens: usage.OutputTokens,
			},
		}
	}
}

// handleResponsesStreamEnd handles cleanup and error reporting for Responses API streams
func (s *OpenAI) handleResponsesStreamEnd(ctx context.Context, stream *ssestream.Stream[responses.ResponseStreamEventUnion], cancel context.CancelCauseFunc, watchdogDone <-chan struct{}, output chan<- llm.TextStreamEvent) {
	if err := stream.Err(); err != nil {
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: ctxErr,
			}
		} else {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: err,
			}
		}
	}

	stream.Close()
	cancel(nil)
	<-watchdogDone
}

// convertToResponseParams converts ChatCompletionNewParams to ResponseNewParams
func (s *OpenAI) convertToResponseParams(params openai.ChatCompletionNewParams, llmContext *llm.Context, cfg llm.LanguageModelConfig) responses.ResponseNewParams {
	result := responses.ResponseNewParams{
		Model: params.Model,
	}

	if params.MaxCompletionTokens.Valid() {
		result.MaxOutputTokens = param.NewOpt(params.MaxCompletionTokens.Value)
	}
	if params.Temperature.Valid() {
		result.Temperature = param.NewOpt(params.Temperature.Value)
	}
	if params.TopP.Valid() {
		result.TopP = param.NewOpt(params.TopP.Value)
	}
	if params.User.Valid() && s.config.SendUserID {
		result.SafetyIdentifier = param.NewOpt(params.User.Value)
	}
	if s.config.ReasoningEnabled && !cfg.ReasoningDisabled {
		result.Reasoning = shared.ReasoningParam{
			Effort:  getReasoningEffort(s.config.ReasoningEffort),
			Summary: shared.ReasoningSummaryAuto,
		}
	}

	// Convert messages to string input format for the Responses API
	var inputBuilder strings.Builder
	var systemInstructions string

	for _, msg := range params.Messages {
		switch {
		case msg.OfSystem != nil:
			if msg.OfSystem.Content.OfString.Valid() {
				systemInstructions = msg.OfSystem.Content.OfString.Value
			}
		case msg.OfUser != nil:
			s.appendRolePrefix(&inputBuilder, "User")
			if msg.OfUser.Content.OfString.Valid() {
				inputBuilder.WriteString(msg.OfUser.Content.OfString.Value)
			}
		case msg.OfAssistant != nil:
			s.appendRolePrefix(&inputBuilder, "Assistant")
			if msg.OfAssistant.Content.OfString.Valid() {
				inputBuilder.WriteString(msg.OfAssistant.Content.OfString.Value)
			}
			// Include tool call info so the model correlates results with their calls
			for _, tc := range msg.OfAssistant.ToolCalls {
				if tc.OfFunction != nil {
					inputBuilder.WriteString(fmt.Sprintf("\n[Called tool: %s (id: %s) with arguments: %s]",
						tc.OfFunction.Function.Name,
						tc.OfFunction.ID,
						tc.OfFunction.Function.Arguments))
				}
			}
		case msg.OfTool != nil:
			s.appendRolePrefix(&inputBuilder, fmt.Sprintf("[Tool Result for call id: %s]", msg.OfTool.ToolCallID))
			if msg.OfTool.Content.OfString.Valid() {
				inputBuilder.WriteString(msg.OfTool.Content.OfString.Value)
			}
		}
	}

	if systemInstructions != "" {
		result.Instructions = param.NewOpt(systemInstructions)
	}
	if inputBuilder.Len() > 0 {
		result.Input = responses.ResponseNewParamsInputUnion{
			OfString: param.NewOpt(inputBuilder.String()),
		}
	}

	tools := s.convertTools(params.Tools, cfg)
	if len(tools) > 0 {
		result.Tools = tools
	}

	return result
}

// appendRolePrefix adds a role prefix to the input builder with appropriate spacing
func (s *OpenAI) appendRolePrefix(builder *strings.Builder, role string) {
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	builder.WriteString(role)
	builder.WriteString(": ")
}

// convertTools converts completion tools and native tools to Responses API format
func (s *OpenAI) convertTools(completionTools []openai.ChatCompletionToolUnionParam, cfg llm.LanguageModelConfig) []responses.ToolUnionParam {
	var tools []responses.ToolUnionParam

	for _, tool := range completionTools {
		if tool.OfFunction == nil {
			continue
		}
		functionTool := responses.FunctionToolParam{
			Name: tool.OfFunction.Function.Name,
		}
		if tool.OfFunction.Function.Description.Valid() {
			functionTool.Description = param.NewOpt(tool.OfFunction.Function.Description.Value)
		}
		if tool.OfFunction.Function.Parameters != nil {
			functionTool.Parameters = tool.OfFunction.Function.Parameters
		}
		tools = append(tools, responses.ToolUnionParam{
			OfFunction: &functionTool,
		})
	}

	// Add native tools if enabled
	if !cfg.ToolsDisabled {
		for _, nativeTool := range s.config.EnabledNativeTools {
			if nativeTool == "web_search" {
				tools = append(tools, responses.ToolUnionParam{
					OfWebSearchPreview: &responses.WebSearchToolParam{
						Type: responses.WebSearchToolTypeWebSearchPreview,
					},
				})
			}
		}
	}

	return tools
}

func (s *OpenAI) streamResult(params openai.ChatCompletionNewParams, llmContext *llm.Context, cfg llm.LanguageModelConfig) (*llm.TextStreamResult, error) {
	eventStream := make(chan llm.TextStreamEvent)
	go func() {
		defer close(eventStream)
		s.streamResultToChannels(params, llmContext, cfg, eventStream)
	}()

	return &llm.TextStreamResult{Stream: eventStream}, nil
}

func (s *OpenAI) GetDefaultConfig() llm.LanguageModelConfig {
	return llm.LanguageModelConfig{
		Model:              s.config.DefaultModel,
		MaxGeneratedTokens: s.config.OutputTokenLimit,
	}
}

func (s *OpenAI) createConfig(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := s.GetDefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

func (s *OpenAI) completionRequestFromConfig(cfg llm.LanguageModelConfig) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model: getModelConstant(cfg.Model),
	}

	if cfg.MaxGeneratedTokens > 0 {
		// Use max_tokens for OpenAI-compatible APIs (like Mistral) that don't support max_completion_tokens
		if s.config.UseMaxTokens {
			params.MaxTokens = openai.Int(int64(cfg.MaxGeneratedTokens))
		} else {
			params.MaxCompletionTokens = openai.Int(int64(cfg.MaxGeneratedTokens))
		}
	}

	if cfg.JSONOutputFormat != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "output_format",
					Schema: cfg.JSONOutputFormat,
					Strict: openai.Bool(true),
				},
			},
		}
	}

	return params
}

// reasoningEffortMap maps string effort levels to SDK constants
var reasoningEffortMap = map[string]shared.ReasoningEffort{
	"minimal": shared.ReasoningEffortMinimal,
	"low":     shared.ReasoningEffortLow,
	"medium":  shared.ReasoningEffortMedium,
	"high":    shared.ReasoningEffortHigh,
}

// getReasoningEffort converts a string effort level to the SDK constant, defaulting to medium
func getReasoningEffort(effort string) shared.ReasoningEffort {
	if e, ok := reasoningEffortMap[effort]; ok {
		return e
	}
	return shared.ReasoningEffortMedium
}

// getModelConstant converts string model names to the SDK's model constants
func getModelConstant(model string) shared.ChatModel {
	// Try to match common model names to constants
	switch model {
	case "gpt-4o":
		return shared.ChatModelGPT4o
	case "gpt-4o-mini":
		return shared.ChatModelGPT4oMini
	case "gpt-4-turbo":
		return shared.ChatModelGPT4Turbo
	case "gpt-4":
		return shared.ChatModelGPT4
	case "gpt-3.5-turbo":
		return shared.ChatModelGPT3_5Turbo
	case "o1-preview":
		return shared.ChatModelO1Preview
	case "o1-mini":
		return shared.ChatModelO1Mini
	default:
		// For custom models or newer versions, use the string as-is
		return model
	}
}

func (s *OpenAI) ChatCompletion(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	cfg := s.createConfig(opts)
	params := s.completionRequestFromConfig(cfg)
	params = modifyCompletionRequestWithRequest(params, request, cfg)

	// Only set stream_options for APIs that support it (not OpenAI-compatible APIs like Mistral)
	if !s.config.DisableStreamOptions {
		params.StreamOptions.IncludeUsage = openai.Bool(true)
	}

	if s.config.SendUserID {
		if request.Context.RequestingUser != nil {
			params.User = openai.String(request.Context.RequestingUser.Id)
		}
	}
	return s.streamResult(params, request.Context, cfg)
}

func (s *OpenAI) ChatCompletionNoStream(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	// This could perform better if we didn't use the streaming API here, but the complexity is not worth it.
	result, err := s.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (s *OpenAI) Transcribe(file io.Reader) (*subtitles.Subtitles, error) {
	params := openai.AudioTranscriptionNewParams{
		Model:          openai.AudioModelWhisper1,
		File:           file,
		ResponseFormat: openai.AudioResponseFormatVTT,
	}

	resp, err := s.client.Audio.Transcriptions.New(context.Background(), params)
	if err != nil {
		return nil, fmt.Errorf("unable to create whisper transcription: %w", err)
	}

	// The response for VTT format is the Text field
	timedTranscript, err := subtitles.NewSubtitlesFromVTT(strings.NewReader(resp.Text))
	if err != nil {
		return nil, fmt.Errorf("unable to parse whisper transcription: %w", err)
	}

	return timedTranscript, nil
}

func (s *OpenAI) GenerateImage(prompt string) (image.Image, error) {
	params := openai.ImageGenerateParams{
		Prompt:         prompt,
		Size:           openai.ImageGenerateParamsSize256x256,
		ResponseFormat: openai.ImageGenerateParamsResponseFormatB64JSON,
		N:              openai.Int(1),
	}

	resp, err := s.client.Images.Generate(context.Background(), params)
	if err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, errors.New("no image data returned")
	}

	var imgBytes []byte
	if resp.Data[0].B64JSON != "" {
		imgBytes, err = base64.StdEncoding.DecodeString(resp.Data[0].B64JSON)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("no base64 image data")
	}

	r := bytes.NewReader(imgBytes)
	imgData, err := png.Decode(r)
	if err != nil {
		return nil, err
	}

	return imgData, nil
}

func (s *OpenAI) CountTokens(text string) int {
	// Counting tokens is really annoying, so we approximate for now.
	charCount := float64(len(text)) / 4.0
	wordCount := float64(len(strings.Fields(text))) / 0.75

	// Average the two
	return int((charCount + wordCount) / 2.0)
}

func (s *OpenAI) InputTokenLimit() int {
	if s.config.InputTokenLimit > 0 {
		return s.config.InputTokenLimit
	}

	switch {
	case strings.HasPrefix(s.config.DefaultModel, "gpt-4o"),
		strings.HasPrefix(s.config.DefaultModel, "o1-preview"),
		strings.HasPrefix(s.config.DefaultModel, "o1-mini"),
		strings.HasPrefix(s.config.DefaultModel, "gpt-4-turbo"),
		strings.HasPrefix(s.config.DefaultModel, "gpt-4-0125-preview"),
		strings.HasPrefix(s.config.DefaultModel, "gpt-4-1106-preview"):
		return 128000
	case strings.HasPrefix(s.config.DefaultModel, "gpt-4"):
		return 8192
	case s.config.DefaultModel == "gpt-3.5-turbo-instruct":
		return 4096
	case strings.HasPrefix(s.config.DefaultModel, "gpt-3.5-turbo"),
		s.config.DefaultModel == "gpt-3.5-turbo-0125",
		s.config.DefaultModel == "gpt-3.5-turbo-1106":
		return 16385
	}

	return 128000 // Default fallback
}

func (s *OpenAI) CreateEmbedding(ctx context.Context, text string) ([]float32, error) {
	params := openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfString: openai.String(text),
		},
		Model: getEmbeddingModelConstant(s.config.EmbeddingModel),
	}

	// Only set dimensions if it's explicitly configured (> 0)
	if s.config.EmbeddingDimensions > 0 {
		params.Dimensions = openai.Int(int64(s.config.EmbeddingDimensions))
	}

	resp, err := s.client.Embeddings.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	// Convert float64 to float32
	embedding := make([]float32, len(resp.Data[0].Embedding))
	for i, v := range resp.Data[0].Embedding {
		embedding[i] = float32(v)
	}
	return embedding, nil
}

// BatchCreateEmbeddings generates embeddings for multiple texts in a single API call
func (s *OpenAI) BatchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	params := openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: texts,
		},
		Model: getEmbeddingModelConstant(s.config.EmbeddingModel),
	}

	// Only set dimensions if it's explicitly configured (> 0)
	if s.config.EmbeddingDimensions > 0 {
		params.Dimensions = openai.Int(int64(s.config.EmbeddingDimensions))
	}

	resp, err := s.client.Embeddings.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create embeddings batch: %w", err)
	}

	embeddings := make([][]float32, len(resp.Data))
	for i, data := range resp.Data {
		// Convert float64 to float32
		embedding := make([]float32, len(data.Embedding))
		for j, v := range data.Embedding {
			embedding[j] = float32(v)
		}
		embeddings[i] = embedding
	}

	return embeddings, nil
}

// getEmbeddingModelConstant converts string model names to the SDK's embedding model constants
func getEmbeddingModelConstant(model string) openai.EmbeddingModel {
	switch model {
	case "text-embedding-3-large":
		return openai.EmbeddingModelTextEmbedding3Large
	case "text-embedding-3-small":
		return openai.EmbeddingModelTextEmbedding3Small
	case "text-embedding-ada-002":
		return openai.EmbeddingModelTextEmbeddingAda002
	default:
		// For custom models, use the string as-is
		return model
	}
}

func (s *OpenAI) Dimensions() int {
	return s.config.EmbeddingDimensions
}

// FetchModels retrieves the list of available models from the OpenAI API
func FetchModels(apiKey string, apiURL string, orgID string, httpClient *http.Client) ([]llm.ModelInfo, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(httpClient),
	}

	// Add base URL if provided (for OpenAI Compatible services)
	if apiURL != "" {
		opts = append(opts, option.WithBaseURL(strings.TrimSuffix(apiURL, "/")))
	}

	// Add organization ID if provided
	if orgID != "" {
		opts = append(opts, option.WithOrganization(orgID))
	}

	client := openai.NewClient(opts...)

	// Use AutoPaging to automatically handle pagination
	autoPager := client.Models.ListAutoPaging(context.Background())

	var models []llm.ModelInfo

	// Iterate through all pages
	for autoPager.Next() {
		model := autoPager.Current()
		models = append(models, llm.ModelInfo{
			ID:          model.ID,
			DisplayName: model.ID, // OpenAI doesn't have separate display names
		})
	}

	// Check if there was an error during iteration
	if err := autoPager.Err(); err != nil {
		return nil, err
	}

	return models, nil
}
