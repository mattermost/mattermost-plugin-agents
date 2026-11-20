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

	t.Run("rubrics graded together keep their own verdicts", func(t *testing.T) {
		rubrics := make([]string, len(tests))
		for i, tc := range tests {
			rubrics[i] = tc.rubric
		}
		output := "La comida es muy rico"
		results, err := eval.LLMRubrics(rubrics, output)
		require.NoError(t, err)
		require.Len(t, results, len(rubrics))
		t.Log("Rubric results:", results)
		assert.True(t, results[0].Pass, "the spanish rubric passes")
		for i := 1; i < len(rubrics); i++ {
			assert.False(t, results[i].Pass, "rubric %q does not apply to %q", rubrics[i], output)
		}
	})
}

func TestVerdictsInOrder(t *testing.T) {
	tests := []struct {
		name     string
		verdicts []rubricVerdict
		count    int
		want     []bool
		wantErr  bool
	}{
		{
			name:     "verdicts out of order are matched by rubric number",
			verdicts: []rubricVerdict{{Rubric: 2, Pass: false}, {Rubric: 1, Pass: true}},
			count:    2,
			want:     []bool{true, false},
		},
		{
			name:     "a missing verdict is an error",
			verdicts: []rubricVerdict{{Rubric: 1, Pass: true}},
			count:    2,
			wantErr:  true,
		},
		{
			name:     "a duplicate verdict is an error",
			verdicts: []rubricVerdict{{Rubric: 1, Pass: true}, {Rubric: 1, Pass: false}},
			count:    2,
			wantErr:  true,
		},
		{
			name:     "a verdict for an unknown rubric is an error",
			verdicts: []rubricVerdict{{Rubric: 1, Pass: true}, {Rubric: 3, Pass: true}},
			count:    2,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results, err := verdictsInOrder(tc.verdicts, tc.count)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			got := make([]bool, len(results))
			for i, r := range results {
				got[i] = r.Pass
			}
			assert.Equal(t, tc.want, got)
		})
	}
}
