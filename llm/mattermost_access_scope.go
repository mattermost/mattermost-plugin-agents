// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
)

// MattermostAccessScope defines runtime guardrails for a bridge completion run.
// When nil, no restrictions are applied (fully backward compatible).
type MattermostAccessScope struct {
	// TeamID anchors the run to a single team. Required when allowed_channel_ids is set.
	TeamID string `json:"team_id"`
	// AllowedChannelIDs is an optional allowlist of specific channel IDs.
	// Treated as an intersection with the team constraint, not an override.
	AllowedChannelIDs []string `json:"allowed_channel_ids,omitempty"`
}

// Validate checks that the scope fields are internally consistent.
// Call this at parse time in the API handler.
func (s *MattermostAccessScope) Validate() error {
	if s == nil {
		return nil
	}

	hasChannelIDs := len(s.AllowedChannelIDs) > 0

	if s.TeamID == "" && hasChannelIDs {
		return fmt.Errorf("team_id is required when allowed_channel_ids is set")
	}

	if s.TeamID != "" && !model.IsValidId(s.TeamID) {
		return fmt.Errorf("team_id must be a valid ID")
	}

	for _, id := range s.AllowedChannelIDs {
		if !model.IsValidId(id) {
			return fmt.Errorf("allowed_channel_ids contains invalid ID %q", id)
		}
	}

	return nil
}

// AllowsTeam returns true if the given team ID is permitted by this scope.
// A nil scope permits all teams.
func (s *MattermostAccessScope) AllowsTeam(teamID string) bool {
	if s == nil {
		return true
	}
	return s.TeamID == "" || s.TeamID == teamID
}

// AllowsChannel returns true if the given channel is permitted by this scope.
// Checks team membership and the channel ID allowlist.
// A nil scope permits all channels.
func (s *MattermostAccessScope) AllowsChannel(channel *model.Channel) bool {
	if s == nil || channel == nil {
		return true
	}

	// Team check: channels with a team must match (DMs/GMs have empty TeamId)
	if s.TeamID != "" && channel.TeamId != "" && channel.TeamId != s.TeamID {
		return false
	}

	// Channel ID allowlist check
	if len(s.AllowedChannelIDs) > 0 {
		if !slices.Contains(s.AllowedChannelIDs, channel.Id) {
			return false
		}
	}

	return true
}

// ChannelDeniedError returns a formatted error for scope violations.
func (s *MattermostAccessScope) ChannelDeniedError(channelID string) error {
	return fmt.Errorf("channel %s is outside the execution scope for this run", channelID)
}

// TeamDeniedError returns a formatted error for scope violations.
func (s *MattermostAccessScope) TeamDeniedError(teamID string) error {
	return fmt.Errorf("team %s is outside the execution scope for this run", teamID)
}
