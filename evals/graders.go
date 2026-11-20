// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type RubricResult struct {
	Reasoning string
	Score     float64
	Pass      bool
}

// rubricVerdict is the grader's verdict on one numbered rubric.
type rubricVerdict struct {
	Rubric    int     `json:"rubric"`
	Reasoning string  `json:"reasoning"`
	Score     float64 `json:"score"`
	Pass      bool    `json:"pass"`
}

type rubricVerdicts struct {
	Verdicts []rubricVerdict `json:"verdicts"`
}

const llmRubricSystem = `You are grading the output of an AI agent that runs inside Mattermost against numbered rubrics. Each rubric is a statement about the output; the output passes a rubric when the statement is true.

- Judge each rubric on its own, as if it were the only one. Do not let one rubric's verdict influence another's.
- Judge only what each rubric states. Ignore qualities it does not mention.
- Read the output as a Mattermost user would see it: Markdown is rendered, and an emoji name such as "thumbsup" or ":heart_eyes_cat:" stands for the emoji it names.
- An output that refuses, errors, or asks for more information instead of doing the task fails any rubric about the task's content.

Respond with a JSON object with this structure: {verdicts: [{rubric: number, reasoning: string, score: number, pass: boolean}]}, with one verdict per rubric, in order. Write each verdict's reasoning before deciding; score is between 0 and 1.
Example:
<Output>The steamclock is broken</Output>
<Rubrics>
1. The content contains the state of the clock
2. Contains a reference to the mentos project
</Rubrics>
{"verdicts": [{"rubric": 1, "reasoning": "The output says the clock is broken", "score": 1.0, "pass": true}, {"rubric": 2, "reasoning": "The output only talks about a clock and never mentions the mentos project", "score": 0.0, "pass": false}]}`

// withVerdictsOutput requests rubricVerdicts as structured output. Go slices
// map to a nullable array, which Anthropic rejects, so verdicts is made a plain
// array.
func withVerdictsOutput(cfg *llm.LanguageModelConfig) {
	schema := llm.NewJSONSchemaFromStruct[rubricVerdicts]()
	verdicts := schema.Properties["verdicts"]
	verdicts.Types = nil
	verdicts.Type = "array"
	cfg.JSONOutputFormat = schema
}

// LLMRubrics grades output against every rubric in a single grader request,
// so the output is sent once rather than once per rubric. Results are in the
// order of rubrics.
func (e *Eval) LLMRubrics(rubrics []string, output string) ([]RubricResult, error) {
	var numbered strings.Builder
	for i, rubric := range rubrics {
		fmt.Fprintf(&numbered, "%d. %s\n", i+1, rubric)
	}
	req := llm.CompletionRequest{
		Posts: []llm.Post{
			{
				Role:    llm.PostRoleSystem,
				Message: llmRubricSystem,
			},
			{
				Role:    llm.PostRoleUser,
				Message: fmt.Sprintf("<Output>%s</Output>\n<Rubrics>\n%s</Rubrics>", output, numbered.String()),
			},
		},
		Context:   llm.NewContext(),
		Operation: llm.OperationEvalGrading,
	}

	llmResult, gradeErr := e.GraderLLM.ChatCompletionNoStream(context.Background(), req, llm.WithMaxGeneratedTokens(500+500*len(rubrics)), withVerdictsOutput)
	if gradeErr != nil {
		return nil, fmt.Errorf("failed to grade with llm: %w", gradeErr)
	}

	var parsed rubricVerdicts
	if err := json.Unmarshal([]byte(llmResult), &parsed); err != nil {
		return nil, fmt.Errorf("failed to unmarshal llm result: %w", err)
	}
	return verdictsInOrder(parsed.Verdicts, len(rubrics))
}

// verdictsInOrder matches verdicts to rubrics by number and fails unless every
// rubric has exactly one verdict.
func verdictsInOrder(verdicts []rubricVerdict, count int) ([]RubricResult, error) {
	results := make([]RubricResult, count)
	seen := make([]bool, count)
	for _, v := range verdicts {
		i := v.Rubric - 1
		if i < 0 || i >= count {
			return nil, fmt.Errorf("grader returned a verdict for rubric %d of %d", v.Rubric, count)
		}
		if seen[i] {
			return nil, fmt.Errorf("grader returned more than one verdict for rubric %d", v.Rubric)
		}
		seen[i] = true
		results[i] = RubricResult{Reasoning: v.Reasoning, Score: v.Score, Pass: v.Pass}
	}
	for i, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("grader returned no verdict for rubric %d", i+1)
		}
	}
	return results, nil
}

// LLMRubric grades output against a single rubric.
func (e *Eval) LLMRubric(rubric, output string) (*RubricResult, error) {
	results, err := e.LLMRubrics([]string{rubric}, output)
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// LLMRubricsT grades output against every rubric in one grader request and
// records a result for each rubric.
func LLMRubricsT(e *EvalT, rubrics []string, output string) {
	e.Helper()
	if len(rubrics) == 0 {
		return
	}
	results, err := e.LLMRubrics(rubrics, output)
	require.NoError(e, err, "failed to grade rubrics %q", rubrics)
	for i, rubric := range rubrics {
		result := results[i]
		RecordScore(e, &EvalResult{
			Rubric:    rubric,
			Output:    output,
			Reasoning: result.Reasoning,
			Score:     result.Score,
			Pass:      result.Pass && result.Score >= minPassingScore,
		})
		// The verdict is already recorded above, so it bypasses EvalT's capture of
		// assertion failures.
		assert.True(e.T, result.Pass, "LLM Rubric Failed: %s", rubric)
		assert.GreaterOrEqual(e.T, result.Score, minPassingScore, "LLM Rubric Score is too low: %s", rubric)
	}
}

// LLMRubricT grades output against a single rubric and records the result.
func LLMRubricT(e *EvalT, rubric, output string) {
	e.Helper()
	LLMRubricsT(e, []string{rubric}, output)
}

// minPassingScore is the grader score below which a rubric fails even when
// the grader says it passes.
const minPassingScore = 0.6
