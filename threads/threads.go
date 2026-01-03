// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package threads

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/format"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
)

type Threads struct {
	llm     llm.LanguageModel
	prompts *llm.Prompts
	client  mmapi.Client
}

func New(
	llm llm.LanguageModel,
	prompts *llm.Prompts,
	client mmapi.Client,
) *Threads {
	return &Threads{
		llm:     llm,
		prompts: prompts,
		client:  client,
	}
}

func (t *Threads) Summarize(threadRootID string, context *llm.Context) (*llm.TextStreamResult, error) {
	return t.Analyze(threadRootID, context, prompts.PromptSummarizeThreadSystem)
}

func (t *Threads) FindActionItems(threadRootID string, context *llm.Context) (*llm.TextStreamResult, error) {
	return t.Analyze(threadRootID, context, prompts.PromptFindActionItemsSystem)
}

func (t *Threads) FindOpenQuestions(threadRootID string, context *llm.Context) (*llm.TextStreamResult, error) {
	return t.Analyze(threadRootID, context, prompts.PromptFindOpenQuestionsSystem)
}

func (t *Threads) Analyze(postIDToAnalyze string, context *llm.Context, promptName string) (*llm.TextStreamResult, error) {
	posts, err := t.createInitalPosts(postIDToAnalyze, context, promptName)
	if err != nil {
		return nil, fmt.Errorf("failed to create initial posts: %w", err)
	}

	completionReqest := llm.CompletionRequest{
		Posts:   posts,
		Context: context,
	}
	analysisStream, err := t.llm.ChatCompletion(completionReqest, llm.WithToolsDisabled())
	if err != nil {
		return nil, err
	}

	return analysisStream, nil
}

func (t *Threads) FollowUpAnalyze(postIDToAnalyze string, context *llm.Context, promptName string) ([]llm.Post, error) {
	return t.createInitalPosts(postIDToAnalyze, context, promptName)
}

func (t *Threads) createInitalPosts(postIDToAnalyze string, context *llm.Context, promptName string) ([]llm.Post, error) {
	threadData, err := mmapi.GetThreadData(t.client, postIDToAnalyze)
	if err != nil {
		return nil, err
	}
	formattedThread := format.ThreadData(threadData)
	context.Parameters = map[string]any{"Thread": formattedThread}

	systemPromptName := prompts.PromptSummarizeThreadSystem
	userPromptName := prompts.PromptThreadUser
	switch promptName {
	case "summarize_thread":
		systemPromptName = prompts.PromptSummarizeThreadSystem
		userPromptName = prompts.PromptThreadUser
	case "action_items":
		systemPromptName = prompts.PromptFindActionItemsSystem
		userPromptName = prompts.PromptFindActionItemsUser
	case "open_questions":
		systemPromptName = prompts.PromptFindOpenQuestionsSystem
		userPromptName = prompts.PromptFindOpenQuestionsUser
	}
	systemPrompt, err := t.prompts.Format(systemPromptName, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format system prompt: %w", err)
	}

	userPrompt, err := t.prompts.Format(userPromptName, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format user prompt: %w", err)
	}

	posts := []llm.Post{
		{
			Role:    llm.PostRoleSystem,
			Message: systemPrompt,
		},
		{
			Role:    llm.PostRoleUser,
			Message: userPrompt,
		},
	}

	return posts, nil
}
