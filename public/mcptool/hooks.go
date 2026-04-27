// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcptool

import "encoding/json"

// BeforeHookRequest is the JSON body POSTed to a before-hook callback.
// Args carries the validated, decoded tool arguments serialized as JSON — the
// exact same payload the resolver will operate on.
type BeforeHookRequest struct {
	ToolName string          `json:"tool_name"`
	Args     json.RawMessage `json:"args"`
	UserID   string          `json:"user_id"`
}

// BeforeHookResponse is the JSON body returned from a before-hook callback.
// Empty Error means the tool call may proceed. A non-empty Error rejects the call
// and that string is returned to the LLM as the tool error.
type BeforeHookResponse struct {
	Error string `json:"error,omitempty"`
}
