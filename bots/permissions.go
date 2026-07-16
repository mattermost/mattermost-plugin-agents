// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"context"
	"fmt"
	"slices"

	"errors"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var ErrUsageRestriction = errors.New("usage restriction")

func (m *MMBots) CheckUsageRestrictions(ctx context.Context, requestingUserID string, bot *Bot, channel *model.Channel) error {
	if err := m.CheckUsageRestrictionsForUser(ctx, bot, requestingUserID); err != nil {
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
	case llm.UserAccessLevelAttributeBased:
		// Gated exclusively by Checker.CanUseAgent, which never invokes this
		// legacy check for that mode; the case only keeps stale direct
		// callers off the "unknown user assistance level" fallthrough.
		return nil
	}
	return fmt.Errorf("unknown user assistance level")
}

// CheckUsageRestrictionsForUserConfig is the composite per-request user gate:
// the ABAC agent decision (with the legacy UserAccessLevel switch as the
// legacyCheck closure), then the ABAC service decision for cfg.ServiceID.
// Every user-attributable completion entry point funnels through here.
func (m *MMBots) CheckUsageRestrictionsForUserConfig(ctx context.Context, cfg llm.BotConfig, requestingUserID string) error {
	legacy := func() error { return UsageRestrictionsForUserConfig(m.pluginAPI, cfg, requestingUserID) }
	if err := m.accessChecker.CanUseAgent(ctx, requestingUserID, &cfg, legacy); err != nil {
		return wrapDeny(err)
	}
	if err := m.accessChecker.CanUseService(ctx, requestingUserID, cfg.ServiceID); err != nil {
		return wrapDeny(err)
	}
	return nil
}

// wrapDeny makes ABAC denials satisfy the ErrUsageRestriction sentinel that
// existing callers branch on; legacy errors already wrap it and infra errors
// pass through unchanged.
func wrapDeny(err error) error {
	if errors.Is(err, accesscontrol.ErrAccessDenied) && !errors.Is(err, ErrUsageRestriction) {
		return fmt.Errorf("%w: %w", ErrUsageRestriction, err)
	}
	return err
}

func (m *MMBots) CheckUsageRestrictionsForUser(ctx context.Context, bot *Bot, requestingUserID string) error {
	return m.CheckUsageRestrictionsForUserConfig(ctx, bot.GetConfig(), requestingUserID)
}
