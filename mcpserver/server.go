// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/server"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/tools"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// Option defines a function that configures a MattermostMCPServer
type Option func(*MattermostMCPServer) error

// MattermostMCPServer provides a high-level interface for creating an MCP server
// with Mattermost-specific tools and authentication
type MattermostMCPServer struct {
	mcpServer    *server.MCPServer
	authProvider auth.AuthenticationProvider
	logger       *mlog.Logger
	config       Config
}

// NewMattermostStdioMCPServer creates a new Mattermost MCP server using STDIO transport with Personal Access Token authentication
func NewMattermostStdioMCPServer(serverURL, token string, opts ...Option) (*MattermostMCPServer, error) {
	// Validate required parameters
	if serverURL == "" {
		return nil, fmt.Errorf("server URL cannot be empty")
	}
	if token == "" {
		return nil, fmt.Errorf("personal access token cannot be empty")
	}

	// Create default logger with reasonable configuration
	defaultLogger, err := createDefaultLogger()
	if err != nil {
		return nil, fmt.Errorf("failed to create default logger: %w", err)
	}

	// Initialize server with defaults
	mattermostServer := &MattermostMCPServer{
		logger: defaultLogger,
		config: Config{
			ServerURL:           serverURL,
			PersonalAccessToken: token,
			Transport:           "stdio", // Always STDIO for this constructor
			DevMode:             false,
		},
	}

	// Apply all options
	for _, opt := range opts {
		if err := opt(mattermostServer); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	// Create PAT authentication provider (after options are applied so it uses the correct logger)
	mattermostServer.authProvider = auth.NewTokenAuthenticationProvider(serverURL, token, mattermostServer.logger)

	// Create the mcp-go server
	mattermostServer.mcpServer = server.NewMCPServer(
		"mattermost-mcp-server",
		"0.1.0",
		server.WithToolCapabilities(false),
		server.WithLogging(), // Enable logging capabilities
	)

	// For STDIO transport, always validate token at startup
	if err := mattermostServer.authProvider.ValidateAuth(context.Background()); err != nil {
		return nil, fmt.Errorf("startup token validation failed: %w", err)
	}

	// Initialize data directory for file operations
	if err := tools.EnsureDataDirectory(); err != nil {
		mattermostServer.logger.Warn("failed to initialize data directory", mlog.Err(err))
	}

	// Register all Mattermost tools
	mattermostServer.registerTools()

	return mattermostServer, nil
}

// Serve starts the server using the configured transport
func (s *MattermostMCPServer) Serve() error {
	switch s.config.Transport {
	case "stdio":
		return s.serveStdio()
	case "http":
		return s.serveHTTP()
	default:
		return fmt.Errorf("unsupported transport type: %s", s.config.Transport)
	}
}

// serveStdio starts the server using stdio transport
func (s *MattermostMCPServer) serveStdio() error {
	// Configure error logger to use our mlog logger
	errorLogger := log.New(&mlogWriter{logger: s.logger}, "", 0)

	return server.ServeStdio(s.mcpServer, server.WithErrorLogger(errorLogger))
}

// serveHTTP starts the server using HTTP transport
func (s *MattermostMCPServer) serveHTTP() error {
	// TODO: Implement HTTP/SSE transport for OAuth authentication
	// This will be implemented when OAuth support is added
	s.logger.Info("HTTP transport requested but not yet implemented")
	s.logger.Info("Future implementation will support OAuth authentication and StreamableHTTP")
	return fmt.Errorf("HTTP transport not yet implemented - will be added for OAuth support")
}

// createDefaultLogger creates a logger with sensible defaults for the MCP server
func createDefaultLogger() (*mlog.Logger, error) {
	// Use the same configuration helper for consistency
	return CreateLoggerWithOptions(false, "") // No debug, no file logging
}

// CreateLoggerWithOptions creates a logger with debug and file logging options
// This function sets up a fully configured logger and enables std log redirection
func CreateLoggerWithOptions(enableDebug bool, logFile string) (*mlog.Logger, error) {
	logger, err := mlog.NewLogger()
	if err != nil {
		return nil, fmt.Errorf("failed to create new logger: %w", err)
	}

	// Start with default levels - Info and above for production use
	levels := []mlog.Level{mlog.LvlInfo, mlog.LvlWarn, mlog.LvlError}
	if enableDebug {
		// Prepend debug level to ensure it's first in the list
		levels = append([]mlog.Level{mlog.LvlDebug}, levels...)
	}

	cfg := make(mlog.LoggerConfiguration)

	// Console logging configuration
	cfg["console"] = mlog.TargetCfg{
		Type:          "console",
		Levels:        levels,
		Format:        "plain",
		FormatOptions: json.RawMessage(`{"enable_color": false, "delim": " "}`),
		Options:       json.RawMessage(`{"out": "stderr"}`),
		MaxQueueSize:  1000,
	}

	// Add file logging if requested
	if logFile != "" {
		cfg["file"] = mlog.TargetCfg{
			Type:         "file",
			Levels:       levels,
			Format:       "json", // JSON format for file logs (better for parsing)
			Options:      json.RawMessage(fmt.Sprintf(`{"compress": false, "filename": "%s"}`, logFile)),
			MaxQueueSize: 1000,
		}
	}

	err = logger.ConfigureTargets(cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to configure logger targets: %w", err)
	}

	// Enable std log redirection - this ensures third-party libraries
	// using Go's standard log package route through our structured logger
	logger.RedirectStdLog(mlog.LvlInfo) // Redirect std logs at Info level

	return logger, nil
}

// Option functions for configuring MattermostMCPServer

// WithLogger configures the server to use a specific logger
func WithLogger(logger *mlog.Logger) Option {
	return func(s *MattermostMCPServer) error {
		if logger == nil {
			return fmt.Errorf("logger cannot be nil")
		}
		s.logger = logger
		return nil
	}
}

// WithDevMode enables or disables development mode (enables additional tools for testing)
func WithDevMode(enabled bool) Option {
	return func(s *MattermostMCPServer) error {
		s.config.DevMode = enabled
		return nil
	}
}

// mlogWriter adapts *mlog.Logger to io.Writer for the mcp-go error logger
type mlogWriter struct {
	logger *mlog.Logger
}

func (w *mlogWriter) Write(p []byte) (n int, err error) {
	// Logger is guaranteed to be non-nil by constructor
	w.logger.Error(string(p))
	return len(p), nil
}

// registerTools registers all tools using the tool provider
func (s *MattermostMCPServer) registerTools() {
	// Create the tools provider
	toolProvider := tools.NewMattermostToolProvider(s.authProvider, s.logger, s.config.ServerURL, s.config.DevMode)

	// Let the provider provide all tools to the MCP server
	toolProvider.ProvideTools(s.mcpServer)
}

// GetMCPServer returns the underlying MCP server for testing purposes
func (s *MattermostMCPServer) GetMCPServer() *server.MCPServer {
	return s.mcpServer
}
