// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestChannelGuestCount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("postgres container unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// The columns the server's channel store reads, as the server creates them.
	_, err = db.Exec(`
		CREATE TABLE Users (Id varchar(26) PRIMARY KEY, DeleteAt bigint NOT NULL DEFAULT 0);
		CREATE TABLE ChannelMembers (
			ChannelId varchar(26) NOT NULL,
			UserId varchar(26) NOT NULL,
			SchemeGuest boolean,
			PRIMARY KEY (ChannelId, UserId)
		);
		INSERT INTO Users (Id, DeleteAt) VALUES
			('member', 0), ('guest', 0), ('deactivated-guest', 1700000000000), ('other-guest', 0);
		INSERT INTO ChannelMembers (ChannelId, UserId, SchemeGuest) VALUES
			('with-guest', 'member', false),
			('with-guest', 'guest', true),
			('with-guest', 'deactivated-guest', true),
			('members-only', 'member', false),
			('members-only', 'deactivated-guest', true),
			('other', 'other-guest', true);
	`)
	require.NoError(t, err)

	client := NewTestDBClient(db)
	tests := []struct {
		name      string
		channelID string
		want      int64
	}{
		{name: "counts active guests, not members", channelID: "with-guest", want: 1},
		{name: "a deactivated guest does not count", channelID: "members-only", want: 0},
		{name: "a channel with no members", channelID: "empty", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := client.ChannelGuestCount(tc.channelID)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("a failed query is an error", func(t *testing.T) {
		_, err := db.Exec(`DROP TABLE ChannelMembers`)
		require.NoError(t, err)
		_, err = client.ChannelGuestCount("with-guest")
		assert.Error(t, err)
	})
}
