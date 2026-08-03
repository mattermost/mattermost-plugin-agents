// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func boolPtr(b bool) *bool { return &b }

func TestValidateAskAnotherUserArgs(t *testing.T) {
	cases := []struct {
		name string
		args AskAnotherUserArgs
		// wantErr asserts a specific error substring; wantAnyErr asserts
		// rejection without pinning the wording (length-cap rows).
		wantErr    string
		wantAnyErr bool
	}{
		{
			name: "valid free-form only",
			args: AskAnotherUserArgs{Username: "bob", Question: "Which release was it?"},
		},
		{
			name: "valid with options",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Ship it?",
				Options:  []AskUserQuestionOption{{Label: "Yes"}, {Label: "No"}},
			},
		},
		{
			name:    "empty question",
			args:    AskAnotherUserArgs{Username: "bob", Question: " "},
			wantErr: "question must not be empty",
		},
		{
			name:    "empty username",
			args:    AskAnotherUserArgs{Question: "Q?"},
			wantErr: "username must not be empty",
		},
		{
			name: "too many options",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Q?",
				Options: []AskUserQuestionOption{
					{Label: "A"}, {Label: "B"}, {Label: "C"},
					{Label: "D"}, {Label: "E"}, {Label: "F"},
				},
			},
			wantErr: "between 1 and 5",
		},
		{
			name: "duplicate labels",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Q?",
				Options:  []AskUserQuestionOption{{Label: "Same"}, {Label: "Same"}},
			},
			wantErr: "duplicate option label",
		},
		{
			name: "empty label",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Q?",
				Options:  []AskUserQuestionOption{{Label: " "}},
			},
			wantErr: "labels must not be empty",
		},
		{
			name:    "free-form off without options",
			args:    AskAnotherUserArgs{Username: "bob", Question: "Q?", AllowFreeForm: boolPtr(false)},
			wantErr: "allow_free_form must not be false",
		},
		{
			name: "question at max length passes",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: strings.Repeat("q", askAnotherUserMaxQuestionRunes),
			},
		},
		{
			// Caps are rune counts, not byte counts: 1000 two-byte runes
			// must pass even though the byte length is double the cap.
			name: "multibyte question at max rune length passes",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: strings.Repeat("é", askAnotherUserMaxQuestionRunes),
			},
		},
		{
			name: "question over max length rejected",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: strings.Repeat("q", askAnotherUserMaxQuestionRunes+1),
			},
			wantAnyErr: true,
		},
		{
			name: "context at max length passes",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Q?",
				Context:  strings.Repeat("c", askAnotherUserMaxContextRunes),
			},
		},
		{
			name: "context over max length rejected",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Q?",
				Context:  strings.Repeat("c", askAnotherUserMaxContextRunes+1),
			},
			wantAnyErr: true,
		},
		{
			name: "option label at max length passes",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Q?",
				Options:  []AskUserQuestionOption{{Label: strings.Repeat("l", askAnotherUserMaxLabelRunes)}},
			},
		},
		{
			name: "option label over max length rejected",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Q?",
				Options:  []AskUserQuestionOption{{Label: strings.Repeat("l", askAnotherUserMaxLabelRunes+1)}},
			},
			wantAnyErr: true,
		},
		{
			name: "option description at max length passes",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Q?",
				Options: []AskUserQuestionOption{{
					Label:       "A",
					Description: strings.Repeat("d", askAnotherUserMaxDescriptionRunes),
				}},
			},
		},
		{
			name: "option description over max length rejected",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Q?",
				Options: []AskUserQuestionOption{{
					Label:       "A",
					Description: strings.Repeat("d", askAnotherUserMaxDescriptionRunes+1),
				}},
			},
			wantAnyErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAskAnotherUserArgs(tc.args)
			if tc.wantAnyErr {
				require.Error(t, err)
				return
			}
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestResolveAskAnotherUserAnswer(t *testing.T) {
	choiceInput := json.RawMessage(`{
		"username": "bob",
		"question": "Ship it?",
		"options": [{"label": "Yes, ship it"}, {"label": "Hold off"}]
	}`)
	multiSelectInput := json.RawMessage(`{
		"username": "bob",
		"question": "Which releases?",
		"options": [{"label": "4.2.0"}, {"label": "4.2.1"}, {"label": "4.3.0"}],
		"multi_select": true
	}`)
	freeFormInput := json.RawMessage(`{
		"username": "bob",
		"question": "Which release was it?"
	}`)
	noFreeFormInput := json.RawMessage(`{
		"username": "bob",
		"question": "Ship it?",
		"options": [{"label": "Yes"}, {"label": "No"}],
		"allow_free_form": false
	}`)

	cases := []struct {
		name     string
		input    json.RawMessage
		answer   AskAnotherUserAnswer
		declined bool
		want     string
		wantErr  string
	}{
		{
			name:   "choice answer matches C7 example",
			input:  choiceInput,
			answer: AskAnotherUserAnswer{Selected: []string{"Yes, ship it"}},
			want:   `{"status":"answered","target_username":"bob","selected":["Yes, ship it"],"free_form":""}`,
		},
		{
			name:   "free-form answer matches C7 example",
			input:  freeFormInput,
			answer: AskAnotherUserAnswer{FreeForm: "It was the 4.2.1 release, not 4.2.0"},
			want:   `{"status":"answered","target_username":"bob","selected":[],"free_form":"It was the 4.2.1 release, not 4.2.0"}`,
		},
		{
			name:   "multi-select two labels",
			input:  multiSelectInput,
			answer: AskAnotherUserAnswer{Selected: []string{"4.2.0", "4.2.1"}},
			want:   `{"status":"answered","target_username":"bob","selected":["4.2.0","4.2.1"],"free_form":""}`,
		},
		{
			name:     "decline matches C7 example",
			input:    choiceInput,
			declined: true,
			want:     `{"status":"declined","target_username":"bob"}`,
		},
		{
			name:     "decline with garbage selections still succeeds",
			input:    choiceInput,
			answer:   AskAnotherUserAnswer{Selected: []string{"not an option"}, FreeForm: "junk"},
			declined: true,
			want:     `{"status":"declined","target_username":"bob"}`,
		},
		{
			name:    "invalid option label",
			input:   choiceInput,
			answer:  AskAnotherUserAnswer{Selected: []string{"Maybe"}},
			wantErr: "not one of the offered options",
		},
		{
			name:    "free-form rejected when disallowed",
			input:   noFreeFormInput,
			answer:  AskAnotherUserAnswer{FreeForm: "anything"},
			wantErr: "free-form answer is not allowed",
		},
		{
			name:    "empty answer",
			input:   choiceInput,
			answer:  AskAnotherUserAnswer{},
			wantErr: "no option selected and no text entered",
		},
		{
			name:    "single-select violation with two selections",
			input:   choiceInput,
			answer:  AskAnotherUserAnswer{Selected: []string{"Yes, ship it", "Hold off"}},
			wantErr: "single-select",
		},
		{
			name:    "duplicate selection",
			input:   multiSelectInput,
			answer:  AskAnotherUserAnswer{Selected: []string{"4.2.0", "4.2.0"}},
			wantErr: "selected more than once",
		},
		{
			name:    "whitespace free-form counts as empty",
			input:   freeFormInput,
			answer:  AskAnotherUserAnswer{FreeForm: "   "},
			wantErr: "no option selected and no text entered",
		},
		{
			name:    "malformed input",
			input:   json.RawMessage(`{not json`),
			answer:  AskAnotherUserAnswer{FreeForm: "answer"},
			wantErr: "failed to parse question arguments",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveAskAnotherUserAnswer(tc.input, "bob", tc.answer, tc.declined)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, got)

			// Byte-shape checks (C7): answered results carry both selected
			// and free_form keys even when empty; declines carry neither.
			var keys map[string]any
			require.NoError(t, json.Unmarshal([]byte(got), &keys))
			if tc.declined {
				assert.NotContains(t, keys, "selected")
				assert.NotContains(t, keys, "free_form")
			} else {
				assert.Contains(t, keys, "selected")
				assert.Contains(t, keys, "free_form")
			}
		})
	}
}

func TestAskAnotherUserResolverIsBackstopOnly(t *testing.T) {
	tool := NewAskAnotherUserTool()
	require.NotNil(t, tool.Resolver)

	_, err := tool.Resolver(context.Background(), nil, func(args any) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dispatched by the conversation layer")
}

func TestGetToolsRegistersAskAnotherUserUnconditionally(t *testing.T) {
	cases := []struct {
		name           string
		llmContext     *llm.Context
		wantAskUser    bool
		wantAskAnother bool
	}{
		{
			name:           "interactive context includes both tools",
			llmContext:     &llm.Context{ToolCatalog: llm.ToolCatalogContext{InteractiveUserPresent: true}},
			wantAskUser:    true,
			wantAskAnother: true,
		},
		{
			name:           "non-interactive context still includes AskAnotherUser",
			llmContext:     &llm.Context{},
			wantAskUser:    false,
			wantAskAnother: true,
		},
		{
			name:           "nil context still includes AskAnotherUser",
			llmContext:     nil,
			wantAskUser:    false,
			wantAskAnother: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewMMToolProvider(nil, nil)
			tools := provider.GetTools(nil, tc.llmContext)

			foundAskUser := false
			foundAskAnother := false
			for _, tool := range tools {
				switch tool.Name {
				case AskUserQuestionToolName:
					foundAskUser = true
				case AskAnotherUserToolName:
					foundAskAnother = true
					assert.True(t, tool.DeferredResult, "AskAnotherUser must be a deferred-result tool")
					assert.Empty(t, tool.UserInteraction, "AskAnotherUser must not be a user-interaction tool")
					assert.Empty(t, tool.ServerOrigin, "AskAnotherUser is a built-in (empty origin)")
				}
			}
			assert.Equal(t, tc.wantAskUser, foundAskUser)
			assert.Equal(t, tc.wantAskAnother, foundAskAnother)
		})
	}
}
