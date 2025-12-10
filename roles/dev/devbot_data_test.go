// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dev_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost/server/public/model"
)

// CreateDevBotThreadData creates thread data for DevBot conversation
func CreateDevBotThreadData(message string) *evals.ThreadExport {
	user := &model.User{
		Id:       "user1",
		Username: "devuser",
		Email:    "dev@example.com",
	}

	channel := &model.Channel{
		Id:          "channel1",
		Type:        model.ChannelTypeDirect,
		DisplayName: "Dev Discussion",
		Name:        "dev-discussion",
	}

	team := &model.Team{
		Id:          "team1",
		DisplayName: "Engineering Team",
		Name:        "engineering-team",
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
func ProcessStreamWithTools(t *testing.T, textStream *llm.TextStreamResult, post *model.Post, contextBuilder *llmcontext.Builder, bot *bots.Bot, threadData *evals.ThreadExport, conv *conversations.Conversations) (string, error) {
	result := ""

	for event := range textStream.Stream {
		switch event.Type {
		case llm.EventTypeText:
			if textChunk, ok := event.Value.(string); ok {
				result += textChunk
			}
		case llm.EventTypeError:
			if err, ok := event.Value.(error); ok {
				return "", err
			}
		case llm.EventTypeEnd:
			return result, nil
		case llm.EventTypeToolCalls:
			if toolCalls, ok := event.Value.([]llm.ToolCall); ok {
				t.Logf("TOOLS: Executing %d tool calls", len(toolCalls))
				return executeToolsAndGetFinalResponse(t, toolCalls, post, contextBuilder, bot, threadData, result, conv)
			}
			return "", fmt.Errorf("invalid tool calls format")
		}
	}

	return result, nil
}

// executeToolsAndGetFinalResponse executes tools and makes a second LLM call for the final response
func executeToolsAndGetFinalResponse(t *testing.T, toolCalls []llm.ToolCall, post *model.Post, contextBuilder *llmcontext.Builder, bot *bots.Bot, threadData *evals.ThreadExport, partialResult string, conv *conversations.Conversations) (string, error) {
	llmContext := contextBuilder.BuildLLMContextUserRequest(
		bot,
		threadData.RequestingUser(),
		threadData.Channel,
		contextBuilder.WithLLMContextDefaultTools(bot),
	)

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
		toolCalls[i].Result = result
		toolCalls[i].Status = llm.ToolCallStatusSuccess
	}

	posts := []llm.Post{
		{
			Role:    llm.PostRoleUser,
			Message: post.Message,
		},
		{
			Role:    llm.PostRoleBot,
			Message: partialResult,
			ToolUse: toolCalls,
		},
	}

	finalContext := contextBuilder.BuildLLMContextUserRequest(
		bot,
		threadData.RequestingUser(),
		threadData.Channel,
		contextBuilder.WithLLMContextNoTools(),
	)

	completionRequest := llm.CompletionRequest{
		Posts:   posts,
		Context: finalContext,
	}

	finalResult, err := bot.LLM().ChatCompletion(completionRequest)
	if err != nil {
		return "", fmt.Errorf("failed to get final chat completion: %w", err)
	}

	finalResponse, err := finalResult.ReadAll()
	if err != nil {
		return "", fmt.Errorf("failed to read final response: %w", err)
	}

	if finalResponse == "" {
		posts = append(posts, llm.Post{
			Role:    llm.PostRoleSystem,
			Message: "Please provide a comprehensive response based on the tool results above. Synthesize the information and answer the user's original question.",
		})

		retryRequest := llm.CompletionRequest{
			Posts:   posts,
			Context: finalContext,
		}

		retryResult, err := bot.LLM().ChatCompletion(retryRequest)
		if err != nil {
			return "", fmt.Errorf("failed to get retry chat completion after empty response: %w", err)
		}

		finalResponse, err = retryResult.ReadAll()
		if err != nil {
			return "", fmt.Errorf("failed to read retry response: %w", err)
		}
	}

	return finalResponse, nil
}
