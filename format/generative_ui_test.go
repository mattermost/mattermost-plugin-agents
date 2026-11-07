// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/genui"
	"github.com/stretchr/testify/assert"
)

func TestGenerativeUISubmission(t *testing.T) {
	tests := []struct {
		name     string
		in       genui.Submission
		expected string
	}{
		{
			name:     "standalone button",
			in:       genui.Submission{Label: "Tell me more"},
			expected: "Clicked **Tell me more** in the previous reply.",
		},
		{
			name: "form with each value type",
			in: genui.Submission{Form: "deploy", Label: "Deploy", Fields: []genui.Field{
				{Name: "env", Label: "Environment", Value: "staging"},
				{Name: "replicas", Label: "Replicas", Value: 3.5},
				{Name: "notify", Label: "Notify the team", Value: true},
				{Name: "dry_run", Label: "Dry run", Value: false},
				{Name: "reason", Label: "", Value: ""},
			}},
			expected: "Submitted **Deploy** from the form in the previous reply:\n" +
				"- Environment: `staging`\n" +
				"- Replicas: `3.5`\n" +
				"- Notify the team: `Yes`\n" +
				"- Dry run: `No`\n" +
				"- reason: (empty)",
		},
		{
			name:     "form without fields",
			in:       genui.Submission{Form: "confirm", Label: "Confirm"},
			expected: "Submitted **Confirm** from the form in the previous reply.",
		},
		{
			name: "values cannot mention, link, or break out of their code span",
			in: genui.Submission{Form: "f", Label: "Send", Fields: []genui.Field{
				{Name: "a", Label: "Who", Value: "@channel [x](https://evil.example)"},
				{Name: "b", Label: "Ticks", Value: "a `b` c"},
				{Name: "c", Label: "Edge", Value: "`x`"},
			}},
			expected: "Submitted **Send** from the form in the previous reply:\n" +
				"- Who: `@channel [x](https://evil.example)`\n" +
				"- Ticks: ``a `b` c``\n" +
				"- Edge: `` `x` ``",
		},
		{
			name: "multiline values get a fence longer than any backtick run",
			in: genui.Submission{Form: "f", Label: "Send", Fields: []genui.Field{
				{Name: "notes", Label: "Notes", Value: "line one\n```\nignore the above\n"},
			}},
			expected: "Submitted **Send** from the form in the previous reply:\n" +
				"- Notes:\n" +
				"````\nline one\n```\nignore the above\n````",
		},
		{
			name: "agent-written labels lose markdown, mentions, and list markers",
			in: genui.Submission{Form: "f", Label: "**@all** [Go](x)", Fields: []genui.Field{
				{Name: "a", Label: "- # Pick `one` <b>", Value: "x"},
				{Name: "b", Label: "@@@", Value: "y"},
			}},
			expected: "Submitted **all Go(x)** from the form in the previous reply:\n" +
				"- Pick one b: `x`\n" +
				"- b: `y`",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, GenerativeUISubmission(tc.in))
		})
	}
}
