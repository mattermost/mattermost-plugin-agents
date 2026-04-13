// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
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
