// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

// attachmentMCPTools are the embedded tools a run needs from its first turn
// when the post that started it carries files, even when the agent loads MCP
// tools dynamically. Large and binary attachments reach the model as
// descriptors that tell it to call read_file.
var attachmentMCPTools = []llm.EnabledMCPTool{
	{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "read_file"},
}

// attachmentToolOptions preloads read_file for a run whose initiating post has
// attachments, saving the model a load_tool round trip. Agents that expose MCP
// tools directly already have it, so preloading there would duplicate it. The
// agent's allowlist and the turn's tool policy still apply to the preloaded
// tool, so it never reaches a run that could not otherwise be offered it.
func (c *Conversations) attachmentToolOptions(bot *bots.Bot, initiator *model.Post) []llm.ContextOption {
	if initiator == nil || len(initiator.FileIds) == 0 || !bot.GetConfig().MCPDynamicToolLoading {
		return nil
	}
	return []llm.ContextOption{c.contextBuilder.WithLLMContextPreloadedMCPTools(attachmentMCPTools)}
}
