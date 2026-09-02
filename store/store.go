// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
)

// Store provides database operations for the AI plugin.
type Store struct {
	db      *sqlx.DB
	builder sq.StatementBuilderType
}

// New creates a new Store from an existing sqlx.DB connection.
// Reuses the same connection that mmapi.NewDBClient provides.
func New(db *sqlx.DB) *Store {
	builder := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

	return &Store{
		db:      db,
		builder: builder,
	}
}

// DB returns the underlying sqlx.DB for use in migration drivers.
func (s *Store) DB() *sqlx.DB {
	return s.db
}

// execBuilder builds and executes a statement. Errors are wrapped as
// "failed to build <desc> query" / "failed to <desc>".
func (s *Store) execBuilder(b sq.Sqlizer, desc string) error {
	query, args, err := b.ToSql()
	if err != nil {
		return fmt.Errorf("failed to build %s query: %w", desc, err)
	}
	if _, err := s.db.Exec(query, args...); err != nil {
		return fmt.Errorf("failed to %s: %w", desc, err)
	}
	return nil
}

// getBuilder builds a query and scans a single row into dest. All errors,
// including sql.ErrNoRows, are wrapped; callers match sentinels with errors.Is.
func (s *Store) getBuilder(dest any, b sq.Sqlizer, desc string) error {
	query, args, err := b.ToSql()
	if err != nil {
		return fmt.Errorf("failed to build %s query: %w", desc, err)
	}
	if err := s.db.Get(dest, query, args...); err != nil {
		return fmt.Errorf("failed to %s: %w", desc, err)
	}
	return nil
}

// selectBuilder builds a query and scans all rows into dest.
func (s *Store) selectBuilder(dest any, b sq.Sqlizer, desc string) error {
	query, args, err := b.ToSql()
	if err != nil {
		return fmt.Errorf("failed to build %s query: %w", desc, err)
	}
	if err := s.db.Select(dest, query, args...); err != nil {
		return fmt.Errorf("failed to %s: %w", desc, err)
	}
	return nil
}
