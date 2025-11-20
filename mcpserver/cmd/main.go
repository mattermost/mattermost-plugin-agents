// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"os"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver"
	loggerlib "github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/spf13/cobra"
)

const version = "0.1.0"

var (
	mmServerURL         string
	mmInternalServerURL string
	token               string
	debug               bool
	logFile             string
	devMode             bool
	transport           string
	httpPort            int
	httpBindAddr        string
	siteURL             string
	trackAIGenerated    *bool // Pointer to distinguish unset from false
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "mattermost-mcp-server",
		Short: "Mattermost Model Context Protocol (MCP) Server",
		Long: `A Model Context Protocol (MCP) server that provides tools for interacting with Mattermost.

The server supports reading posts, searching, creating content, and managing teams/channels.
Authentication is handled via Personal Access Tokens (PAT).`,
		Version: version,
		RunE:    runServer,
	}

	// Define flags
	rootCmd.Flags().StringVarP(&mmServerURL, "server-url", "s", "", "Mattermost server URL (required, or set MM_SERVER_URL env var)")
	rootCmd.Flags().StringVar(&mmInternalServerURL, "internal-server-url", "", "Internal Mattermost server URL for API communication (optional, or set MM_INTERNAL_SERVER_URL env var)")
	rootCmd.Flags().StringVarP(&token, "token", "t", "", "Personal Access Token (required, or set MM_ACCESS_TOKEN env var)")
	rootCmd.Flags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")
	rootCmd.Flags().StringVarP(&logFile, "logfile", "l", "", "Path to log file (logs to file in addition to stderr)")
	rootCmd.Flags().BoolVar(&devMode, "dev", false, "Enable development mode with additional tools for setting up test data")
	rootCmd.Flags().StringVar(&transport, "transport", "stdio", "Transport type (stdio or http)")

	// HTTP transport flags
	rootCmd.Flags().IntVar(&httpPort, "http-port", 8080, "Port for HTTP server (used when transport is http)")
	rootCmd.Flags().StringVar(&httpBindAddr, "http-bind-addr", "127.0.0.1", "Bind address for HTTP server (defaults to localhost for security, use 0.0.0.0 for all interfaces)")
	rootCmd.Flags().StringVar(&siteURL, "site-url", "", "External URL for OAuth and CORS (required when http-bind-addr is localhost or when using reverse proxy)")

	// AI tracking flag
	rootCmd.Flags().Bool("track-ai-generated", false, "Track AI-generated content in posts (default: false for stdio, true for http)")

	// Note: We don't mark flags as required since they can also come from environment variables

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runServer(cmd *cobra.Command, args []string) error {
	// Create logger with debug and file logging options
	// This automatically configures std log redirection
	logger, err := loggerlib.CreateLoggerWithOptions(debug, logFile)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Check if track-ai-generated flag was explicitly set
	if cmd.Flags().Changed("track-ai-generated") {
		val, _ := cmd.Flags().GetBool("track-ai-generated")
		trackAIGenerated = &val
	}

	// Check for environment variables if flags not provided
	if mmServerURL == "" {
		mmServerURL = os.Getenv("MM_SERVER_URL")
		if mmServerURL == "" {
			logger.Error("server URL is required (use --server-url or MM_SERVER_URL environment variable)")
			logger.Flush()
			return fmt.Errorf("server URL is required")
		}
	}

	if mmInternalServerURL == "" {
		mmInternalServerURL = os.Getenv("MM_INTERNAL_SERVER_URL")
	}

	if token == "" && transport == "stdio" {
		token = os.Getenv("MM_ACCESS_TOKEN")
		if token == "" {
			logger.Error("personal access token is required (use --token or MM_ACCESS_TOKEN environment variable)")
			logger.Flush()
			return fmt.Errorf("personal access token is required")
		}
	}

	// Validate transport type
	if transport != "stdio" && transport != "http" {
		logger.Error("invalid transport type", "transport", transport)
		logger.Flush()
		return fmt.Errorf("invalid transport type: %s (supported types: 'stdio', 'http')", transport)
	}

	logger.Debug("starting mattermost mcp server",
		"server_url", mmServerURL,
		"transport", transport,
	)

	if devMode {
		logger.Info("development mode enabled", "dev_mode", devMode)
	}

	// Create Mattermost MCP server based on transport type
	var mcpServer interface{ Serve() error }

	switch transport {
	case "stdio":
		// Create STDIO transport configuration
		stdioConfig := mcpserver.StdioConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL:         mmServerURL,
				MMInternalServerURL: mmInternalServerURL,
				DevMode:             devMode,
				TrackAIGenerated:    trackAIGenerated,
			},
			PersonalAccessToken: token,
		}

		mcpServer, err = mcpserver.NewStdioServer(stdioConfig, logger)
	case "http":
		// Create HTTP transport configuration
		httpConfig := mcpserver.HTTPConfig{
			BaseConfig: mcpserver.BaseConfig{
				MMServerURL:         mmServerURL,
				MMInternalServerURL: mmInternalServerURL,
				DevMode:             devMode,
				TrackAIGenerated:    trackAIGenerated,
			},
			HTTPPort:     httpPort,
			HTTPBindAddr: httpBindAddr,
			SiteURL:      siteURL,
		}

		mcpServer, err = mcpserver.NewHTTPServer(httpConfig, logger)
	default:
		logger.Error("unsupported transport type", "transport", transport)
		logger.Flush()
		return fmt.Errorf("unsupported transport type: %s", transport)
	}
	if err != nil {
		logger.Error("failed to create MCP server", "error", err)
		logger.Flush()
		return fmt.Errorf("failed to create MCP server: %w", err)
	}

	// Start the MCP server
	if err := mcpServer.Serve(); err != nil {
		logger.Error("server error", "error", err)
		logger.Flush()
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}
