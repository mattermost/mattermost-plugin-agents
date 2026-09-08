// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi

import (
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type builder interface {
	ToSql() (string, []any, error)
}

type DBClient struct {
	*sqlx.DB
	builder sq.StatementBuilderType
}

// NewDBClient creates the DB part of the client, only supported on postgres, panics on failures.
func NewDBClient(pluginAPI *pluginapi.Client) *DBClient {
	driverName := pluginAPI.Store.DriverName()
	if driverName != model.DatabaseDriverPostgres {
		panic("this plugin is only supported on postgres")
	}
	origDB, err := pluginAPI.Store.GetMasterDB()
	if err != nil {
		panic(fmt.Sprintf("failed to get master db: %v", err))
	}

	builder := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

	return &DBClient{
		DB:      sqlx.NewDb(origDB, driverName),
		builder: builder,
	}
}

func (db *DBClient) DoQuery(dest any, b builder) error {
	sqlString, args, err := b.ToSql()
	if err != nil {
		return fmt.Errorf("failed to build sql: %w", err)
	}

	sqlString = db.Rebind(sqlString)

	return sqlx.Select(db, dest, sqlString, args...)
}

func (db *DBClient) Builder() sq.StatementBuilderType {
	return db.builder
}

func (db *DBClient) ExecBuilder(b builder) (sql.Result, error) {
	sqlString, args, err := b.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build sql: %w", err)
	}

	sqlString = db.Rebind(sqlString)

	return db.Exec(sqlString, args...)
}
