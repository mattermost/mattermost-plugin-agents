// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

var testConnStr string

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	cancel()
	if err != nil {
		fmt.Printf("Failed to start postgres container: %v\n", err)
		os.Exit(1)
	}
	testConnStr, err = container.ConnectionString(context.Background(), "sslmode=disable")
	if err != nil {
		fmt.Printf("Failed to get connection string: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := testcontainers.TerminateContainer(container); err != nil {
		fmt.Printf("Failed to terminate container: %v\n", err)
	}
	os.Exit(code)
}

// testDB returns a database with the real plugin migrations applied in its own
// schema, plus the Mattermost tables the library reads.
func testDB(t *testing.T) *sqlx.DB {
	t.Helper()

	setupDB, err := sqlx.Connect("postgres", testConnStr)
	require.NoError(t, err)
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	_, err = setupDB.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	setupDB.Close()

	db, err := sqlx.Connect("postgres", testConnStr+"&search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE")
		db.Close()
	})

	for _, ddl := range []string{
		`CREATE TABLE Posts (Id VARCHAR(26) PRIMARY KEY, ChannelId VARCHAR(26), DeleteAt BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE FileInfo (Id VARCHAR(26) PRIMARY KEY, PostId VARCHAR(26), DeleteAt BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE Channels (Id VARCHAR(26) PRIMARY KEY)`,
	} {
		_, err = db.Exec(ddl)
		require.NoError(t, err)
	}
	require.NoError(t, store.New(db).RunMigrations())
	return db
}

// seedSource inserts a live post and file the way Mattermost stores them.
func seedSource(t *testing.T, db *sqlx.DB, postID, fileID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO Posts (Id) VALUES ($1) ON CONFLICT DO NOTHING`, postID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO FileInfo (Id, PostId) VALUES ($1, $2)`, fileID, postID)
	require.NoError(t, err)
}
