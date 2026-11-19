// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsAccessLevelFor(t *testing.T) {
	readWrite := model.NewId()
	manager := model.NewId()
	access := SettingsAccess{
		ReadWriteUserIDs:    []string{readWrite},
		AgentManagerUserIDs: []string{manager},
	}

	tests := []struct {
		name   string
		userID string
		want   SettingsAccessLevel
	}{
		{name: "read/write user", userID: readWrite, want: SettingsAccessWrite},
		{name: "agent managers get no settings access", userID: manager, want: SettingsAccessNone},
		{name: "user on neither list", userID: model.NewId(), want: SettingsAccessNone},
		{name: "no user", userID: "", want: SettingsAccessNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, access.LevelFor(tt.userID))
		})
	}
}

func TestSettingsAccessManagesAllAgents(t *testing.T) {
	manager := model.NewId()
	access := SettingsAccess{
		ReadWriteUserIDs:    []string{model.NewId()},
		AgentManagerUserIDs: []string{manager},
	}

	tests := []struct {
		name   string
		userID string
		want   bool
	}{
		{name: "agent manager", userID: manager, want: true},
		{name: "read/write user who is not an agent manager", userID: access.ReadWriteUserIDs[0], want: false},
		{name: "anyone else", userID: model.NewId(), want: false},
		{name: "no user", userID: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, access.ManagesAllAgents(tt.userID))
		})
	}
}

func TestContainerSettingsAccessWithoutLists(t *testing.T) {
	var c Container
	c.Update(&Config{})
	assert.Equal(t, SettingsAccessNone, c.SettingsAccess().LevelFor(model.NewId()))
}

func TestSettingsAccessNormalize(t *testing.T) {
	a := model.NewId()
	b := model.NewId()

	tests := []struct {
		name    string
		in      SettingsAccess
		want    SettingsAccess
		wantErr bool
	}{
		{
			name: "nil lists become empty",
			in:   SettingsAccess{},
			want: SettingsAccess{ReadWriteUserIDs: []string{}, AgentManagerUserIDs: []string{}},
		},
		{
			name: "duplicates are dropped",
			in:   SettingsAccess{ReadWriteUserIDs: []string{b, b}, AgentManagerUserIDs: []string{a, a}},
			want: SettingsAccess{ReadWriteUserIDs: []string{b}, AgentManagerUserIDs: []string{a}},
		},
		{
			name: "agent managers keep their settings access",
			in:   SettingsAccess{ReadWriteUserIDs: []string{b}, AgentManagerUserIDs: []string{a, b}},
			want: SettingsAccess{ReadWriteUserIDs: []string{b}, AgentManagerUserIDs: []string{a, b}},
		},
		{
			name:    "read/write usernames are rejected",
			in:      SettingsAccess{ReadWriteUserIDs: []string{"alice"}},
			wantErr: true,
		},
		{
			name:    "agent manager usernames are rejected",
			in:      SettingsAccess{AgentManagerUserIDs: []string{"alice"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.in.Normalize()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
