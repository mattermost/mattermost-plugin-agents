// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
)

// TestMCPServerCreation tests basic MCP server creation and startup
func TestMCPServerCreation(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	t.Run("CreateMCPServer", func(t *testing.T) {
		suite.CreateMCPServer(false)
		assert.NotNil(t, suite.mcpServer, "MCP server should be created")
	})

	t.Run("CreateMCPServerWithDevMode", func(t *testing.T) {
		suite.CreateMCPServer(true)
		assert.NotNil(t, suite.mcpServer, "MCP server with dev mode should be created")
	})
}

// TestMCPServerConfiguration tests various configuration scenarios
func TestMCPServerConfiguration(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	t.Run("ValidConfiguration", func(t *testing.T) {
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: suite.serverURL,
				DevMode:     false,
			},
			PersonalAccessToken: suite.adminToken,
		}
		mcpServer, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		require.NoError(t, err, "Valid configuration should not return error")
		assert.NotNil(t, mcpServer, "MCP server should be created with valid config")
	})

	t.Run("InvalidServerURL", func(t *testing.T) {
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: "http://invalid-server-url:9999",
				DevMode:     false,
			},
			PersonalAccessToken: suite.adminToken,
		}
		_, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		assert.Error(t, err, "Invalid server URL should return error")
		assert.Contains(t, err.Error(), "startup token validation failed", "Error should mention token validation failure")
	})

	t.Run("InvalidToken", func(t *testing.T) {
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: suite.serverURL,
				DevMode:     false,
			},
			PersonalAccessToken: "invalid-token-12345",
		}
		_, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		assert.Error(t, err, "Invalid token should return error")
		assert.Contains(t, err.Error(), "startup token validation failed", "Error should mention token validation failure")
	})

	t.Run("EmptyToken", func(t *testing.T) {
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: suite.serverURL,
				DevMode:     false,
			},
			PersonalAccessToken: "",
		}
		_, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		// Empty token should fail option validation
		assert.Error(t, err, "Empty token should fail validation")
		assert.Contains(t, err.Error(), "personal access token cannot be empty", "Error should mention empty token")
	})

	t.Run("DevModeConfiguration", func(t *testing.T) {
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: suite.serverURL,
				DevMode:     true,
			},
			PersonalAccessToken: suite.adminToken,
		}
		mcpServer, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		require.NoError(t, err, "Dev mode configuration should not return error")
		assert.NotNil(t, mcpServer, "MCP server should be created with dev mode")
	})

	t.Run("StdioTransportFixed", func(t *testing.T) {
		// STDIO constructor always uses stdio transport
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: suite.serverURL,
				DevMode:     false,
			},
			PersonalAccessToken: suite.adminToken,
		}
		mcpServer, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		require.NoError(t, err, "STDIO server should be created successfully")
		assert.NotNil(t, mcpServer, "MCP server should be created")
		// Transport is always stdio for this constructor
	})
}

// TestAuthentication tests authentication scenarios
func TestAuthentication(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	t.Run("TokenAuthenticationProvider", func(t *testing.T) {
		authProvider := auth.NewTokenAuthenticationProvider(suite.serverURL, "", suite.adminToken, suite.logger)
		assert.NotNil(t, authProvider, "Token authentication provider should be created")

		// Test token validation with configured token
		err := authProvider.ValidateAuth(context.Background())
		require.NoError(t, err, "Should validate authentication with configured token")
	})

	t.Run("TokenValidationAtStartup", func(t *testing.T) {
		// This tests the startup token validation that happens in NewMattermostMCPServer
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: suite.serverURL,
				DevMode:     false,
			},
			PersonalAccessToken: suite.adminToken,
		}
		mcpServer, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		require.NoError(t, err, "Startup token validation should succeed with valid token")
		assert.NotNil(t, mcpServer, "MCP server should be created after successful token validation")
	})

	t.Run("TokenAuthenticationFailure", func(t *testing.T) {
		invalidToken := "invalid-token-xyz"
		authProvider := auth.NewTokenAuthenticationProvider(suite.serverURL, "", invalidToken, suite.logger)

		err := authProvider.ValidateAuth(context.Background())
		assert.Error(t, err, "Invalid token should fail validation")
	})
}

// TestMCPServerStartupValidation tests server startup validation scenarios
func TestMCPServerStartupValidation(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	t.Run("SuccessfulStartupValidation", func(t *testing.T) {
		// This internally calls validateTokenAtStartup
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: suite.serverURL,
				DevMode:     false,
			},
			PersonalAccessToken: suite.adminToken,
		}
		mcpServer, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		require.NoError(t, err, "Startup validation should succeed")
		assert.NotNil(t, mcpServer, "MCP server should be created after successful validation")
	})

	t.Run("StartupValidationWithInvalidServer", func(t *testing.T) {
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: "http://nonexistent-server:8065",
				DevMode:     false,
			},
			PersonalAccessToken: suite.adminToken,
		}
		_, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		assert.Error(t, err, "Startup validation should fail with invalid server")
		assert.Contains(t, err.Error(), "startup token validation failed", "Error should mention startup validation failure")
	})

	t.Run("StartupValidationWithUnauthorizedToken", func(t *testing.T) {
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: suite.serverURL,
				DevMode:     false,
			},
			PersonalAccessToken: "unauthorized-token-123",
		}
		_, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		assert.Error(t, err, "Startup validation should fail with unauthorized token")
		assert.Contains(t, err.Error(), "startup token validation failed", "Error should mention startup validation failure")
	})

	t.Run("ValidTokenAlwaysValidated", func(t *testing.T) {
		// STDIO servers always validate tokens at startup
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL: suite.serverURL,
				DevMode:     false,
			},
			PersonalAccessToken: suite.adminToken,
		}
		mcpServer, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)

		require.NoError(t, err, "Server creation should succeed with valid token")
		assert.NotNil(t, mcpServer, "MCP server should be created")
	})
}
