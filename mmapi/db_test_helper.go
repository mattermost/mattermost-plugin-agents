// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi

import (
	sq "github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
)

// NewTestDBClient creates a DBClient for testing purposes using a raw sqlx.DB connection.
// This bypasses the plugin API requirement for test environments.
func NewTestDBClient(db *sqlx.DB) *DBClient {
	builder := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	return &DBClient{
		DB:      db,
		builder: builder,
	}
}
