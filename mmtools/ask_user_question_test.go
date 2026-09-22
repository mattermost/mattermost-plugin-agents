// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestResolveUserInteractionAnswer(t *testing.T) {
	questionInput := json.RawMessage(`{
		"question": "Which channel should I post in?",
		"options": [{"label": "UX Design"}, {"label": "Design team"}, {"label": "Product"}]
	}`)
	multiSelectInput := json.RawMessage(`{
		"question": "Which channels?",
		"options": [{"label": "UX Design"}, {"label": "Design team"}, {"label": "Product"}],
		"multi_select": true
	}`)
	noFreeFormInput := json.RawMessage(`{
		"question": "Which channel should I post in?",
		"options": [{"label": "UX Design"}, {"label": "Design team"}],
		"allow_free_form": false
	}`)

	cases := []struct {
		name    string
		kind    string
		input   json.RawMessage
		answer  UserInteractionAnswer
		want    string
		wantErr string
	}{
		{
			name:   "single select valid",
			kind:   llm.UserInteractionSelect,
			input:  questionInput,
			answer: UserInteractionAnswer{Selected: []string{"Design team"}},
			want:   `{"selected":["Design team"]}`,
		},
		{
			name:   "multi select valid",
			kind:   llm.UserInteractionSelect,
			input:  multiSelectInput,
			answer: UserInteractionAnswer{Selected: []string{"UX Design", "Product"}},
			want:   `{"selected":["UX Design","Product"]}`,
		},
		{
			name:   "single select custom only",
			kind:   llm.UserInteractionSelect,
			input:  questionInput,
			answer: UserInteractionAnswer{Custom: "Post it in #random"},
			want:   `{"selected":null,"custom":"Post it in #random"}`,
		},
		{
			name:   "multi select with custom alongside predefined",
			kind:   llm.UserInteractionSelect,
			input:  multiSelectInput,
			answer: UserInteractionAnswer{Selected: []string{"UX Design"}, Custom: "and somewhere else"},
			want:   `{"selected":["UX Design"],"custom":"and somewhere else"}`,
		},
		{
			name:   "whitespace custom treated as empty",
			kind:   llm.UserInteractionSelect,
			input:  questionInput,
			answer: UserInteractionAnswer{Selected: []string{"Design team"}, Custom: "   "},
			want:   `{"selected":["Design team"]}`,
		},
		{
			name:    "custom rejected when free-form disabled",
			kind:    llm.UserInteractionSelect,
			input:   noFreeFormInput,
			answer:  UserInteractionAnswer{Custom: "anything"},
			wantErr: "free-form answer is not allowed",
		},
		{
			name:    "single select predefined plus custom is too many",
			kind:    llm.UserInteractionSelect,
			input:   questionInput,
			answer:  UserInteractionAnswer{Selected: []string{"Design team"}, Custom: "also this"},
			wantErr: "single-select",
		},
		{
			name:    "no selection",
			kind:    llm.UserInteractionSelect,
			input:   questionInput,
			answer:  UserInteractionAnswer{},
			wantErr: "no option selected",
		},
		{
			name:    "multiple selections on single select",
			kind:    llm.UserInteractionSelect,
			input:   questionInput,
			answer:  UserInteractionAnswer{Selected: []string{"UX Design", "Product"}},
			wantErr: "single-select",
		},
		{
			name:    "selection not among options",
			kind:    llm.UserInteractionSelect,
			input:   questionInput,
			answer:  UserInteractionAnswer{Selected: []string{"Engineering"}},
			wantErr: "not one of the offered options",
		},
		{
			name:    "duplicate selection",
			kind:    llm.UserInteractionSelect,
			input:   multiSelectInput,
			answer:  UserInteractionAnswer{Selected: []string{"Product", "Product"}},
			wantErr: "selected more than once",
		},
		{
			name:    "malformed input",
			kind:    llm.UserInteractionSelect,
			input:   json.RawMessage(`{not json`),
			answer:  UserInteractionAnswer{Selected: []string{"UX Design"}},
			wantErr: "failed to parse question arguments",
		},
		{
			name:    "empty question",
			kind:    llm.UserInteractionSelect,
			input:   json.RawMessage(`{"question": " ", "options": [{"label": "A"}]}`),
			answer:  UserInteractionAnswer{Selected: []string{"A"}},
			wantErr: "question must not be empty",
		},
		{
			name:    "no options",
			kind:    llm.UserInteractionSelect,
			input:   json.RawMessage(`{"question": "Q?", "options": []}`),
			answer:  UserInteractionAnswer{Selected: []string{"A"}},
			wantErr: "at least one option",
		},
		{
			name:    "duplicate option labels",
			kind:    llm.UserInteractionSelect,
			input:   json.RawMessage(`{"question": "Q?", "options": [{"label": "A"}, {"label": "A"}]}`),
			answer:  UserInteractionAnswer{Selected: []string{"A"}},
			wantErr: "duplicate option label",
		},
		{
			name:    "unknown interaction kind",
			kind:    "telepathy",
			input:   questionInput,
			answer:  UserInteractionAnswer{Selected: []string{"UX Design"}},
			wantErr: "unknown user interaction kind",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveUserInteractionAnswer(tc.kind, tc.input, tc.answer)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, got)
		})
	}
}

// TestNormalizeAskUserQuestionArguments covers the argument shapes models and
// providers emit instead of the declared schema. Every shape here used to
// reach the user as a generic approval card whose Accept failed server-side
// with "failed to parse question arguments", leaving the question stuck.
func TestNormalizeAskUserQuestionArguments(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "schema-shaped arguments pass through",
			input: `{"question":"Q?","options":[{"label":"A","description":"d"},{"label":"B"}],"multi_select":true}`,
			want:  `{"question":"Q?","options":[{"label":"A","description":"d"},{"label":"B"}],"multi_select":true}`,
		},
		{
			name:  "options stringified as a JSON array",
			input: `{"question":"Q?","options":"[{\"label\":\"A\"},{\"label\":\"B\"}]"}`,
			want:  `{"question":"Q?","options":[{"label":"A"},{"label":"B"}]}`,
		},
		{
			name:  "options stringified inside a markdown fence",
			input: "{\"question\":\"Q?\",\"options\":\"```json\\n[{\\\"label\\\":\\\"A\\\"}]\\n```\"}",
			want:  `{"question":"Q?","options":[{"label":"A"}]}`,
		},
		{
			name:  "options given as bare string labels",
			input: `{"question":"Q?","options":["A","B"]}`,
			want:  `{"question":"Q?","options":[{"label":"A"},{"label":"B"}]}`,
		},
		{
			name:  "each option stringified individually",
			input: `{"question":"Q?","options":["{\"label\":\"A\",\"description\":\"d\"}","{\"label\":\"B\"}"]}`,
			want:  `{"question":"Q?","options":[{"label":"A","description":"d"},{"label":"B"}]}`,
		},
		{
			name:  "lone option object not wrapped in an array",
			input: `{"question":"Q?","options":{"label":"A"}}`,
			want:  `{"question":"Q?","options":[{"label":"A"}]}`,
		},
		{
			name:  "whole argument object double encoded",
			input: `"{\"question\":\"Q?\",\"options\":[{\"label\":\"A\"}]}"`,
			want:  `{"question":"Q?","options":[{"label":"A"}]}`,
		},
		{
			name:  "multi_select stringified",
			input: `{"question":"Q?","options":[{"label":"A"},{"label":"B"}],"multi_select":"true"}`,
			want:  `{"question":"Q?","options":[{"label":"A"},{"label":"B"}],"multi_select":true}`,
		},
		{
			name:  "allow_free_form stringified",
			input: `{"question":"Q?","options":[{"label":"A"}],"allow_free_form":"false"}`,
			want:  `{"question":"Q?","options":[{"label":"A"}],"allow_free_form":false}`,
		},
		{
			name:  "booleans given as numbers",
			input: `{"question":"Q?","options":[{"label":"A"}],"multi_select":1,"allow_free_form":0}`,
			want:  `{"question":"Q?","options":[{"label":"A"}],"multi_select":true,"allow_free_form":false}`,
		},
		{
			// Keeping the question answerable beats honoring a select mode
			// the model mangled beyond recognition.
			name:  "uncoercible boolean falls back to its default",
			input: `{"question":"Q?","options":[{"label":"A"}],"multi_select":{"value":"yes"}}`,
			want:  `{"question":"Q?","options":[{"label":"A"}]}`,
		},
		{
			// The reported failure: the model wrote the call in Anthropic's
			// pseudo-XML syntax, so options carried markup and a lone option's
			// fields leaked to the top level.
			name:    "options carrying pseudo-XML markup",
			input:   `{"question":"Q?","label":"Draft the ticket","description":"d","options":"<parameter name=\"label\">File a GitHub issue instead"}`,
			wantErr: "options must be an array of objects with a label",
		},
		{
			name:    "options missing entirely",
			input:   `{"question":"Q?"}`,
			wantErr: "at least one option",
		},
		{
			name:    "option label is not a string",
			input:   `{"question":"Q?","options":[{"label":1}]}`,
			wantErr: "options must be an array of objects whose label must be a string",
		},
		{
			name:    "question is not a string",
			input:   `{"question":{"text":"Q?"},"options":[{"label":"A"}]}`,
			wantErr: "question must be a string",
		},
		{
			name:    "arguments are not an object",
			input:   `["Q?"]`,
			wantErr: "arguments must be a JSON object",
		},
		{
			name:    "arguments are not JSON at all",
			input:   `{not json`,
			wantErr: "arguments must be a JSON object",
		},
		{
			name:    "repaired options are still empty",
			input:   `{"question":"Q?","options":"[]"}`,
			wantErr: "at least one option",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeAskUserQuestionArguments(json.RawMessage(tc.input))
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrUnanswerableQuestion)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))

			// Normalized arguments must satisfy the declared schema so the
			// webapp's strict parse renders a question card.
			var args AskUserQuestionArgs
			require.NoError(t, json.Unmarshal(got, &args))
		})
	}
}

// TestResolveUserInteractionAnswerAcceptsRepairedArguments pins that a
// question the model built with a stringified option list is answerable
// end-to-end, rather than failing the accept with a parse error.
func TestResolveUserInteractionAnswerAcceptsRepairedArguments(t *testing.T) {
	input := json.RawMessage(`{"question":"Q?","options":"[{\"label\":\"A\"},{\"label\":\"B\"}]","multi_select":"true"}`)

	got, err := ResolveUserInteractionAnswer(llm.UserInteractionSelect, input, UserInteractionAnswer{Selected: []string{"A", "B"}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"selected":["A","B"]}`, got)
}

// TestResolveUserInteractionAnswerSeparatesQuestionFromAnswerFaults pins the
// distinction the approval flow relies on: a broken question is unanswerable
// and must be failed, while a bad answer to a good question must not be.
func TestResolveUserInteractionAnswerSeparatesQuestionFromAnswerFaults(t *testing.T) {
	cases := []struct {
		name            string
		input           string
		answer          UserInteractionAnswer
		wantUnanswerabe bool
	}{
		{
			name:            "unusable options",
			input:           `{"question":"Q?","options":"not json"}`,
			answer:          UserInteractionAnswer{Selected: []string{"A"}},
			wantUnanswerabe: true,
		},
		{
			name:            "empty question text",
			input:           `{"question":" ","options":[{"label":"A"}]}`,
			answer:          UserInteractionAnswer{Selected: []string{"A"}},
			wantUnanswerabe: true,
		},
		{
			name:   "selection not among the offered options",
			input:  `{"question":"Q?","options":[{"label":"A"}]}`,
			answer: UserInteractionAnswer{Selected: []string{"Z"}},
		},
		{
			name:   "nothing selected",
			input:  `{"question":"Q?","options":[{"label":"A"}]}`,
			answer: UserInteractionAnswer{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveUserInteractionAnswer(llm.UserInteractionSelect, json.RawMessage(tc.input), tc.answer)
			require.Error(t, err)
			assert.Equal(t, tc.wantUnanswerabe, errors.Is(err, ErrUnanswerableQuestion))
		})
	}
}

func TestAskUserQuestionResolverIsBackstopOnly(t *testing.T) {
	tool := NewAskUserQuestionTool()
	require.NotNil(t, tool.Resolver)

	_, err := tool.Resolver(context.Background(), nil, func(args any) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be answered by the user")
}

func TestGetToolsGatesAskUserQuestionOnInteractiveContext(t *testing.T) {
	cases := []struct {
		name       string
		llmContext *llm.Context
		wantTool   bool
	}{
		{
			name:       "interactive context includes the tool",
			llmContext: &llm.Context{ToolCatalog: llm.ToolCatalogContext{InteractiveUserPresent: true}},
			wantTool:   true,
		},
		{
			name:       "non-interactive context excludes the tool",
			llmContext: &llm.Context{},
			wantTool:   false,
		},
		{
			name:       "nil context excludes the tool",
			llmContext: nil,
			wantTool:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewMMToolProvider(nil, nil)
			tools := provider.GetTools(nil, tc.llmContext)

			found := false
			for _, tool := range tools {
				if tool.Name == AskUserQuestionToolName {
					found = true
					assert.Equal(t, llm.UserInteractionSelect, tool.UserInteraction)
				}
			}
			assert.Equal(t, tc.wantTool, found)
		})
	}
}
