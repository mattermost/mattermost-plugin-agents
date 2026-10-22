// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package scopedsession holds the plugin side of server-enforced scoped
// sessions: short-lived sessions an agent's bot uses for one channel-triggered
// run, which can read exactly the channels in their scope and nothing else.
//
// The scope shape mirrors the server's model.SessionScope. Until server/public
// ships that type, the plugin keeps its own copy here, and the server is
// reached through the Minter interface.
package scopedsession

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// PropKey is the Session.Props key holding the JSON-encoded Scope.
const PropKey = "scope"

// Scope is a fixed set of read permissions plus the channels they apply to.
// For a scoped session, read access is exactly the scope: the user's channel
// and team memberships neither add to it nor are required for it.
type Scope struct {
	Permissions       []string  `json:"permissions"`
	Resources         Resources `json:"resources"`
	OnBehalfOfUserID  string    `json:"on_behalf_of_user_id,omitempty"`
	CreatedByPluginID string    `json:"created_by_plugin_id"`
}

// Resources lists the channels a scope covers.
type Resources struct {
	ChannelIDs             []string `json:"channel_ids,omitempty"`
	OpenTeamPublicChannels bool     `json:"open_team_public_channels,omitempty"`
}

// ReadPermissions is every permission a scope may hold.
func ReadPermissions() []string {
	return []string{
		model.PermissionReadChannel.Id,
		model.PermissionReadChannelContent.Id,
		model.PermissionReadPublicChannel.Id,
		model.PermissionListTeamChannels.Id,
		model.PermissionViewTeam.Id,
	}
}

// FromSession reports the session's scope. A scope prop that does not parse
// still marks the session as scoped, with an empty scope that allows nothing.
func FromSession(session *model.Session) (*Scope, bool) {
	if session == nil {
		return nil, false
	}
	raw, ok := session.Props[PropKey]
	if !ok {
		return nil, false
	}
	var scope Scope
	if err := json.Unmarshal([]byte(raw), &scope); err != nil {
		return &Scope{}, true
	}
	return &scope, true
}

func (s *Scope) allowsReadingContent() bool {
	return s != nil && slices.Contains(s.Permissions, model.PermissionReadChannelContent.Id)
}

// ContentChannels is the channel set whose content this scope can read, for
// stores that filter in their own queries. A scope that cannot read content
// yields an empty set, which matches nothing.
func (s *Scope) ContentChannels(complianceEnabled bool) Resources {
	if !s.allowsReadingContent() {
		return Resources{}
	}
	return Resources{
		ChannelIDs:             slices.Clone(s.Resources.ChannelIDs),
		OpenTeamPublicChannels: s.Resources.OpenTeamPublicChannels && !complianceEnabled,
	}
}

// AllowsChannelContent reports whether the scope can read channel's posts and
// files. team is channel's team; it is only consulted for the open-team rule.
// Server-wide compliance mode turns the open-team rule off, as it already
// blocks non-member public reads.
func (s *Scope) AllowsChannelContent(channel *model.Channel, team *model.Team, complianceEnabled bool) bool {
	if !s.allowsReadingContent() || channel == nil || channel.DeleteAt != 0 || channel.PolicyEnforced {
		return false
	}
	if channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate {
		return false
	}
	if slices.Contains(s.Resources.ChannelIDs, channel.Id) {
		return true
	}
	return s.Resources.OpenTeamPublicChannels &&
		!complianceEnabled &&
		channel.Type == model.ChannelTypeOpen &&
		team != nil && team.Id == channel.TeamId &&
		IsOpenTeam(team)
}

// ComplianceEnabled reports server-wide compliance mode. A missing config
// counts as enabled, so open-team reads fail closed.
func ComplianceEnabled(cfg *model.Config) bool {
	if cfg == nil || cfg.ComplianceSettings.Enable == nil {
		return true
	}
	return *cfg.ComplianceSettings.Enable
}

// IsOpenTeam reports whether any user on the server can join team. Teams only
// some users can join are not open, so their public channels are never in an
// open-team scope. postgres.openTeamSQL encodes the same rule.
func IsOpenTeam(team *model.Team) bool {
	return team != nil &&
		team.AllowOpenInvite &&
		team.DeleteAt == 0 &&
		!team.IsGroupConstrained() &&
		strings.TrimSpace(team.AllowedDomains) == "" &&
		!team.PolicyEnforced
}
