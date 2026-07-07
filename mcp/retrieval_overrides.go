// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import "github.com/mattermost/mattermost-plugin-agents/v2/llm"

type ToolRetrievalOverride struct {
	Summary string
}

func ToolRetrievalOverrideKey(serverOrigin, toolName string) string {
	return serverOrigin + "\x00" + llm.BareMCPToolName(toolName)
}
