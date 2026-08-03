// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

	// Result status values in the C7 tool-result JSON.
	askAnotherUserStatusAnswered = "answered"
	askAnotherUserStatusDeclined = "declined"
)

// AskAnotherUserArgs is the LLM-visible input schema for the tool.
type AskAnotherUserArgs struct {
	Username      string                  `json:"username" jsonschema_description:"Mattermost username of the user to ask, without the leading @. Must not be the requesting user."`
	Question      string                  `json:"question" jsonschema_description:"The question to ask. Must be specific, self-contained, and answerable without extra context."`
	Options       []AskUserQuestionOption `json:"options,omitempty" jsonschema_description:"Optional choices to present (2 to 5). Omit for a purely free-form question."`
	MultiSelect   bool                    `json:"multi_select,omitempty" jsonschema_description:"Set to true to let the user pick more than one option. Defaults to single-select."`
	AllowFreeForm *bool                   `json:"allow_free_form,omitempty" jsonschema_description:"Whether the user may type their own answer. Defaults to true. Must not be false when options is empty."`
	Context       string                  `json:"context,omitempty" jsonschema_description:"One short sentence shown to the user explaining why you are asking."`
}

// FreeFormEnabled reports whether the target may type a free-form answer. An
// omitted field (nil) means enabled; an explicit false disables.
func (a AskAnotherUserArgs) FreeFormEnabled() bool {
	return a.AllowFreeForm == nil || *a.AllowFreeForm
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
// need Mattermost lookups (question/username non-blank, at most 5 options
// with non-empty unique labels, allow_free_form=false requires options).
// Target-user resolution errors live in conversations.dispatchAskAnotherUser.
func ValidateAskAnotherUserArgs(args AskAnotherUserArgs) error {
	if strings.TrimSpace(args.Username) == "" {
		return errors.New("username must not be empty")
	}
	if strings.TrimSpace(args.Question) == "" {
		return errors.New("question must not be empty")
	}
	if len(args.Options) > 5 {
		return errors.New("provide between 1 and 5 options")
	}
	seen := make(map[string]bool, len(args.Options))
	for _, opt := range args.Options {
		if strings.TrimSpace(opt.Label) == "" {
			return errors.New("option labels must not be empty")
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
