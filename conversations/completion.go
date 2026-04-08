// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
)

// BuildNewConversationPosts creates the post list for a new (non-threaded) conversation.
// Returns system prompt + user message posts.
func BuildNewConversationPosts(
	pr *llm.Prompts,
	context *llm.Context,
	userMessage llm.Post,
) ([]llm.Post, error) {
	prompt, err := pr.Format(prompts.PromptDirectMessageQuestionSystem, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format prompt: %w", err)
	}
	return []llm.Post{
		{Role: llm.PostRoleSystem, Message: prompt},
		userMessage,
	}, nil
}
