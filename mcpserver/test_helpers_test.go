// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	mmcontainer "github.com/mattermost/testcontainers-mattermost-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver"
	loggerlib "github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/tools"
)

func init() {
	// Suppress stdlib log output from testcontainers internals.
	log.SetOutput(io.Discard)
}

// testLogger routes MCP server log output through t.Log so it only appears on failure.
// Replaces the mlog stderr logger that unconditionally wrote to stderr.
type testLogger struct {
	t *testing.T
}

func (l *testLogger) Debug(msg string, keyValuePairs ...any) {
	l.t.Helper()
	l.t.Logf("[DEBUG] %s%s", msg, formatKV(keyValuePairs))
}

func (l *testLogger) Info(msg string, keyValuePairs ...any) {
	l.t.Helper()
	l.t.Logf("[INFO] %s%s", msg, formatKV(keyValuePairs))
}

func (l *testLogger) Warn(msg string, keyValuePairs ...any) {
	l.t.Helper()
	l.t.Logf("[WARN] %s%s", msg, formatKV(keyValuePairs))
}

func (l *testLogger) Error(msg string, keyValuePairs ...any) {
	l.t.Helper()
	l.t.Logf("[ERROR] %s%s", msg, formatKV(keyValuePairs))
}

func (l *testLogger) Flush() error { return nil }

// formatKV formats key-value pairs into a " key=value key=value" string.
func formatKV(kvs []any) string {
	if len(kvs) == 0 {
		return ""
	}
	var s string
	for i := 0; i+1 < len(kvs); i += 2 {
		s += fmt.Sprintf(" %v=%v", kvs[i], kvs[i+1])
	}
	return s
}

// Compile-time check that testLogger satisfies logger.Logger.
var _ loggerlib.Logger = (*testLogger)(nil)

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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start Mattermost container with PAT enabled.
	// Retry once — the container init (team/user creation via mmctl) can hit transient races.
	var container *mmcontainer.MattermostContainer
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		container, err = mmcontainer.RunContainer(ctx,
			mmcontainer.WithLicense(""),
		)
		if err == nil {
			break
		}
		t.Logf("RunContainer attempt %d failed: %v", attempt+1, err)
	}
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

	return &TestSuite{
		t:          t,
		container:  container,
		serverURL:  serverURL,
		adminToken: adminToken,
		logger:     &testLogger{t: t},
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
	suite.createMCPServerWithConfig(devMode, nil)
}

// CreateMCPServerWithSearch creates an MCP server with a custom semantic search service
func (suite *TestSuite) CreateMCPServerWithSearch(devMode bool, searchService tools.SemanticSearchService) {
	suite.createMCPServerWithConfig(devMode, searchService)
}

func (suite *TestSuite) createMCPServerWithConfig(devMode bool, searchService tools.SemanticSearchService) {
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
	mcpServer, err := mcpserver.NewStdioServer(stdioConfig, suite.logger, searchService)
	require.NoError(suite.t, err, "Failed to create MCP server")

	suite.mcpServer = mcpServer
	suite.devMode = devMode
}
