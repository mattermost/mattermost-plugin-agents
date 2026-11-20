// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/model"
)

// runsScoped reports whether the call runs under a scoped session. Scoped
// sessions cannot list the bot's own memberships, so tools that look things up
// by name search by name instead. A scope that cannot be read counts as scoped.
func runsScoped(ctx context.Context) bool {
	scope, err := scopedsession.ScopeFromContext(ctx)
	return scope != nil || err != nil
}

// channelsOnTeam lists the channels of teamID the caller can see: its own
// channels there, or for a scoped run the in-scope channels on the team.
func channelsOnTeam(ctx context.Context, client *model.Client4, teamID string) ([]*model.Channel, error) {
	if runsScoped(ctx) {
		found, _, err := client.SearchAllChannelsForUserWithOpts(ctx, &model.ChannelSearch{TeamIds: []string{teamID}})
		if err != nil {
			return nil, err
		}
		channels := make([]*model.Channel, 0, len(found))
		for i := range found {
			channels = append(channels, &found[i].Channel)
		}
		return channels, nil
	}

	user, _, err := client.GetMe(ctx, "")
	if err != nil {
		return nil, err
	}
	channels, _, err := client.GetChannelsForTeamForUser(ctx, teamID, user.Id, false, "")
	return channels, err
}

// notFoundChannelHint is the lookup suggestion for a channel that was not found.
func notFoundChannelHint(ctx context.Context) string {
	if runsScoped(ctx) {
		return "Call search_channels or list_team_channels to find channels you can read"
	}
	return "Call get_user_channels to list all channels you have access to"
}

func notFoundTeamHint(ctx context.Context) string {
	if runsScoped(ctx) {
		return "Call search_teams to find teams you can read"
	}
	return "Call get_user_channels to list all channels (includes team info) you have access to"
}
