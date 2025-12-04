// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost/server/public/model"
)

// HandleToolCall handles tool call approval/rejection
func (c *Conversations) HandleToolCall(userID string, post *model.Post, channel *model.Channel, acceptedToolIDs []string) error {
	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		return fmt.Errorf("unable to get bot")
	}

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return err
	}

	toolsJSON := post.GetProp(streaming.ToolCallProp)
	if toolsJSON == nil {
		return errors.New("post missing pending tool calls")
	}

	var tools []llm.ToolCall
	unmarshalErr := json.Unmarshal([]byte(toolsJSON.(string)), &tools)
	if unmarshalErr != nil {
		return errors.New("post pending tool calls not valid JSON")
	}

	llmContext := c.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		channel,
		c.contextBuilder.WithLLMContextDefaultTools(bot),
	)

	for i := range tools {
		if slices.Contains(acceptedToolIDs, tools[i].ID) {
			result, resolveErr := llmContext.Tools.ResolveTool(tools[i].Name, func(args any) error {
				return json.Unmarshal(tools[i].Arguments, args)
			}, llmContext)
			if resolveErr != nil {
				// Maybe in the future we can return this to the user and have a retry. For now just tell the LLM it failed.
				tools[i].Result = "Tool call failed"
				tools[i].Status = llm.ToolCallStatusError
				continue
			}
			tools[i].Result = result
			tools[i].Status = llm.ToolCallStatusSuccess
		} else {
			tools[i].Result = "Tool call rejected by user"
			tools[i].Status = llm.ToolCallStatusRejected
		}
	}

	responseRootID := post.Id
	if post.RootId != "" {
		responseRootID = post.RootId
	}

	// Update post with the tool call results
	resolvedToolsJSON, err := json.Marshal(tools)
	if err != nil {
		return fmt.Errorf("failed to marshal tool call results: %w", err)
	}
	post.AddProp(streaming.ToolCallProp, string(resolvedToolsJSON))

	if updateErr := c.mmClient.UpdatePost(post); updateErr != nil {
		return fmt.Errorf("failed to update post with tool call results: %w", updateErr)
	}

	// Only continue if at lest one tool call was successful
	if !slices.ContainsFunc(tools, func(tc llm.ToolCall) bool {
		return tc.Status == llm.ToolCallStatusSuccess
	}) {
		return nil
	}

	previousConversation, err := mmapi.GetThreadData(c.mmClient, responseRootID)
	if err != nil {
		return fmt.Errorf("failed to get previous conversation: %w", err)
	}
	previousConversation.CutoffBeforePostID(post.Id)
	previousConversation.Posts = append(previousConversation.Posts, post)

	posts, err := c.existingConversationToLLMPosts(bot, previousConversation, llmContext)
	if err != nil {
		return fmt.Errorf("failed to convert existing conversation to LLM posts: %w", err)
	}

	completionRequest := llm.CompletionRequest{
		Posts:   posts,
		Context: llmContext,
	}
	result, err := bot.LLM().ChatCompletion(completionRequest)
	if err != nil {
		return fmt.Errorf("failed to get chat completion: %w", err)
	}

	responsePost := &model.Post{
		ChannelId: channel.Id,
		RootId:    responseRootID,
	}
	if err := c.streamingService.StreamToNewPost(context.Background(), bot.GetMMBot().UserId, user.Id, result, responsePost, post.Id); err != nil {
		return fmt.Errorf("failed to stream result to new post: %w", err)
	}

	return nil
}
