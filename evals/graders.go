// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type RubricResult struct {
	Reasoning string
	Score     float64
	Pass      bool
}

const llmRubricSystem = `You are grading the output of an AI agent that runs inside Mattermost against a rubric. The rubric is a statement about the output; the output passes when the statement is true.

- Judge only what the rubric states. Ignore qualities it does not mention.
- Read the output as a Mattermost user would see it: Markdown is rendered, and an emoji name such as "thumbsup" or ":heart_eyes_cat:" stands for the emoji it names.
- An output that refuses, errors, or asks for more information instead of doing the task fails any rubric about the task's content.

Respond with a JSON object with this structure: {reasoning: string, score: number, pass: boolean}. Write the reasoning before deciding; score is between 0 and 1.
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
		Context:   llm.NewContext(),
		Operation: llm.OperationEvalGrading,
	}

	llmResult, gradeErr := e.GraderLLM.ChatCompletionNoStream(context.Background(), req, llm.WithMaxGeneratedTokens(1000), llm.WithJSONOutput[RubricResult]())
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
	require.NoError(e, err, "failed to grade rubric %q", rubric)
	RecordScore(e, &EvalResult{
		Rubric:    rubric,
		Output:    output,
		Reasoning: result.Reasoning,
		Score:     result.Score,
		Pass:      result.Pass && result.Score >= minPassingScore,
	})
	// The verdict is already recorded above, so it bypasses EvalT's capture of
	// assertion failures.
	assert.True(e.T, result.Pass, "LLM Rubric Failed")
	assert.GreaterOrEqual(e.T, result.Score, minPassingScore, "LLM Rubric Score is too low")
}

// minPassingScore is the grader score below which a rubric fails even when
// the grader says it passes.
const minPassingScore = 0.6
