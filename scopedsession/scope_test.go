// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scopedsession

import (
	"context"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession/scopedtest"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllowsChannelContent covers what the plugin adds on top of
// model.SessionScopeAllowsChannel, which the server tests: the content
// permission, and passing the open-team switch through.
func TestAllowsChannelContent(t *testing.T) {
	triggerID := model.NewId()
	openTeam := &model.Team{Id: model.NewId(), AllowOpenInvite: true}
	inviteOnlyTeam := &model.Team{Id: model.NewId()}
	openPublic := &model.Channel{Id: model.NewId(), TeamId: openTeam.Id, Type: model.ChannelTypeOpen}
	inviteOnlyPublic := &model.Channel{Id: model.NewId(), TeamId: inviteOnlyTeam.Id, Type: model.ChannelTypeOpen}
	listed := &model.Channel{Id: triggerID, Type: model.ChannelTypePrivate}
	scope := &model.SessionScope{
		Permissions: model.ScopeReadPermissions,
		Resources:   model.SessionScopeResources{ChannelIDs: []string{triggerID}, OpenTeamPublicChannels: true, PublicChannelTeamIDs: []string{inviteOnlyTeam.Id}},
	}
	compliance := model.SessionScopeLimits{ComplianceMode: true}
	noJoin := model.SessionScopeLimits{UsersCannotJoinOpenTeams: true}

	tests := []struct {
		name    string
		scope   *model.SessionScope
		channel *model.Channel
		team    *model.Team
		limits  model.SessionScopeLimits
		allowed bool
	}{
		{name: "listed private channel the bot is not in", scope: scope, channel: listed, allowed: true},
		{name: "public channel on an open team", scope: scope, channel: openPublic, team: openTeam, allowed: true},
		{name: "public channel on the triggering team, which is not open", scope: scope, channel: inviteOnlyPublic, team: inviteOnlyTeam, allowed: true},
		{name: "open-team grant off because people cannot join teams", scope: scope, channel: openPublic, team: openTeam, limits: noJoin},
		{name: "triggering team's grant survives people not being able to join teams", scope: scope, channel: inviteOnlyPublic, team: inviteOnlyTeam, limits: noJoin, allowed: true},
		{name: "compliance mode turns off both public grants", scope: scope, channel: inviteOnlyPublic, team: inviteOnlyTeam, limits: compliance},
		{name: "listed channel in compliance mode", scope: scope, channel: listed, limits: compliance, allowed: true},
		{name: "scope without the content permission", scope: &model.SessionScope{Permissions: []string{model.PermissionViewTeam.Id}, Resources: scope.Resources}, channel: listed},
		{name: "empty scope from a malformed prop", scope: &model.SessionScope{}, channel: listed},
		{name: "no scope", channel: listed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.allowed, AllowsChannelContent(tt.scope, tt.channel, tt.team, tt.limits))
		})
	}
}

func TestContentChannels(t *testing.T) {
	resources := model.SessionScopeResources{ChannelIDs: []string{"a", "b"}, OpenTeamPublicChannels: true, PublicChannelTeamIDs: []string{"t"}}
	tests := []struct {
		name     string
		scope    *model.SessionScope
		limits   model.SessionScopeLimits
		expected model.SessionScopeResources
	}{
		{name: "full scope", scope: &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: resources}, expected: resources},
		{
			name:     "people who cannot join teams drop the open-team grant only",
			scope:    &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: resources},
			limits:   model.SessionScopeLimits{UsersCannotJoinOpenTeams: true},
			expected: model.SessionScopeResources{ChannelIDs: []string{"a", "b"}, PublicChannelTeamIDs: []string{"t"}},
		},
		{
			name:     "compliance mode drops both public grants",
			scope:    &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: resources},
			limits:   model.SessionScopeLimits{ComplianceMode: true},
			expected: model.SessionScopeResources{ChannelIDs: []string{"a", "b"}},
		},
		{name: "no content permission reads nothing", scope: &model.SessionScope{Resources: resources}, expected: model.SessionScopeResources{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ContentChannels(tt.scope, tt.limits))
		})
	}
}

func TestScopeFromContext(t *testing.T) {
	scope := &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: model.SessionScopeResources{ChannelIDs: []string{"c"}}}
	ended := NewBinding(scope)
	ended.End()

	tests := []struct {
		name     string
		ctx      context.Context
		expected *model.SessionScope
		wantErr  error
	}{
		{name: "live binding", ctx: WithBinding(context.Background(), NewBinding(scope)), expected: scope},
		{name: "ended binding is denied", ctx: WithBinding(context.Background(), ended), wantErr: ErrRunEnded},
		{name: "binding without a scope is denied", ctx: WithBinding(context.Background(), NewBinding(nil)), wantErr: errAny},
		{name: "nil binding is denied", ctx: WithBinding(context.Background(), nil), wantErr: errAny},
		{name: "no binding is the user's own read", ctx: context.Background()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ScopeFromContext(tt.ctx)
			if tt.wantErr != nil {
				require.Error(t, err)
				if tt.wantErr != errAny {
					require.ErrorIs(t, err, tt.wantErr)
				}
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

var errAny = errors.New("any error")

type fakeSessionAPI struct {
	createErr *model.AppError
	session   *model.Session
	revoked   []string
}

func (f *fakeSessionAPI) CreateScopedSession(string, *model.SessionScope, int64) (*model.Session, *model.AppError) {
	return f.session, f.createErr
}

func (f *fakeSessionAPI) RevokeSession(sessionID string) *model.AppError {
	f.revoked = append(f.revoked, sessionID)
	return nil
}

func TestPluginAPIMinter(t *testing.T) {
	minted := &model.Session{Id: model.NewId()}
	tests := []struct {
		name    string
		api     *fakeSessionAPI
		wantErr bool
	}{
		{name: "server mints", api: &fakeSessionAPI{session: minted}},
		{name: "server refuses", api: &fakeSessionAPI{createErr: model.NewAppError("CreateScopedSession", "id", nil, "", 400)}, wantErr: true},
		{name: "server returns nothing", api: &fakeSessionAPI{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := NewPluginAPIMinter(tt.api).CreateScopedSession(model.NewId(), &model.SessionScope{}, model.GetMillis())
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, session)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, minted, session)
		})
	}
}

type fakeSettings struct {
	cfg  *model.Config
	join bool
}

func (f fakeSettings) GetConfig() *model.Config { return f.cfg }

func (f fakeSettings) RolesGrantPermission(roles []string, permissionID string) bool {
	return f.join && len(roles) == 1 && roles[0] == model.SystemUserRoleId && permissionID == model.PermissionJoinPublicTeams.Id
}

func TestServerLimits(t *testing.T) {
	cfg := func(compliance *bool) *model.Config {
		return &model.Config{ComplianceSettings: model.ComplianceSettings{Enable: compliance}}
	}
	off, on := false, true
	failClosed := model.SessionScopeLimits{ComplianceMode: true, UsersCannotJoinOpenTeams: true}

	tests := []struct {
		name     string
		settings ServerSettings
		limits   model.SessionScopeLimits
		reason   PublicGrantsOff
	}{
		{name: "no limits when compliance is off and system_user can join", settings: fakeSettings{cfg: cfg(&off), join: true}, reason: PublicGrantsOn},
		{name: "compliance mode", settings: fakeSettings{cfg: cfg(&on), join: true}, limits: model.SessionScopeLimits{ComplianceMode: true}, reason: PublicGrantsOffCompliance},
		{name: "system_user cannot join public teams", settings: fakeSettings{cfg: cfg(&off)}, limits: model.SessionScopeLimits{UsersCannotJoinOpenTeams: true}, reason: PublicGrantsOffUsersCannotJoin},
		{name: "compliance setting missing means compliance is off", settings: fakeSettings{cfg: cfg(nil), join: true}, reason: PublicGrantsOn},
		{name: "compliance setting missing and system_user cannot join", settings: fakeSettings{cfg: cfg(nil)}, limits: model.SessionScopeLimits{UsersCannotJoinOpenTeams: true}, reason: PublicGrantsOffUsersCannotJoin},
		{name: "config missing", settings: fakeSettings{join: true}, limits: failClosed, reason: PublicGrantsOffUnknown},
		{name: "no settings", limits: failClosed, reason: PublicGrantsOffUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.limits, ServerLimits(tt.settings))
			assert.Equal(t, tt.reason, PublicGrantsOffReason(tt.settings))
		})
	}
}

func TestTeamClosedReasons(t *testing.T) {
	tests := []struct {
		name     string
		team     *model.Team
		expected []TeamClosedReason
	}{
		{name: "open team", team: &model.Team{AllowOpenInvite: true}},
		{name: "invite only", team: &model.Team{}, expected: []TeamClosedReason{TeamClosedInviteOnly}},
		{name: "domain restricted", team: &model.Team{AllowOpenInvite: true, AllowedDomains: "example.com"}, expected: []TeamClosedReason{TeamClosedDomainRestricted}},
		{name: "group constrained", team: &model.Team{AllowOpenInvite: true, GroupConstrained: model.NewPointer(true)}, expected: []TeamClosedReason{TeamClosedGroupConstrained}},
		{name: "policy enforced", team: &model.Team{AllowOpenInvite: true, PolicyEnforced: true}, expected: []TeamClosedReason{TeamClosedPolicyEnforced}},
		{name: "archived", team: &model.Team{AllowOpenInvite: true, DeleteAt: 1}, expected: []TeamClosedReason{TeamClosedArchived}},
		{
			name:     "several reasons",
			team:     &model.Team{AllowedDomains: "example.com", PolicyEnforced: true},
			expected: []TeamClosedReason{TeamClosedInviteOnly, TeamClosedDomainRestricted, TeamClosedPolicyEnforced},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reasons := TeamClosedReasons(tt.team)
			assert.Equal(t, tt.expected, reasons)
			assert.Equal(t, tt.team.IsOpenToAllUsers(), len(reasons) == 0, "reasons must agree with the server's open-team rule")
		})
	}
}

func TestTeamGrantOff(t *testing.T) {
	tests := []struct {
		reason PublicGrantsOff
		off    bool
	}{
		{reason: PublicGrantsOn},
		{reason: PublicGrantsOffUsersCannotJoin},
		{reason: PublicGrantsOffCompliance, off: true},
		{reason: PublicGrantsOffUnknown, off: true},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			assert.Equal(t, tt.off, TeamGrantOff(tt.reason))
		})
	}
}

// A false compliance setting reaches the plugin as nil over the plugin RPC, so
// the grants must stay on for it.
func TestServerLimitsWithRPCDecodedConfig(t *testing.T) {
	tests := []struct {
		name       string
		compliance bool
		limits     model.SessionScopeLimits
	}{
		{name: "compliance off", limits: model.SessionScopeLimits{}},
		{name: "compliance on", compliance: true, limits: model.SessionScopeLimits{ComplianceMode: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := scopedtest.RPCConfig(t, &model.Config{ComplianceSettings: model.ComplianceSettings{Enable: model.NewPointer(tt.compliance)}})
			if !tt.compliance {
				require.Nil(t, cfg.ComplianceSettings.Enable, "the RPC drops a false pointer; this test is about that")
			}
			assert.Equal(t, tt.limits, ServerLimits(fakeSettings{cfg: cfg, join: true}))
		})
	}
}
