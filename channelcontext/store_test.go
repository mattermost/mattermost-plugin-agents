// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelcontext

import (
	"fmt"
	"net/url"
	"os"
	"sync"
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

	dbName := fmt.Sprintf("channelcontext_test_%s", model.NewId())
	_, err = rootDB.Exec("CREATE DATABASE " + dbName)
	require.NoError(t, err, "Failed to create test database")

	rootURL, err := url.Parse(rootDSN)
	require.NoError(t, err, "Failed to parse root DSN")
	rootURL.Path = "/" + dbName

	db, err := sqlx.Connect("postgres", rootURL.String())
	if err != nil {
		_, _ = rootDB.Exec("DROP DATABASE " + dbName)
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

	require.NoError(t, store.New(db).RunMigrations(), "Failed to run migrations")

	return mmapi.NewTestDBClient(db)
}

func TestStoreInstructions(t *testing.T) {
	s := NewStore(testDB(t))
	channelID := model.NewId()

	got, err := s.GetInstructions(channelID)
	require.NoError(t, err)
	require.Nil(t, got, "a channel without instructions has no row")

	first := Instructions{ChannelID: channelID, Instructions: "first", UpdatedBy: model.NewId(), UpdateAt: 1}
	require.NoError(t, s.SetInstructions(first))
	got, err = s.GetInstructions(channelID)
	require.NoError(t, err)
	require.Equal(t, &first, got)

	second := Instructions{ChannelID: channelID, Instructions: "second", UpdatedBy: model.NewId(), UpdateAt: 2}
	require.NoError(t, s.SetInstructions(second))
	got, err = s.GetInstructions(channelID)
	require.NoError(t, err)
	require.Equal(t, &second, got, "a second write replaces the first")

	otherChannel, err := s.GetInstructions(model.NewId())
	require.NoError(t, err)
	require.Nil(t, otherChannel, "instructions are scoped to their channel")

	require.NoError(t, s.DeleteInstructions(channelID))
	got, err = s.GetInstructions(channelID)
	require.NoError(t, err)
	require.Nil(t, got)

	require.NoError(t, s.DeleteInstructions(channelID), "deleting missing instructions is a no-op")
}

func TestStorePins(t *testing.T) {
	s := NewStore(testDB(t))
	channelID := model.NewId()
	otherChannelID := model.NewId()

	newer := Pin{ChannelID: channelID, PostID: model.NewId(), PinnedBy: model.NewId(), PinnedAt: 200}
	older := Pin{ChannelID: channelID, PostID: model.NewId(), PinnedBy: model.NewId(), PinnedAt: 100}
	elsewhere := Pin{ChannelID: otherChannelID, PostID: model.NewId(), PinnedBy: model.NewId(), PinnedAt: 50}

	for _, pin := range []Pin{newer, older, elsewhere} {
		added, err := s.AddPin(pin, MaxPinnedPosts)
		require.NoError(t, err)
		require.True(t, added)
	}

	pins, err := s.ListPins(channelID)
	require.NoError(t, err)
	require.Equal(t, []Pin{older, newer}, pins, "pins are listed oldest first and scoped to the channel")

	duplicate := newer
	duplicate.PinnedAt = 300
	added, err := s.AddPin(duplicate, MaxPinnedPosts)
	require.NoError(t, err)
	require.False(t, added, "re-pinning a pinned post inserts nothing")

	has, err := s.HasPin(channelID, newer.PostID)
	require.NoError(t, err)
	require.True(t, has)
	has, err = s.HasPin(otherChannelID, newer.PostID)
	require.NoError(t, err)
	require.False(t, has)

	require.NoError(t, s.RemovePin(channelID, newer.PostID))
	require.NoError(t, s.RemovePin(channelID, newer.PostID), "removing a missing pin is a no-op")
	pins, err = s.ListPins(channelID)
	require.NoError(t, err)
	require.Equal(t, []Pin{older}, pins)
}

func TestStoreAddPinLimit(t *testing.T) {
	s := NewStore(testDB(t))
	channelID := model.NewId()
	const limit = 3

	// Pins race from many goroutines; the single-statement limit check must
	// keep the channel at the limit.
	var wg sync.WaitGroup
	for i := range limit * 3 {
		wg.Go(func() {
			_, err := s.AddPin(Pin{ChannelID: channelID, PostID: model.NewId(), PinnedBy: model.NewId(), PinnedAt: int64(i)}, limit)
			require.NoError(t, err)
		})
	}
	wg.Wait()

	pins, err := s.ListPins(channelID)
	require.NoError(t, err)
	require.Len(t, pins, limit)

	added, err := s.AddPin(Pin{ChannelID: channelID, PostID: model.NewId(), PinnedBy: model.NewId(), PinnedAt: 2000}, limit)
	require.NoError(t, err)
	require.False(t, added, "a full channel accepts no more pins")

	added, err = s.AddPin(Pin{ChannelID: model.NewId(), PostID: model.NewId(), PinnedBy: model.NewId(), PinnedAt: 2000}, limit)
	require.NoError(t, err)
	require.True(t, added, "the limit is per channel")
}
