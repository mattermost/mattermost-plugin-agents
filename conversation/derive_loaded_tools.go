// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/store"
)

// DeriveLoadedMCPTools returns the deduped, first-load-order list of MCP
// tool names that retained conversation history materialized via load_tool.
//
// A name is included iff history contains an assistant tool_use block with
// Name == mcp.LoadToolName and a tool_result block whose ToolUseID matches
// that tool_use, whose Status is success, and whose Content decodes to an
// mcp.LoadToolResult with Loaded == true and a non-empty Name.
func DeriveLoadedMCPTools(turns []store.Turn) []string {
	if len(turns) == 0 {
		return nil
	}

	loadToolUseIDs := make(map[string]struct{})
	seen := make(map[string]struct{})
	var names []string

	for _, turn := range turns {
		blocks, err := unmarshalBlocks(turn.Content)
		if err != nil {
			continue
		}
		for _, block := range blocks {
			switch block.Type {
			case BlockTypeToolUse:
				if block.Name != mcp.LoadToolName || block.ID == "" {
					continue
				}
				loadToolUseIDs[block.ID] = struct{}{}
			case BlockTypeToolResult:
				if block.ToolUseID == "" {
					continue
				}
				if _, ok := loadToolUseIDs[block.ToolUseID]; !ok {
					continue
				}
				if block.Status != StatusSuccess {
					continue
				}
				var payload mcp.LoadToolResult
				if jsonErr := json.Unmarshal([]byte(block.Content), &payload); jsonErr != nil {
					continue
				}
				if !payload.Loaded || payload.Name == "" {
					continue
				}
				if _, already := seen[payload.Name]; already {
					continue
				}
				seen[payload.Name] = struct{}{}
				names = append(names, payload.Name)
			}
		}
	}

	return names
}

// RestoreLoadedMCPToolsFromTurns loads MCP tools retained in conversation history.
func RestoreLoadedMCPToolsFromTurns(toolStore *llm.ToolStore, turns []store.Turn) []llm.Tool {
	if !toolStore.HasUnloadedMCPTools() {
		return nil
	}
	return toolStore.LoadMCPTools(DeriveLoadedMCPTools(turns))
}
