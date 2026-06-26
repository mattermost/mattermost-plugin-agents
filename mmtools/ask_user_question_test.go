// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
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
