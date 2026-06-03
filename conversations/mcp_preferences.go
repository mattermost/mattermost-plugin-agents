// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

func (c *Conversations) withUserDisabledMCPServerOptions(opts []llm.ContextOption, userID string, channel *model.Channel, logContext string) []llm.ContextOption {
	if c == nil || c.mmClient == nil || c.contextBuilder == nil || userID == "" || channel == nil {
		return opts
	}
	if channel.Type != model.ChannelTypeDirect && channel.Type != model.ChannelTypeGroup {
		return opts
	}

	prefs, err := mcp.LoadUserPreferences(c.mmClient, userID)
	if err != nil {
		c.mmClient.LogWarn("Failed to load user tool preferences", "error", err.Error(), "userID", userID, "context", logContext)
		return opts
	}
	if len(prefs.DisabledServers) == 0 {
		return opts
	}

	filterOpt := c.contextBuilder.WithLLMContextDisabledMCPServers(prefs.DisabledServers)
	return append([]llm.ContextOption{filterOpt}, opts...)
}
