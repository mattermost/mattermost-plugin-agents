// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/v2/api"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// A peer applying a config update must tell its own connected clients to
// refetch agents and search availability.
func TestOnPluginClusterEventConfigUpdateInvalidatesClients(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sqlx.Connect("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("CREATE TABLE Agents_ConfigHistory (Config TEXT NOT NULL, Active BOOLEAN NOT NULL)")
	require.NoError(t, err)
	cfgJSON, err := json.Marshal(&config.Config{})
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO Agents_ConfigHistory (Config, Active) VALUES ($1, true)", string(cfgJSON))
	require.NoError(t, err)

	mockAPI := &plugintest.API{}
	mockAPI.On("PublishWebSocketEvent", api.WebsocketEventBotsInvalidate, mock.Anything, mock.Anything).Once()
	defer mockAPI.AssertExpectations(t)

	p := &Plugin{pluginAPI: pluginapi.NewClient(mockAPI, nil), store: store.New(db)}
	p.SetAPI(mockAPI)
	p.OnPluginClusterEvent(&plugin.Context{}, model.PluginClusterEvent{Id: clusterEventConfigUpdate})
}
