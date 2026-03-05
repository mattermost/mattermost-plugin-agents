// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost-plugin-ai/bots"
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

// responseRootIDFromPost returns the root ID for responding in a thread.
// If the post is already in a thread, it returns the root; otherwise the post's own ID.
func responseRootIDFromPost(post *model.Post) string {
	if post.RootId != "" {
		return post.RootId
	}
	return post.Id
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
	toolsJSONValue, ok := toolsJSON.(string)
	if !ok {
		return errors.New("post pending tool calls not valid JSON")
	}
	unmarshalErr := json.Unmarshal([]byte(toolsJSONValue), &tools)
	if unmarshalErr != nil {
		return errors.New("post pending tool calls not valid JSON")
	}

	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)
	allowToolsInChannel := allowToolsInChannelFromPost(post)
	requesterID, _ := post.GetProp(streaming.LLMRequesterUserID).(string)
	toolCallKVKey := ""
	if !isDM {
		// Defense-in-depth: block channel tool calls if config flag is off
		if c.configProvider == nil || !c.configProvider.EnableChannelMentionToolCalling() {
			return ErrChannelToolCallingDisabled
		}
		// Block if the post doesn't have the allow_tools_in_channel prop set
		if !allowToolsInChannel {
			return errors.New("tool calling not allowed for this post")
		}
		if requesterID == "" {
			return errors.New("post missing requester id")
		}
		if requesterID != userID {
			return errors.New("only the original requester can approve/reject tool calls")
		}
		toolCallKVKey = streaming.ToolCallPrivateKVKey(post.Id, requesterID)
		if kvErr := c.mmClient.KVGet(toolCallKVKey, &tools); kvErr != nil {
			if mmapi.IsKVNotFound(kvErr) {
				return errors.New("post missing pending tool calls")
			}
			return fmt.Errorf("failed to load tool calls from KV store: %w", kvErr)
		}
	}

	// Extract web search context from conversation history to preserve citations
	webSearchParams := c.extractWebSearchContext(post)

	contextOpts := []llm.ContextOption{
		c.contextBuilder.WithLLMContextDefaultTools(bot),
	}
	if len(webSearchParams) > 0 {
		contextOpts = append(contextOpts, c.contextBuilder.WithLLMContextParameters(webSearchParams))
	}

	llmContext := c.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		channel,
		contextOpts...,
	)
	toolsDisabled := applyToolAvailability(llmContext, isDM, allowToolsInChannel)

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

	if !isDM {
		hasReviewableResult := slices.ContainsFunc(tools, func(tc llm.ToolCall) bool {
			return tc.Status == llm.ToolCallStatusSuccess || tc.Status == llm.ToolCallStatusError
		})
		if !hasReviewableResult {
			redactedTools := streaming.RedactToolCalls(tools)
			resolvedToolsJSON, marshalErr := json.Marshal(redactedTools)
			if marshalErr != nil {
				return fmt.Errorf("failed to marshal tool call results: %w", marshalErr)
			}
			post.AddProp(streaming.ToolCallProp, string(resolvedToolsJSON))
			post.AddProp(streaming.ToolCallRedactedProp, "true")
			post.DelProp(streaming.PendingToolResultProp)
			if updateErr := c.mmClient.UpdatePost(post); updateErr != nil {
				return fmt.Errorf("failed to update post with tool call results: %w", updateErr)
			}
			if deleteErr := c.mmClient.KVDelete(toolCallKVKey); deleteErr != nil {
				c.mmClient.LogError("Failed to delete tool call KV entry", "error", deleteErr, "post_id", post.Id, "kv_key", toolCallKVKey)
			}
			return nil
		}

		resultKVKey := streaming.ToolResultPrivateKVKey(post.Id, requesterID)
		if kvErr := c.mmClient.KVSet(resultKVKey, tools); kvErr != nil {
			return fmt.Errorf("failed to store tool call results: %w", kvErr)
		}

		redactedTools := streaming.RedactToolCalls(tools)
		resolvedToolsJSON, marshalErr := json.Marshal(redactedTools)
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal tool call results: %w", marshalErr)
		}
		post.AddProp(streaming.ToolCallProp, string(resolvedToolsJSON))
		post.AddProp(streaming.ToolCallRedactedProp, "true")
		post.AddProp(streaming.PendingToolResultProp, "true")
		// Persist web search context so HandleToolResult and subsequent messages can find it
		if params := llmContext.Parameters; len(params) > 0 {
			if _, hasWebSearch := params[mmtools.WebSearchContextKey]; hasWebSearch {
				webSearchJSON, marshalErr := json.Marshal(params)
				if marshalErr != nil {
					c.mmClient.LogError("Failed to marshal web search context", "error", marshalErr)
				} else {
					post.AddProp(streaming.WebSearchContextProp, string(webSearchJSON))
				}
			}
		}
		if updateErr := c.mmClient.UpdatePost(post); updateErr != nil {
			return fmt.Errorf("failed to update post with tool call results: %w", updateErr)
		}
		// Note: toolCallKVKey is NOT deleted here because the UI may still need to fetch
		// private tool call arguments during the result review stage. It will be deleted
		// in HandleToolResult after the flow completes.

		return nil
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
			}
		}
	}

	if updateErr := c.mmClient.UpdatePost(post); updateErr != nil {
		return fmt.Errorf("failed to update post with tool call results: %w", updateErr)
	}

	// Only continue if at least one tool call was successful
	if !slices.ContainsFunc(tools, func(tc llm.ToolCall) bool {
		return tc.Status == llm.ToolCallStatusSuccess
	}) {
		return nil
	}

	return c.completeAndStreamToolResponse(bot, user, channel, post, llmContext, toolsDisabled, allowToolsInChannel)
}

// HandleToolResult handles tool result approval after tool execution.
func (c *Conversations) HandleToolResult(userID string, post *model.Post, channel *model.Channel, acceptedToolIDs []string) error {
	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		return fmt.Errorf("unable to get bot")
	}

	if post.GetProp(streaming.PendingToolResultProp) == nil {
		return errors.New("post missing pending tool results")
	}

	requesterID, ok := post.GetProp(streaming.LLMRequesterUserID).(string)
	if !ok || requesterID == "" {
		return errors.New("post missing requester id")
	}
	if requesterID != userID {
		return errors.New("only the original requester can approve/reject tool results")
	}

	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)
	allowToolsInChannel := allowToolsInChannelFromPost(post)

	// Defense-in-depth: block channel tool results if config flag is off
	if !isDM {
		if c.configProvider == nil || !c.configProvider.EnableChannelMentionToolCalling() {
			return ErrChannelToolCallingDisabled
		}
		if !allowToolsInChannel {
			return errors.New("tool calling not allowed for this post")
		}
	}

	resultKVKey := streaming.ToolResultPrivateKVKey(post.Id, requesterID)
	toolCallKVKey := streaming.ToolCallPrivateKVKey(post.Id, requesterID)
	var tools []llm.ToolCall
	if kvErr := c.mmClient.KVGet(resultKVKey, &tools); kvErr != nil {
		if mmapi.IsKVNotFound(kvErr) {
			return errors.New("post missing pending tool results")
		}
		return fmt.Errorf("failed to load tool call results from KV store: %w", kvErr)
	}

	for i := range tools {
		if slices.Contains(acceptedToolIDs, tools[i].ID) {
			continue
		}
		tools[i].Result = ""
		tools[i].Status = llm.ToolCallStatusRejected
	}

	hasApprovedResults := slices.ContainsFunc(tools, func(tc llm.ToolCall) bool {
		return tc.Status != llm.ToolCallStatusRejected
	})
	if !hasApprovedResults {
		redactedTools := streaming.RedactToolCalls(tools)
		redactedToolsJSON, marshalErr := json.Marshal(redactedTools)
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal tool call results: %w", marshalErr)
		}
		post.AddProp(streaming.ToolCallProp, string(redactedToolsJSON))
		post.AddProp(streaming.ToolCallRedactedProp, "true")
		post.DelProp(streaming.PendingToolResultProp)
		if updateErr := c.mmClient.UpdatePost(post); updateErr != nil {
			return fmt.Errorf("failed to update post after tool result rejection: %w", updateErr)
		}
		c.deleteToolCallKVEntries(post.Id, resultKVKey, toolCallKVKey)
		return nil
	}

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return err
	}

	// Extract web search context from conversation history to preserve citations
	webSearchParams := c.extractWebSearchContext(post)

	contextOpts := []llm.ContextOption{
		c.contextBuilder.WithLLMContextDefaultTools(bot),
	}
	if len(webSearchParams) > 0 {
		contextOpts = append(contextOpts, c.contextBuilder.WithLLMContextParameters(webSearchParams))
	}

	llmContext := c.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		channel,
		contextOpts...,
	)
	toolsDisabled := applyToolAvailability(llmContext, isDM, allowToolsInChannel)

	resolvedToolsJSON, err := json.Marshal(tools)
	if err != nil {
		return fmt.Errorf("failed to marshal tool call results: %w", err)
	}

	// Build a copy of the post with full (unredacted) tool results for conversation context.
	// Clone() shares the Props map, so create a separate copy to avoid mutating the original.
	toolCallPostCopy := post.Clone()
	toolCallPostCopy.Props = make(model.StringInterface, len(post.GetProps()))
	for key, value := range post.GetProps() {
		toolCallPostCopy.Props[key] = value
	}
	toolCallPostCopy.AddProp(streaming.ToolCallProp, string(resolvedToolsJSON))

	// Update the original post: unredact tool calls and clear pending flags.
	// This makes the data available after page refresh and in future
	// conversation context without needing the KV store.
	post.AddProp(streaming.ToolCallProp, string(resolvedToolsJSON))
	post.DelProp(streaming.ToolCallRedactedProp)
	post.DelProp(streaming.PendingToolResultProp)
	// Persist web search context so subsequent messages in the thread preserve citations
	if params := llmContext.Parameters; len(params) > 0 {
		if _, hasWebSearch := params[mmtools.WebSearchContextKey]; hasWebSearch {
			webSearchJSON, marshalErr := json.Marshal(params)
			if marshalErr != nil {
				c.mmClient.LogError("Failed to marshal web search context", "error", marshalErr)
			} else {
				post.AddProp(streaming.WebSearchContextProp, string(webSearchJSON))
			}
		}
	}
	if updateErr := c.mmClient.UpdatePost(post); updateErr != nil {
		return fmt.Errorf("failed to update post after tool result approval: %w", updateErr)
	}

	if err := c.completeAndStreamToolResponse(bot, user, channel, toolCallPostCopy, llmContext, toolsDisabled, allowToolsInChannel); err != nil {
		return err
	}

	c.deleteToolCallKVEntries(post.Id, resultKVKey, toolCallKVKey)
	return nil
}

// completeAndStreamToolResponse builds the conversation history from the thread,
// runs LLM completion with tool results, and streams the response to a new post.
func (c *Conversations) completeAndStreamToolResponse(
	bot *bots.Bot,
	user *model.User,
	channel *model.Channel,
	toolCallPost *model.Post,
	llmContext *llm.Context,
	toolsDisabled bool,
	allowToolsInChannel bool,
) error {
	responseRootID := responseRootIDFromPost(toolCallPost)

	previousConversation, err := mmapi.GetThreadData(c.mmClient, responseRootID)
	if err != nil {
		return fmt.Errorf("failed to get previous conversation: %w", err)
	}
	previousConversation.CutoffBeforePostID(toolCallPost.Id)
	previousConversation.Posts = append(previousConversation.Posts, toolCallPost)

	posts, err := c.existingConversationToLLMPosts(bot, previousConversation, llmContext)
	if err != nil {
		return fmt.Errorf("failed to convert existing conversation to LLM posts: %w", err)
	}

	completionRequest := llm.CompletionRequest{
		Posts:            posts,
		Context:          llmContext,
		Operation:        llm.OperationConversationToolFollowup,
		OperationSubType: llm.SubTypeToolCall,
	}
	var opts []llm.LanguageModelOption
	if toolsDisabled {
		opts = append(opts, llm.WithToolsDisabled())
	}
	result, err := bot.LLM().ChatCompletion(completionRequest, opts...)
	if err != nil {
		return fmt.Errorf("failed to get chat completion: %w", err)
	}

	// Decorate the stream with web search annotations if available
	if webSearchData := mmtools.ConsumeWebSearchContexts(llmContext); len(webSearchData) > 0 {
		result = mmtools.DecorateStreamWithAnnotations(result, webSearchData, nil)
	}

	responsePost := &model.Post{
		ChannelId: channel.Id,
		RootId:    responseRootID,
	}
	setAllowToolsInChannelProp(responsePost, allowToolsInChannel)
	if err := c.streamingService.StreamToNewPost(context.Background(), bot.GetMMBot().UserId, user.Id, result, responsePost, toolCallPost.Id); err != nil {
		return fmt.Errorf("failed to stream result to new post: %w", err)
	}

	return nil
}

// deleteToolCallKVEntries cleans up KV store entries, logging any deletion errors.
func (c *Conversations) deleteToolCallKVEntries(postID string, kvKeys ...string) {
	for _, key := range kvKeys {
		if err := c.mmClient.KVDelete(key); err != nil {
			c.mmClient.LogError("Failed to delete KV entry", "error", err, "post_id", postID, "kv_key", key)
		}
	}
}
