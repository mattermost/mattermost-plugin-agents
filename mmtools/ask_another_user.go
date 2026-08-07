// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const (
	// AskAnotherUserToolName is the runtime name of the built-in
	// ask-another-user tool.
	AskAnotherUserToolName = "AskAnotherUser"

	askAnotherUserDescription = "Ask a specific other Mattermost user (not the requesting user) a clarifying question. " +
		"LAST RESORT: only use this after search and channel-history tools cannot answer the question, and only when one specific person clearly can. " +
		"The question is delivered as a direct message card from the agent to that user; the conversation waits until they answer or decline. " +
		"Ask only specific, self-contained questions answerable without extra context. The user may decline; if they do, proceed sensibly without the answer. " +
		"The result contains the answer (selected options and/or free-form text) or a decline marker. Ask one user one question at a time."

	// Result status values in the C7 tool-result JSON. The enum is
	// answered | declined | canceled (V2-C5).
	askAnotherUserStatusAnswered = "answered"
	askAnotherUserStatusDeclined = "declined"
	askAnotherUserStatusCanceled = "canceled"

	// Maximum lengths (in runes) accepted by ValidateAskAnotherUserArgs.
	// Oversized fields become error tool results so the model can shorten
	// and retry; without caps, generated text flows unbounded into a DM
	// post and its props (F-006).
	askAnotherUserMaxQuestionRunes    = 1000
	askAnotherUserMaxContextRunes     = 500
	askAnotherUserMaxLabelRunes       = 100
	askAnotherUserMaxDescriptionRunes = 200
)

// AskAnotherUserArgs is the LLM-visible input schema for the tool.
type AskAnotherUserArgs struct {
	Username      string                  `json:"username" jsonschema_description:"Mattermost username of the user to ask, without the leading @. Must not be the requesting user."`
	Question      string                  `json:"question" jsonschema_description:"The question to ask. Must be specific, self-contained, and answerable without extra context."`
	Options       []AskUserQuestionOption `json:"options,omitempty" jsonschema_description:"Optional choices to present (up to 5). Omit for a purely free-form question."`
	MultiSelect   bool                    `json:"multi_select,omitempty" jsonschema_description:"Set to true to let the user pick more than one option. Defaults to single-select."`
	AllowFreeForm *bool                   `json:"allow_free_form,omitempty" jsonschema_description:"Whether the user may type their own answer. Defaults to true. Must not be false when options is empty."`
	Context       string                  `json:"context,omitempty" jsonschema_description:"One short sentence shown to the user explaining why you are asking."`
}

// FreeFormEnabled reports whether the target may type a free-form answer. An
// omitted field (nil) means enabled; an explicit false disables.
func (a AskAnotherUserArgs) FreeFormEnabled() bool {
	return a.AllowFreeForm == nil || *a.AllowFreeForm
}

// CanonicalAskUsername returns the canonical form of a username argument:
// surrounding whitespace trimmed, then a single leading '@' stripped. Models
// routinely emit "@bob" or pad the name; user lookups and emptiness checks
// must run against the canonical form.
func CanonicalAskUsername(raw string) string {
	return strings.TrimPrefix(strings.TrimSpace(raw), "@")
}

// AskUserReservedPhrases are the canonical system phrases of the ask-user
// question card (V2-C3): substrings of the server-authored attribution,
// destination-disclosure, access-policy, and AI-content-caption lines. Model
// text must never be able to fake that chrome, so
// SanitizeAskAnotherUserArgs strips or rejects arguments containing them.
// Matching is a case-insensitive substring check per line — deliberately
// minimal (no fuzzy/regex/confusable handling); the real defense is the
// card's visual separation of model text from system chrome (F4a).
var AskUserReservedPhrases = []string{
	"asked on behalf of",
	"asked by the",
	"asked via the",
	"your answer will be shared",
	"your answer may be shared",
	"running unattended",
	"is restricted by an attribute-based",
	"ai-generated content",
}

// containsAskUserReservedPhrase reports whether s contains any reserved
// phrase, case-insensitively.
func containsAskUserReservedPhrase(s string) bool {
	lower := strings.ToLower(s)
	for _, phrase := range AskUserReservedPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// stripAskUserReservedLines drops every line of s that contains a reserved
// phrase and rejoins the remainder.
func stripAskUserReservedLines(s string) string {
	if !containsAskUserReservedPhrase(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if !containsAskUserReservedPhrase(line) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// SanitizeAskAnotherUserArgs applies the V2-C3 anti-impersonation rule:
// multi-line fields (question, context) have offending lines STRIPPED so an
// accidental match keeps the model productive; single-line option labels and
// descriptions are REJECTED outright because silently removing part of a
// choice would change its meaning. Called after ValidateAskAnotherUserArgs;
// stripping can only shorten fields, so no re-validation is needed. Errors
// become error tool results the model can react to by rephrasing.
func SanitizeAskAnotherUserArgs(args AskAnotherUserArgs) (AskAnotherUserArgs, error) {
	args.Question = stripAskUserReservedLines(args.Question)
	args.Context = stripAskUserReservedLines(args.Context)
	if strings.TrimSpace(args.Question) == "" {
		return args, errors.New("question must not be empty after removing reserved system phrasing")
	}
	for _, opt := range args.Options {
		if containsAskUserReservedPhrase(opt.Label) || containsAskUserReservedPhrase(opt.Description) {
			return args, errors.New("option labels and descriptions must not contain reserved system phrasing")
		}
	}
	return args, nil
}

// AskAnotherUserAnswer is the target's submitted answer.
type AskAnotherUserAnswer struct {
	Selected []string `json:"selected"`
	FreeForm string   `json:"free_form"`
}

// AskAnotherUserResult is the tool-result JSON fed to the LLM for answered
// questions (C7). Answered results always carry both selected and free_form,
// even when empty; declines are marshaled from a reduced shape that carries
// neither (see ResolveAskAnotherUserAnswer).
type AskAnotherUserResult struct {
	Status         string   `json:"status"` // "answered" | "declined"
	TargetUsername string   `json:"target_username"`
	Selected       []string `json:"selected"`
	FreeForm       string   `json:"free_form"`
}

// NewAskAnotherUserTool returns the built-in ask-another-user tool. The
// resolver is an error backstop; dispatch happens in the conversations layer.
func NewAskAnotherUserTool() llm.Tool {
	return llm.Tool{
		Name:           AskAnotherUserToolName,
		Description:    askAnotherUserDescription,
		Schema:         llm.NewJSONSchemaFromStruct[AskAnotherUserArgs](),
		DeferredResult: true,
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "", errors.New("AskAnotherUser is dispatched by the conversation layer and cannot be executed directly")
		},
	}
}

// ValidateAskAnotherUserArgs enforces the argument invariants that do not
// need Mattermost lookups (question/username non-blank, question/context/
// option fields within length caps, at most 5 options with non-empty unique
// labels, allow_free_form=false requires options). Target-user resolution
// errors live in conversations.dispatchAskAnotherUser.
func ValidateAskAnotherUserArgs(args AskAnotherUserArgs) error {
	if CanonicalAskUsername(args.Username) == "" {
		return errors.New("username must not be empty")
	}
	if strings.TrimSpace(args.Question) == "" {
		return errors.New("question must not be empty")
	}
	if utf8.RuneCountInString(args.Question) > askAnotherUserMaxQuestionRunes {
		return fmt.Errorf("question must be at most %d characters", askAnotherUserMaxQuestionRunes)
	}
	if utf8.RuneCountInString(args.Context) > askAnotherUserMaxContextRunes {
		return fmt.Errorf("context must be at most %d characters", askAnotherUserMaxContextRunes)
	}
	if len(args.Options) > 5 {
		return errors.New("provide at most 5 options")
	}
	seen := make(map[string]bool, len(args.Options))
	for _, opt := range args.Options {
		if strings.TrimSpace(opt.Label) == "" {
			return errors.New("option labels must not be empty")
		}
		if utf8.RuneCountInString(opt.Label) > askAnotherUserMaxLabelRunes {
			return fmt.Errorf("option labels must be at most %d characters", askAnotherUserMaxLabelRunes)
		}
		if utf8.RuneCountInString(opt.Description) > askAnotherUserMaxDescriptionRunes {
			return fmt.Errorf("option descriptions must be at most %d characters", askAnotherUserMaxDescriptionRunes)
		}
		if seen[opt.Label] {
			return fmt.Errorf("duplicate option label %q", opt.Label)
		}
		seen[opt.Label] = true
	}
	if !args.FreeFormEnabled() && len(args.Options) == 0 {
		return errors.New("allow_free_form must not be false when no options are provided")
	}
	return nil
}

// ResolveAskAnotherUserCancel returns the C7-family tool-result JSON for an
// initiator-canceled question (V2-C4):
// {"status":"canceled","target_username":"<username>"}. The username is
// parsed best-effort from the original tool input; an unparseable input
// yields an empty username but still a valid payload — the cancel result
// must always be writable.
func ResolveAskAnotherUserCancel(input json.RawMessage) (string, error) {
	targetUsername := ""
	var args AskAnotherUserArgs
	if err := json.Unmarshal(input, &args); err == nil {
		targetUsername = CanonicalAskUsername(args.Username)
	}

	result, err := json.Marshal(struct {
		Status         string `json:"status"`
		TargetUsername string `json:"target_username"`
	}{Status: askAnotherUserStatusCanceled, TargetUsername: targetUsername})
	if err != nil {
		return "", fmt.Errorf("failed to marshal cancel result: %w", err)
	}
	return string(result), nil
}

// ResolveAskAnotherUserAnswer validates the target's answer against the
// original tool arguments and returns the C7 result JSON. declined skips
// answer validation entirely and returns the decline payload.
func ResolveAskAnotherUserAnswer(input json.RawMessage, targetUsername string, answer AskAnotherUserAnswer, declined bool) (string, error) {
	var args AskAnotherUserArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("failed to parse question arguments: %w", err)
	}

	if declined {
		// Declines carry neither selected nor free_form (C7).
		result, err := json.Marshal(struct {
			Status         string `json:"status"`
			TargetUsername string `json:"target_username"`
		}{Status: askAnotherUserStatusDeclined, TargetUsername: targetUsername})
		if err != nil {
			return "", fmt.Errorf("failed to marshal decline result: %w", err)
		}
		return string(result), nil
	}

	selections := answer.Selected
	// Whitespace-only free-form text counts as no answer.
	freeForm := strings.TrimSpace(answer.FreeForm)
	if freeForm != "" && !args.FreeFormEnabled() {
		return "", errors.New("free-form answer is not allowed for this question")
	}
	hasFreeForm := freeForm != ""

	if len(selections) == 0 && !hasFreeForm {
		return "", errors.New("no option selected and no text entered")
	}

	chosen := len(selections)
	if hasFreeForm {
		chosen++
	}
	if !args.MultiSelect && chosen > 1 {
		return "", errors.New("question is single-select but multiple options were selected")
	}

	valid := make(map[string]bool, len(args.Options))
	for _, opt := range args.Options {
		valid[opt.Label] = true
	}
	seen := make(map[string]bool, len(selections))
	for _, sel := range selections {
		if !valid[sel] {
			return "", fmt.Errorf("selected option %q is not one of the offered options", sel)
		}
		if seen[sel] {
			return "", fmt.Errorf("option %q selected more than once", sel)
		}
		seen[sel] = true
	}

	if selections == nil {
		selections = []string{}
	}
	result, err := json.Marshal(AskAnotherUserResult{
		Status:         askAnotherUserStatusAnswered,
		TargetUsername: targetUsername,
		Selected:       selections,
		FreeForm:       freeForm,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal answer result: %w", err)
	}
	return string(result), nil
}
