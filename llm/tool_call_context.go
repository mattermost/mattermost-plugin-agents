// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "context"

// toolCallIDContextKey keys the ID of the tool call currently being resolved.
type toolCallIDContextKey struct{}

// ContextWithToolCallID returns a context carrying the ID of the tool call
// being resolved. Tool execution entry points (the tool runner and the
// approval-resume path) stamp this so downstream layers — e.g. the MCP client
// preparing embedded call metadata — can key per-call state without the ID
// ever being LLM-controlled.
func ContextWithToolCallID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, toolCallIDContextKey{}, id)
}

// ToolCallIDFromContext returns the tool call ID stamped by
// ContextWithToolCallID, or "" when absent.
func ToolCallIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(toolCallIDContextKey{}).(string)
	return id
}
