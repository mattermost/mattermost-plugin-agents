// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost/server/public/model"
)

// extractWebSearchContext retrieves web search context from the thread.
// The context may be stored on a previous post if multiple tool calls occurred.
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
