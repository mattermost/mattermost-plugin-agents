// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package scopedsession holds the plugin side of server-enforced scoped
// sessions (model.SessionScope): short-lived sessions an agent's bot uses for
// one channel-triggered run, which can read exactly the channels in their
// scope and nothing else. The server enforces the scope on its API; this
// package applies the same scope to reads the plugin serves itself.
package scopedsession

import (
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
	failClosed := model.SessionScopeLimits{ComplianceMode: true, UsersCannotJoinOpenTeams: true}
	if settings == nil {
		return failClosed
	}
	cfg := settings.GetConfig()
	if cfg == nil {
		return failClosed
	}
	// The plugin RPC transmits a false pointer as nil, so a missing setting is
	// compliance mode off, the default.
	if cfg.ComplianceSettings.Enable != nil && *cfg.ComplianceSettings.Enable {
		return model.SessionScopeLimits{ComplianceMode: true}
	}
	return model.SessionScopeLimits{
		UsersCannotJoinOpenTeams: !settings.RolesGrantPermission([]string{model.SystemUserRoleId}, model.PermissionJoinPublicTeams.Id),
	}
}
