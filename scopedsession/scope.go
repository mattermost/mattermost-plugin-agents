// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package scopedsession holds the plugin side of server-enforced scoped
// sessions (model.SessionScope): short-lived sessions an agent's bot uses for
// one channel-triggered run, which can read exactly the channels in their
// scope and nothing else. The server enforces the scope on its API; this
// package applies the same scope to reads the plugin serves itself.
package scopedsession

import (
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
)

func readsContent(scope *model.SessionScope) bool {
	return scope != nil && scope.HasPermission(model.PermissionReadChannelContent.Id)
}

// ContentChannels is the channel set whose content scope can read, for stores
// that filter in their own queries. A scope that cannot read content yields
// an empty set, which matches nothing. See OpenTeamGrantDisabled for when the
// open-team grant is off.
func ContentChannels(scope *model.SessionScope, openTeamGrantDisabled bool) model.SessionScopeResources {
	if !readsContent(scope) {
		return model.SessionScopeResources{}
	}
	return model.SessionScopeResources{
		ChannelIDs:             slices.Clone(scope.Resources.ChannelIDs),
		OpenTeamPublicChannels: scope.Resources.OpenTeamPublicChannels && !openTeamGrantDisabled,
	}
}

// AllowsChannelContent reports whether scope can read channel's posts and
// files. team is channel's team, or nil when it has none.
func AllowsChannelContent(scope *model.SessionScope, channel *model.Channel, team *model.Team, openTeamGrantDisabled bool) bool {
	return readsContent(scope) && model.SessionScopeAllowsChannel(scope, channel, team, openTeamGrantDisabled)
}

// ServerSettings is the part of the Mattermost API that decides whether the
// open-team grant applies.
type ServerSettings interface {
	GetConfig() *model.Config
	RolesGrantPermission(roleNames []string, permissionID string) bool
}

// OpenTeamGrantOff says why the open-team grant is off.
type OpenTeamGrantOff string

const (
	OpenTeamGrantOn                 OpenTeamGrantOff = ""
	OpenTeamGrantOffUnknown         OpenTeamGrantOff = "unknown"
	OpenTeamGrantOffCompliance      OpenTeamGrantOff = "compliance_mode"
	OpenTeamGrantOffUsersCannotJoin OpenTeamGrantOff = "users_cannot_join_teams"
)

// OpenTeamGrantOffReason mirrors the server: the open-team grant is off in
// compliance mode and when the system_user role lacks join_public_teams.
// Without readable settings it is off, so open-team reads fail closed.
func OpenTeamGrantOffReason(settings ServerSettings) OpenTeamGrantOff {
	if settings == nil {
		return OpenTeamGrantOffUnknown
	}
	cfg := settings.GetConfig()
	switch {
	case cfg == nil || cfg.ComplianceSettings.Enable == nil:
		return OpenTeamGrantOffUnknown
	case *cfg.ComplianceSettings.Enable:
		return OpenTeamGrantOffCompliance
	case !settings.RolesGrantPermission([]string{model.SystemUserRoleId}, model.PermissionJoinPublicTeams.Id):
		return OpenTeamGrantOffUsersCannotJoin
	}
	return OpenTeamGrantOn
}

// OpenTeamGrantDisabled reports whether the open-team grant is off.
func OpenTeamGrantDisabled(settings ServerSettings) bool {
	return OpenTeamGrantOffReason(settings) != OpenTeamGrantOn
}

// NewRunScope is the scope of one channel-triggered run: read access to the
// listed channels and to every public channel on an open team.
func NewRunScope(requesterID string, channelIDs []string) *model.SessionScope {
	return &model.SessionScope{
		Permissions: slices.Clone(model.ScopeReadPermissions),
		Resources: model.SessionScopeResources{
			ChannelIDs:             slices.Clone(channelIDs),
			OpenTeamPublicChannels: true,
		},
		OnBehalfOfUserID: requesterID,
	}
}

// TeamClosedReason says why a team does not count as open to all users.
type TeamClosedReason string

const (
	TeamClosedArchived         TeamClosedReason = "archived"
	TeamClosedInviteOnly       TeamClosedReason = "invite_only"
	TeamClosedGroupConstrained TeamClosedReason = "group_constrained"
	TeamClosedDomainRestricted TeamClosedReason = "domain_restricted"
	TeamClosedPolicyEnforced   TeamClosedReason = "policy_enforced"
	TeamClosedOther            TeamClosedReason = "other"
)

// TeamClosedReasons explains why team is not open to all users. The server's
// Team.IsOpenToAllUsers decides: the result is empty exactly when it is true,
// and a team it rejects for a reason not listed here is reported as
// TeamClosedOther.
func TeamClosedReasons(team *model.Team) []TeamClosedReason {
	if team.IsOpenToAllUsers() {
		return nil
	}
	var reasons []TeamClosedReason
	if team.DeleteAt != 0 {
		reasons = append(reasons, TeamClosedArchived)
	}
	if !team.AllowOpenInvite {
		reasons = append(reasons, TeamClosedInviteOnly)
	}
	if team.IsGroupConstrained() {
		reasons = append(reasons, TeamClosedGroupConstrained)
	}
	if team.AllowedDomains != "" {
		reasons = append(reasons, TeamClosedDomainRestricted)
	}
	if team.PolicyEnforced {
		reasons = append(reasons, TeamClosedPolicyEnforced)
	}
	if len(reasons) == 0 {
		reasons = append(reasons, TeamClosedOther)
	}
	return reasons
}
