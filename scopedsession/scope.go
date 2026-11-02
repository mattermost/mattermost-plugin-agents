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
// an empty set, which matches nothing. limits turn the public-channel grants
// off; see ServerLimits.
func ContentChannels(scope *model.SessionScope, limits model.SessionScopeLimits) model.SessionScopeResources {
	if !readsContent(scope) {
		return model.SessionScopeResources{}
	}
	return scope.Resources.WithLimits(limits)
}

// AllowsChannelContent reports whether scope can read channel's posts and
// files. team is channel's team, or nil when it has none.
func AllowsChannelContent(scope *model.SessionScope, channel *model.Channel, team *model.Team, limits model.SessionScopeLimits) bool {
	return readsContent(scope) && model.SessionScopeAllowsChannel(scope, channel, team, limits)
}

// ServerSettings is the part of the Mattermost API that decides whether the
// public-channel grants apply.
type ServerSettings interface {
	GetConfig() *model.Config
	RolesGrantPermission(roleNames []string, permissionID string) bool
}

// ServerLimits mirrors the server: compliance mode turns both public-channel
// grants off, and a system_user role without join_public_teams turns the
// open-team grant off. Without a readable config both are off, so public
// reads fail closed.
func ServerLimits(settings ServerSettings) model.SessionScopeLimits {
	limits, _ := serverLimits(settings)
	return limits
}

func serverLimits(settings ServerSettings) (limits model.SessionScopeLimits, readable bool) {
	failClosed := model.SessionScopeLimits{ComplianceMode: true, UsersCannotJoinOpenTeams: true}
	if settings == nil {
		return failClosed, false
	}
	cfg := settings.GetConfig()
	if cfg == nil {
		return failClosed, false
	}
	// The plugin RPC transmits a false pointer as nil, so a missing setting is
	// compliance mode off, the default.
	if cfg.ComplianceSettings.Enable != nil && *cfg.ComplianceSettings.Enable {
		return model.SessionScopeLimits{ComplianceMode: true}, true
	}
	return model.SessionScopeLimits{
		UsersCannotJoinOpenTeams: !settings.RolesGrantPermission([]string{model.SystemUserRoleId}, model.PermissionJoinPublicTeams.Id),
	}, true
}

// PublicGrantsOff says why a public-channel grant is off.
type PublicGrantsOff string

const (
	PublicGrantsOn PublicGrantsOff = ""
	// PublicGrantsOffUnknown: the server's settings could not be read, so both
	// grants are off.
	PublicGrantsOffUnknown PublicGrantsOff = "unknown"
	// PublicGrantsOffCompliance: compliance mode turns both grants off.
	PublicGrantsOffCompliance PublicGrantsOff = "compliance_mode"
	// PublicGrantsOffUsersCannotJoin: people cannot join teams on their own,
	// which turns off the open-team grant only.
	PublicGrantsOffUsersCannotJoin PublicGrantsOff = "users_cannot_join_teams"
)

// PublicGrantsOffReason says why the open-team grant is off, or PublicGrantsOn.
// The grant to the triggering team's public channels is off exactly when the
// reason is PublicGrantsOffCompliance or PublicGrantsOffUnknown; see
// TeamGrantOff.
func PublicGrantsOffReason(settings ServerSettings) PublicGrantsOff {
	limits, readable := serverLimits(settings)
	switch {
	case !readable:
		return PublicGrantsOffUnknown
	case limits.ComplianceMode:
		return PublicGrantsOffCompliance
	case limits.UsersCannotJoinOpenTeams:
		return PublicGrantsOffUsersCannotJoin
	}
	return PublicGrantsOn
}

// TeamGrantOff reports whether the grant to the triggering team's public
// channels is off under reason.
func TeamGrantOff(reason PublicGrantsOff) bool {
	return reason == PublicGrantsOffCompliance || reason == PublicGrantsOffUnknown
}

// NewRunScope is the scope of one channel-triggered run: read access to the
// listed channels, to every public channel on teamID (the team of the channel
// that triggered the run), and to every public channel on an open team.
func NewRunScope(requesterID string, channelIDs []string, teamID string) *model.SessionScope {
	return &model.SessionScope{
		Permissions: slices.Clone(model.ScopeReadPermissions),
		Resources: model.SessionScopeResources{
			ChannelIDs:             slices.Clone(channelIDs),
			OpenTeamPublicChannels: true,
			PublicChannelTeamIDs:   []string{teamID},
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
