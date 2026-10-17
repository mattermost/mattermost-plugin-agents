// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// UserInteractionAnswer is a user's answer to a pending user-interaction tool
// call: the predefined option labels picked, plus optional free-form text.
type UserInteractionAnswer struct {
	Selected []string `json:"selected"`
	Custom   string   `json:"custom,omitempty"`
}

// UserInteractionResult is the tool result recorded for an accepted
// interaction tool call. IsError marks an accepted call whose outcome failed,
// such as a connect the user did not complete.
type UserInteractionResult struct {
	Content string
	IsError bool
}

// UserInteractionEnv is the state an accepted interaction is resolved against,
// gathered by the caller after the user's decision.
type UserInteractionEnv struct {
	// Context is the tool context rebuilt after the user's decision.
	Context *llm.Context
	// ConnectServerOrigin is the tool origin of the enabled MCP server named by
	// a connect call, or "" when no such server is configured.
	ConnectServerOrigin string
}

// ResolveUserInteractionAnswer turns a user's acceptance of a pending
// interaction tool call into the tool result. kind is the block's
// UserInteraction value, input the tool_use block's original arguments, and
// answer the structured selection (predefined labels plus optional free-form
// text). Connect calls carry no answer; their result is derived from env.
func ResolveUserInteractionAnswer(kind string, input json.RawMessage, answer UserInteractionAnswer, env UserInteractionEnv) (UserInteractionResult, error) {
	switch kind {
	case llm.UserInteractionSelect:
		content, err := resolveAskUserQuestionAnswer(input, answer)
		return UserInteractionResult{Content: content}, err
	case llm.UserInteractionConnect:
		return resolveConnectMCPServerAccepted(input, env)
	default:
		return UserInteractionResult{}, fmt.Errorf("unknown user interaction kind %q", kind)
	}
}

// UserInteractionSkippedResult is the tool result recorded when the user
// skips a pending interaction tool call instead of accepting it.
func UserInteractionSkippedResult(kind string, input json.RawMessage) string {
	switch kind {
	case llm.UserInteractionConnect:
		return connectMCPServerSkippedResult(input)
	default:
		return "User skipped the question"
	}
}
