// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
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

// setupTestStore creates a Store connected to the test container with a fresh schema.
// Each test gets an isolated schema that is dropped on cleanup.
func setupTestStore(t *testing.T) *Store {
	t.Helper()

	db, err := sqlx.Connect("postgres", testConnStr)
	require.NoError(t, err)

	schemaName := fmt.Sprintf("test_%d", time.Now().UnixNano())
	_, err = db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schemaName))
	require.NoError(t, err)

	_, err = db.Exec(fmt.Sprintf("SET search_path TO %s", schemaName))
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schemaName))
		db.Close()
	})

	return New(db)
}

func TestRunMigrations(t *testing.T) {
	tests := []struct {
		name     string
		validate func(t *testing.T, s *Store)
	}{
		{
			name: "fresh install creates all tables",
			validate: func(t *testing.T, s *Store) {
				// Check Agents_System table exists
				var exists bool
				err := s.db.Get(&exists, `
					SELECT EXISTS (
						SELECT 1 FROM information_schema.tables
						WHERE table_name = 'agents_system'
						AND table_schema = current_schema()
					)`)
				require.NoError(t, err)
				assert.True(t, exists, "Agents_System table should exist")

				// Check LLM_PostMeta table exists
				err = s.db.Get(&exists, `
					SELECT EXISTS (
						SELECT 1 FROM information_schema.tables
						WHERE table_name = 'llm_postmeta'
						AND table_schema = current_schema()
					)`)
				require.NoError(t, err)
				assert.True(t, exists, "LLM_PostMeta table should exist")

				// Check Agents_ConfigHistory table exists
				err = s.db.Get(&exists, `
					SELECT EXISTS (
						SELECT 1 FROM information_schema.tables
						WHERE table_name = 'agents_confighistory'
						AND table_schema = current_schema()
					)`)
				require.NoError(t, err)
				assert.True(t, exists, "Agents_ConfigHistory table should exist")

				// Check Agents_DB_Migrations tracking table exists
				err = s.db.Get(&exists, `
					SELECT EXISTS (
						SELECT 1 FROM information_schema.tables
						WHERE table_name = 'agents_db_migrations'
						AND table_schema = current_schema()
					)`)
				require.NoError(t, err)
				assert.True(t, exists, "Agents_DB_Migrations tracking table should exist")
			},
		},
		{
			name: "idempotent re-run succeeds",
			validate: func(t *testing.T, s *Store) {
				// Run migrations a second time — should not error
				err := s.RunMigrations()
				require.NoError(t, err)
			},
		},
		{
			name: "migration tracking records correct count",
			validate: func(t *testing.T, s *Store) {
				var count int
				err := s.db.Get(&count, `
					SELECT COUNT(*) FROM Agents_DB_Migrations`)
				require.NoError(t, err)
				assert.Equal(t, 4, count, "Should have 4 migration records")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)

			// Run migrations first
			err := s.RunMigrations()
			require.NoError(t, err)

			tt.validate(t, s)
		})
	}
}

func TestSystemKeyValue(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store)
	}{
		{
			name:  "get nonexistent key returns empty string",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				val, err := s.GetSystemValue("nonexistent")
				require.NoError(t, err)
				assert.Equal(t, "", val)
			},
		},
		{
			name: "set and get round-trip",
			setup: func(t *testing.T, s *Store) {
				err := s.SetSystemValue("test_key", "test_value")
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				val, err := s.GetSystemValue("test_key")
				require.NoError(t, err)
				assert.Equal(t, "test_value", val)
			},
		},
		{
			name: "overwrite existing key",
			setup: func(t *testing.T, s *Store) {
				err := s.SetSystemValue("overwrite_key", "original")
				require.NoError(t, err)
				err = s.SetSystemValue("overwrite_key", "updated")
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				val, err := s.GetSystemValue("overwrite_key")
				require.NoError(t, err)
				assert.Equal(t, "updated", val)
			},
		},
		{
			name: "multiple keys are independent",
			setup: func(t *testing.T, s *Store) {
				err := s.SetSystemValue("key_a", "value_a")
				require.NoError(t, err)
				err = s.SetSystemValue("key_b", "value_b")
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				valA, err := s.GetSystemValue("key_a")
				require.NoError(t, err)
				assert.Equal(t, "value_a", valA)

				valB, err := s.GetSystemValue("key_b")
				require.NoError(t, err)
				assert.Equal(t, "value_b", valB)
			},
		},
		{
			name: "empty value is valid",
			setup: func(t *testing.T, s *Store) {
				err := s.SetSystemValue("empty_key", "")
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				val, err := s.GetSystemValue("empty_key")
				require.NoError(t, err)
				assert.Equal(t, "", val)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)

			// Run migrations to create the tables
			err := s.RunMigrations()
			require.NoError(t, err)

			tt.setup(t, s)
			tt.validate(t, s)
		})
	}
}
