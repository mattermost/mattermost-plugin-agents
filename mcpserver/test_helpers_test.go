// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"encoding/json"
	"testing"

	mmcontainer "github.com/mattermost/testcontainers-mattermost-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver"
	loggerlib "github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// TestSuite represents the integration test suite
type TestSuite struct {
	t          *testing.T
	container  *mmcontainer.MattermostContainer
	serverURL  string
	adminToken string
	logger     loggerlib.Logger
	mcpServer  interface {
		Serve() error
		GetMCPServer() *mcp.Server
	}
	devMode bool
}

// SetupTestSuite initializes a Mattermost container and MCP server for testing
func SetupTestSuite(t *testing.T) *TestSuite {
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
	mlogger, err := mlog.NewLogger()
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
	err = mlogger.ConfigureTargets(cfg, nil)
	require.NoError(t, err, "Failed to configure logger")

	return &TestSuite{
		t:          t,
		container:  container,
		serverURL:  serverURL,
		adminToken: adminToken,
		logger:     loggerlib.NewStandaloneLogger(mlogger),
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

	stdioConfig := mcpserver.StdioConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: suite.serverURL,
			DevMode:     devMode,
		},
		PersonalAccessToken: suite.adminToken,
	}
	mcpServer, err := mcpserver.NewStdioServer(stdioConfig, suite.logger)
	require.NoError(suite.t, err, "Failed to create MCP server")

	suite.mcpServer = mcpServer
	suite.devMode = devMode
}
