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
// an empty set, which matches nothing. Compliance mode turns the open-team
// grant off, as it does on the server.
func ContentChannels(scope *model.SessionScope, complianceEnabled bool) model.SessionScopeResources {
	if !readsContent(scope) {
		return model.SessionScopeResources{}
	}
	return model.SessionScopeResources{
		ChannelIDs:             slices.Clone(scope.Resources.ChannelIDs),
		OpenTeamPublicChannels: scope.Resources.OpenTeamPublicChannels && !complianceEnabled,
	}
}

// AllowsChannelContent reports whether scope can read channel's posts and
// files. team is channel's team, or nil when it has none.
func AllowsChannelContent(scope *model.SessionScope, channel *model.Channel, team *model.Team, complianceEnabled bool) bool {
	return readsContent(scope) && model.SessionScopeAllowsChannel(scope, channel, team, complianceEnabled)
}

// ComplianceEnabled reports server-wide compliance mode. A missing config
// counts as enabled, so open-team reads fail closed.
func ComplianceEnabled(cfg *model.Config) bool {
	if cfg == nil || cfg.ComplianceSettings.Enable == nil {
		return true
	}
	return *cfg.ComplianceSettings.Enable
}
