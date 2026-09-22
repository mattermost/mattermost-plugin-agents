// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const (
	// AskUserQuestionToolName is the runtime name of the built-in question tool.
	AskUserQuestionToolName = "AskUserQuestion"

	askUserQuestionDescription = "Ask the requesting user a question and present a set of options to pick from. " +
		"Use this when you need the user's input to proceed — choosing between approaches, picking a target, or confirming intent — and the answer cannot be inferred from the conversation. " +
		"Provide 2 to 5 concise, mutually exclusive options that each represent a real answer. Set multi_select to true only when picking several options together is meaningful. " +
		"By default the user can also type their own free-form answer, so do NOT add a catch-all option like \"Something else\", \"Other\", or \"None of the above\" — the free-form field already serves that purpose and such an option just wastes a slot. Set allow_free_form to false to require a listed option, in which case the options must be exhaustive. " +
		"The tool result contains the option label(s) the user selected and any free-form text they typed. The user may also skip the question; if they do, proceed sensibly without the answer. " +
		"Do not use this tool to ask open-ended questions — ask those in your normal response text instead."

	// maxArgumentUnwrapDepth bounds how far repairAskUserQuestionArgs will dig
	// through nested JSON-encoded strings before giving up.
	maxArgumentUnwrapDepth = 3
)

// ErrUnanswerableQuestion marks a question whose own arguments are unusable,
// as opposed to a valid question the user answered wrongly. No answer can ever
// resolve such a call, so callers must fail it and let the model re-ask
// instead of leaving it pending for the user to retry.
var ErrUnanswerableQuestion = errors.New("AskUserQuestion cannot be answered")

// AskUserQuestionOption is a single choice presented to the user.
type AskUserQuestionOption struct {
	Label       string `json:"label" jsonschema_description:"Short label for the option (1-5 words). Labels must be unique within the question."`
	Description string `json:"description,omitempty" jsonschema_description:"Optional one-line explanation of what choosing this option means."`
}

// AskUserQuestionArgs is the LLM-visible input schema for the question tool.
type AskUserQuestionArgs struct {
	Question      string                  `json:"question" jsonschema_description:"The question to ask the user. Must be clear and answerable by picking from the options."`
	Options       []AskUserQuestionOption `json:"options" jsonschema_description:"The choices to present. Provide 2 to 5 distinct options."`
	MultiSelect   bool                    `json:"multi_select,omitempty" jsonschema_description:"Set to true to let the user select more than one option. Defaults to single-select."`
	AllowFreeForm *bool                   `json:"allow_free_form,omitempty" jsonschema_description:"Whether to offer a free-form \"Something else…\" option that lets the user type their own answer. Defaults to true; set to false to require the user to pick from the listed options."`
}

// freeFormEnabled reports whether the free-form "Something else…" option should
// be offered. An omitted field (nil) means enabled; an explicit false disables.
func (a AskUserQuestionArgs) freeFormEnabled() bool {
	return a.AllowFreeForm == nil || *a.AllowFreeForm
}

// AskUserQuestionResult is the tool result content written after the user
// answers. It is JSON so both the LLM and the webapp can consume it.
type AskUserQuestionResult struct {
	Selected []string `json:"selected"`
	Custom   string   `json:"custom,omitempty"`
}

// UserInteractionAnswer is a user's answer to a pending user-interaction tool
// call: the predefined option labels picked, plus optional free-form text.
type UserInteractionAnswer struct {
	Selected []string `json:"selected"`
	Custom   string   `json:"custom,omitempty"`
}

// NewAskUserQuestionTool returns the built-in question tool. The resolver is
// an error backstop: the call is answered through the tool-approval flow
// (Conversations.HandleToolCall), never executed server-side.
func NewAskUserQuestionTool() llm.Tool {
	return llm.Tool{
		Name:               AskUserQuestionToolName,
		Description:        askUserQuestionDescription,
		Schema:             llm.NewJSONSchemaFromStruct[AskUserQuestionArgs](),
		UserInteraction:    llm.UserInteractionSelect,
		NormalizeArguments: NormalizeAskUserQuestionArguments,
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "", errors.New("AskUserQuestion must be answered by the user and cannot be executed directly")
		},
	}
}

// NormalizeAskUserQuestionArguments rewrites a question's raw arguments into
// the canonical schema shape, repairing the deviations models and providers
// commonly emit (see repairAskUserQuestionArgs). Arguments that cannot be
// repaired into an answerable question return an error so the call is failed
// while the model can still retry, rather than reaching the user as a card no
// answer can resolve.
func NormalizeAskUserQuestionArguments(input json.RawMessage) (json.RawMessage, error) {
	args, err := parseAskUserQuestionArgs(input)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to re-encode question arguments: %s", ErrUnanswerableQuestion, err)
	}
	return canonical, nil
}

// ResolveUserInteractionAnswer turns a user's answer to a pending interaction
// tool call into the tool result content. kind is the block's UserInteraction
// value, input the tool_use block's original arguments, and answer the
// structured selection (predefined labels plus optional free-form text).
func ResolveUserInteractionAnswer(kind string, input json.RawMessage, answer UserInteractionAnswer) (string, error) {
	switch kind {
	case llm.UserInteractionSelect:
		return resolveAskUserQuestionAnswer(input, answer)
	default:
		return "", fmt.Errorf("unknown user interaction kind %q", kind)
	}
}

// resolveAskUserQuestionAnswer validates the answer against the options the LLM
// offered and returns the JSON tool result.
func resolveAskUserQuestionAnswer(input json.RawMessage, answer UserInteractionAnswer) (string, error) {
	args, err := parseAskUserQuestionArgs(input)
	if err != nil {
		return "", err
	}

	selections := answer.Selected
	// Whitespace-only free-form text counts as no custom answer.
	custom := strings.TrimSpace(answer.Custom)
	if custom != "" && !args.freeFormEnabled() {
		return "", errors.New("free-form answer is not allowed for this question")
	}
	hasCustom := custom != ""

	if len(selections) == 0 && !hasCustom {
		return "", errors.New("no option selected")
	}

	chosen := len(selections)
	if hasCustom {
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

	result, err := json.Marshal(AskUserQuestionResult{Selected: selections, Custom: custom})
	if err != nil {
		return "", fmt.Errorf("failed to marshal question result: %w", err)
	}
	return string(result), nil
}

// parseAskUserQuestionArgs decodes and validates a question's arguments. The
// declared schema is tried first; anything else goes through the repair pass.
// Every failure wraps ErrUnanswerableQuestion — the question itself is at
// fault, not the user's answer.
func parseAskUserQuestionArgs(input json.RawMessage) (AskUserQuestionArgs, error) {
	var args AskUserQuestionArgs
	if err := json.Unmarshal(input, &args); err != nil {
		repaired, repairErr := repairAskUserQuestionArgs(input)
		if repairErr != nil {
			return AskUserQuestionArgs{}, fmt.Errorf("%w: failed to parse question arguments: %s", ErrUnanswerableQuestion, repairErr)
		}
		args = repaired
	}
	if err := validateAskUserQuestionArgs(args); err != nil {
		return AskUserQuestionArgs{}, fmt.Errorf("%w: %s", ErrUnanswerableQuestion, err)
	}
	return args, nil
}

// repairAskUserQuestionArgs reads arguments that miss the declared schema in
// the ways models and providers actually get it wrong: a JSON-encoded string
// where an object, array, or bool belongs (optionally markdown-fenced), bare
// string labels instead of option objects, and a lone option object the model
// forgot to wrap in an array. A boolean flag that survives none of this falls
// back to its schema default, because keeping the question answerable matters
// more than the select mode the model may have intended; a broken question or
// option list has no such fallback and fails.
func repairAskUserQuestionArgs(input json.RawMessage) (AskUserQuestionArgs, error) {
	var args AskUserQuestionArgs

	obj, err := decodeJSONObject(input)
	if err != nil {
		return args, err
	}

	question, err := decodeJSONString(obj["question"])
	if err != nil {
		return args, fmt.Errorf("question %s", err)
	}
	args.Question = question

	options, err := decodeQuestionOptions(obj["options"])
	if err != nil {
		return args, fmt.Errorf("options %s", err)
	}
	args.Options = options

	if v, ok := decodeJSONBool(obj["multi_select"]); ok {
		args.MultiSelect = v
	}
	if v, ok := decodeJSONBool(obj["allow_free_form"]); ok {
		args.AllowFreeForm = &v
	}

	return args, nil
}

// unwrapJSONString returns the payload of a JSON-encoded string when that
// payload is itself valid JSON, stripping any markdown fencing around it.
func unwrapJSONString(raw json.RawMessage) (json.RawMessage, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	inner := json.RawMessage(strings.TrimSpace(llm.StripMarkdownCodeFencing(s)))
	if !json.Valid(inner) {
		return nil, false
	}
	return inner, true
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func decodeJSONObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	for depth := 0; depth <= maxArgumentUnwrapDepth; depth++ {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err == nil && obj != nil {
			return obj, nil
		}
		inner, ok := unwrapJSONString(raw)
		if !ok {
			break
		}
		raw = inner
	}
	return nil, errors.New("arguments must be a JSON object")
}

func decodeJSONString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("is missing")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errors.New("must be a string")
	}
	return s, nil
}

func decodeJSONBool(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "yes", "1":
			return true, true
		case "false", "no", "0":
			return false, true
		}
		return false, false
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n != 0, true
	}
	return false, false
}

var errMalformedOptions = errors.New("must be an array of objects with a label")

func decodeQuestionOptions(raw json.RawMessage) ([]AskUserQuestionOption, error) {
	if len(raw) == 0 {
		return nil, errors.New("are missing")
	}
	for depth := 0; depth <= maxArgumentUnwrapDepth; depth++ {
		var elems []json.RawMessage
		if err := json.Unmarshal(raw, &elems); err == nil {
			options := make([]AskUserQuestionOption, 0, len(elems))
			for _, elem := range elems {
				opt, optErr := decodeQuestionOption(elem)
				if optErr != nil {
					return nil, optErr
				}
				options = append(options, opt)
			}
			return options, nil
		}
		if isJSONObject(raw) {
			opt, optErr := decodeQuestionOption(raw)
			if optErr != nil {
				return nil, optErr
			}
			return []AskUserQuestionOption{opt}, nil
		}
		inner, ok := unwrapJSONString(raw)
		if !ok {
			break
		}
		raw = inner
	}
	return nil, errMalformedOptions
}

func decodeQuestionOption(raw json.RawMessage) (AskUserQuestionOption, error) {
	if isJSONObject(raw) {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return AskUserQuestionOption{}, errMalformedOptions
		}
		label, err := decodeJSONString(obj["label"])
		if err != nil {
			return AskUserQuestionOption{}, fmt.Errorf("must be an array of objects whose label %s", err)
		}
		description, _ := decodeJSONString(obj["description"])
		return AskUserQuestionOption{Label: label, Description: description}, nil
	}

	// A bare string element is an unambiguous label. It may also be the whole
	// option object stringified, which takes precedence.
	if inner, ok := unwrapJSONString(raw); ok && isJSONObject(inner) {
		return decodeQuestionOption(inner)
	}
	if label, err := decodeJSONString(raw); err == nil {
		return AskUserQuestionOption{Label: label}, nil
	}
	return AskUserQuestionOption{}, errMalformedOptions
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
