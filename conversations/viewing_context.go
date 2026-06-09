// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

// Post prop keys for the channel/team the user is viewing in the center panel
// when they post to the agent from the RHS. These are attached client-side by
// the webapp and consumed (and access-checked) by the backend.
const (
	ViewingChannelIDProp = "viewing_channel_id"
	ViewingTeamIDProp    = "viewing_team_id"
)

// stringProp returns the prop as a string, or empty if not set or not a string.
func stringProp(post *model.Post, key string) string {
	if post == nil {
		return ""
	}
	v := post.GetProp(key)
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// resolveViewingContextOption returns a context option that, when the post
// carries viewing_channel_id / viewing_team_id props, looks up the channel
// (and optionally team) and attaches them to the LLM context as the viewed
// channel/team. The user's read permission on the claimed channel is checked
// before attaching; on any failure (missing channel, no permission, etc.) the
// option is a no-op so the agent behaves as it does today.
//
// conversationChannelID is the channel where the conversation lives (typically
// the agent's bot DM). When the claimed viewing channel matches the
// conversation channel, the option is skipped to avoid redundant context.
func resolveViewingContextOption(
	client mmapi.Client,
	builder *llmcontext.Builder,
	post *model.Post,
	userID string,
	conversationChannelID string,
) llm.ContextOption {
	noop := func(*llm.Context) {}
	if client == nil || builder == nil || post == nil {
		return noop
	}

	viewingChannelID := stringProp(post, ViewingChannelIDProp)
	if viewingChannelID == "" {
		return noop
	}

	// No redundant viewing context when the user is already in the agent's DM.
	if viewingChannelID == conversationChannelID {
		return noop
	}

	if !client.HasPermissionToChannel(userID, viewingChannelID, model.PermissionReadChannel) {
		client.LogDebug("Dropping viewing context: user lacks read permission",
			"user_id", userID, "viewing_channel_id", viewingChannelID)
		return noop
	}

	channel, err := client.GetChannel(viewingChannelID)
	if err != nil || channel == nil {
		if err != nil {
			client.LogDebug("Dropping viewing context: failed to load channel",
				"viewing_channel_id", viewingChannelID, "error", err.Error())
		}
		return noop
	}

	opts := []llm.ContextOption{builder.WithLLMContextViewingChannel(channel)}

	// Honor an explicit viewing_team_id only when the user actually has the
	// channel's team (covers DM/GM where Channel.TeamId is empty) and only if
	// it matches the channel's team for non-DM channels.
	if viewingTeamID := stringProp(post, ViewingTeamIDProp); viewingTeamID != "" {
		if channel.TeamId == "" || channel.TeamId == viewingTeamID {
			if team, teamErr := client.GetTeam(viewingTeamID); teamErr == nil && team != nil {
				opts = append(opts, builder.WithLLMContextViewingTeam(team))
			}
		}
	}

	return func(c *llm.Context) {
		for _, o := range opts {
			o(c)
		}
	}
}
