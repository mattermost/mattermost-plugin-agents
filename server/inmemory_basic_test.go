// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryServerCreation(t *testing.T) {
	// Test in-memory server creation
	config := mcpserver.InMemoryConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: "http://localhost:8065",
			DevMode:     false,
		},
	}

	mcpLogger, err := logger.CreateLoggerWithOptions(false, "")
	require.NoError(t, err)

	server, err := mcpserver.NewInMemoryServer(config, mcpLogger)
	require.NoError(t, err)
	assert.NotNil(t, server)

	// Test creating a client transport (without validation by passing nil resolver)
	clientTransport, err := server.CreateConnectionForUser("test_user_123", "", nil)
	require.NoError(t, err)
	assert.NotNil(t, clientTransport)
}

func TestInMemoryServerMultipleUsers(t *testing.T) {
	config := mcpserver.InMemoryConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: "http://localhost:8065",
			DevMode:     false,
		},
	}

	mcpLogger, err := logger.CreateLoggerWithOptions(false, "")
	require.NoError(t, err)

	server, err := mcpserver.NewInMemoryServer(config, mcpLogger)
	require.NoError(t, err)

	// Create multiple user connections
	users := []string{"user1", "user2", "user3"}

	for _, userID := range users {
		transport, connErr := server.CreateConnectionForUser(userID, "", nil)
		require.NoError(t, connErr)
		assert.NotNil(t, transport)
	}
}
