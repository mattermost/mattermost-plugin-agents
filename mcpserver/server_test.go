// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mmcontainer "github.com/mattermost/testcontainers-mattermost-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/tools"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// TestSuite represents the integration test suite
type TestSuite struct {
	t          *testing.T
	container  *mmcontainer.MattermostContainer
	serverURL  string
	adminToken string
	logger     *mlog.Logger
	mcpServer  *mcpserver.MattermostMCPServer
	devMode    bool
}

// SetupTestSuite initializes a Mattermost container and MCP server for testing
func SetupTestSuite(t *testing.T) *TestSuite {
	// Set up test data directory to avoid "executable not in expected 'bin' directory structure" errors
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0700), "Failed to create test data directory")

	// Override GetDataDirectoryInternal for this test
	originalGetDataDirectory := tools.GetDataDirectoryInternal
	tools.GetDataDirectoryInternal = func() (string, error) {
		return dataDir, nil
	}
	t.Cleanup(func() { tools.GetDataDirectoryInternal = originalGetDataDirectory })

	ctx := context.Background()

	// Start Mattermost container with PAT enabled
	container, err := mmcontainer.RunContainer(ctx,
		mmcontainer.WithLicense(""),
	)
	require.NoError(t, err, "Failed to start Mattermost container")

	// Enable personal access tokens in the server config
	err = container.SetConfig(ctx, "ServiceSettings.EnableUserAccessTokens", "true")
	require.NoError(t, err, "Failed to enable personal access tokens")

	// Get connection details
	serverURL, err := container.URL(ctx)
	require.NoError(t, err, "Failed to get server URL")

	// Get admin client and create a PAT token
	adminClient, err := container.GetAdminClient(ctx)
	require.NoError(t, err, "Failed to get admin client")

	// Create a personal access token for testing
	pat, _, err := adminClient.CreateUserAccessToken(ctx, "me", "MCP Integration Test Token")
	require.NoError(t, err, "Failed to create PAT token")
	adminToken := pat.Token

	// Set up logger for testing
	logger, err := mlog.NewLogger()
	require.NoError(t, err, "Failed to create logger")

	cfg := make(mlog.LoggerConfiguration)
	cfg["console"] = mlog.TargetCfg{
		Type:          "console",
		Levels:        []mlog.Level{mlog.LvlDebug, mlog.LvlInfo, mlog.LvlWarn, mlog.LvlError},
		Format:        "plain",
		FormatOptions: json.RawMessage(`{"enable_color": false}`),
		Options:       json.RawMessage(`{"out": "stderr"}`),
		MaxQueueSize:  1000,
	}
	err = logger.ConfigureTargets(cfg, nil)
	require.NoError(t, err, "Failed to configure logger")

	return &TestSuite{
		t:          t,
		container:  container,
		serverURL:  serverURL,
		adminToken: adminToken,
		logger:     logger,
	}
}

// TearDown cleans up the test suite
func (suite *TestSuite) TearDown() {
	if suite.container != nil {
		ctx := context.Background()
		if err := suite.container.Terminate(ctx); err != nil {
			suite.t.Logf("Failed to terminate container: %v", err)
		}
	}
	if suite.logger != nil {
		suite.logger.Flush()
	}
}

// CreateMCPServer creates and configures an MCP server for testing
func (suite *TestSuite) CreateMCPServer(devMode bool) {
	require.NotNil(suite.t, suite.logger, "Logger must be initialized")
	require.NotEmpty(suite.t, suite.serverURL, "Server URL must be set")
	require.NotEmpty(suite.t, suite.adminToken, "Admin token must be set")

	mcpServer, err := mcpserver.NewMattermostStdioMCPServer(suite.serverURL, suite.adminToken,
		mcpserver.WithLogger(suite.logger),
		mcpserver.WithDevMode(devMode),
	)
	require.NoError(suite.t, err, "Failed to create MCP server")

	suite.mcpServer = mcpServer
	suite.devMode = devMode
}

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
		mcpServer, err := mcpserver.NewMattermostStdioMCPServer(suite.serverURL, suite.adminToken,
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(false),
		)

		require.NoError(t, err, "Valid configuration should not return error")
		assert.NotNil(t, mcpServer, "MCP server should be created with valid config")
	})

	t.Run("InvalidServerURL", func(t *testing.T) {
		_, err := mcpserver.NewMattermostStdioMCPServer("http://invalid-server-url:9999", suite.adminToken,
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(false),
		)

		assert.Error(t, err, "Invalid server URL should return error")
		assert.Contains(t, err.Error(), "startup token validation failed", "Error should mention token validation failure")
	})

	t.Run("InvalidToken", func(t *testing.T) {
		_, err := mcpserver.NewMattermostStdioMCPServer(suite.serverURL, "invalid-token-12345",
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(false),
		)

		assert.Error(t, err, "Invalid token should return error")
		assert.Contains(t, err.Error(), "startup token validation failed", "Error should mention token validation failure")
	})

	t.Run("EmptyToken", func(t *testing.T) {
		_, err := mcpserver.NewMattermostStdioMCPServer(suite.serverURL, "",
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(false),
		)

		// Empty token should fail option validation
		assert.Error(t, err, "Empty token should fail validation")
		assert.Contains(t, err.Error(), "personal access token cannot be empty", "Error should mention empty token")
	})

	t.Run("DevModeConfiguration", func(t *testing.T) {
		mcpServer, err := mcpserver.NewMattermostStdioMCPServer(suite.serverURL, suite.adminToken,
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(true),
		)

		require.NoError(t, err, "Dev mode configuration should not return error")
		assert.NotNil(t, mcpServer, "MCP server should be created with dev mode")
	})

	t.Run("StdioTransportFixed", func(t *testing.T) {
		// STDIO constructor always uses stdio transport
		mcpServer, err := mcpserver.NewMattermostStdioMCPServer(suite.serverURL, suite.adminToken,
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(false),
		)

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
		authProvider := auth.NewTokenAuthenticationProvider(suite.serverURL, suite.adminToken, suite.logger)
		assert.NotNil(t, authProvider, "Token authentication provider should be created")

		// Test token validation with configured token
		err := authProvider.ValidateAuth(context.Background())
		require.NoError(t, err, "Should validate authentication with configured token")
	})

	t.Run("TokenValidationAtStartup", func(t *testing.T) {
		// This tests the startup token validation that happens in NewMattermostMCPServer
		mcpServer, err := mcpserver.NewMattermostStdioMCPServer(suite.serverURL, suite.adminToken,
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(false),
		)

		require.NoError(t, err, "Startup token validation should succeed with valid token")
		assert.NotNil(t, mcpServer, "MCP server should be created after successful token validation")
	})

	t.Run("TokenAuthenticationFailure", func(t *testing.T) {
		invalidToken := "invalid-token-xyz"
		authProvider := auth.NewTokenAuthenticationProvider(suite.serverURL, invalidToken, suite.logger)

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
		mcpServer, err := mcpserver.NewMattermostStdioMCPServer(suite.serverURL, suite.adminToken,
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(false),
		)

		require.NoError(t, err, "Startup validation should succeed")
		assert.NotNil(t, mcpServer, "MCP server should be created after successful validation")
	})

	t.Run("StartupValidationWithInvalidServer", func(t *testing.T) {
		_, err := mcpserver.NewMattermostStdioMCPServer("http://nonexistent-server:8065", suite.adminToken,
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(false),
		)

		assert.Error(t, err, "Startup validation should fail with invalid server")
		assert.Contains(t, err.Error(), "startup token validation failed", "Error should mention startup validation failure")
	})

	t.Run("StartupValidationWithUnauthorizedToken", func(t *testing.T) {
		_, err := mcpserver.NewMattermostStdioMCPServer(suite.serverURL, "unauthorized-token-123",
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(false),
		)

		assert.Error(t, err, "Startup validation should fail with unauthorized token")
		assert.Contains(t, err.Error(), "startup token validation failed", "Error should mention startup validation failure")
	})

	t.Run("ValidTokenAlwaysValidated", func(t *testing.T) {
		// STDIO servers always validate tokens at startup
		mcpServer, err := mcpserver.NewMattermostStdioMCPServer(suite.serverURL, suite.adminToken,
			mcpserver.WithLogger(suite.logger),
			mcpserver.WithDevMode(false),
		)

		require.NoError(t, err, "Server creation should succeed with valid token")
		assert.NotNil(t, mcpServer, "MCP server should be created")
	})
}
