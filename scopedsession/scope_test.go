// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scopedsession

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllowsChannelContent(t *testing.T) {
	triggerID := model.NewId()
	openTeam := &model.Team{Id: model.NewId(), AllowOpenInvite: true}
	scope := &model.SessionScope{
		Permissions: model.ScopeReadPermissions,
		Resources:   model.SessionScopeResources{ChannelIDs: []string{triggerID}, OpenTeamPublicChannels: true},
	}

	tests := []struct {
		name       string
		scope      *model.SessionScope
		channel    *model.Channel
		team       *model.Team
		compliance bool
		allowed    bool
	}{
		{
			name:    "listed private channel the bot is not in",
			scope:   scope,
			channel: &model.Channel{Id: triggerID, Type: model.ChannelTypePrivate},
			allowed: true,
		},
		{
			name:    "public channel on an open team",
			scope:   scope,
			channel: &model.Channel{Id: model.NewId(), TeamId: openTeam.Id, Type: model.ChannelTypeOpen},
			team:    openTeam,
			allowed: true,
		},
		{
			name:    "private channel on an open team",
			scope:   scope,
			channel: &model.Channel{Id: model.NewId(), TeamId: openTeam.Id, Type: model.ChannelTypePrivate},
			team:    openTeam,
		},
		{
			name:    "public channel on an invite-only team",
			scope:   scope,
			channel: &model.Channel{Id: model.NewId(), TeamId: "closed", Type: model.ChannelTypeOpen},
			team:    &model.Team{Id: "closed"},
		},
		{
			name:    "public channel on a team restricted to email domains",
			scope:   scope,
			channel: &model.Channel{Id: model.NewId(), TeamId: "domains", Type: model.ChannelTypeOpen},
			team:    &model.Team{Id: "domains", AllowOpenInvite: true, AllowedDomains: "example.com"},
		},
		{
			name:    "public channel on a group-constrained team",
			scope:   scope,
			channel: &model.Channel{Id: model.NewId(), TeamId: "groups", Type: model.ChannelTypeOpen},
			team:    &model.Team{Id: "groups", AllowOpenInvite: true, GroupConstrained: new(true)},
		},
		{
			name:    "public channel on a policy-enforced team",
			scope:   scope,
			channel: &model.Channel{Id: model.NewId(), TeamId: "policy", Type: model.ChannelTypeOpen},
			team:    &model.Team{Id: "policy", AllowOpenInvite: true, PolicyEnforced: true},
		},
		{
			name:    "public channel on a deleted team",
			scope:   scope,
			channel: &model.Channel{Id: model.NewId(), TeamId: "gone", Type: model.ChannelTypeOpen},
			team:    &model.Team{Id: "gone", AllowOpenInvite: true, DeleteAt: 1},
		},
		{
			name:       "open-team public channel with compliance mode on",
			scope:      scope,
			channel:    &model.Channel{Id: model.NewId(), TeamId: openTeam.Id, Type: model.ChannelTypeOpen},
			team:       openTeam,
			compliance: true,
		},
		{
			name:       "listed channel with compliance mode on",
			scope:      scope,
			channel:    &model.Channel{Id: triggerID, Type: model.ChannelTypePrivate},
			compliance: true,
			allowed:    true,
		},
		{
			name:    "listed DM",
			scope:   &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: model.SessionScopeResources{ChannelIDs: []string{triggerID}}},
			channel: &model.Channel{Id: triggerID, Type: model.ChannelTypeDirect},
		},
		{
			name:    "listed group message",
			scope:   &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: model.SessionScopeResources{ChannelIDs: []string{triggerID}}},
			channel: &model.Channel{Id: triggerID, Type: model.ChannelTypeGroup},
		},
		{
			name:    "listed channel under an access policy",
			scope:   scope,
			channel: &model.Channel{Id: triggerID, Type: model.ChannelTypePrivate, PolicyEnforced: true},
		},
		{
			name:    "listed deleted channel",
			scope:   scope,
			channel: &model.Channel{Id: triggerID, Type: model.ChannelTypePrivate, DeleteAt: 1},
		},
		{
			name:    "team that does not own the channel",
			scope:   scope,
			channel: &model.Channel{Id: model.NewId(), TeamId: "other", Type: model.ChannelTypeOpen},
			team:    openTeam,
		},
		{
			name:    "scope without the content permission",
			scope:   &model.SessionScope{Permissions: []string{model.PermissionViewTeam.Id}, Resources: scope.Resources},
			channel: &model.Channel{Id: triggerID, Type: model.ChannelTypePrivate},
		},
		{
			name:    "empty scope from a malformed prop",
			scope:   &model.SessionScope{},
			channel: &model.Channel{Id: triggerID, Type: model.ChannelTypePrivate},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.allowed, AllowsChannelContent(tt.scope, tt.channel, tt.team, tt.compliance))
		})
	}
}

func TestContentChannels(t *testing.T) {
	resources := model.SessionScopeResources{ChannelIDs: []string{"a", "b"}, OpenTeamPublicChannels: true}
	tests := []struct {
		name       string
		scope      *model.SessionScope
		compliance bool
		expected   model.SessionScopeResources
	}{
		{name: "full scope", scope: &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: resources}, expected: resources},
		{name: "compliance drops open teams", scope: &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: resources}, compliance: true, expected: model.SessionScopeResources{ChannelIDs: []string{"a", "b"}}},
		{name: "no content permission reads nothing", scope: &model.SessionScope{Resources: resources}, expected: model.SessionScopeResources{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ContentChannels(tt.scope, tt.compliance))
		})
	}
}

func TestServerSupports(t *testing.T) {
	tests := []struct {
		version   string
		supported bool
	}{
		{version: "12.2.0", supported: true},
		{version: "12.3.1", supported: true},
		{version: "13.0.0", supported: true},
		{version: "12.1.9", supported: false},
		{version: "11.5.0", supported: false},
		{version: "", supported: false},
		{version: "not a version", supported: false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			assert.Equal(t, tt.supported, ServerSupports(tt.version))
		})
	}
}

func TestRegistryScopeForSession(t *testing.T) {
	minted := &model.Session{Id: model.NewId(), ExpiresAt: model.GetMillis() + 60_000}
	mintedScope := &model.SessionScope{Permissions: model.ScopeReadPermissions, Resources: model.SessionScopeResources{ChannelIDs: []string{"c"}}}
	encoded, err := json.Marshal(mintedScope)
	require.NoError(t, err)
	elsewhere := &model.Session{Id: model.NewId(), Props: map[string]string{model.SessionPropScope: string(encoded)}}
	normal := &model.Session{Id: model.NewId()}
	lookupErr := errors.New("db down")

	registry := NewRegistry(func(id string) (*model.Session, error) {
		switch id {
		case elsewhere.Id:
			return elsewhere, nil
		case normal.Id:
			return normal, nil
		default:
			return nil, lookupErr
		}
	})
	registry.Register(minted, mintedScope)
	revoked := &model.Session{Id: model.NewId(), ExpiresAt: model.GetMillis() + 60_000}
	registry.Register(revoked, mintedScope)
	registry.Revoke(revoked.Id)

	tests := []struct {
		name      string
		sessionID string
		expected  *model.SessionScope
		wantErr   error
	}{
		{name: "minted on this node", sessionID: minted.Id, expected: mintedScope},
		{name: "revoked on this node is denied", sessionID: revoked.Id, wantErr: ErrRevoked},
		{name: "scoped elsewhere", sessionID: elsewhere.Id, expected: mintedScope},
		{name: "unscoped session", sessionID: normal.Id},
		{name: "no session", sessionID: ""},
		{name: "failed lookup is an error, not unscoped", sessionID: model.NewId(), wantErr: lookupErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope, scopeErr := registry.ScopeFromContext(auth.WithSessionID(context.Background(), tt.sessionID))
			if tt.wantErr != nil {
				require.ErrorIs(t, scopeErr, tt.wantErr)
				assert.Nil(t, scope)
				return
			}
			require.NoError(t, scopeErr)
			assert.Equal(t, tt.expected, scope)
		})
	}
}

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
