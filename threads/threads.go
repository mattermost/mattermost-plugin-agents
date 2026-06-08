// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package threads

import (
	stdcontext "context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
)

// AnalyzeResult contains the result of a thread analysis call.
type AnalyzeResult struct {
	Stream         *llm.TextStreamResult
	ConversationID string
}

type Threads struct {
	llm         llm.LanguageModel
	prompts     *llm.Prompts
	client      mmapi.Client
	convService *conversation.Service
}

func New(
	llm llm.LanguageModel,
	prompts *llm.Prompts,
	client mmapi.Client,
	convService *conversation.Service,
) *Threads {
	return &Threads{
		llm:         llm,
		prompts:     prompts,
		client:      client,
		convService: convService,
	}
}

func (t *Threads) Summarize(ctx stdcontext.Context, threadRootID string, context *llm.Context, botID string, userID string) (*AnalyzeResult, error) {
	return t.Analyze(ctx, threadRootID, context, prompts.PromptSummarizeThreadSystem, botID, userID)
}

func (t *Threads) FindActionItems(ctx stdcontext.Context, threadRootID string, context *llm.Context, botID string, userID string) (*AnalyzeResult, error) {
	return t.Analyze(ctx, threadRootID, context, prompts.PromptFindActionItemsSystem, botID, userID)
}

func (t *Threads) FindOpenQuestions(ctx stdcontext.Context, threadRootID string, context *llm.Context, botID string, userID string) (*AnalyzeResult, error) {
	return t.Analyze(ctx, threadRootID, context, prompts.PromptFindOpenQuestionsSystem, botID, userID)
}

// Analyze performs thread analysis by creating a conversation entity, building a
// CompletionRequest from its turns, and calling the LLM with tools disabled.
func (t *Threads) Analyze(ctx stdcontext.Context, postIDToAnalyze string, context *llm.Context, promptName string, botID string, userID string) (*AnalyzeResult, error) {
	// Fetch and format thread data.
	threadData, err := mmapi.GetThreadData(t.client, postIDToAnalyze)
	if err != nil {
		return nil, fmt.Errorf("failed to get thread data: %w", err)
	}
	formattedThread := format.ThreadData(threadData)
	context.Parameters = map[string]any{"Thread": formattedThread}

	// Determine system and user prompt template names.
	systemPromptName, userPromptName := resolvePromptNames(promptName)

	systemPrompt, err := t.prompts.Format(systemPromptName, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format system prompt: %w", err)
	}

	userPrompt, err := t.prompts.Format(userPromptName, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format user prompt: %w", err)
	}

	// Create conversation entity with the system prompt and first user turn.
	createResult, err := t.convService.CreateConversation(conversation.CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		Operation:    llm.OperationThreadAnalysis,
		SystemPrompt: systemPrompt,
		UserMessage:  userPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	// Build CompletionRequest from stored conversation turns.
	conv, err := t.convService.GetConversation(createResult.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	request, err := t.convService.BuildCompletionRequest(conv, context)
	if err != nil {
		return nil, fmt.Errorf("failed to build completion request: %w", err)
	}
	request.OperationSubType = promptName

	// Call LLM with tools disabled.
	stream, err := t.llm.ChatCompletion(ctx, *request, llm.WithToolsDisabled())
	if err != nil {
		return nil, err
	}

	return &AnalyzeResult{
		Stream:         stream,
		ConversationID: createResult.ConversationID,
	}, nil
}

// resolvePromptNames returns the system and user prompt template names for the given
// analysis prompt name.
func resolvePromptNames(promptName string) (systemPromptName, userPromptName string) {
	switch promptName {
	case prompts.PromptFindActionItemsSystem:
		return prompts.PromptFindActionItemsSystem, prompts.PromptFindActionItemsUser
	case prompts.PromptFindOpenQuestionsSystem:
		return prompts.PromptFindOpenQuestionsSystem, prompts.PromptFindOpenQuestionsUser
	default:
		return prompts.PromptSummarizeThreadSystem, prompts.PromptThreadUser
	}
}
