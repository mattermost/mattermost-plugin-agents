// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type RubricResult struct {
	Reasoning string
	Score     float64
	Pass      bool
}

const llmRubricSystem = `You are grading output according to the specificed rebric. If the statemnt in the rubric is true, then the output passes the test. You must respond with a JSON object with this structure: {reasoning: string, score: number, pass: boolean}
Examples:
<Output>The steamclock is broken</Output>
<Rubric>The content contains the state of the clock</Rubric>
{"reasoning": "The output says the clock is broken", "score": 1.0, "pass": true}

<Output>I am sorry I can not find the thread you referenced</Output>
<Rubric>Contains a reference to the mentos project</Rubric>
{"reasoning": "The output contains a failure message instead of a reference to the mentos project", "score": 0.0, "pass": false}`

func (e *Eval) LLMRubric(rubric, output string) (*RubricResult, error) {
	req := llm.CompletionRequest{
		Posts: []llm.Post{
			{
				Role:    llm.PostRoleSystem,
				Message: llmRubricSystem,
			},
			{
				Role:    llm.PostRoleUser,
				Message: fmt.Sprintf("<Output>%s</Output>\n<Rubric>%s</Rubric>", output, rubric),
			},
		},
		Context: llm.NewContext(),
	}

	llmResult, gradeErr := e.GraderLLM.ChatCompletionNoStream(req, llm.WithMaxGeneratedTokens(1000), llm.WithJSONOutput[RubricResult]())
	if gradeErr != nil {
		return nil, fmt.Errorf("failed to grade with llm: %w", gradeErr)
	}

	rubricResult := RubricResult{}
	unmarshalErr := json.Unmarshal([]byte(llmResult), &rubricResult)
	if unmarshalErr != nil {
		return nil, fmt.Errorf("failed to unmarshal llm result: %w", unmarshalErr)
	}

	return &rubricResult, nil
}

func LLMRubricT(e *EvalT, rubric, output string) {
	e.Helper()
	result, err := e.LLMRubric(rubric, output)
	require.NoError(e.T, err)
	RecordScore(e, &EvalResult{
		Rubric:    rubric,
		Output:    output,
		Reasoning: result.Reasoning,
		Score:     result.Score,
		Pass:      result.Pass,
	})
	assert.True(e.T, result.Pass, "LLM Rubric Failed")
	assert.GreaterOrEqual(e.T, result.Score, 0.6, "LLM Rubric Score is too low")
}
