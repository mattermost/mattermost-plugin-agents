// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"fmt"
	"slices"

	"errors"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
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

// CheckMentionRestrictions returns nil if requestingUserID may trigger a
// channel-visible response by @mentioning bot in channel, otherwise an error
// wrapping ErrUsageRestriction. This is intentionally separate from
// CheckUsageRestrictions: it applies only to the channel @mention path (which
// posts publicly), not to the UI-button / DM / RHS flows that stream privately
// to the requesting user.
func (m *MMBots) CheckMentionRestrictions(requestingUserID string, bot *Bot, channel *model.Channel) error {
	switch bot.GetConfig().MentionAccessLevel {
	case llm.MentionAccessLevelEveryone:
		return nil
	case llm.MentionAccessLevelChannelAdmins:
		// PermissionManageChannelRoles is held by the channel_admin role and by
		// system admins, so this admits exactly the intended set.
		if m.pluginAPI.User.HasPermissionToChannel(requestingUserID, channel.Id, model.PermissionManageChannelRoles) {
			return nil
		}
		return fmt.Errorf("mentions restricted to channel admins: %w", ErrUsageRestriction)
	case llm.MentionAccessLevelDisabled:
		return fmt.Errorf("channel mentions disabled for bot: %w", ErrUsageRestriction)
	}
	return fmt.Errorf("unknown mention access level %d: %w", bot.GetConfig().MentionAccessLevel, ErrUsageRestriction)
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

func teamMemberActive(client *pluginapi.Client, teamID, userID string) (bool, error) {
	if client == nil {
		return false, fmt.Errorf("team membership check requires plugin client")
	}
	member, err := client.Team.GetMember(teamID, userID)
	if errors.Is(err, pluginapi.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return member != nil && member.DeleteAt == 0, nil
}

// UsageRestrictionsForUserConfig returns nil if userID is allowed by cfg's
// UserAccessLevel / UserIDs / TeamIDs, otherwise an error wrapping ErrUsageRestriction.
// Callers without an MMBots instance (e.g. API code when bots may be nil) should use this
// with the plugin client; MMBots.CheckUsageRestrictionsForUserConfig delegates here.
func UsageRestrictionsForUserConfig(client *pluginapi.Client, cfg llm.BotConfig, requestingUserID string) error {
	switch cfg.UserAccessLevel {
	case llm.UserAccessLevelAll:
		return nil
	case llm.UserAccessLevelAllow:
		if slices.Contains(cfg.UserIDs, requestingUserID) {
			return nil
		}
		for _, teamID := range cfg.TeamIDs {
			isMember, err := teamMemberActive(client, teamID, requestingUserID)
			if err != nil {
				return err
			}
			if isMember {
				return nil
			}
		}
		return fmt.Errorf("user not allowed: %w", ErrUsageRestriction)
	case llm.UserAccessLevelBlock:
		if slices.Contains(cfg.UserIDs, requestingUserID) {
			return fmt.Errorf("user blocked: %w", ErrUsageRestriction)
		}
		for _, teamID := range cfg.TeamIDs {
			isMember, err := teamMemberActive(client, teamID, requestingUserID)
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

// CheckUsageRestrictionsForUserConfig returns nil if userID is allowed by cfg's
// UserAccessLevel / UserIDs / TeamIDs, otherwise an error wrapping ErrUsageRestriction.
// This is the shared source of truth for user-scope access checks; both config-bot
// Bot-based callers (CheckUsageRestrictionsForUser) and DB-agent BotConfig-based
// callers (api.canUserAccessAgent) use it.
func (m *MMBots) CheckUsageRestrictionsForUserConfig(cfg llm.BotConfig, requestingUserID string) error {
	return UsageRestrictionsForUserConfig(m.pluginAPI, cfg, requestingUserID)
}

func (m *MMBots) CheckUsageRestrictionsForUser(bot *Bot, requestingUserID string) error {
	return m.CheckUsageRestrictionsForUserConfig(bot.GetConfig(), requestingUserID)
}
