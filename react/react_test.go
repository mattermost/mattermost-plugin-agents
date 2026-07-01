// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package react_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/react"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestReactResolve(t *testing.T) {
	tests := []struct {
		name          string
		message       string
		llmResponse   string
		llmError      error
		expectedEmoji string
		expectedError bool
		errorContains string
	}{
		{
			name:          "success",
			message:       "Great job on the presentation!",
			llmResponse:   "thumbsup",
			llmError:      nil,
			expectedEmoji: "thumbsup",
			expectedError: false,
		},
		{
			name:          "invalid emoji",
			message:       "Great job on the presentation!",
			llmResponse:   "not_an_emoji",
			llmError:      nil,
			expectedEmoji: "",
			expectedError: true,
			errorContains: "LLM returned something other than emoji",
		},
		{
			name:          "llm error",
			message:       "Great job on the presentation!",
			llmResponse:   "",
			llmError:      errors.New("llm error"),
			expectedEmoji: "",
			expectedError: true,
			errorContains: "failed to get emoji from LLM",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			mockLLM := mocks.NewMockLanguageModel(t)
			prompts, err := llm.NewPrompts(prompts.PromptsFolder)
			assert.NoError(t, err)

			mockLLM.EXPECT().ChatCompletionNoStream(mock.Anything, mock.Anything, mock.Anything).Return(tc.llmResponse, tc.llmError)

			r := react.New(mockLLM, prompts)
			llmCtx := llm.NewContext()

			// Execute
			emoji, err := r.Resolve(context.Background(), tc.message, llmCtx)

			// Assert
			if tc.expectedError {
				assert.Error(t, err)
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedEmoji, emoji)
			}
		})
	}
}

func TestReactEval(t *testing.T) {
	tests := []struct {
		name    string
		message string
		rubric  string
	}{
		{
			name:    "positive message",
			message: "Great job on the presentation! How is it going with yours?",
			rubric:  "The word/emoji is positive",
		},
		{
			name:    "negative message",
			message: "I'm disappointed with your performance on this project.",
			rubric:  "The word/emoji is negative or sad",
		},
		{
			name:    "cat message",
			message: "I just love cats! They are so cute and cuddly.",
			rubric:  "The word/emoji is a cat emoji or a heart/love emoji",
		},
	}

	for _, tc := range tests {
		evals.Run(t, "react "+tc.name, func(t *evals.EvalT) {
			r := react.New(t.LLM, t.Prompts)
			llmContext := llm.NewContext()

			result, err := r.Resolve(context.Background(), tc.message, llmContext)

			require.NoError(t, err)
			assert.NotEmpty(t, result, "Expected a non-empty emoji reaction")
			evals.LLMRubricT(t, tc.rubric, result)
		})
	}
}
