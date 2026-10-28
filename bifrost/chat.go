// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// streamChat handles the streaming chat completion.
func (b *LLM) streamChat(ctx context.Context, request llm.CompletionRequest, cfg llm.LanguageModelConfig, output chan<- llm.TextStreamEvent) {
	span := telemetry.SpanFromContext(ctx)
	span.SetAttributes(
		telemetry.LLMPath.String("chat"),
		telemetry.LLMUseResponsesAPI.Bool(b.useResponsesAPI),
	)
	bifrostCtx, cancel := schemas.NewBifrostContextWithTimeout(ctx, b.streamingTimeout*10)
	defer cancel()
	request = bufferFiles(request)
	b.withHopRequest(bifrostCtx, request, cfg)

	// Convert to Bifrost request
	bifrostReq := b.convertToBifrostRequest(request, cfg)
	if bifrostReq.Params != nil {
		recordReasoningSent(span, bifrostReq.Params.Reasoning)
	} else {
		recordReasoningSent(span, nil)
	}

	// Make streaming request
	streamChan, bifrostErr := b.client.ChatCompletionStreamRequest(bifrostCtx, bifrostReq)
	if bifrostErr != nil {
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: providerError(span, "bifrost error", bifrostErr, b.redactionKeys()...),
		}
		return
	}

	// Process stream
	var toolCalls []llm.ToolCall
	var toolCallsBuffer map[int]*toolCallBuffer
	var reasoning reasoningAccumulator

	ping := b.startStreamWatchdog(bifrostCtx.Done(), cancel)

	for chunk := range streamChan {
		ping()

		if chunk.BifrostError != nil {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: providerError(span, "bifrost stream error", chunk.BifrostError, b.redactionKeys()...),
			}
			return
		}

		// Process response chunk
		if chunk.BifrostChatResponse != nil {
			resp := chunk.BifrostChatResponse
			if len(resp.Choices) > 0 {
				choice := resp.Choices[0]

				// Handle text content from delta (streaming)
				if choice.ChatStreamResponseChoice != nil && choice.Delta != nil && choice.Delta.Content != nil {
					content := *choice.Delta.Content
					if content != "" {
						// Emit reasoning end before first text if we have accumulated reasoning
						reasoning.emitEnd(output)
						output <- llm.TextStreamEvent{
							Type:  llm.EventTypeText,
							Value: content,
						}
					}
				}

				// Handle reasoning/thinking content (streaming)
				if choice.ChatStreamResponseChoice != nil && choice.Delta != nil {
					if choice.Delta.Reasoning != nil && *choice.Delta.Reasoning != "" {
						output <- llm.TextStreamEvent{
							Type:  llm.EventTypeReasoning,
							Value: *choice.Delta.Reasoning,
						}
						reasoning.buffer.WriteString(*choice.Delta.Reasoning)
					}
					for _, rd := range choice.Delta.ReasoningDetails {
						if rd.Signature != nil && *rd.Signature != "" {
							reasoning.signature = *rd.Signature
						}
					}
				}

				// Handle tool calls (streaming)
				if choice.ChatStreamResponseChoice != nil && choice.Delta != nil && len(choice.Delta.ToolCalls) > 0 {
					if toolCallsBuffer == nil {
						toolCallsBuffer = make(map[int]*toolCallBuffer)
					}
					for _, tc := range choice.Delta.ToolCalls {
						idx := int(tc.Index)
						if toolCallsBuffer[idx] == nil {
							toolCallsBuffer[idx] = &toolCallBuffer{}
						}
						if tc.ID != nil {
							toolCallsBuffer[idx].id = *tc.ID
						}
						if tc.Function.Name != nil {
							toolCallsBuffer[idx].name = *tc.Function.Name
						}
						toolCallsBuffer[idx].arguments.WriteString(tc.Function.Arguments)
					}
				}

				// Check finish reason
				if choice.FinishReason != nil {
					span.SetAttributes(telemetry.LLMFinishReason.String(*choice.FinishReason))
					switch *choice.FinishReason {
					case "tool_calls":
						// Convert buffered tool calls in index order. They
						// are emitted once the stream ends: providers send
						// usage in a chunk after the finish reason, and
						// returning here would drop it for every tool round.
						toolCalls = flushToolCallBuffers(toolCallsBuffer, false)
					case "stop":
						// Emit reasoning end if we accumulated reasoning
						reasoning.emitEnd(output)
					}
				}
			}

			// Handle usage data
			if resp.Usage != nil {
				usage := convertChatUsage(resp.Usage)
				if usage.InputTokens > 0 || usage.OutputTokens > 0 {
					setTokenUsageSpanAttributes(span, usage)
					setCompositionSpanAttributes(span, request, usage)
					output <- llm.TextStreamEvent{
						Type:  llm.EventTypeUsage,
						Value: usage,
					}
				}
			}
		}
	}

	// Tool calls completed by a "tool_calls" finish reason.
	if len(toolCalls) > 0 {
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeToolCalls,
			Value: toolCalls,
		}
		return
	}

	// Emit any unsent reasoning
	reasoning.emitEnd(output)

	// If we have pending tool calls, emit them in index order
	if len(toolCallsBuffer) > 0 {
		toolCalls = flushToolCallBuffers(toolCallsBuffer, true)
		if len(toolCalls) > 0 {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeToolCalls,
				Value: toolCalls,
			}
			return
		}
	}

	output <- llm.TextStreamEvent{
		Type:  llm.EventTypeEnd,
		Value: nil,
	}
}

// chatReasoningFor creates the ChatReasoning configuration for cfg.Model on
// the provider registered as name, or nil when no reasoning block should be
// sent.
func (b *LLM) chatReasoningFor(name schemas.ModelProvider, cfg llm.LanguageModelConfig) *schemas.ChatReasoning {
	params, ok := b.providerReasoning(messagesAPIProvider(b.baseProvider(name), cfg.Model), cfg)
	if !ok {
		return nil
	}
	reasoning := &schemas.ChatReasoning{Effort: params.effort, MaxTokens: params.maxTokens}
	if params.hideThinking {
		reasoning.Display = new("omitted")
	}
	return reasoning
}

// convertToBifrostRequest converts our CompletionRequest to Bifrost's format,
// built for the primary. Bifrost replays it to each fallback, whose body the
// hopRequestPlugin rebuilds with chatRequestForHop.
func (b *LLM) convertToBifrostRequest(request llm.CompletionRequest, cfg llm.LanguageModelConfig) *schemas.BifrostChatRequest {
	messages, params := b.chatRequestForHop(request, cfg, b.provider, cfg.Model)
	return &schemas.BifrostChatRequest{
		Provider: b.provider,
		Model:    cfg.Model,
		Input:    messages,
		Params:   params,
		// Attach fallback chain so Bifrost retries with alternative providers on failure.
		Fallbacks: b.requestFallbacks(request),
	}
}

// chatRequestForHop returns the messages and parameters for model on the
// provider registered as name, shaped for that hop's own rules.
func (b *LLM) chatRequestForHop(request llm.CompletionRequest, cfg llm.LanguageModelConfig, name schemas.ModelProvider, model string) ([]schemas.ChatMessage, *schemas.ChatParameters) {
	rules := b.messageRulesFor(name, model, countRequestImages(request.Posts))
	messages := convertMessages(normalizePosts(request.Posts, rules), rules)
	tools := convertTools(request, cfg)

	var params schemas.ChatParameters
	if len(tools) > 0 {
		params.Tools = tools
		if cfg.ToolsDisabled {
			none := string(schemas.ChatToolChoiceTypeNone)
			params.ToolChoice = &schemas.ChatToolChoice{ChatToolChoiceStr: &none}
		}
	}
	// Apply structured output (JSON schema) configuration
	if cfg.JSONOutputFormat != nil {
		params.ResponseFormat = buildChatResponseFormat(cfg.JSONOutputFormat)
	}
	if rules.promptCaching {
		params.CacheControl = &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral}
	}
	return messages, b.chatHopParams(params, name, model, cfg)
}

// convertMessages converts posts normalized for rules to Bifrost ChatMessage format.
func convertMessages(posts []llm.Post, rules messageRules) []schemas.ChatMessage {
	messages := make([]schemas.ChatMessage, 0, len(posts))

	for _, post := range posts {
		var msg schemas.ChatMessage

		switch post.Role {
		case llm.PostRoleSystem:
			if isBlank(post.Message) {
				continue
			}
			msg = schemas.ChatMessage{
				Role: schemas.ChatMessageRoleSystem,
				Content: &schemas.ChatMessageContent{
					ContentStr: new(post.Message),
				},
			}

		case llm.PostRoleUser:
			if len(post.Files) > 0 {
				// Multimodal message with images
				parts := createMultimodalContent(post, rules.imageDimensionLimit)
				if len(parts) == 0 {
					continue
				}
				msg = schemas.ChatMessage{
					Role: schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{
						ContentBlocks: parts,
					},
				}
			} else {
				if isBlank(post.Message) {
					continue
				}
				msg = schemas.ChatMessage{
					Role: schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{
						ContentStr: new(post.Message),
					},
				}
			}

		case llm.PostRoleBot:
			msg = schemas.ChatMessage{
				Role: schemas.ChatMessageRoleAssistant,
			}
			if post.Message != "" {
				msg.Content = &schemas.ChatMessageContent{
					ContentStr: new(post.Message),
				}
			}

			// Add reasoning details for thinking-enabled conversations.
			if post.Reasoning != "" {
				if msg.ChatAssistantMessage == nil {
					msg.ChatAssistantMessage = &schemas.ChatAssistantMessage{}
				}
				msg.ReasoningDetails = []schemas.ChatReasoningDetails{{
					Index:     0,
					Type:      schemas.BifrostReasoningDetailsTypeText,
					Text:      new(post.Reasoning),
					Signature: new(post.ReasoningSignature),
				}}
			}

			// Handle tool calls in assistant messages
			if len(post.ToolUse) > 0 {
				toolCalls := make([]schemas.ChatAssistantMessageToolCall, 0, len(post.ToolUse))
				for i, tc := range post.ToolUse {
					toolCalls = append(toolCalls, schemas.ChatAssistantMessageToolCall{
						Index: uint16(i % 65536), //nolint:gosec // index will never exceed uint16 max in practice
						ID:    new(tc.ID),
						Type:  new("function"),
						Function: schemas.ChatAssistantMessageToolCallFunction{
							Name:      new(tc.Name),
							Arguments: string(tc.Arguments),
						},
					})
				}
				if msg.ChatAssistantMessage == nil {
					msg.ChatAssistantMessage = &schemas.ChatAssistantMessage{}
				}
				msg.ToolCalls = toolCalls

				// Add the assistant message with tool calls
				messages = append(messages, msg)

				for _, tc := range post.ToolUse {
					// OpenAI-wire converters strip IsError; Anthropic and
					// Bedrock map it to their tool-result error markers.
					toolResultMsg := schemas.ChatMessage{
						Role: schemas.ChatMessageRoleTool,
						Content: &schemas.ChatMessageContent{
							ContentStr: new(tc.Result),
						},
						ChatToolMessage: &schemas.ChatToolMessage{
							ToolCallID: new(tc.ID),
						},
					}
					if toolResultIsError(tc) {
						toolResultMsg.IsError = new(true)
					}
					messages = append(messages, toolResultMsg)
				}
				continue // Skip adding msg again
			}

			if msg.Content == nil && msg.ChatAssistantMessage == nil {
				continue
			}
		}

		messages = append(messages, msg)
	}

	// Bifrost does not merge same-role turns for Anthropic or Bedrock on the
	// chat path, and Bedrock Converse rejects them.
	if rules.strictMessages {
		messages = mergeConsecutiveSameRoleMessages(messages)
	}

	return messages
}

// mergeConsecutiveSameRoleMessages merges consecutive messages with the same role
// into a single message with combined content blocks. Tool messages are never merged.
func mergeConsecutiveSameRoleMessages(messages []schemas.ChatMessage) []schemas.ChatMessage {
	if len(messages) <= 1 {
		return messages
	}
	merged := make([]schemas.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		if len(merged) > 0 && merged[len(merged)-1].Role == msg.Role &&
			msg.Role != schemas.ChatMessageRoleTool {
			// Merge into previous message by converting both to content blocks
			prev := &merged[len(merged)-1]
			prevBlocks := messageToContentBlocks(prev)
			newBlocks := messageToContentBlocks(&msg)
			prev.Content = &schemas.ChatMessageContent{
				ContentBlocks: append(prevBlocks, newBlocks...),
			}
			// Merge assistant metadata (tool calls, reasoning)
			if msg.ChatAssistantMessage != nil {
				if prev.ChatAssistantMessage == nil {
					prev.ChatAssistantMessage = msg.ChatAssistantMessage
				} else {
					prev.ToolCalls = append(
						prev.ToolCalls,
						msg.ToolCalls...)
					if msg.ReasoningDetails != nil {
						prev.ReasoningDetails = append(
							prev.ReasoningDetails,
							msg.ReasoningDetails...)
					}
				}
			}
		} else {
			merged = append(merged, msg)
		}
	}
	return merged
}

// messageToContentBlocks extracts content blocks from a ChatMessage.
func messageToContentBlocks(msg *schemas.ChatMessage) []schemas.ChatContentBlock {
	if msg.Content == nil {
		return nil
	}
	if len(msg.Content.ContentBlocks) > 0 {
		return msg.Content.ContentBlocks
	}
	if msg.Content.ContentStr != nil {
		return []schemas.ChatContentBlock{{
			Type: schemas.ChatContentBlockTypeText,
			Text: msg.Content.ContentStr,
		}}
	}
	return nil
}

// createMultimodalContent creates content blocks for messages with images.
func createMultimodalContent(post llm.Post, maxDim int) []schemas.ChatContentBlock {
	return multimodalContent(post, maxDim,
		func(text string) schemas.ChatContentBlock {
			return schemas.ChatContentBlock{
				Type: schemas.ChatContentBlockTypeText,
				Text: new(text),
			}
		},
		func(dataURL string) schemas.ChatContentBlock {
			return schemas.ChatContentBlock{
				Type: "image_url",
				ImageURLStruct: &schemas.ChatInputImage{
					URL: dataURL,
				},
			}
		},
	)
}

// convertTools converts llm.Tool to Bifrost ChatTool format.
func convertTools(request llm.CompletionRequest, cfg llm.LanguageModelConfig) []schemas.ChatTool {
	if request.Context == nil || request.Context.Tools == nil {
		return nil
	}
	// Keep tools defined when the history has tool_use blocks; tool_choice="none"
	// (set by the caller) forbids further calls.
	if cfg.ToolsDisabled && !hasToolUseHistory(request.Posts) {
		return nil
	}

	tools := request.Context.Tools.GetTools()
	result := make([]schemas.ChatTool, 0, len(tools))

	for _, tool := range tools {
		params := toolFunctionParams(tool.Schema)

		bifrostTool := schemas.ChatTool{
			Type: schemas.ChatToolTypeFunction,
			Function: &schemas.ChatToolFunction{
				Name:        tool.Name,
				Description: new(tool.Description),
				Parameters:  params,
			},
		}
		result = append(result, bifrostTool)
	}

	return result
}

// toolResultIsError reports whether a replayed tool call did not produce a
// real result: it failed, or was rejected, denied by policy, or never run.
func toolResultIsError(tc llm.ToolCall) bool {
	return tc.Status == llm.ToolCallStatusError || tc.Status == llm.ToolCallStatusRejected
}
