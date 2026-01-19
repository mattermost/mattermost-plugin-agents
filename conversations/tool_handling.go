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
	"github.com/mattermost/mattermost-plugin-ai/mmtools"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost/server/public/model"
)

// extractWebSearchContext retrieves web search context from the thread
// The context may be stored on a previous post if multiple tool calls occurred
func (c *Conversations) extractWebSearchContext(currentPost *model.Post) map[string]interface{} {
	rootID := currentPost.RootId
	if rootID == "" {
		rootID = currentPost.Id
	}

	// Get thread to search for web search context in previous posts
	threadData, err := mmapi.GetThreadData(c.mmClient, rootID)
	if err != nil {
		c.mmClient.LogDebug("Unable to get thread data for web search context extraction", "error", err)
		return nil
	}

	// Search through posts in reverse order (most recent first) for web search context
	// We want the most recent context in case multiple searches occurred
	for i := len(threadData.Posts) - 1; i >= 0; i-- {
		post := threadData.Posts[i]
		webSearchContextProp := post.GetProp(streaming.WebSearchContextProp)
		if webSearchContextProp == nil {
			continue
		}

		webSearchContextJSON, ok := webSearchContextProp.(string)
		if !ok {
			c.mmClient.LogWarn("Web search context prop is not a string", "post_id", post.Id)
			continue
		}

		c.mmClient.LogDebug("Found web search context in thread",
			"current_post", currentPost.Id,
			"context_post", post.Id)

		return c.unmarshalWebSearchContext(webSearchContextJSON, post.Id)
	}

	c.mmClient.LogDebug("No web search context found in thread", "root_id", rootID)
	return nil
}

func (c *Conversations) unmarshalWebSearchContext(webSearchContextJSON string, postID string) map[string]interface{} {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(webSearchContextJSON), &params); err != nil {
		c.mmClient.LogError("Failed to unmarshal web search context", "error", err, "post_id", postID)
		return nil
	}

	// Reconstruct proper types for web search context values
	if raw, ok := params[mmtools.WebSearchContextKey]; ok {
		// Re-marshal and unmarshal to get proper types
		contextBytes, marshalErr := json.Marshal(raw)
		if marshalErr != nil {
			c.mmClient.LogError("Failed to re-marshal web search context", "error", marshalErr, "post_id", postID)
			return nil
		}

		var searchContexts []mmtools.WebSearchContextValue
		if unmarshalErr := json.Unmarshal(contextBytes, &searchContexts); unmarshalErr != nil {
			c.mmClient.LogError("Failed to unmarshal web search context values", "error", unmarshalErr, "post_id", postID)
			return nil
		}

		params[mmtools.WebSearchContextKey] = searchContexts

		c.mmClient.LogDebug("Reconstructed web search context",
			"post_id", postID,
			"num_contexts", len(searchContexts))
	}

	// Reconstruct allowed URLs
	if raw, ok := params[mmtools.WebSearchAllowedURLsKey]; ok {
		urlBytes, marshalErr := json.Marshal(raw)
		if marshalErr == nil {
			var allowedURLs []string
			if unmarshalErr := json.Unmarshal(urlBytes, &allowedURLs); unmarshalErr == nil {
				params[mmtools.WebSearchAllowedURLsKey] = allowedURLs
				c.mmClient.LogDebug("Reconstructed allowed URLs", "post_id", postID, "num_urls", len(allowedURLs))
			}
		}
	}

	// Reset search tracking for the new user request cycle
	// The count and executed queries should start fresh for each user question,
	// but we keep the search results and allowed URLs for context/citations
	params[mmtools.WebSearchCountKey] = 0
	params[mmtools.WebSearchExecutedQueriesKey] = []string{}
	c.mmClient.LogDebug("Reset web search tracking for new request cycle", "post_id", postID)

	return params
}

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

	// Extract web search context from conversation history to preserve citations
	webSearchParams := c.extractWebSearchContext(post)

	var contextOpts []llm.ContextOption
	contextOpts = append(contextOpts, c.contextBuilder.WithLLMContextDefaultTools(bot))
	if len(webSearchParams) > 0 {
		contextOpts = append(contextOpts, c.contextBuilder.WithLLMContextParameters(webSearchParams))
	}

	llmContext := c.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		channel,
		contextOpts...,
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

	// Persist web search context if it exists (so it's available for subsequent tool calls)
	if webSearchParams := llmContext.Parameters; len(webSearchParams) > 0 {
		if _, hasWebSearch := webSearchParams[mmtools.WebSearchContextKey]; hasWebSearch {
			webSearchJSON, marshalErr := json.Marshal(webSearchParams)
			if marshalErr != nil {
				c.mmClient.LogError("Failed to marshal web search context", "error", marshalErr)
			} else {
				post.AddProp(streaming.WebSearchContextProp, string(webSearchJSON))
				c.mmClient.LogDebug("Persisted web search context to post props",
					"post_id", post.Id,
					"has_results", hasWebSearch)
			}
		}
	}

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

	// Decorate the stream with web search annotations if available
	webSearchData := mmtools.ConsumeWebSearchContexts(llmContext)
	c.mmClient.LogDebug("Checking for web search data in HandleToolCall", "has_data", len(webSearchData) > 0, "num_contexts", len(webSearchData))
	if len(webSearchData) > 0 {
		flatResults := mmtools.FlattenWebSearchResults(webSearchData)
		c.mmClient.LogDebug("Flattened web search results", "num_results", len(flatResults))
		result = mmtools.DecorateStreamWithAnnotations(result, webSearchData, nil)
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
