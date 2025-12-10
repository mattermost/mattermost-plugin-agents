// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package baseline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/datasources"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/mmtools/types"
	"github.com/mattermost/mattermost/server/public/model"
)

// BotAdapter wraps existing bots.Bot to implement the baseline.Bot interface
// without modifying any existing code.
type BotAdapter struct {
	bot               *bots.Bot
	conv              *conversations.Conversations
	contextBuilder    *llmcontext.Builder
	prompts           *llm.Prompts // For formatting PM bot system prompts
	pluginAPI         mmapi.Client
	name              string
	threadData        *evals.ThreadExport
	verbose           bool
	toolProvider      ToolMetadataProvider // For extracting tool metadata
	systemPrompt      string               // Store system prompt from first call
	LastResponse      string               // Store last response for debugging
	LastToolCalls     []ToolCall           // Store last tool calls for debugging
	LastFirstLLMCall  string               // Store the first LLM call prompt (before tool execution)
	LastSecondLLMCall string               // Store the second LLM call prompt (after tool execution)
}

// ToolMetadataProvider defines the interface for extracting tool metadata
type ToolMetadataProvider interface {
	GetToolMetadata(toolName string) (types.ToolMetadata, bool)
	GetSupportedDataSources(toolName string) []string
	BuildSearchQueries(toolName, topic string) map[string]string
}

// ToolCall represents a tool invocation for debugging
type ToolCall struct {
	Name      string           `json:"name"`
	Arguments interface{}      `json:"arguments,omitempty"`
	Result    interface{}      `json:"result,omitempty"`
	Metadata  ToolCallMetadata `json:"metadata,omitempty"`
}

// ToolCallMetadata captures query generation and tool selection metadata
type ToolCallMetadata struct {
	SupportedDataSources []string          `json:"supported_data_sources,omitempty"`
	IntentKeywords       []string          `json:"intent_keywords,omitempty"`
	DetectedFeatures     []string          `json:"detected_features,omitempty"`
	ExpandedFeatures     []string          `json:"expanded_features,omitempty"`
	CompositeQueries     map[string]string `json:"composite_queries,omitempty"`
}

// NewBotAdapter creates a new adapter that wraps an existing bot and conversation system.
func NewBotAdapter(
	bot *bots.Bot,
	conv *conversations.Conversations,
	contextBuilder *llmcontext.Builder,
	prompts *llm.Prompts,
	pluginAPI mmapi.Client,
	name string,
	threadData *evals.ThreadExport,
) *BotAdapter {
	// Extract system prompt from bot config or leave empty for PM bots using templates
	systemPrompt := bot.GetConfig().CustomInstructions

	return &BotAdapter{
		bot:            bot,
		conv:           conv,
		contextBuilder: contextBuilder,
		prompts:        prompts,
		pluginAPI:      pluginAPI,
		name:           name,
		threadData:     threadData,
		verbose:        false,
		toolProvider:   nil, // Will be set if needed
		systemPrompt:   systemPrompt,
	}
}

// SetToolProvider sets the tool metadata provider for capturing query information
func (ba *BotAdapter) SetToolProvider(provider ToolMetadataProvider) {
	ba.toolProvider = provider
}

// SetVerbose enables or disables verbose debug logging
func (ba *BotAdapter) SetVerbose(verbose bool) {
	ba.verbose = verbose
}

// Respond processes the message using the existing conversation system.
func (ba *BotAdapter) Respond(ctx context.Context, msg string) (Answer, error) {
	start := time.Now()

	if ba.verbose && ba.pluginAPI != nil {
		ba.pluginAPI.LogDebug("BotAdapter.Respond() called", "message_length", len(msg))
	}

	// Create a new post with the message
	post := &model.Post{
		Id:        "baseline-eval-post",
		UserId:    ba.threadData.RequestingUser().Id,
		ChannelId: ba.threadData.Channel.Id,
		Message:   msg,
		CreateAt:  model.GetMillis(),
	}

	// Capture the first LLM call (before tool execution)
	ba.LastFirstLLMCall = ba.buildFirstLLMCallSummary(post)

	if ba.verbose && ba.pluginAPI != nil {
		ba.pluginAPI.LogDebug("Calling conv.ProcessUserRequest")
	}

	textStream, err := ba.conv.ProcessUserRequest(
		ba.bot,
		ba.threadData.RequestingUser(),
		ba.threadData.Channel,
		post,
	)
	if err != nil {
		if ba.verbose && ba.pluginAPI != nil {
			ba.pluginAPI.LogDebug("ProcessUserRequest failed", "error", err.Error())
		}
		return Answer{}, err
	}

	if ba.verbose && ba.pluginAPI != nil {
		ba.pluginAPI.LogDebug("ProcessUserRequest succeeded, processing LLM response stream")
	}

	response, err := ba.processStreamWithTools(ctx, textStream, post)
	if err != nil {
		if ba.verbose && ba.pluginAPI != nil {
			ba.pluginAPI.LogDebug("Stream processing failed", "error", err.Error())
		}
		return Answer{}, err
	}

	// Store response for debugging
	ba.LastResponse = response

	if ba.verbose && ba.pluginAPI != nil {
		ba.pluginAPI.LogDebug("Response received", "length", len(response))
	}

	latency := time.Since(start)

	return Answer{
		Text:    response,
		Latency: latency,
		Tokens:  TokenUsage{},
		Metadata: map[string]interface{}{
			MetadataKeyModelType: ModelTypeEnhanced,
			MetadataKeyBotID:     ba.bot.GetConfig().ID,
		},
	}, nil
}

// processStreamWithTools handles the complete tool execution flow
func (ba *BotAdapter) processStreamWithTools(ctx context.Context, textStream *llm.TextStreamResult, post *model.Post) (string, error) {
	result := ""
	eventCount := 0
	hasToolCalls := false

	if ba.verbose && ba.pluginAPI != nil {
		ba.pluginAPI.LogDebug("Starting to process stream from rolebot handler")
	}

	for event := range textStream.Stream {
		eventCount++
		if ba.verbose && ba.pluginAPI != nil {
			ba.pluginAPI.LogDebug("Received stream event", "type", event.Type, "event_number", eventCount)
		}

		switch event.Type {
		case llm.EventTypeText:
			if textChunk, ok := event.Value.(string); ok {
				result += textChunk
				if ba.verbose && ba.pluginAPI != nil {
					ba.pluginAPI.LogDebug("Accumulated text", "chunk_length", len(textChunk), "total_length", len(result))
				}
			}
		case llm.EventTypeError:
			if err, ok := event.Value.(error); ok {
				if ba.verbose && ba.pluginAPI != nil {
					ba.pluginAPI.LogDebug("Stream error received", "error", err.Error())
				}
				return "", err
			}
		case llm.EventTypeEnd:
			// If we got to the end without tool calls, return the result
			if ba.verbose && ba.pluginAPI != nil {
				ba.pluginAPI.LogDebug("Stream ended", "total_events", eventCount, "had_tool_calls", hasToolCalls, "response_length", len(result))
			}
			return result, nil
		case llm.EventTypeToolCalls:
			// Execute tools and get final response
			if ba.verbose && ba.pluginAPI != nil {
				ba.pluginAPI.LogDebug("Starting tool execution")
			}
			if toolCalls, ok := event.Value.([]llm.ToolCall); ok {
				if ba.verbose && ba.pluginAPI != nil {
					ba.pluginAPI.LogDebug("Processing tool calls", "count", len(toolCalls))
				}
				return ba.executeToolsAndGetFinalResponse(ctx, toolCalls, post, result)
			}
			return "", fmt.Errorf("invalid tool calls format")
		}
	}

	if ba.verbose && ba.pluginAPI != nil {
		ba.pluginAPI.LogDebug("Stream channel closed", "total_events", eventCount, "response_length", len(result))
	}

	return result, nil
}

// executeToolsAndGetFinalResponse executes tools and makes a second LLM call for the final response
func (ba *BotAdapter) executeToolsAndGetFinalResponse(ctx context.Context, toolCalls []llm.ToolCall, post *model.Post, partialResult string) (string, error) {
	if ba.verbose && ba.pluginAPI != nil {
		ba.pluginAPI.LogDebug("Executing tool calls", "count", len(toolCalls))
	}

	// Clear previous tool calls
	ba.LastToolCalls = []ToolCall{}

	// Create LLM context for tool execution (similar to tool_handling.go)
	llmContext := ba.contextBuilder.BuildLLMContextUserRequest(
		ba.bot,
		ba.threadData.RequestingUser(),
		ba.threadData.Channel,
		ba.contextBuilder.WithLLMContextDefaultTools(ba.bot),
	)

	// Execute all tools automatically (approve all for evaluation)
	for i := range toolCalls {
		if ba.verbose && ba.pluginAPI != nil {
			ba.pluginAPI.LogDebug("Executing tool", "name", toolCalls[i].Name)
		}

		// Parse arguments to understand the keywords being used
		var args map[string]interface{}
		if err := json.Unmarshal(toolCalls[i].Arguments, &args); err == nil {
			if topic, ok := args["topic"].(string); ok {
				if ba.verbose && ba.pluginAPI != nil {
					ba.pluginAPI.LogDebug("Tool topic keyword", "tool", toolCalls[i].Name, "topic", topic)
				}
			}
			if framework, ok := args["framework"].(string); ok {
				if ba.verbose && ba.pluginAPI != nil {
					ba.pluginAPI.LogDebug("Tool framework keyword", "tool", toolCalls[i].Name, "framework", framework)
				}
			}
		}

		// Extract tool metadata before execution
		metadata := ba.extractToolMetadata(toolCalls[i].Name, args)

		result, err := llmContext.Tools.ResolveTool(toolCalls[i].Name, func(args any) error {
			return json.Unmarshal(toolCalls[i].Arguments, args)
		}, llmContext)

		// Capture tool call for debugging
		toolCallDebug := ToolCall{
			Name:      toolCalls[i].Name,
			Arguments: args,
			Metadata:  metadata,
		}

		if err != nil {
			if ba.verbose && ba.pluginAPI != nil {
				ba.pluginAPI.LogDebug("Tool failed", "name", toolCalls[i].Name, "error", err.Error())
			}
			toolCalls[i].Result = "Tool call failed"
			toolCalls[i].Status = llm.ToolCallStatusError
			toolCallDebug.Result = fmt.Sprintf("Error: %v", err)
		} else {
			toolCalls[i].Result = result
			toolCallDebug.Result = result
			toolCalls[i].Status = llm.ToolCallStatusSuccess
			if ba.verbose && ba.pluginAPI != nil {
				ba.pluginAPI.LogDebug("Tool succeeded", "name", toolCalls[i].Name, "result_length", len(result))
			}
		}

		ba.LastToolCalls = append(ba.LastToolCalls, toolCallDebug)
	}

	// Create proper message sequence for the second LLM call:
	// 1. System prompt (crucial for PM bots to follow template instructions!)
	// 2. User message (original query)
	// 3. Assistant message with tool calls and results
	posts := []llm.Post{}

	// Include system prompt - reuse the SAME prompt from first LLM call
	// PM bots detect intent once in ProcessUserRequest and save it to post props
	systemPrompt := ba.systemPrompt
	if systemPrompt == "" && ba.prompts != nil {
		// Get the saved intent from post props (set by PM conversation handler in first call)
		if savedIntent, ok := post.GetProp(conversations.PromptTypeProp).(string); ok && savedIntent != "" {
			if ba.verbose && ba.pluginAPI != nil {
				ba.pluginAPI.LogDebug("Reusing saved intent from first LLM call", "intent", savedIntent)
			}

			// Format using the SAME intent that was used in the first call
			promptContext := ba.contextBuilder.BuildLLMContextUserRequest(
				ba.bot,
				ba.threadData.RequestingUser(),
				ba.threadData.Channel,
			)

			formatted, err := ba.prompts.Format(savedIntent, promptContext)
			if err == nil {
				systemPrompt = formatted
				if ba.verbose && ba.pluginAPI != nil {
					ba.pluginAPI.LogDebug("Reused system prompt from first call", "intent", savedIntent, "length", len(systemPrompt))
				}
			} else {
				if ba.verbose && ba.pluginAPI != nil {
					ba.pluginAPI.LogDebug("Failed to format system prompt with saved intent", "intent", savedIntent, "error", err.Error())
				}
				systemPrompt = ba.systemPrompt
			}
		}
	}

	// Add system prompt if we have one
	if systemPrompt != "" {
		posts = append(posts, llm.Post{
			Role:    llm.PostRoleSystem,
			Message: systemPrompt,
		})
	}

	// Add user message and assistant response with tool calls
	posts = append(posts,
		llm.Post{
			Role:    llm.PostRoleUser,
			Message: post.Message,
		},
		llm.Post{
			Role:    llm.PostRoleBot,
			Message: partialResult, // Include any text the assistant generated before tool calls
			ToolUse: toolCalls,     // Attach tool calls to assistant message
		},
	)

	// Create context without tools for final call to avoid infinite recursion
	finalContext := ba.contextBuilder.BuildLLMContextUserRequest(
		ba.bot,
		ba.threadData.RequestingUser(),
		ba.threadData.Channel,
		ba.contextBuilder.WithLLMContextNoTools(), // No tools for final response
	)

	// Capture the second LLM call (after tool execution)
	ba.LastSecondLLMCall = ba.buildSecondLLMCallSummary(posts, toolCalls)

	// Make final LLM call with tool results
	completionRequest := llm.CompletionRequest{
		Posts:   posts,
		Context: finalContext,
	}

	if ba.verbose && ba.pluginAPI != nil {
		ba.pluginAPI.LogDebug("Making final LLM call with tool results",
			"requesting_user", llmContext.RequestingUser.Username,
			"tools_count", len(llmContext.Tools.GetTools()))
	}
	// Temperature is controlled by the evaluation framework's LLM provider configuration
	finalResult, err := ba.bot.LLM().ChatCompletion(completionRequest)
	if err != nil {
		return "", fmt.Errorf("failed to get final chat completion: %w", err)
	}

	// Process the final stream manually to handle any unexpected tool calls
	finalResponse := ""
streamLoop:
	for event := range finalResult.Stream {
		switch event.Type {
		case llm.EventTypeText:
			if textChunk, ok := event.Value.(string); ok {
				finalResponse += textChunk
			}
		case llm.EventTypeError:
			if err, ok := event.Value.(error); ok {
				return "", fmt.Errorf("error in final response stream: %w", err)
			}
		case llm.EventTypeEnd:
			// Stream ended normally - exit the loop completely
			break streamLoop
		case llm.EventTypeToolCalls:
			// This shouldn't happen with WithLLMContextNoTools(), but handle it gracefully
			if ba.verbose && ba.pluginAPI != nil {
				ba.pluginAPI.LogDebug("Unexpected tool calls in final response, ignoring them")
			}
			// Continue processing to get any text that was generated
		}
	}

	if ba.verbose && ba.pluginAPI != nil {
		ba.pluginAPI.LogDebug("Final response received", "length", len(finalResponse))
	}
	return finalResponse, nil
}

// Name returns the bot's identifier for results tracking.
func (ba *BotAdapter) Name() string {
	return ba.name
}

// GetSystemPrompt returns the bot's custom instructions (system prompt)
func (ba *BotAdapter) GetSystemPrompt() string {
	return ba.bot.GetConfig().CustomInstructions
}

// buildFirstLLMCallSummary creates a summary of the first LLM call (before tool execution)
func (ba *BotAdapter) buildFirstLLMCallSummary(post *model.Post) string {
	var summary strings.Builder

	summary.WriteString("=== FIRST LLM CALL (Tool Selection) ===\n\n")
	summary.WriteString("--- System Prompt ---\n")

	// Note: PM bots use template system which injects system prompt in the conversation flow
	// For testing purposes, we show CustomInstructions if available (baseline bot) or note template usage
	systemPrompt := ba.bot.GetConfig().CustomInstructions
	if systemPrompt == "" {
		summary.WriteString("(Template-based prompt - injected via PM conversation handler)\n")
	} else {
		summary.WriteString(systemPrompt)
	}

	summary.WriteString("\n\n--- User Message ---\n")
	summary.WriteString(post.Message)
	summary.WriteString("\n\n--- Available Tools ---\n")
	summary.WriteString("(Tools are provided to LLM with their schemas for selection)\n")

	return summary.String()
}

// buildSecondLLMCallSummary creates a summary of the second LLM call (after tool execution)
func (ba *BotAdapter) buildSecondLLMCallSummary(posts []llm.Post, toolCalls []llm.ToolCall) string {
	var summary strings.Builder

	summary.WriteString("=== SECOND LLM CALL (Final Response Generation) ===\n\n")
	summary.WriteString("--- System Prompt ---\n")

	// Extract system prompt from posts (for PM bots using template system)
	systemPrompt := ba.bot.GetConfig().CustomInstructions
	for _, post := range posts {
		if post.Role == llm.PostRoleSystem {
			systemPrompt = post.Message
			break
		}
	}
	summary.WriteString(systemPrompt)
	summary.WriteString("\n\n")

	for i, post := range posts {
		// Skip system role posts as they're already shown above
		if post.Role == llm.PostRoleSystem {
			continue
		}
		summary.WriteString(fmt.Sprintf("--- Message %d (Role: %v) ---\n", i+1, post.Role))
		if post.Message != "" {
			summary.WriteString(fmt.Sprintf("%s\n\n", post.Message))
		}

		if len(post.ToolUse) > 0 {
			summary.WriteString(fmt.Sprintf("Tool Calls Executed: %d\n", len(post.ToolUse)))
			for j, tc := range post.ToolUse {
				summary.WriteString(fmt.Sprintf("\n--- Tool Call %d ---\n", j+1))
				summary.WriteString(fmt.Sprintf("Name: %s\n", tc.Name))
				summary.WriteString(fmt.Sprintf("Status: %v\n", tc.Status))
				if len(tc.Result) > 0 {
					summary.WriteString(fmt.Sprintf("Result:\n%s\n", tc.Result))
				}
			}
			summary.WriteString("\n")
		}
	}

	return summary.String()
}

// extractToolMetadata extracts metadata about tool execution including query generation
func (ba *BotAdapter) extractToolMetadata(toolName string, args map[string]interface{}) ToolCallMetadata {
	metadata := ToolCallMetadata{
		CompositeQueries: make(map[string]string),
	}

	// Skip if no tool provider is set
	if ba.toolProvider == nil {
		return metadata
	}

	// Extract supported data sources
	metadata.SupportedDataSources = ba.toolProvider.GetSupportedDataSources(toolName)

	// Get tool metadata for intent keywords
	if toolMeta, exists := ba.toolProvider.GetToolMetadata(toolName); exists {
		metadata.IntentKeywords = toolMeta.IntentKeywords
	}

	// Extract topic from args if available
	topic := ""
	if topicVal, exists := args["topic"]; exists {
		if topicStr, ok := topicVal.(string); ok {
			topic = topicStr
		}
	}

	// Extract and expand features if topic is available
	if topic != "" {
		analyzer := datasources.NewTopicAnalyzer()

		// Detect features from topic
		metadata.DetectedFeatures = analyzer.GetMattermostFeatures(topic)

		// Expand features to include synonyms
		if len(metadata.DetectedFeatures) > 0 {
			metadata.ExpandedFeatures = analyzer.ExpandMattermostFeatureSynonyms(metadata.DetectedFeatures)
		}

		// Build composite queries
		metadata.CompositeQueries = ba.toolProvider.BuildSearchQueries(toolName, topic)
	}

	return metadata
}
