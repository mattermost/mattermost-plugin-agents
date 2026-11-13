// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package postgres

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchWithScope(t *testing.T) {
	db := testDB(t)
	defer cleanupDB(t, db)

	pgVector, err := NewPGVector(db, PGVectorConfig{Dimensions: 3})
	require.NoError(t, err)

	teams := []struct {
		id               string
		allowOpenInvite  bool
		deleteAt         int64
		groupConstrained bool
		allowedDomains   string
		policy           bool
	}{
		{id: "open", allowOpenInvite: true},
		{id: "invite_only"},
		{id: "domains", allowOpenInvite: true, allowedDomains: "example.com"},
		{id: "groups", allowOpenInvite: true, groupConstrained: true},
		{id: "team_policy", allowOpenInvite: true, policy: true},
		{id: "deleted_team", allowOpenInvite: true, deleteAt: 1},
	}
	for _, team := range teams {
		_, err = db.Exec("INSERT INTO Teams (Id, AllowOpenInvite, DeleteAt, GroupConstrained, AllowedDomains) VALUES ($1, $2, $3, $4, $5)",
			team.id, team.allowOpenInvite, team.deleteAt, team.groupConstrained, team.allowedDomains)
		require.NoError(t, err)
		if team.policy {
			_, err = db.Exec("INSERT INTO AccessControlPolicies (ID, Type) VALUES ($1, 'team')", team.id)
			require.NoError(t, err)
		}
	}

	channels := []struct {
		id       string
		teamID   string
		kind     model.ChannelType
		deleteAt int64
		policy   bool
		botIn    bool
	}{
		{id: "trigger_private", teamID: "invite_only", kind: model.ChannelTypePrivate},
		{id: "extra_private", teamID: "invite_only", kind: model.ChannelTypePrivate},
		{id: "open_public", teamID: "open", kind: model.ChannelTypeOpen},
		{id: "open_private_member", teamID: "open", kind: model.ChannelTypePrivate, botIn: true},
		{id: "open_public_policy", teamID: "open", kind: model.ChannelTypeOpen, policy: true},
		{id: "open_public_deleted", teamID: "open", kind: model.ChannelTypeOpen, deleteAt: 1},
		{id: "invite_only_public", teamID: "invite_only", kind: model.ChannelTypeOpen, botIn: true},
		{id: "domains_public", teamID: "domains", kind: model.ChannelTypeOpen},
		{id: "groups_public", teamID: "groups", kind: model.ChannelTypeOpen},
		{id: "policy_team_public", teamID: "team_policy", kind: model.ChannelTypeOpen},
		{id: "deleted_team_public", teamID: "deleted_team", kind: model.ChannelTypeOpen},
		{id: "bot_dm", kind: model.ChannelTypeDirect, botIn: true},
		{id: "bot_gm", kind: model.ChannelTypeGroup, botIn: true},
	}
	now := model.GetMillis()
	var docs []embeddings.PostDocument
	var vectors [][]float32
	for _, channel := range channels {
		_, err = db.Exec("INSERT INTO Channels (Id, TeamId, Name, DisplayName, Type, DeleteAt) VALUES ($1, $2, $1, $1, $3, $4)",
			channel.id, channel.teamID, string(channel.kind), channel.deleteAt)
		require.NoError(t, err)
		if channel.policy {
			_, err = db.Exec("INSERT INTO AccessControlPolicies (ID, Type) VALUES ($1, 'channel')", channel.id)
			require.NoError(t, err)
		}
		if channel.botIn {
			addTestChannelMembers(t, db, channel.id, []string{"bot"})
		}
		postID := "post_" + channel.id
		addTestPosts(t, db, []string{postID}, []int64{now})
		docs = append(docs, embeddings.PostDocument{PostID: postID, CreateAt: now, TeamID: channel.teamID, ChannelID: channel.id, UserID: "author", Content: channel.id})
		vectors = append(vectors, []float32{0.5, 0.5, 0.5})
	}
	require.NoError(t, pgVector.Store(context.Background(), docs, vectors))

	tests := []struct {
		name     string
		scope    model.SessionScopeResources
		expected []string
	}{
		{
			name: "listed channels and open-team public channels, regardless of membership",
			scope: model.SessionScopeResources{
				ChannelIDs:             []string{"trigger_private", "extra_private", "bot_dm", "bot_gm"},
				OpenTeamPublicChannels: true,
			},
			expected: []string{"trigger_private", "extra_private", "open_public"},
		},
		{
			name:     "listed channels only",
			scope:    model.SessionScopeResources{ChannelIDs: []string{"trigger_private"}},
			expected: []string{"trigger_private"},
		},
		{
			name:     "listed channel under an access policy",
			scope:    model.SessionScopeResources{ChannelIDs: []string{"open_public_policy"}},
			expected: nil,
		},
		{
			name:     "empty scope reads nothing",
			scope:    model.SessionScopeResources{},
			expected: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, searchErr := pgVector.Search(context.Background(), []float32{0.5, 0.5, 0.5}, embeddings.SearchOptions{
				Limit:  100,
				UserID: "bot",
				Scope:  &tt.scope,
			})
			require.NoError(t, searchErr)
			var got []string
			for _, result := range results {
				got = append(got, result.Document.ChannelID)
			}
			assert.ElementsMatch(t, tt.expected, got)
		})
	}
}
