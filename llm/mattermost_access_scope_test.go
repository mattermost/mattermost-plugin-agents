// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMattermostAccessScope_Validate(t *testing.T) {
	validTeamID := model.NewId()
	validChannelID := model.NewId()

	tests := []struct {
		name    string
		scope   *MattermostAccessScope
		wantErr string
	}{
		{
			name:  "nil scope is valid",
			scope: nil,
		},
		{
			name:  "team_id only is valid",
			scope: &MattermostAccessScope{TeamID: validTeamID},
		},
		{
			name:  "all fields set is valid",
			scope: &MattermostAccessScope{TeamID: validTeamID, AllowedChannelTypes: []string{"O"}, AllowedChannelIDs: []string{validChannelID}},
		},
		{
			name:    "channel types without team_id",
			scope:   &MattermostAccessScope{AllowedChannelTypes: []string{"O"}},
			wantErr: "team_id is required",
		},
		{
			name:    "channel ids without team_id",
			scope:   &MattermostAccessScope{AllowedChannelIDs: []string{validChannelID}},
			wantErr: "team_id is required",
		},
		{
			name:    "invalid team_id format",
			scope:   &MattermostAccessScope{TeamID: "bad"},
			wantErr: "team_id must be a valid ID",
		},
		{
			name:    "invalid channel type",
			scope:   &MattermostAccessScope{TeamID: validTeamID, AllowedChannelTypes: []string{"X"}},
			wantErr: "invalid channel type",
		},
		{
			name:    "invalid channel id in allowlist",
			scope:   &MattermostAccessScope{TeamID: validTeamID, AllowedChannelIDs: []string{"not-valid"}},
			wantErr: "allowed_channel_ids contains invalid ID",
		},
		{
			name:  "multiple valid channel types",
			scope: &MattermostAccessScope{TeamID: validTeamID, AllowedChannelTypes: []string{"O", "P", "D", "G"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.scope.Validate()
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMattermostAccessScope_AllowsTeam(t *testing.T) {
	teamA := model.NewId()
	teamB := model.NewId()

	tests := []struct {
		name   string
		scope  *MattermostAccessScope
		teamID string
		want   bool
	}{
		{
			name:   "nil scope allows any team",
			scope:  nil,
			teamID: teamA,
			want:   true,
		},
		{
			name:   "empty team_id allows any team",
			scope:  &MattermostAccessScope{},
			teamID: teamA,
			want:   true,
		},
		{
			name:   "matching team allowed",
			scope:  &MattermostAccessScope{TeamID: teamA},
			teamID: teamA,
			want:   true,
		},
		{
			name:   "different team denied",
			scope:  &MattermostAccessScope{TeamID: teamA},
			teamID: teamB,
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.scope.AllowsTeam(tc.teamID))
		})
	}
}

func TestMattermostAccessScope_AllowsChannel(t *testing.T) {
	teamA := model.NewId()
	teamB := model.NewId()
	chanPublicA := model.NewId()
	chanPublicB := model.NewId()
	chanPrivate := model.NewId()
	chanDM := model.NewId()

	tests := []struct {
		name    string
		scope   *MattermostAccessScope
		channel *model.Channel
		want    bool
	}{
		{
			name:    "nil scope allows any channel",
			scope:   nil,
			channel: &model.Channel{Id: chanPublicA, TeamId: teamA, Type: model.ChannelTypeOpen},
			want:    true,
		},
		{
			name:    "nil channel allowed",
			scope:   &MattermostAccessScope{TeamID: teamA},
			channel: nil,
			want:    true,
		},
		{
			name:    "public channel in correct team allowed",
			scope:   &MattermostAccessScope{TeamID: teamA, AllowedChannelTypes: []string{"O"}},
			channel: &model.Channel{Id: chanPublicA, TeamId: teamA, Type: model.ChannelTypeOpen},
			want:    true,
		},
		{
			name:    "public channel in wrong team denied",
			scope:   &MattermostAccessScope{TeamID: teamA, AllowedChannelTypes: []string{"O"}},
			channel: &model.Channel{Id: chanPublicB, TeamId: teamB, Type: model.ChannelTypeOpen},
			want:    false,
		},
		{
			name:    "private channel denied when only public allowed",
			scope:   &MattermostAccessScope{TeamID: teamA, AllowedChannelTypes: []string{"O"}},
			channel: &model.Channel{Id: chanPrivate, TeamId: teamA, Type: model.ChannelTypePrivate},
			want:    false,
		},
		{
			name:    "DM allowed when D is in allowed types",
			scope:   &MattermostAccessScope{TeamID: teamA, AllowedChannelTypes: []string{"O", "D"}},
			channel: &model.Channel{Id: chanDM, Type: model.ChannelTypeDirect},
			want:    true,
		},
		{
			name:    "DM denied when only public allowed (DMs have empty TeamId)",
			scope:   &MattermostAccessScope{TeamID: teamA, AllowedChannelTypes: []string{"O"}},
			channel: &model.Channel{Id: chanDM, Type: model.ChannelTypeDirect},
			want:    false,
		},
		{
			name:    "channel in allowlist passes",
			scope:   &MattermostAccessScope{TeamID: teamA, AllowedChannelIDs: []string{chanPublicA}},
			channel: &model.Channel{Id: chanPublicA, TeamId: teamA, Type: model.ChannelTypeOpen},
			want:    true,
		},
		{
			name:    "channel not in allowlist denied",
			scope:   &MattermostAccessScope{TeamID: teamA, AllowedChannelIDs: []string{chanPublicA}},
			channel: &model.Channel{Id: chanPublicB, TeamId: teamA, Type: model.ChannelTypeOpen},
			want:    false,
		},
		{
			name:    "channel passes type but fails allowlist",
			scope:   &MattermostAccessScope{TeamID: teamA, AllowedChannelTypes: []string{"O"}, AllowedChannelIDs: []string{chanPublicA}},
			channel: &model.Channel{Id: chanPublicB, TeamId: teamA, Type: model.ChannelTypeOpen},
			want:    false,
		},
		{
			name:    "no channel type restriction allows all types in team",
			scope:   &MattermostAccessScope{TeamID: teamA},
			channel: &model.Channel{Id: chanPrivate, TeamId: teamA, Type: model.ChannelTypePrivate},
			want:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.scope.AllowsChannel(tc.channel))
		})
	}
}

func TestMattermostAccessScope_BlocksDMGM(t *testing.T) {
	tests := []struct {
		name  string
		scope *MattermostAccessScope
		want  bool
	}{
		{
			name:  "nil scope does not block",
			scope: nil,
			want:  false,
		},
		{
			name:  "empty channel types does not block",
			scope: &MattermostAccessScope{AllowedChannelTypes: []string{}},
			want:  false,
		},
		{
			name:  "public only blocks DM/GM",
			scope: &MattermostAccessScope{AllowedChannelTypes: []string{"O"}},
			want:  true,
		},
		{
			name:  "public and private blocks DM/GM",
			scope: &MattermostAccessScope{AllowedChannelTypes: []string{"O", "P"}},
			want:  true,
		},
		{
			name:  "DM allowed does not block",
			scope: &MattermostAccessScope{AllowedChannelTypes: []string{"O", "D"}},
			want:  false,
		},
		{
			name:  "GM allowed does not block",
			scope: &MattermostAccessScope{AllowedChannelTypes: []string{"O", "G"}},
			want:  false,
		},
		{
			name:  "both DM and GM allowed does not block",
			scope: &MattermostAccessScope{AllowedChannelTypes: []string{"D", "G"}},
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.scope.BlocksDMGM())
		})
	}
}

func TestMattermostAccessScope_ErrorMessages(t *testing.T) {
	scope := &MattermostAccessScope{TeamID: model.NewId()}

	t.Run("channel denied error", func(t *testing.T) {
		err := scope.ChannelDeniedError("chan123")
		assert.Contains(t, err.Error(), "chan123")
		assert.Contains(t, err.Error(), "outside the execution scope")
	})

	t.Run("team denied error", func(t *testing.T) {
		err := scope.TeamDeniedError("team456")
		assert.Contains(t, err.Error(), "team456")
		assert.Contains(t, err.Error(), "outside the execution scope")
	})
}
