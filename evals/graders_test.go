// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLLMRubricEval calibrates the grader against outputs with a known verdict, so
// a grader prompt or model change that flips them is caught before it skews
// every other eval.
func TestLLMRubricEval(t *testing.T) {
	NumEvalsOrSkip(t)
	eval, err := NewEval()
	require.NoError(t, err)

	tests := []struct {
		name   string
		rubric string
		output string
		pass   bool
	}{
		{
			name:   "matching language",
			rubric: "The output is in spanish",
			output: "La comida es muy rico",
			pass:   true,
		},
		{
			name:   "emoji name stands for the emoji",
			rubric: "Names an emoji that depicts a cat or expresses love, such as a heart",
			output: "heart_eyes_cat",
			pass:   true,
		},
		{
			name:   "bare emoji name",
			rubric: "Names an emoji that is negative or sad",
			output: "disappointed",
			pass:   true,
		},
		{
			name:   "refusal does not satisfy a content rubric",
			rubric: "attributes each bug to a user",
			output: "I can't see the bug reports in this thread. Paste them here and I'll organize them:\n\n| Bug | Reported by |\n|-----|-------------|\n|     |             |",
			pass:   false,
		},
		{
			name:   "missing required detail",
			rubric: "mentions web search as a tool it can use",
			output: "I don't have any tools in this conversation. See https://docs.mattermost.com for what agents can do.",
			pass:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := eval.LLMRubric(tc.rubric, tc.output)
			require.NoError(t, err)

			t.Log("Rubric result:", result)
			assert.Equal(t, tc.pass, result.Pass)
			assert.NotEmpty(t, result.Reasoning)
		})
	}
}
