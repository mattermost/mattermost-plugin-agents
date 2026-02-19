// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/stretchr/testify/require"
)

func TestInMemoryServerCreation(t *testing.T) {
	config := mcpserver.InMemoryConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: "http://localhost:8065",
			DevMode:     false,
		},
	}

	mcpLogger, err := logger.CreateLoggerWithOptions(false, "")
	require.NoError(t, err)

	server, err := mcpserver.NewInMemoryServer(config, mcpLogger, nil)
	require.NoError(t, err)

	// Test creating a client transport (without validation by passing nil resolver)
	_, err = server.CreateConnectionForUser("test_user_123", "", nil)
	require.NoError(t, err)
}
