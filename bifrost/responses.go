// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"slices"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// convertToResponsesMessages converts posts normalized for rules to Bifrost ResponsesMessage format.
func convertToResponsesMessages(posts []llm.Post, rules messageRules) []schemas.ResponsesMessage {
	messages := make([]schemas.ResponsesMessage, 0, len(posts))

	for _, post := range posts {
		switch post.Role {
		case llm.PostRoleSystem:
			if isBlank(post.Message) {
				continue
			}
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
				parts := createResponsesMultimodalContent(post, rules.imageDimensionLimit)
				if len(parts) == 0 {
					continue
				}
				msg := schemas.ResponsesMessage{
					Role: new(schemas.ResponsesInputMessageRoleUser),
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: parts,
					},
				}
				messages = append(messages, msg)
			} else if !isBlank(post.Message) {
				msg := schemas.ResponsesMessage{
					Role: new(schemas.ResponsesInputMessageRoleUser),
					Content: &schemas.ResponsesMessageContent{
						ContentStr: new(post.Message),
					},
				}
				messages = append(messages, msg)
			}

		case llm.PostRoleBot:
			messages = append(messages, assistantReplayMessages(post)...)
			for _, tc := range replayableToolCalls(post.ToolUse) {
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
				// Mark failures with status "incomplete", never ResponsesToolMessage.Error:
				// the OpenAI converter forwards "error" verbatim and OpenAI rejects it,
				// while "status" is valid OpenAI wire and Anthropic maps it to is_error.
				if toolResultIsError(tc) {
					funcOutputMsg.Status = new("incomplete")
				}
				messages = append(messages, funcOutputMsg)
			}
		}
	}

	return messages
}

// assistantReplayMessages renders a persisted assistant post as Responses API
// input messages. With arrival-order segments, text and server tool activity
// interleave as they happened; otherwise the activity record precedes the text.
func assistantReplayMessages(post llm.Post) []schemas.ResponsesMessage {
	newTextMessage := func(text string) schemas.ResponsesMessage {
		return schemas.ResponsesMessage{
			Role: new(schemas.ResponsesInputMessageRoleAssistant),
			Content: &schemas.ResponsesMessageContent{
				ContentStr: new(text),
			},
		}
	}

	if len(post.AssistantSegments) == 0 {
		messages := make([]schemas.ResponsesMessage, 0, 2)
		if record := serverToolActivityRecord(post.ServerTools); record != "" {
			messages = append(messages, newTextMessage(record))
		}
		if !isBlank(post.Message) {
			messages = append(messages, newTextMessage(post.Message))
		}
		return messages
	}

	serverToolsByID := make(map[string]llm.ServerToolUse, len(post.ServerTools))
	for i := range post.ServerTools {
		serverToolsByID[post.ServerTools[i].ID] = post.ServerTools[i]
	}

	messages := make([]schemas.ResponsesMessage, 0, len(post.AssistantSegments))
	headerEmitted := false
	for _, segment := range post.AssistantSegments {
		switch segment.Kind {
		case llm.TurnSegmentText:
			messages = append(messages, newTextMessage(segment.Text))
		case llm.TurnSegmentServerTool:
			use, ok := serverToolsByID[segment.ServerToolID]
			if !ok {
				continue
			}
			line := serverToolActivityLine(&use)
			if line == "" {
				continue
			}
			// The header describes the whole turn, so it prefixes only the
			// first activity record instead of repeating per segment.
			if !headerEmitted {
				headerEmitted = true
				line = serverToolReplayHeader + "\n" + line
			}
			messages = append(messages, newTextMessage(line))
		}
	}
	return messages
}

// createResponsesMultimodalContent creates content blocks for Responses API messages with images.
func createResponsesMultimodalContent(post llm.Post, maxDim int) []schemas.ResponsesMessageContentBlock {
	return multimodalContent(post, maxDim,
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

// webToolResponsesTool builds a native web_search / web_fetch tool definition.
//
// Unless the agent explicitly enabled the code sandbox (code_interpreter —
// Anthropic's code_execution), AllowedCallers is pinned to "direct" —
// Anthropic's documented opt-out from dynamic filtering, which would otherwise
// auto-provision the code-execution sandbox on newer Claude models:
// https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool#dynamic-filtering
// Bifrost strips the field for providers that don't accept it; pinned by
// TestWebSearchAllowedCallersStrippedForOpenAI and
// TestAnthropicOnlyWebFetchDroppedForOpenAI.
func webToolResponsesTool(toolType schemas.ResponsesToolType, sandboxEnabled bool) schemas.ResponsesTool {
	tool := schemas.ResponsesTool{Type: toolType}
	if !sandboxEnabled {
		tool.AllowedCallers = []string{anthropicDirectToolCaller}
	}
	return tool
}

// convertToResponsesTools creates Responses API tools including the enabled
// native tools and function tools.
func convertToResponsesTools(request llm.CompletionRequest, cfg llm.LanguageModelConfig, nativeTools []string) []schemas.ResponsesTool {
	var result []schemas.ResponsesTool
	sandboxEnabled := slices.Contains(nativeTools, llm.NativeToolCodeInterpreter)

	// Add native tools (always add when configured, regardless of ToolsDisabled)
	for _, nativeTool := range nativeTools {
		switch nativeTool {
		case llm.NativeToolWebSearch:
			if cfg.SkipNativeWebSearch {
				continue
			}
			result = append(result, webToolResponsesTool(schemas.ResponsesToolTypeWebSearch, sandboxEnabled))
		case llm.NativeToolWebFetch:
			result = append(result, webToolResponsesTool(schemas.ResponsesToolTypeWebFetch, sandboxEnabled))
		case llm.NativeToolCodeInterpreter:
			// OpenAI rejects code_interpreter without a container; "auto"
			// provisions a fresh one per request. Anthropic ignores it.
			result = append(result, schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeCodeInterpreter,
				ResponsesToolCodeInterpreter: &schemas.ResponsesToolCodeInterpreter{
					Container: map[string]any{"type": "auto"},
				},
			})
		}
	}

	// When NativeWebSearchAllowed is true but web_search is not in enabledNativeTools,
	// add it dynamically. SkipNativeWebSearch is the license gate for that tool.
	if !cfg.SkipNativeWebSearch && cfg.NativeWebSearchAllowed && !slices.Contains(nativeTools, llm.NativeToolWebSearch) {
		result = append(result, webToolResponsesTool(schemas.ResponsesToolTypeWebSearch, sandboxEnabled))
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

// responsesReasoningFor creates the ResponsesParametersReasoning configuration
// for cfg.Model on the provider registered as name, or nil when no reasoning
// block should be sent.
func (b *LLM) responsesReasoningFor(name schemas.ModelProvider, cfg llm.LanguageModelConfig) *schemas.ResponsesParametersReasoning {
	provider := messagesAPIProvider(b.baseProvider(name), cfg.Model)
	entry := b.registration(name)
	// Bifrost sends a chat-only hop's Responses request as a chat completion
	// and keeps reasoning_effort, so it gets the chat path's reasoning below.
	if (provider == schemas.OpenAI || provider == schemas.Azure) && (entry == nil || !entry.chatOnly) {
		if b.thinkingOff(cfg) {
			return nil
		}
		// Enable reasoning summaries so the provider returns reasoning text in
		// the stream; without this OpenAI omits reasoning_summary events.
		return &schemas.ResponsesParametersReasoning{
			Effort:  new(standardEffort(b.reasoningEffort)),
			Summary: new("auto"),
		}
	}

	// Bifrost will route a Responses-API request to chat completions for
	// providers without native Responses support (e.g. Mistral). Those
	// providers don't accept reasoning_effort, so providerReasoning drops it
	// for them too.
	params, ok := b.providerReasoning(provider, cfg)
	if !ok {
		return nil
	}
	reasoning := &schemas.ResponsesParametersReasoning{Effort: params.effort, MaxTokens: params.maxTokens}
	switch {
	case params.hideThinking:
		// Bifrost maps summary "none" to Anthropic's thinking.display "omitted".
		reasoning.Summary = new("none")
	case provider == schemas.Gemini || provider == schemas.Vertex:
		// Enable summary so Gemini/Vertex return reasoning text in the stream.
		reasoning.Summary = new("auto")
	}
	return reasoning
}

// convertToBifrostResponsesRequest converts our CompletionRequest to Bifrost's
// Responses API format, built for the primary. Bifrost replays it to each
// fallback, whose body the hopRequestPlugin rebuilds with
// responsesRequestForHop.
func (b *LLM) convertToBifrostResponsesRequest(request llm.CompletionRequest, cfg llm.LanguageModelConfig) (*schemas.BifrostResponsesRequest, error) {
	messages, params, err := b.responsesRequestForHop(request, cfg, b.provider, cfg.Model)
	if err != nil {
		return nil, err
	}
	return &schemas.BifrostResponsesRequest{
		Provider: b.provider,
		Model:    cfg.Model,
		Input:    messages,
		Params:   params,
		// Attach fallback chain so Bifrost retries with alternative providers on failure.
		Fallbacks: b.requestFallbacks(request),
	}, nil
}

// responsesRequestForHop returns the messages and parameters for model on the
// provider registered as name, shaped for that hop's own rules.
func (b *LLM) responsesRequestForHop(request llm.CompletionRequest, cfg llm.LanguageModelConfig, name schemas.ModelProvider, model string) ([]schemas.ResponsesMessage, *schemas.ResponsesParameters, error) {
	rules := b.messageRulesFor(name, model, countRequestImages(request.Posts))
	messages := convertToResponsesMessages(normalizePosts(request.Posts, rules), rules)
	tools := convertToResponsesTools(request, cfg, b.enabledNativeTools)

	var params schemas.ResponsesParameters
	if len(tools) > 0 {
		params.Tools = tools
		if cfg.ToolsDisabled {
			none := string(schemas.ResponsesToolChoiceTypeNone)
			params.ToolChoice = &schemas.ResponsesToolChoice{ResponsesToolChoiceStr: &none}
		}
	}
	// Apply structured output (JSON schema) configuration
	if cfg.JSONOutputFormat != nil {
		textConfig, err := buildResponsesTextConfig(cfg.JSONOutputFormat)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build responses text config: %w", err)
		}
		params.Text = textConfig
	}
	// The Anthropic provider reads cache_control from ExtraParams on the
	// Responses path (there is no typed field on ResponsesParameters).
	if rules.promptCaching {
		params.ExtraParams = map[string]any{
			"cache_control": &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral},
		}
	}
	return messages, b.responsesHopParams(params, name, model, cfg), nil
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
	b.applyCompletionBetaHeaders(bifrostCtx)
	request = bufferFiles(request)
	b.withHopRequest(bifrostCtx, request, cfg)

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
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: providerError(span, "bifrost error", bifrostErr, b.redactionKeys()...),
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
	providerRoute := b.provider
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
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: providerError(span, "bifrost stream error", chunk.BifrostError, b.redactionKeys()...),
			}
			return
		}

		// Process Responses API stream response
		if chunk.BifrostResponsesStreamResponse != nil {
			resp := chunk.BifrostResponsesStreamResponse
			if routed := resp.ExtraFields.RoutingInfo.Provider; routed != "" {
				providerRoute = routed
			} else if resp.Response != nil && resp.Response.ExtraFields.RoutingInfo.Provider != "" {
				providerRoute = resp.Response.ExtraFields.RoutingInfo.Provider
			}

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
				if serverTools.observeItem(resp.Item, providerRoute) {
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
				if serverTools.observeItem(resp.Item, providerRoute) {
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

				// Usage comes before the tool calls: a round that ends in
				// tool calls returns below, and it must still be counted.
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

				// Response completed - emit tool calls if any, in sorted key order
				if len(toolCallsBuffer) > 0 {
					toolCalls = flushToolCallBuffers(toolCallsBuffer, true)
				}
				if reason := responsesFinishReason(resp.Response, len(toolCalls) > 0); reason != "" {
					span.SetAttributes(telemetry.LLMFinishReason.String(reason))
				}
				if len(toolCalls) > 0 {
					output <- llm.TextStreamEvent{
						Type:  llm.EventTypeToolCalls,
						Value: toolCalls,
					}
					return
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

// responsesFinishReason reports why a Responses API turn ended: the
// provider's stop reason when it sends one, else the incomplete reason,
// else "tool_calls" or the response status.
func responsesFinishReason(resp *schemas.BifrostResponsesResponse, hasToolCalls bool) string {
	if resp != nil && resp.StopReason != nil && *resp.StopReason != "" {
		return *resp.StopReason
	}
	if resp != nil && resp.IncompleteDetails != nil && resp.IncompleteDetails.Reason != "" {
		return resp.IncompleteDetails.Reason
	}
	if hasToolCalls {
		return "tool_calls"
	}
	if resp != nil && resp.Status != nil {
		return *resp.Status
	}
	return ""
}
