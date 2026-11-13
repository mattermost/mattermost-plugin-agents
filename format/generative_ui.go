// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/mattermost/mattermost-plugin-agents/v2/genui"
)

// GenerativeUISubmission renders a generative UI submission as the message of
// the user post it creates. The same text is the user turn the agent sees, so
// the thread shows exactly what the model received. Labels are written by the
// agent, so they are reduced to plain text; values are what the user typed or
// picked and are quoted as code, so neither can mention anyone, add links, or
// pose as new instructions.
func GenerativeUISubmission(s genui.Submission) string {
	var b strings.Builder
	if s.Form == "" {
		b.WriteString("Clicked **")
		b.WriteString(plainLabel(s.Label, "button"))
		b.WriteString("** in the previous reply.")
		return b.String()
	}

	b.WriteString("Submitted **")
	b.WriteString(plainLabel(s.Label, "Submit"))
	b.WriteString("** from the form in the previous reply")
	if len(s.Fields) == 0 {
		b.WriteString(".")
		return b.String()
	}
	b.WriteString(":")
	for _, f := range s.Fields {
		b.WriteString("\n- ")
		b.WriteString(plainLabel(f.Label, f.Name))
		b.WriteString(":")
		value := submissionValue(f.Value)
		if strings.Contains(value, "\n") {
			b.WriteString("\n")
			b.WriteString(codeBlock(value))
			continue
		}
		b.WriteString(" ")
		b.WriteString(inlineCode(value))
	}
	return b.String()
}

func submissionValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "Yes"
		}
		return "No"
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

// plainLabel keeps letters, digits, spaces, and punctuation that has no
// markdown or mention meaning, falling back when nothing is left.
func plainLabel(label, fallback string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			return r
		case unicode.IsSpace(r):
			return ' '
		case strings.ContainsRune(".,:;!?'\"()/&%+=-", r):
			return r
		default:
			return -1
		}
	}, label)
	cleaned = strings.TrimLeft(strings.Join(strings.Fields(cleaned), " "), "-+= ")
	if cleaned == "" {
		return fallback
	}
	return cleaned
}

func longestRun(s string, ch rune) int {
	longest, current := 0, 0
	for _, r := range s {
		if r == ch {
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return longest
}

func inlineCode(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(empty)"
	}
	fence := strings.Repeat("`", longestRun(value, '`')+1)
	if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") {
		value = " " + value + " "
	}
	return fence + value + fence
}

func codeBlock(value string) string {
	fence := strings.Repeat("`", max(3, longestRun(value, '`')+1))
	return fence + "\n" + strings.TrimRight(value, "\n") + "\n" + fence
}
