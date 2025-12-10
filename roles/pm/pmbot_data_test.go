// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package pm_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/roles"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetModelsForComparison returns the list of models to compare
func GetModelsForComparison() []string {
	// Allow override via environment variable for specific model comparison
	// Supports comma-separated list: TEST_MODEL="gpt-4o,claude-3-5-sonnet,mattermodel-5.4"
	if envModel := os.Getenv("TEST_MODEL"); envModel != "" {
		models := strings.Split(envModel, ",")
		// Trim whitespace from each model name
		for i, model := range models {
			models[i] = strings.TrimSpace(model)
		}
		return models
	}

	// Default models for comparison
	return []string{
		"mattermodel-5.4",
		"gpt-4o",
		"gpt-4o-mini",
	}
}

// CreatePMBotThreadData creates thread data for PM conversation
func CreatePMBotThreadData(message string) *evals.ThreadExport {
	user := &model.User{
		Id:       "user1",
		Username: "pmuser",
		Email:    "pm@example.com",
	}

	channel := &model.Channel{
		Id:          "channel1",
		Type:        model.ChannelTypeDirect,
		DisplayName: "PM Discussion",
		Name:        "pm-discussion",
	}

	team := &model.Team{
		Id:          "team1",
		DisplayName: "Product Team",
		Name:        "product-team",
	}

	post := &model.Post{
		Id:        "post1",
		UserId:    user.Id,
		ChannelId: channel.Id,
		Message:   message,
		CreateAt:  model.GetMillis(),
	}

	return &evals.ThreadExport{
		Posts: map[string]*model.Post{
			post.Id: post,
		},
		Channel: channel,
		Team:    team,
		Users: map[string]*model.User{
			user.Id: user,
		},
		FileInfos: map[string]*model.FileInfo{},
		Files:     map[string][]byte{},
		PostList: &model.PostList{
			Order: []string{post.Id},
			Posts: map[string]*model.Post{
				post.Id: post,
			},
		},
	}
}

// ProcessStreamWithTools handles tool execution similar to BotAdapter.processStreamWithTools
func ProcessStreamWithTools(t *testing.T, textStream *llm.TextStreamResult, post *model.Post, contextBuilder *llmcontext.Builder, bot *bots.Bot, threadData *evals.ThreadExport, conv *conversations.Conversations) (*roles.StreamResult, error) {
	result := ""

	for event := range textStream.Stream {
		switch event.Type {
		case llm.EventTypeText:
			if textChunk, ok := event.Value.(string); ok {
				result += textChunk
			}
		case llm.EventTypeError:
			if err, ok := event.Value.(error); ok {
				return nil, err
			}
		case llm.EventTypeEnd:
			// If we got to the end without tool calls, return the result with no tool results
			return &roles.StreamResult{
				Response:    result,
				ToolResults: []string{},
			}, nil
		case llm.EventTypeToolCalls:
			// Execute tools and get final response
			if toolCalls, ok := event.Value.([]llm.ToolCall); ok {
				t.Logf("TOOLS: Executing %d tool calls", len(toolCalls))
				return executeToolsAndGetFinalResponse(t, toolCalls, post, contextBuilder, bot, threadData, result, conv)
			}
			return nil, fmt.Errorf("invalid tool calls format")
		}
	}

	return &roles.StreamResult{
		Response:    result,
		ToolResults: []string{},
	}, nil
}

// executeToolsAndGetFinalResponse executes tools and makes a second LLM call for the final response
func executeToolsAndGetFinalResponse(t *testing.T, toolCalls []llm.ToolCall, post *model.Post, contextBuilder *llmcontext.Builder, bot *bots.Bot, threadData *evals.ThreadExport, partialResult string, conv *conversations.Conversations) (*roles.StreamResult, error) {
	// Create LLM context for tool execution
	llmContext := contextBuilder.BuildLLMContextUserRequest(
		bot,
		threadData.RequestingUser(),
		threadData.Channel,
		contextBuilder.WithLLMContextDefaultTools(bot),
	)

	// Execute all tools automatically (approve all for evaluation)
	// Capture tool results for grounding validation
	var toolResults []string

	for i := range toolCalls {
		t.Logf("TOOLS: Executing tool %s", toolCalls[i].Name)
		result, err := llmContext.Tools.ResolveTool(toolCalls[i].Name, func(args any) error {
			return json.Unmarshal(toolCalls[i].Arguments, args)
		}, llmContext)
		if err != nil {
			t.Logf("TOOLS: Tool %s failed: %v", toolCalls[i].Name, err)
			toolCalls[i].Result = "Tool call failed"
			toolCalls[i].Status = llm.ToolCallStatusError
			continue
		}
		t.Logf("TOOLS: Tool %s completed successfully", toolCalls[i].Name)

		// Debug flag handling should be passed from caller
		debugFlag := false // Replace with actual flag
		if debugFlag {
			resultStr := fmt.Sprintf("%v", result)
			if len(resultStr) > 200 {
				t.Logf("DEBUG: Tool %s result: %s...", toolCalls[i].Name, resultStr[:200])
			} else {
				t.Logf("DEBUG: Tool %s result: %s", toolCalls[i].Name, resultStr)
			}
		}
		toolCalls[i].Result = result
		toolCalls[i].Status = llm.ToolCallStatusSuccess

		// Capture formatted tool result for grounding validation
		resultStr := fmt.Sprintf("%v", result)
		toolResults = append(toolResults, roles.FormatToolResultForGrounding(toolCalls[i].Name, resultStr))
	}

	// Create proper conversation structure for tool continuation.
	// The trick: Both Anthropic and OpenAI packages automatically handle tool results
	// when they see ToolUse in an ASSISTANT message with Result fields populated.
	// They will create the appropriate tool_result blocks (Anthropic) or tool messages (OpenAI).

	// Build the conversation posts - only need user message and assistant with tools
	posts := []llm.Post{
		// Original user message
		{
			Role:    llm.PostRoleUser,
			Message: post.Message,
		},
		// Assistant's response with tool calls AND results
		// The LLM packages will see the Result field is populated and handle it correctly
		{
			Role:    llm.PostRoleBot,
			Message: partialResult, // Any text generated before tools
			ToolUse: toolCalls,     // Tool calls with Result and Status now populated
		},
	}

	// Create context without tools for final call to avoid infinite recursion
	finalContext := contextBuilder.BuildLLMContextUserRequest(
		bot,
		threadData.RequestingUser(),
		threadData.Channel,
		contextBuilder.WithLLMContextNoTools(), // No tools for final response
	)

	// Make final LLM call with tool results
	completionRequest := llm.CompletionRequest{
		Posts:   posts,
		Context: finalContext,
	}

	finalResult, err := bot.LLM().ChatCompletion(completionRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to get final chat completion: %w", err)
	}

	// Process the final stream as text only (no more tool calls for evaluation)
	finalResponse, err := finalResult.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read final response: %w", err)
	}

	// If we got an empty response after tool execution, add a prompt to get a response
	if finalResponse == "" {
		// Add a system message to prompt for a response based on tool results
		posts = append(posts, llm.Post{
			Role:    llm.PostRoleSystem,
			Message: "Please provide a comprehensive response based on the tool results above. Synthesize the information and answer the user's original question.",
		})

		// Try once more with the additional prompt
		retryRequest := llm.CompletionRequest{
			Posts:   posts,
			Context: finalContext,
		}

		retryResult, err := bot.LLM().ChatCompletion(retryRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to get retry chat completion after empty response: %w", err)
		}

		finalResponse, err = retryResult.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("failed to read retry response: %w", err)
		}
	}

	return &roles.StreamResult{
		Response:    finalResponse,
		ToolResults: toolResults,
	}, nil
}
