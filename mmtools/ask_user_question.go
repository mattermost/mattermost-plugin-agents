// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

const (
	// AskUserQuestionToolName is the runtime name of the built-in question tool.
	AskUserQuestionToolName = "AskUserQuestion"

	askUserQuestionDescription = "Ask the requesting user a question and present a set of options to pick from. " +
		"Use this when you need the user's input to proceed — choosing between approaches, picking a target, or confirming intent — and the answer cannot be inferred from the conversation. " +
		"Provide 2 to 5 concise, mutually exclusive options. Set multi_select to true only when picking several options together is meaningful. " +
		"The tool result contains the option label(s) the user selected. The user may also skip the question; if they do, proceed sensibly without the answer. " +
		"Do not use this tool to ask open-ended questions — ask those in your normal response text instead."
)

// AskUserQuestionOption is a single choice presented to the user.
type AskUserQuestionOption struct {
	Label       string `json:"label" jsonschema_description:"Short label for the option (1-5 words). Labels must be unique within the question."`
	Description string `json:"description,omitempty" jsonschema_description:"Optional one-line explanation of what choosing this option means."`
}

// AskUserQuestionArgs is the LLM-visible input schema for the question tool.
type AskUserQuestionArgs struct {
	Question    string                  `json:"question" jsonschema_description:"The question to ask the user. Must be clear and answerable by picking from the options."`
	Options     []AskUserQuestionOption `json:"options" jsonschema_description:"The choices to present. Provide 2 to 5 distinct options."`
	MultiSelect bool                    `json:"multi_select,omitempty" jsonschema_description:"Set to true to let the user select more than one option. Defaults to single-select."`
}

// AskUserQuestionResult is the tool result content written after the user
// answers. It is JSON so both the LLM and the webapp can consume it.
type AskUserQuestionResult struct {
	Selected []string `json:"selected"`
}

// NewAskUserQuestionTool returns the built-in question tool. The resolver is
// an error backstop: the call is answered through the tool-approval flow
// (Conversations.HandleToolCall), never executed server-side.
func NewAskUserQuestionTool() llm.Tool {
	return llm.Tool{
		Name:            AskUserQuestionToolName,
		Description:     askUserQuestionDescription,
		Schema:          llm.NewJSONSchemaFromStruct[AskUserQuestionArgs](),
		UserInteraction: llm.UserInteractionSelect,
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "", errors.New("AskUserQuestion must be answered by the user and cannot be executed directly")
		},
	}
}

// ResolveUserInteractionAnswer turns a user's answer to a pending interaction
// tool call into the tool result content. kind is the block's UserInteraction
// value, input the tool_use block's original arguments, and selections the
// option labels the user picked.
func ResolveUserInteractionAnswer(kind string, input json.RawMessage, selections []string) (string, error) {
	switch kind {
	case llm.UserInteractionSelect:
		return resolveAskUserQuestionAnswer(input, selections)
	default:
		return "", fmt.Errorf("unknown user interaction kind %q", kind)
	}
}

// resolveAskUserQuestionAnswer validates the selections against the options
// the LLM offered and returns the JSON tool result.
func resolveAskUserQuestionAnswer(input json.RawMessage, selections []string) (string, error) {
	var args AskUserQuestionArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("failed to parse question arguments: %w", err)
	}
	if err := validateAskUserQuestionArgs(args); err != nil {
		return "", err
	}

	if len(selections) == 0 {
		return "", errors.New("no option selected")
	}
	if !args.MultiSelect && len(selections) > 1 {
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

	result, err := json.Marshal(AskUserQuestionResult{Selected: selections})
	if err != nil {
		return "", fmt.Errorf("failed to marshal question result: %w", err)
	}
	return string(result), nil
}

// validateAskUserQuestionArgs rejects questions whose answers would be
// ambiguous: an empty question, no options, or duplicate option labels. The
// 2-5 option guidance is enforced only via the schema description so an
// already-asked degenerate question can still be answered.
func validateAskUserQuestionArgs(args AskUserQuestionArgs) error {
	if strings.TrimSpace(args.Question) == "" {
		return errors.New("question must not be empty")
	}
	if len(args.Options) == 0 {
		return errors.New("question must offer at least one option")
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
	return nil
}
