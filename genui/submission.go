// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package genui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxRequestBytes bounds the whole ui_action request body.
	MaxRequestBytes = 16 * 1024
	MaxFields       = 20
	MaxValueRunes   = 2000
	MaxLabelRunes   = 200
)

// ErrInvalidSubmission is returned for a submission that is malformed or does
// not match the block it claims to come from.
var ErrInvalidSubmission = errors.New("invalid generative UI submission")

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Field is one submitted form field. Value is a string, number, or boolean.
type Field struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Value any    `json:"value"`
}

// Submission is a button click in a generated block, with the values of the
// button's form. A button outside any form has an empty Form and no Fields.
type Submission struct {
	Block  int     `json:"block"`
	Form   string  `json:"form"`
	Label  string  `json:"label"`
	Fields []Field `json:"fields"`
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSubmission, fmt.Sprintf(format, args...))
}

// Normalize validates s and returns a copy with control characters removed
// from every string, so what is stored and posted is exactly what was checked.
func (s Submission) Normalize() (Submission, error) {
	out := Submission{Block: s.Block, Form: s.Form, Fields: []Field{}}
	if s.Block < 0 {
		return out, invalid("block index must not be negative")
	}
	if s.Form != "" && !namePattern.MatchString(s.Form) {
		return out, invalid("form name is not a valid identifier")
	}

	label, err := normalizeLabel(s.Label)
	if err != nil {
		return out, err
	}
	if label == "" {
		return out, invalid("button label is required")
	}
	out.Label = label

	if s.Form == "" && len(s.Fields) > 0 {
		return out, invalid("a button outside a form cannot submit fields")
	}
	if len(s.Fields) > MaxFields {
		return out, invalid("at most %d fields can be submitted", MaxFields)
	}

	seen := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		if !namePattern.MatchString(f.Name) {
			return out, invalid("field name is not a valid identifier")
		}
		if seen[f.Name] {
			return out, invalid("field %q is submitted twice", f.Name)
		}
		seen[f.Name] = true

		fieldLabel, err := normalizeLabel(f.Label)
		if err != nil {
			return out, err
		}
		value, err := normalizeValue(f.Value)
		if err != nil {
			return out, fmt.Errorf("field %q: %w", f.Name, err)
		}
		out.Fields = append(out.Fields, Field{Name: f.Name, Label: fieldLabel, Value: value})
	}
	return out, nil
}

func normalizeLabel(label string) (string, error) {
	if utf8.RuneCountInString(label) > MaxLabelRunes {
		return "", invalid("labels are limited to %d characters", MaxLabelRunes)
	}
	return strings.TrimSpace(stripControl(label, false)), nil
}

func normalizeValue(value any) (any, error) {
	switch v := value.(type) {
	case string:
		if utf8.RuneCountInString(v) > MaxValueRunes {
			return nil, invalid("values are limited to %d characters", MaxValueRunes)
		}
		return stripControl(v, true), nil
	case bool:
		return v, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, invalid("number is not finite")
		}
		return v, nil
	default:
		return nil, invalid("value must be a string, number, or boolean")
	}
}

// stripControl removes control characters, keeping newlines and tabs when
// multiline is set.
func stripControl(s string, multiline bool) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if multiline && (r == '\n' || r == '\t') {
			return r
		}
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
}

// MatchProgram checks that the button label, the form name, and every field
// name and label appear as string literals in source, the block the
// submission claims to come from. It keeps the submission honest about what
// the generated UI offered; it is not an authorization check.
func (s Submission) MatchProgram(source string) error {
	required := []string{s.Label}
	if s.Form != "" {
		required = append(required, s.Form)
	}
	for _, f := range s.Fields {
		required = append(required, f.Name)
		if f.Label != "" {
			required = append(required, f.Label)
		}
	}
	for _, value := range required {
		if !strings.Contains(source, stringLiteral(value)) {
			return invalid("%q does not appear in the generated block", value)
		}
	}
	return nil
}

// stringLiteral renders value the way OpenUI Lang writes a string argument.
func stringLiteral(value string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
	return strings.TrimSuffix(buf.String(), "\n")
}
