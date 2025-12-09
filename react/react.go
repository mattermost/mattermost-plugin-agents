// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package react

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost/server/public/model"
)

// React represents a command to generate an emoji reaction for a post
type React struct {
	llm     llm.LanguageModel
	prompts *llm.Prompts
}

// New creates a new React
func New(
	llm llm.LanguageModel,
	prompts *llm.Prompts,
) *React {
	return &React{
		llm:     llm,
		prompts: prompts,
	}
}

func (r *React) Resolve(message string, context *llm.Context) (string, error) {
	context.Parameters = map[string]any{"Message": message}

	// Format prompt for emoji selection
	prompt, err := r.prompts.Format(prompts.PromptEmojiSelectSystem, context)
	if err != nil {
		return "", fmt.Errorf("failed to format prompt: %w", err)
	}

	// Create completion request
	completionRequest := llm.CompletionRequest{
		Posts: []llm.Post{
			{
				Role:    llm.PostRoleSystem,
				Message: prompt,
			},
			{
				Role:    llm.PostRoleUser,
				Message: message,
			},
		},
		Context: context,
	}

	// Get emoji from LLM
	// Note: Using 1000 tokens to accommodate OpenAI Responses API overhead
	// which can consume tokens for internal processing before generating output
	emojiName, err := r.llm.ChatCompletionNoStream(completionRequest, llm.WithMaxGeneratedTokens(500), llm.WithReasoningDisabled(), llm.WithToolsDisabled())
	if err != nil {
		return "", fmt.Errorf("failed to get emoji from LLM: %w", err)
	}

	// Process the emoji name
	emojiName = strings.Trim(strings.TrimSpace(emojiName), ":")

	// Validate the emoji
	if _, found := model.GetSystemEmojiId(emojiName); !found {
		return "", fmt.Errorf("LLM returned something other than emoji: %s", emojiName)
	}

	return emojiName, nil
}
