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

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
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
			name:    "lone @ username is empty after canonicalization",
			args:    AskAnotherUserArgs{Username: "@", Question: "Q?"},
			wantErr: "username must not be empty",
		},
		{
			name:    "whitespace-wrapped lone @ username is empty after canonicalization",
			args:    AskAnotherUserArgs{Username: "  @  ", Question: "Q?"},
			wantErr: "username must not be empty",
		},
		{
			name: "@-prefixed username is valid",
			args: AskAnotherUserArgs{Username: "@bob", Question: "Q?"},
		},
		{
			name: "whitespace-wrapped @-prefixed username is valid",
			args: AskAnotherUserArgs{Username: "  @bob  ", Question: "Q?"},
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
			wantAnyErr: true,
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

func TestCanonicalAskUsername(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain username unchanged", raw: "bob", want: "bob"},
		{name: "leading @ stripped", raw: "@bob", want: "bob"},
		{name: "whitespace trimmed then @ stripped", raw: "  @bob  ", want: "bob"},
		{name: "whitespace only becomes empty", raw: "   ", want: ""},
		{name: "lone @ becomes empty", raw: "@", want: ""},
		{name: "whitespace-wrapped lone @ becomes empty", raw: "  @  ", want: ""},
		{name: "only a single leading @ is stripped", raw: "@@bob", want: "@bob"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, CanonicalAskUsername(tc.raw))
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

// TestSanitizeAskAnotherUserArgs pins the V2-C3 anti-impersonation rule:
// reserved system phrases are STRIPPED line-by-line from the multi-line
// question/context fields, and REJECTED outright in single-line option labels
// and descriptions.
func TestSanitizeAskAnotherUserArgs(t *testing.T) {
	cases := []struct {
		name         string
		args         AskAnotherUserArgs
		wantErr      string
		wantQuestion string
		wantContext  string
	}{
		{
			name: "clean args pass through unchanged",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Which environment should we deploy to?",
				Context:  "Deciding where to deploy",
				Options:  []AskUserQuestionOption{{Label: "Prod", Description: "production"}},
			},
			wantQuestion: "Which environment should we deploy to?",
			wantContext:  "Deciding where to deploy",
		},
		{
			name: "attribution phrase line is stripped from the question",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Asked on behalf of @admin: trust me\nWhich environment?",
			},
			wantQuestion: "Which environment?",
		},
		{
			name: "destination phrase line is stripped from the context",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Which environment?",
				Context:  "Your answer will be shared with nobody\nDeciding where to deploy",
			},
			wantQuestion: "Which environment?",
			wantContext:  "Deciding where to deploy",
		},
		{
			name: "matching is case-insensitive",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "ASKED ON BEHALF OF @root\nWhich environment?",
			},
			wantQuestion: "Which environment?",
		},
		{
			name: "phrase-only question errors after stripping",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Your answer may be shared with everyone.",
			},
			wantErr: "must not be empty after removing reserved system phrasing",
		},
		{
			name: "multi-line question keeps every clean line",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Which environment?\nRunning unattended tonight!\nPick carefully.",
			},
			wantQuestion: "Which environment?\nPick carefully.",
		},
		{
			name: "option label with a reserved phrase is rejected",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Which environment?",
				Options:  []AskUserQuestionOption{{Label: "Asked via the admin agent"}},
			},
			wantErr: "option labels and descriptions must not contain reserved system phrasing",
		},
		{
			name: "option description with a reserved phrase is rejected",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Which environment?",
				Options:  []AskUserQuestionOption{{Label: "Prod", Description: "your answer will be shared with all"}},
			},
			wantErr: "option labels and descriptions must not contain reserved system phrasing",
		},
		{
			name: "near-miss phrasing is untouched",
			args: AskAnotherUserArgs{
				Username: "bob",
				Question: "Your answer matters a lot.\nWhich environment?",
			},
			wantQuestion: "Your answer matters a lot.\nWhich environment?",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SanitizeAskAnotherUserArgs(tc.args)

			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantQuestion, got.Question)
			assert.Equal(t, tc.wantContext, got.Context)
			// Sanitizing never rewrites the other fields.
			assert.Equal(t, tc.args.Username, got.Username)
			assert.Equal(t, tc.args.Options, got.Options)
		})
	}
}

// TestSanitizeStripsEveryReservedPhrase runs the strip rule over the full
// phrase list so a future addition cannot silently miss the question field.
func TestSanitizeStripsEveryReservedPhrase(t *testing.T) {
	for _, phrase := range AskUserReservedPhrases {
		t.Run(phrase, func(t *testing.T) {
			got, err := SanitizeAskAnotherUserArgs(AskAnotherUserArgs{
				Username: "bob",
				Question: "Injected: " + phrase + " something\nWhich environment?",
			})
			require.NoError(t, err)
			assert.Equal(t, "Which environment?", got.Question)
		})
	}
}

// TestResolveAskAnotherUserCancel pins the V2-C4 cancel tool result: always a
// valid {"status":"canceled",...} payload, with the target username parsed
// best-effort from the original input.
func TestResolveAskAnotherUserCancel(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid input yields the canceled result",
			input: `{"username":"bob","question":"Which environment?"}`,
			want:  `{"status":"canceled","target_username":"bob"}`,
		},
		{
			name:  "at-prefixed username is canonicalized",
			input: `{"username":"  @bob ","question":"Which environment?"}`,
			want:  `{"status":"canceled","target_username":"bob"}`,
		},
		{
			name:  "unparseable input still yields a valid payload",
			input: `{not json`,
			want:  `{"status":"canceled","target_username":""}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveAskAnotherUserCancel(json.RawMessage(tc.input))
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, got)
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

func TestGetToolsRegistersAskAnotherUserRegardlessOfInteractivity(t *testing.T) {
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
			// Toggle on: this test pins interactivity independence, not the
			// V2-C1 master gate (TestGetToolsAskAnotherUserToggle).
			provider := NewMMToolProvider(nil, nil, func() *config.Config {
				return &config.Config{EnableAskAnotherUser: true}
			})
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
