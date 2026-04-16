// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"fmt"
	"slices"

	"errors"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var ErrUsageRestriction = errors.New("usage restriction")

func (m *MMBots) CheckUsageRestrictions(requestingUserID string, bot *Bot, channel *model.Channel) error {
	if err := m.CheckUsageRestrictionsForUser(bot, requestingUserID); err != nil {
		return err
	}

	if err := m.checkUsageRestrictionsForChannel(bot, channel); err != nil {
		return err
	}

	return nil
}

func (m *MMBots) checkUsageRestrictionsForChannel(bot *Bot, channel *model.Channel) error {
	switch bot.GetConfig().ChannelAccessLevel {
	case llm.ChannelAccessLevelAll:
		return nil
	case llm.ChannelAccessLevelAllow:
		if !slices.Contains(bot.GetConfig().ChannelIDs, channel.Id) {
			return fmt.Errorf("channel not allowed: %w", ErrUsageRestriction)
		}
		return nil
	case llm.ChannelAccessLevelBlock:
		if slices.Contains(bot.GetConfig().ChannelIDs, channel.Id) {
			return fmt.Errorf("channel blocked: %w", ErrUsageRestriction)
		}
		return nil
	case llm.ChannelAccessLevelNone:
		return fmt.Errorf("channel usage block for bot: %w", ErrUsageRestriction)
	}

	return fmt.Errorf("unknown channel assistance level")
}

func (m *MMBots) isMemberOfTeam(teamID string, userID string) (bool, error) {
	member, err := m.pluginAPI.Team.GetMember(teamID, userID)
	if errors.Is(err, pluginapi.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return member != nil && member.DeleteAt == 0, nil
}

func (m *MMBots) CheckUsageRestrictionsForUser(bot *Bot, requestingUserID string) error {
	switch bot.GetConfig().UserAccessLevel {
	case llm.UserAccessLevelAll:
		return nil
	case llm.UserAccessLevelAllow:
		// Check direct user allowlist
		if slices.Contains(bot.GetConfig().UserIDs, requestingUserID) {
			return nil
		}
		// Check team membership
		for _, teamID := range bot.GetConfig().TeamIDs {
			isMember, err := m.isMemberOfTeam(teamID, requestingUserID)
			if err != nil {
				return err
			}
			if isMember {
				return nil
			}
		}
		return fmt.Errorf("user not allowed: %w", ErrUsageRestriction)
	case llm.UserAccessLevelBlock:
		// Check direct user blocklist
		if slices.Contains(bot.GetConfig().UserIDs, requestingUserID) {
			return fmt.Errorf("user blocked: %w", ErrUsageRestriction)
		}
		// Check team membership
		for _, teamID := range bot.GetConfig().TeamIDs {
			isMember, err := m.isMemberOfTeam(teamID, requestingUserID)
			if err != nil {
				return err
			}
			if isMember {
				return fmt.Errorf("user's team blocked: %w", ErrUsageRestriction)
			}
		}
		return nil
	case llm.UserAccessLevelNone:
		return fmt.Errorf("user usage block for bot: %w", ErrUsageRestriction)
	}

	return fmt.Errorf("unknown user assistance level")
}
