// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcptool

import "encoding/json"

// BeforeHookRequest is the JSON body POSTed to a before-hook callback.
type BeforeHookRequest struct {
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args"`
	UserID   string         `json:"user_id"`
}

// BeforeHookResponse is the JSON body returned from a before-hook callback.
// Empty Error means the tool call may proceed. A non-empty Error rejects the call
// and that string is returned to the LLM as the tool error.
type BeforeHookResponse struct {
	Error string `json:"error,omitempty"`
}

// AfterHookRequest is the JSON body POSTed to an after-hook callback.
// For success-path calls, Output carries the tool's output payload as JSON (one of the *Output types in this package).
// For error-path calls, Error carries the tool resolver error string and Output is omitted.
// UserID is the Mattermost user ID when the MCP server resolved it for the tool call (same semantics as BeforeHookRequest).
type AfterHookRequest struct {
	ToolName string          `json:"tool_name"`
	UserID   string          `json:"user_id,omitempty"`
	Output   json.RawMessage `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// AfterHookResponse is the JSON body returned from an after-hook callback.
// A non-empty Error aborts the tool call. Otherwise Output replaces the tool's output payload.
type AfterHookResponse struct {
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}
