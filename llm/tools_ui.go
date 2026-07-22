// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// ToolUIVisibilityModel / ToolUIVisibilityApp are the values of the MCP Apps
// tool visibility array (_meta.ui.visibility).
const (
	ToolUIVisibilityModel = "model"
	ToolUIVisibilityApp   = "app"
)

// ToolUIMeta is the tool-side MCP Apps metadata (_meta.ui) declared by an MCP
// server on a tool, per the io.modelcontextprotocol/ui extension (2026-01-26).
// It is carried from tool discovery through ToolCall broadcasting and turn
// persistence so the webapp can detect UI-enabled tool calls.
type ToolUIMeta struct {
	// ResourceURI is the ui:// URI of the HTML resource that renders this
	// tool's results.
	ResourceURI string `json:"resource_uri"`
	// Visibility lists who may call the tool ("model", "app").
	// Empty means the spec default ["model", "app"].
	Visibility []string `json:"visibility,omitempty"`
}

// VisibleToModel reports whether the tool may be exposed to the LLM.
// A nil/empty Visibility defaults to visible (spec default ["model","app"]).
func (m *ToolUIMeta) VisibleToModel() bool {
	if m == nil || len(m.Visibility) == 0 {
		return true
	}
	for _, v := range m.Visibility {
		if v == ToolUIVisibilityModel {
			return true
		}
	}
	return false
}
