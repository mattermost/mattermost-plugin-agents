// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scopedsession

import (
	"context"
	"errors"
	"testing"

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
	openPublic := &model.Channel{Id: model.NewId(), TeamId: openTeam.Id, Type: model.ChannelTypeOpen}
	listed := &model.Channel{Id: triggerID, Type: model.ChannelTypePrivate}
	scope := &model.SessionScope{
		Permissions: model.ScopeReadPermissions,
		Resources:   model.SessionScopeResources{ChannelIDs: []string{triggerID}, OpenTeamPublicChannels: true},
	}

	tests := []struct {
		name                  string
		scope                 *model.SessionScope
		channel               *model.Channel
		team                  *model.Team
		openTeamGrantDisabled bool
		allowed               bool
	}{
		{name: "listed private channel the bot is not in", scope: scope, channel: listed, allowed: true},
		{name: "public channel on an open team", scope: scope, channel: openPublic, team: openTeam, allowed: true},
		{name: "public channel on an open team with the grant off", scope: scope, channel: openPublic, team: openTeam, openTeamGrantDisabled: true},
		{name: "listed channel with the open-team grant off", scope: scope, channel: listed, openTeamGrantDisabled: true, allowed: true},
		{name: "scope without the content permission", scope: &model.SessionScope{Permissions: []string{model.PermissionViewTeam.Id}, Resources: scope.Resources}, channel: listed},
		{name: "empty scope from a malformed prop", scope: &model.SessionScope{}, channel: listed},
		{name: "no scope", channel: listed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.allowed, AllowsChannelContent(tt.scope, tt.channel, tt.team, tt.openTeamGrantDisabled))
		})
	}
}

func TestContentChannels(t *testing.T) {
	resources := model.SessionScopeResources{ChannelIDs: []string{"a", "b"}, OpenTeamPublicChannels: true}
	tests := []struct {
		name                  string
		scope                 *model.SessionScope
		openTeamGrantDisabled bool
		expected              model.SessionScopeResources
	}{
		{name: "full scope", scope: &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: resources}, expected: resources},
		{name: "open-team grant off drops open teams", scope: &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: resources}, openTeamGrantDisabled: true, expected: model.SessionScopeResources{ChannelIDs: []string{"a", "b"}}},
		{name: "no content permission reads nothing", scope: &model.SessionScope{Resources: resources}, expected: model.SessionScopeResources{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ContentChannels(tt.scope, tt.openTeamGrantDisabled))
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

func TestOpenTeamGrantDisabled(t *testing.T) {
	cfg := func(compliance *bool) *model.Config {
		return &model.Config{ComplianceSettings: model.ComplianceSettings{Enable: compliance}}
	}
	off, on := false, true

	tests := []struct {
		name     string
		settings ServerSettings
		disabled bool
	}{
		{name: "open when compliance is off and system_user can join", settings: fakeSettings{cfg: cfg(&off), join: true}},
		{name: "compliance mode", settings: fakeSettings{cfg: cfg(&on), join: true}, disabled: true},
		{name: "system_user cannot join public teams", settings: fakeSettings{cfg: cfg(&off)}, disabled: true},
		{name: "compliance setting missing", settings: fakeSettings{cfg: cfg(nil), join: true}, disabled: true},
		{name: "config missing", settings: fakeSettings{join: true}, disabled: true},
		{name: "no settings", disabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.disabled, OpenTeamGrantDisabled(tt.settings))
		})
	}
}
