// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
)

// CompletionOptions holds the configuration for how a completion should be run.
type CompletionOptions struct {
	ToolsDisabled          bool
	NativeWebSearchAllowed bool
}

// BuildLLMOptions converts CompletionOptions into an llm.LanguageModelOption slice.
func (o CompletionOptions) BuildLLMOptions() []llm.LanguageModelOption {
	var opts []llm.LanguageModelOption
	if o.ToolsDisabled {
		opts = append(opts, llm.WithToolsDisabled())
		if o.NativeWebSearchAllowed {
			opts = append(opts, llm.WithNativeWebSearchAllowed())
		}
	}
	return opts
}

// ExecuteCompletion runs an LLM completion with the given posts and context.
// This is the core agentic loop entry point — no side effects, no post streaming.
func ExecuteCompletion(
	lm llm.LanguageModel,
	posts []llm.Post,
	context *llm.Context,
	operation string,
	operationSubType string,
	opts ...llm.LanguageModelOption,
) (*llm.TextStreamResult, error) {
	request := llm.CompletionRequest{
		Posts:            posts,
		Context:          context,
		Operation:        operation,
		OperationSubType: operationSubType,
	}
	return lm.ChatCompletion(request, opts...)
}

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
