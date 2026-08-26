// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"cmp"
	"context"
	"fmt"

	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/codes"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// convertToResponsesMessages converts llm.Post messages to Bifrost ResponsesMessage format.
func (b *LLM) convertToResponsesMessages(posts []llm.Post) []schemas.ResponsesMessage {
	messages := make([]schemas.ResponsesMessage, 0, len(posts))

	for _, post := range posts {
		switch post.Role {
		case llm.PostRoleSystem:
			msg := schemas.ResponsesMessage{
				Role: new(schemas.ResponsesInputMessageRoleSystem),
				Content: &schemas.ResponsesMessageContent{
					ContentStr: new(post.Message),
				},
			}
			messages = append(messages, msg)

		case llm.PostRoleUser:
			if len(post.Files) > 0 {
				// Multimodal message with images
				parts := b.createResponsesMultimodalContent(post)
				msg := schemas.ResponsesMessage{
					Role: new(schemas.ResponsesInputMessageRoleUser),
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: parts,
					},
				}
				messages = append(messages, msg)
			} else {
				msg := schemas.ResponsesMessage{
					Role: new(schemas.ResponsesInputMessageRoleUser),
					Content: &schemas.ResponsesMessageContent{
						ContentStr: new(post.Message),
					},
				}
				messages = append(messages, msg)
			}

		case llm.PostRoleBot:
			// Handle tool calls in assistant messages
			if len(post.ToolUse) > 0 {
				if post.Message != "" {
					messages = append(messages, schemas.ResponsesMessage{
						Role: new(schemas.ResponsesInputMessageRoleAssistant),
						Content: &schemas.ResponsesMessageContent{
							ContentStr: new(post.Message),
						},
					})
				}
				for _, tc := range post.ToolUse {
					funcCallMsg := schemas.ResponsesMessage{
						Type: new(schemas.ResponsesMessageTypeFunctionCall),
						ResponsesToolMessage: &schemas.ResponsesToolMessage{
							CallID:    new(tc.ID),
							Name:      new(tc.Name),
							Arguments: new(string(tc.Arguments)),
						},
					}
					messages = append(messages, funcCallMsg)

					funcOutputMsg := schemas.ResponsesMessage{
						Type: new(schemas.ResponsesMessageTypeFunctionCallOutput),
						ResponsesToolMessage: &schemas.ResponsesToolMessage{
							CallID: new(tc.ID),
							Output: &schemas.ResponsesToolMessageOutputStruct{
								ResponsesToolCallOutputStr: new(tc.Result),
							},
						},
					}
					messages = append(messages, funcOutputMsg)
				}
			} else if post.Message != "" {
				messages = append(messages, schemas.ResponsesMessage{
					Role: new(schemas.ResponsesInputMessageRoleAssistant),
					Content: &schemas.ResponsesMessageContent{
						ContentStr: new(post.Message),
					},
				})
			}
		}
	}

	return messages
}

// createResponsesMultimodalContent creates content blocks for Responses API messages with images.
func (b *LLM) createResponsesMultimodalContent(post llm.Post) []schemas.ResponsesMessageContentBlock {
	return multimodalContent(post,
		func(text string) schemas.ResponsesMessageContentBlock {
			return schemas.ResponsesMessageContentBlock{
				Type: schemas.ResponsesInputMessageContentBlockTypeText,
				Text: new(text),
			}
		},
		func(dataURL string) schemas.ResponsesMessageContentBlock {
			return schemas.ResponsesMessageContentBlock{
				Type: schemas.ResponsesInputMessageContentBlockTypeImage,
				ResponsesInputMessageContentBlockImage: &schemas.ResponsesInputMessageContentBlockImage{
					ImageURL: new(dataURL),
				},
			}
		},
	)
}

// anthropicDirectToolCaller is the allowed_callers value that restricts a
// server tool to direct model invocation. See webToolResponsesTool.
const anthropicDirectToolCaller = "direct"

// sandboxEnabled reports whether the agent explicitly enabled the provider code
// sandbox (the code_interpreter native tool — Anthropic's code_execution).
func (b *LLM) sandboxEnabled() bool {
	return b.isNativeToolEnabled(llm.NativeToolCodeInterpreter)
}

// webToolResponsesTool builds a native web_search / web_fetch tool definition.
//
// Unless the agent explicitly enabled the code sandbox (code_interpreter),
// AllowedCallers is pinned to "direct" — Anthropic's documented opt-out from
// dynamic filtering, which would otherwise auto-provision the code-execution
// sandbox on newer Claude models:
// https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool#dynamic-filtering
// Bifrost strips the field for providers that don't accept it; pinned by
// TestWebSearchAllowedCallersStrippedForOpenAI and
// TestAnthropicOnlyWebFetchDroppedForOpenAI.
func (b *LLM) webToolResponsesTool(toolType schemas.ResponsesToolType) schemas.ResponsesTool {
	tool := schemas.ResponsesTool{Type: toolType}
	if !b.sandboxEnabled() {
		tool.AllowedCallers = []string{anthropicDirectToolCaller}
	}
	return tool
}

// convertToResponsesTools creates Responses API tools including native tools and function tools.
func (b *LLM) convertToResponsesTools(request llm.CompletionRequest, cfg llm.LanguageModelConfig) []schemas.ResponsesTool {
	var result []schemas.ResponsesTool

	// Add native tools (always add when configured, regardless of ToolsDisabled)
	for _, nativeTool := range b.enabledNativeTools {
		switch nativeTool {
		case llm.NativeToolWebSearch:
			result = append(result, b.webToolResponsesTool(schemas.ResponsesToolTypeWebSearch))
		case llm.NativeToolWebFetch:
			result = append(result, b.webToolResponsesTool(schemas.ResponsesToolTypeWebFetch))
		case llm.NativeToolCodeInterpreter:
			result = append(result, schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeCodeInterpreter,
			})
		}
	}

	// When NativeWebSearchAllowed is true but web_search is not in enabledNativeTools,
	// add it dynamically
	if cfg.NativeWebSearchAllowed && !b.isNativeToolEnabled(llm.NativeToolWebSearch) {
		result = append(result, b.webToolResponsesTool(schemas.ResponsesToolTypeWebSearch))
	}

	// Keep function tools defined when the history has tool_use blocks; the
	// caller sets tool_choice="none" to forbid further calls. See hasToolUseHistory.
	keepFunctionTools := !cfg.ToolsDisabled || hasToolUseHistory(request.Posts)
	if keepFunctionTools && request.Context != nil && request.Context.Tools != nil {
		tools := request.Context.Tools.GetTools()
		for _, tool := range tools {
			params := toolFunctionParams(tool.Schema)

			responsesTool := schemas.ResponsesTool{
				Type:        schemas.ResponsesToolTypeFunction,
				Name:        new(tool.Name),
				Description: new(tool.Description),
				ResponsesToolFunction: &schemas.ResponsesToolFunction{
					Parameters: params,
				},
			}
			result = append(result, responsesTool)
		}
	}

	return result
}

// buildResponsesReasoning creates a ResponsesParametersReasoning configuration if reasoning is enabled.
func (b *LLM) buildResponsesReasoning(cfg llm.LanguageModelConfig) *schemas.ResponsesParametersReasoning {
	if !b.reasoningEnabled || cfg.ReasoningDisabled || b.thinkingBlockedBySchema(cfg) {
		return nil
	}

	if b.provider == schemas.OpenAI || b.provider == schemas.Azure {
		// Enable reasoning summaries so the provider returns reasoning text in
		// the stream; without this OpenAI omits reasoning_summary events.
		return &schemas.ResponsesParametersReasoning{
			Effort:  new(cmp.Or(b.reasoningEffort, "medium")),
			Summary: new("auto"),
		}
	}

	// Bifrost will route a Responses-API request to chat completions for
	// providers without native Responses support (e.g. Mistral). Those
	// providers don't accept reasoning_effort, so providerReasoningBudget
	// drops it for them too.
	effort, maxTokens, ok := b.providerReasoningBudget(cfg)
	if !ok {
		return nil
	}
	reasoning := &schemas.ResponsesParametersReasoning{Effort: effort, MaxTokens: maxTokens}
	// Enable summary so Gemini/Vertex return reasoning text in the stream.
	if b.provider == schemas.Gemini || b.provider == schemas.Vertex {
		reasoning.Summary = new("auto")
	}
	return reasoning
}

// convertToBifrostResponsesRequest converts our CompletionRequest to Bifrost's Responses API format.
func (b *LLM) convertToBifrostResponsesRequest(request llm.CompletionRequest, cfg llm.LanguageModelConfig) (*schemas.BifrostResponsesRequest, error) {
	messages := b.convertToResponsesMessages(request.Posts)
	tools := b.convertToResponsesTools(request, cfg)

	req := &schemas.BifrostResponsesRequest{
		Provider: b.provider,
		Model:    cfg.Model,
		Input:    messages,
	}

	// Set parameters
	params := &schemas.ResponsesParameters{}
	if cfg.MaxGeneratedTokens > 0 {
		params.MaxOutputTokens = new(cfg.MaxGeneratedTokens)
	}
	if len(tools) > 0 {
		params.Tools = tools
		if cfg.ToolsDisabled {
			none := string(schemas.ResponsesToolChoiceTypeNone)
			params.ToolChoice = &schemas.ResponsesToolChoice{ResponsesToolChoiceStr: &none}
		}
	}
	// Apply reasoning configuration
	params.Reasoning = b.buildResponsesReasoning(cfg)
	// Apply structured output (JSON schema) configuration
	if cfg.JSONOutputFormat != nil {
		textConfig, err := buildResponsesTextConfig(cfg.JSONOutputFormat)
		if err != nil {
			return nil, fmt.Errorf("failed to build responses text config: %w", err)
		}
		params.Text = textConfig
	}
	// The Anthropic provider reads cache_control from ExtraParams on the
	// Responses path (there is no typed field on ResponsesParameters).
	if b.promptCachingEnabled() {
		params.ExtraParams = map[string]any{
			"cache_control": &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral},
		}
	}
	req.Params = params

	// Attach fallback chain so Bifrost retries with alternative providers on failure.
	req.Fallbacks = b.fallbacks

	return req, nil
}

// streamResponses handles the streaming Responses API completion.
func (b *LLM) streamResponses(ctx context.Context, request llm.CompletionRequest, cfg llm.LanguageModelConfig, output chan<- llm.TextStreamEvent) {
	span := telemetry.SpanFromContext(ctx)
	span.SetAttributes(
		telemetry.LLMPath.String("responses"),
		telemetry.LLMUseResponsesAPI.Bool(b.useResponsesAPI),
	)
	bifrostCtx, cancel := schemas.NewBifrostContextWithTimeout(ctx, b.streamingTimeout*10)
	defer cancel()

	// Convert to Bifrost Responses API request
	bifrostReq, err := b.convertToBifrostResponsesRequest(request, cfg)
	if err != nil {
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: err,
		}
		return
	}
	if bifrostReq.Params != nil {
		recordResponsesReasoningSent(span, bifrostReq.Params.Reasoning)
	} else {
		recordResponsesReasoningSent(span, nil)
	}

	// Make streaming request
	streamChan, bifrostErr := b.client.ResponsesStreamRequest(bifrostCtx, bifrostReq)
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
	toolCallsBuffer := make(map[string]*toolCallBuffer)
	// outputIndexToFuncCallID maps a Responses-API output_index to the function
	// call_id that we accepted via OutputItemAdded for that index. Argument
	// deltas are routed through this map so deltas from non-function output
	// items (e.g. Anthropic native server tools like code_execution that
	// bifrost does not surface as OutputItemAdded events) do not bleed into
	// an unrelated function call's argument buffer.
	outputIndexToFuncCallID := make(map[int]string)

	// serverTools accumulates provider-executed tool activity (web search /
	// web fetch / code execution). Every state change re-emits the cumulative
	// snapshot so receivers can replace prior state.
	serverTools := newServerToolTracker()
	emitServerTools := func() {
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeServerToolUse,
			Value: serverTools.snapshot(),
		}
	}

	var reasoning reasoningAccumulator

	// Annotation buffer and text position tracking
	var annotations []llm.Annotation
	var fallbackSources []webSearchFallbackSource
	pendingAnnotationPositions := make(map[int][]pendingAnnotationPosition)
	var textLen int       // cumulative UTF-16 length of all streamed text
	var blockStartPos int // UTF-16 position where current text block started

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

		// Process Responses API stream response
		if chunk.BifrostResponsesStreamResponse != nil {
			resp := chunk.BifrostResponsesStreamResponse

			switch resp.Type {
			case schemas.ResponsesStreamResponseTypeOutputTextDelta:
				// Emit reasoning end before first text if we have accumulated reasoning
				reasoning.emitEnd(output)
				// Text delta
				if resp.Delta != nil && *resp.Delta != "" {
					output <- llm.TextStreamEvent{
						Type:  llm.EventTypeText,
						Value: *resp.Delta,
					}
					textLen += llm.UTF16CodeUnitCount(*resp.Delta)
				}

			case schemas.ResponsesStreamResponseTypeReasoningSummaryTextDelta:
				// Reasoning text chunk - stream immediately
				if resp.Delta != nil && *resp.Delta != "" {
					output <- llm.TextStreamEvent{
						Type:  llm.EventTypeReasoning,
						Value: *resp.Delta,
					}
					reasoning.buffer.WriteString(*resp.Delta)
				}
				// Capture signature if present
				if resp.Signature != nil && *resp.Signature != "" {
					reasoning.signature = *resp.Signature
				}

			case schemas.ResponsesStreamResponseTypeReasoningSummaryPartAdded,
				schemas.ResponsesStreamResponseTypeReasoningSummaryPartDone,
				schemas.ResponsesStreamResponseTypeReasoningSummaryTextDone:
				// These events mark progress but don't require action
				// Signature may come with these events
				if resp.Signature != nil && *resp.Signature != "" {
					reasoning.signature = *resp.Signature
				}

			case schemas.ResponsesStreamResponseTypeOutputTextAnnotationAdded:
				// Accumulate annotations as they arrive
				if resp.Annotation != nil {
					if ann := convertBifrostAnnotation(resp.Annotation, len(annotations)+1); ann != nil {
						// Bifrost doesn't provide output-text positions during Anthropic streaming.
						// Attach those citations to the current text block and correct the end
						// position when output_text.done arrives.
						missingStart := resp.Annotation.StartIndex == nil
						missingEnd := resp.Annotation.EndIndex == nil
						if resp.Annotation.StartIndex == nil {
							ann.StartIndex = blockStartPos
						}
						if resp.Annotation.EndIndex == nil {
							ann.EndIndex = textLen
						}
						annotations = append(annotations, *ann)
						if missingStart || missingEnd {
							contentIndex := missingContentIndex
							if resp.ContentIndex != nil {
								contentIndex = *resp.ContentIndex
							}
							pendingAnnotationPositions[contentIndex] = append(
								pendingAnnotationPositions[contentIndex],
								pendingAnnotationPosition{
									index:        len(annotations) - 1,
									missingStart: missingStart,
									missingEnd:   missingEnd,
								},
							)
						}
					}
				}

			case schemas.ResponsesStreamResponseTypeOutputTextAnnotationDone:
				// Annotation finalized - no additional action needed

			case schemas.ResponsesStreamResponseTypeOutputTextDone:
				// Text block complete - emit accumulated annotations and advance block position.
				// Keep the annotation buffer so subsequent output_text_done events can include
				// citations accumulated across the full response.
				contentIndex := missingContentIndex
				if resp.ContentIndex != nil {
					contentIndex = *resp.ContentIndex
				}
				flushPendingAnnotationPositions(
					annotations,
					pendingAnnotationPositions,
					contentIndex,
					blockStartPos,
					textLen,
				)
				if len(annotations) > 0 {
					output <- llm.TextStreamEvent{
						Type:  llm.EventTypeAnnotations,
						Value: annotations,
					}
				}
				blockStartPos = textLen

			case schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDelta:
				// Tool call arguments delta. Bifrost does not always populate
				// resp.Item on delta events, so the call_id is recovered via
				// the OutputIndex map populated by the preceding
				// OutputItemAdded event.
				//
				// Routing strictly by OutputIndex matters because providers
				// like Anthropic emit native server-tool blocks (e.g.
				// code_execution) for which Bifrost does not surface an
				// OutputItemAdded of type FunctionCall, but it still emits
				// FunctionCallArgumentsDelta events for them. Without this
				// guard, those orphan deltas were appended to whatever
				// function call most recently started, producing concatenated
				// JSON like `{"team_id":"…"}{"code":"…"}` that later failed
				// to marshal as a tool_use.input json.RawMessage.
				if resp.Item != nil && resp.Item.ResponsesToolMessage != nil {
					tm := resp.Item.ResponsesToolMessage
					callID := ""
					if tm.CallID != nil {
						callID = *tm.CallID
					}
					if callID != "" {
						if toolCallsBuffer[callID] == nil {
							toolCallsBuffer[callID] = &toolCallBuffer{id: callID}
						}
						if tm.Name != nil {
							toolCallsBuffer[callID].name = *tm.Name
						}
						if resp.Delta != nil {
							toolCallsBuffer[callID].arguments.WriteString(*resp.Delta)
						}
					}
				} else if resp.OutputIndex != nil && resp.Delta != nil {
					if callID, ok := outputIndexToFuncCallID[*resp.OutputIndex]; ok {
						if toolCallsBuffer[callID] == nil {
							toolCallsBuffer[callID] = &toolCallBuffer{id: callID}
						}
						toolCallsBuffer[callID].arguments.WriteString(*resp.Delta)
					}
				}

			case schemas.ResponsesStreamResponseTypeCodeInterpreterCallCodeDone:
				// Sandbox code/command finalized before execution starts —
				// surface it so the activity card can show what is running.
				if resp.ItemID != nil && resp.Code != nil && serverTools.setCommand(*resp.ItemID, *resp.Code) {
					emitServerTools()
				}

			case schemas.ResponsesStreamResponseTypeOutputItemAdded:
				// Server tool started (web_search_call / web_fetch_call /
				// code_interpreter_call) — track and surface the activity.
				if serverTools.observeItem(resp.Item) {
					emitServerTools()
				}
				// New output item added - register function calls so their
				// argument deltas can be routed back to the right buffer by
				// OutputIndex.
				if resp.Item != nil && resp.Item.Type != nil {
					if *resp.Item.Type == schemas.ResponsesMessageTypeFunctionCall && resp.Item.ResponsesToolMessage != nil {
						tm := resp.Item.ResponsesToolMessage
						callID := ""
						if tm.CallID != nil {
							callID = *tm.CallID
						}
						if callID != "" {
							if resp.OutputIndex != nil {
								outputIndexToFuncCallID[*resp.OutputIndex] = callID
							}
							if toolCallsBuffer[callID] == nil {
								toolCallsBuffer[callID] = &toolCallBuffer{id: callID}
							}
							if tm.Name != nil {
								toolCallsBuffer[callID].name = *tm.Name
							}
							if tm.Arguments != nil {
								toolCallsBuffer[callID].arguments.WriteString(*tm.Arguments)
							}
						}
					}
				}

			case schemas.ResponsesStreamResponseTypeOutputItemDone:
				fallbackSources = appendFirstWebSearchFallbackSource(fallbackSources, resp.Item)
				// Server tool finished — fold the final payload (query,
				// resolved URL/title, stdout/stderr, error) into the activity.
				if serverTools.observeItem(resp.Item) {
					emitServerTools()
				}
				// Output item completed - finalize function call if any
				if resp.Item != nil && resp.Item.Type != nil {
					if *resp.Item.Type == schemas.ResponsesMessageTypeFunctionCall && resp.Item.ResponsesToolMessage != nil {
						tm := resp.Item.ResponsesToolMessage
						callID := ""
						if tm.CallID != nil {
							callID = *tm.CallID
						}
						if callID != "" && toolCallsBuffer[callID] != nil {
							buf := toolCallsBuffer[callID]
							// Update with final values if available
							if tm.Name != nil && *tm.Name != "" {
								buf.name = *tm.Name
							}
							if tm.Arguments != nil && *tm.Arguments != "" {
								buf.arguments.Reset()
								buf.arguments.WriteString(*tm.Arguments)
							}
						}
					}
				}

			case schemas.ResponsesStreamResponseTypeCompleted:
				// Emit any unsent reasoning
				reasoning.emitEnd(output)

				// Emit any accumulated annotations
				for contentIndex, positions := range pendingAnnotationPositions {
					applyPendingAnnotationPositions(annotations, positions, blockStartPos, textLen)
					delete(pendingAnnotationPositions, contentIndex)
				}
				if len(annotations) == 0 && len(fallbackSources) > 0 {
					annotations = buildFallbackAnnotations(fallbackSources, textLen)
				}
				if len(annotations) > 0 {
					output <- llm.TextStreamEvent{
						Type:  llm.EventTypeAnnotations,
						Value: annotations,
					}
				}

				// Response completed - emit tool calls if any, in sorted key order
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

				// Handle usage data from completed response
				if resp.Response != nil && resp.Response.Usage != nil {
					usage := convertResponsesUsage(resp.Response.Usage)
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
	}

	// If we have pending tool calls, emit them in sorted key order
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
