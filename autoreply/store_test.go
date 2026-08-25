// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package autoreply

import (
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

var rootDSN = "postgres://mmuser:mostest@localhost:5432/postgres?sslmode=disable"

// testDB creates a scratch database from PG_ROOT_DSN (skipping when postgres
// is unreachable), runs the real morph migrations against it, and returns a
// DBClient connected to it. The scratch database is dropped on cleanup.
func testDB(t *testing.T) *mmapi.DBClient {
	t.Helper()

	if dsn := os.Getenv("PG_ROOT_DSN"); dsn != "" {
		rootDSN = dsn
	}

	rootDB, err := sqlx.Connect("postgres", rootDSN)
	if err != nil {
		t.Skipf("PostgreSQL not available, skipping integration test: %v", err)
	}
	defer rootDB.Close()

	dbName := fmt.Sprintf("autoreply_test_%d", model.GetMillis())

	_, err = rootDB.Exec("CREATE DATABASE " + dbName)
	require.NoError(t, err, "Failed to create test database")

	// Derive the scratch-DB DSN from the effective root DSN (rather than
	// hardcoding host/credentials) so a non-default PG_ROOT_DSN works.
	rootURL, err := url.Parse(rootDSN)
	require.NoError(t, err, "Failed to parse root DSN")
	rootURL.Path = "/" + dbName
	testDSN := rootURL.String()

	db, err := sqlx.Connect("postgres", testDSN)
	if err != nil {
		rootConn, _ := sqlx.Connect("postgres", rootDSN)
		if rootConn != nil {
			_, _ = rootConn.Exec("DROP DATABASE " + dbName)
			rootConn.Close()
		}
		require.NoError(t, err, "Failed to connect to test database")
	}

	t.Cleanup(func() {
		db.Close()
		rootConn, connErr := sqlx.Connect("postgres", rootDSN)
		if connErr != nil {
			t.Logf("Failed to connect for cleanup: %v", connErr)
			return
		}
		defer rootConn.Close()
		_, _ = rootConn.Exec("DROP DATABASE " + dbName)
	})

	// Run the real morph migrations, same as production
	s := store.New(db)
	err = s.RunMigrations()
	require.NoError(t, err, "Failed to run migrations")

	return mmapi.NewTestDBClient(db)
}

func TestStoreSetAndGet(t *testing.T) {
	dbClient := testDB(t)
	s := NewStore(dbClient)

	setting := Setting{
		ChannelID: model.NewId(),
		BotID:     model.NewId(),
		Mode:      ModeRootPosts,
		UpdatedBy: model.NewId(),
		UpdateAt:  1234567890123,
	}

	require.NoError(t, s.Set(setting))

	got, err := s.Get(setting.ChannelID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, setting, *got, "every field must round-trip through the database")
}

func TestStoreGetAbsent(t *testing.T) {
	dbClient := testDB(t)
	s := NewStore(dbClient)

	got, err := s.Get(model.NewId())
	require.NoError(t, err, "absence is not an error")
	require.Nil(t, got)
}

func TestStoreSetUpsert(t *testing.T) {
	dbClient := testDB(t)
	s := NewStore(dbClient)

	channelID := model.NewId()
	first := Setting{
		ChannelID: channelID,
		BotID:     model.NewId(),
		Mode:      ModeRootPosts,
		UpdatedBy: model.NewId(),
		UpdateAt:  1000,
	}
	second := Setting{
		ChannelID: channelID,
		BotID:     model.NewId(),
		Mode:      ModeThreads,
		UpdatedBy: model.NewId(),
		UpdateAt:  2000,
	}

	require.NoError(t, s.Set(first))
	require.NoError(t, s.Set(second))

	got, err := s.Get(channelID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, second, *got, "upsert must replace all non-key fields")

	all, err := s.ListAll()
	require.NoError(t, err)
	require.Len(t, all, 1, "the primary key must enforce one row per channel via upsert, not a second row")
}

func TestStoreDelete(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, s *Store, channelID string)
	}{
		{
			name: "delete after set removes the row",
			setup: func(t *testing.T, s *Store, channelID string) {
				require.NoError(t, s.Set(Setting{
					ChannelID: channelID,
					BotID:     model.NewId(),
					Mode:      ModeThreads,
					UpdatedBy: model.NewId(),
					UpdateAt:  1000,
				}))
			},
		},
		{
			name:  "delete a never-set channel is a no-op",
			setup: func(t *testing.T, s *Store, channelID string) {},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbClient := testDB(t)
			s := NewStore(dbClient)
			channelID := model.NewId()

			tc.setup(t, s, channelID)

			require.NoError(t, s.Delete(channelID))

			got, err := s.Get(channelID)
			require.NoError(t, err)
			require.Nil(t, got)
		})
	}
}

func TestStoreListAll(t *testing.T) {
	tests := []struct {
		name     string
		settings []Setting
	}{
		{
			name:     "empty table returns no settings",
			settings: nil,
		},
		{
			name: "three settings for three channels are all returned",
			settings: []Setting{
				{ChannelID: model.NewId(), BotID: model.NewId(), Mode: ModeRootPosts, UpdatedBy: model.NewId(), UpdateAt: 1},
				{ChannelID: model.NewId(), BotID: model.NewId(), Mode: ModeThreads, UpdatedBy: model.NewId(), UpdateAt: 2},
				{ChannelID: model.NewId(), BotID: model.NewId(), Mode: ModeRootPosts, UpdatedBy: model.NewId(), UpdateAt: 3},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbClient := testDB(t)
			s := NewStore(dbClient)

			for _, setting := range tc.settings {
				require.NoError(t, s.Set(setting))
			}

			all, err := s.ListAll()
			require.NoError(t, err)
			require.Len(t, all, len(tc.settings), "distinct channels must not collide")
			require.ElementsMatch(t, tc.settings, all)
		})
	}
}
