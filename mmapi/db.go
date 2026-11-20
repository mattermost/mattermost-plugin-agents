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

// ChannelGuestCount counts a channel's active guest members the way the
// server's channel store does. The plugin API's channel stats cannot be used:
// their GuestCount field carries the member count.
func (db *DBClient) ChannelGuestCount(channelID string) (int64, error) {
	sqlString, args, err := db.builder.
		Select("COUNT(*)").
		From("ChannelMembers").
		Join("Users ON ChannelMembers.UserId = Users.Id").
		Where(sq.Eq{"ChannelMembers.ChannelId": channelID, "ChannelMembers.SchemeGuest": true, "Users.DeleteAt": 0}).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to build guest count query: %w", err)
	}
	var count int64
	if err := db.Get(&count, sqlString, args...); err != nil {
		return 0, fmt.Errorf("failed to count channel guests: %w", err)
	}
	return count, nil
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
