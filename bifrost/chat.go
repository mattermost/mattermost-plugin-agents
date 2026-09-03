// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/codes"

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
		recordBifrostError(span, bifrostErr)
		err := llm.SanitizeProviderError(fmt.Errorf("bifrost error: %s", bifrostErrorString(bifrostErr)), b.redactionKeys()...)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: err,
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
			recordBifrostError(span, chunk.BifrostError)
			err := llm.SanitizeProviderError(fmt.Errorf("bifrost stream error: %s", bifrostErrorString(chunk.BifrostError)), b.redactionKeys()...)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: err,
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
					switch *choice.FinishReason {
					case "tool_calls":
						// Convert buffered tool calls in index order
						toolCalls = flushToolCallBuffers(toolCallsBuffer, false)
						if len(toolCalls) > 0 {
							output <- llm.TextStreamEvent{
								Type:  llm.EventTypeToolCalls,
								Value: toolCalls,
							}
							return
						}
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

	// Emit any unsent reasoning
	reasoning.emitEnd(output)

	// If we have pending tool calls, emit them in index order
	if len(toolCallsBuffer) > 0 && len(toolCalls) == 0 {
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

// buildChatReasoning creates a ChatReasoning configuration if reasoning is enabled.
func (b *LLM) buildChatReasoning(cfg llm.LanguageModelConfig) *schemas.ChatReasoning {
	if !b.reasoningEnabled || cfg.ReasoningDisabled || b.thinkingBlockedBySchema(cfg) {
		return nil
	}
	effort, maxTokens, ok := b.providerReasoningBudget(cfg)
	if !ok {
		return nil
	}
	return &schemas.ChatReasoning{Effort: effort, MaxTokens: maxTokens}
}

// convertToBifrostRequest converts our CompletionRequest to Bifrost's format.
func (b *LLM) convertToBifrostRequest(request llm.CompletionRequest, cfg llm.LanguageModelConfig) *schemas.BifrostChatRequest {
	messages := b.convertMessages(request.Posts, cfg)
	tools := b.convertTools(request, cfg)

	req := &schemas.BifrostChatRequest{
		Provider: b.provider,
		Model:    cfg.Model,
		Input:    messages,
	}

	// Set parameters
	params := &schemas.ChatParameters{}
	if cfg.MaxGeneratedTokens > 0 {
		params.MaxCompletionTokens = new(cfg.MaxGeneratedTokens)
	}
	if len(tools) > 0 {
		params.Tools = tools
		if cfg.ToolsDisabled {
			none := string(schemas.ChatToolChoiceTypeNone)
			params.ToolChoice = &schemas.ChatToolChoice{ChatToolChoiceStr: &none}
		}
	}
	// Apply reasoning configuration
	params.Reasoning = b.buildChatReasoning(cfg)
	// Apply structured output (JSON schema) configuration
	if cfg.JSONOutputFormat != nil {
		params.ResponseFormat = buildChatResponseFormat(cfg.JSONOutputFormat)
	}
	if b.promptCachingEnabled() {
		params.CacheControl = &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral}
	}
	req.Params = params

	// Attach fallback chain so Bifrost retries with alternative providers on failure.
	req.Fallbacks = b.fallbacks

	return req
}

// convertMessages converts llm.Post messages to Bifrost ChatMessage format.
func (b *LLM) convertMessages(posts []llm.Post, cfg llm.LanguageModelConfig) []schemas.ChatMessage {
	messages := make([]schemas.ChatMessage, 0, len(posts))

	for _, post := range posts {
		var msg schemas.ChatMessage

		switch post.Role {
		case llm.PostRoleSystem:
			msg = schemas.ChatMessage{
				Role: schemas.ChatMessageRoleSystem,
				Content: &schemas.ChatMessageContent{
					ContentStr: new(post.Message),
				},
			}

		case llm.PostRoleUser:
			if len(post.Files) > 0 {
				// Multimodal message with images
				parts := b.createMultimodalContent(post)
				msg = schemas.ChatMessage{
					Role: schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{
						ContentBlocks: parts,
					},
				}
			} else {
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
				Content: &schemas.ChatMessageContent{
					ContentStr: new(post.Message),
				},
			}

			// Add reasoning details for thinking-enabled conversations.
			// Anthropic requires historical thinking blocks to include a valid
			// provider-issued signature. If a previous stream failed before the
			// signature arrived, we persist partial reasoning for display only; do
			// not replay it to Anthropic as an unsigned thinking block. Other
			// providers may accept unsigned reasoning, so preserve it for them.
			// Also skip replay when thinking is disabled for this request:
			// Anthropic rejects input thinking blocks when thinking is off.
			if post.Reasoning != "" &&
				(b.provider != schemas.Anthropic || post.ReasoningSignature != "") &&
				!b.thinkingBlockedBySchema(cfg) {
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
				if post.Message == "" {
					msg.Content = nil
				}
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

				// Add tool result messages. Anthropic rejects tool result
				// messages with empty content ("text content blocks must be
				// non-empty"), so substitute a placeholder if the tool
				// returned an empty string.
				for _, tc := range post.ToolUse {
					result := tc.Result
					if result == "" {
						result = "(no output)"
					}
					toolResultMsg := schemas.ChatMessage{
						Role: schemas.ChatMessageRoleTool,
						Content: &schemas.ChatMessageContent{
							ContentStr: new(result),
						},
						ChatToolMessage: &schemas.ChatToolMessage{
							ToolCallID: new(tc.ID),
						},
					}
					messages = append(messages, toolResultMsg)
				}
				continue // Skip adding msg again
			}
		}

		messages = append(messages, msg)
	}

	// Merge consecutive same-role messages for Anthropic
	if b.provider == schemas.Anthropic {
		messages = b.mergeConsecutiveSameRoleMessages(messages)
	}

	return messages
}

// mergeConsecutiveSameRoleMessages merges consecutive messages with the same role
// into a single message with combined content blocks. Tool messages are never merged.
func (b *LLM) mergeConsecutiveSameRoleMessages(messages []schemas.ChatMessage) []schemas.ChatMessage {
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
func (b *LLM) createMultimodalContent(post llm.Post) []schemas.ChatContentBlock {
	return multimodalContent(post,
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
func (b *LLM) convertTools(request llm.CompletionRequest, cfg llm.LanguageModelConfig) []schemas.ChatTool {
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
