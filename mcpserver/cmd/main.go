// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
)

const version = "0.1.0"

type cliConfig struct {
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
	stateless           bool
	trackAIGenerated    *bool
	showVersion         bool
}

func parseFlags(args []string, stderr io.Writer) (cliConfig, error) {
	var cfg cliConfig
	var trackAI bool

	fs := flag.NewFlagSet("mattermost-mcp-server", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Mattermost Model Context Protocol (MCP) Server

A Model Context Protocol (MCP) server that provides tools for interacting with Mattermost.

The server supports reading posts, searching, creating content, and managing teams/channels.
Authentication is handled via Personal Access Tokens (PAT).

Usage:
  mattermost-mcp-server [flags]

Flags:
`)
		fs.PrintDefaults()
	}

	fs.StringVar(&cfg.mmServerURL, "server-url", "", "Mattermost server URL (required, or set MM_SERVER_URL env var)")
	fs.StringVar(&cfg.mmServerURL, "s", "", "Shorthand for --server-url")
	fs.StringVar(&cfg.mmInternalServerURL, "internal-server-url", "", "Internal Mattermost server URL for API communication (optional, or set MM_INTERNAL_SERVER_URL env var)")
	fs.StringVar(&cfg.token, "token", "", "Personal Access Token (required, or set MM_ACCESS_TOKEN env var)")
	fs.StringVar(&cfg.token, "t", "", "Shorthand for --token")
	fs.BoolVar(&cfg.debug, "debug", false, "Enable debug logging")
	fs.BoolVar(&cfg.debug, "d", false, "Shorthand for --debug")
	fs.StringVar(&cfg.logFile, "logfile", "", "Path to log file (logs to file in addition to stderr)")
	fs.StringVar(&cfg.logFile, "l", "", "Shorthand for --logfile")
	fs.BoolVar(&cfg.devMode, "dev", false, "Enable development mode with additional tools for setting up test data")
	fs.StringVar(&cfg.transport, "transport", "stdio", "Transport type (stdio or http)")

	fs.IntVar(&cfg.httpPort, "http-port", 8080, "Port for HTTP server (used when transport is http)")
	fs.StringVar(&cfg.httpBindAddr, "http-bind-addr", "127.0.0.1", "Bind address for HTTP server (defaults to localhost for security, use 0.0.0.0 for all interfaces)")
	fs.StringVar(&cfg.siteURL, "site-url", "", "External URL for OAuth and CORS (required when http-bind-addr is localhost or when using reverse proxy)")
	fs.BoolVar(&cfg.stateless, "stateless", false, "Enable stateless mode for multi-node/HA deployments (no server-side session tracking)")
	fs.BoolVar(&trackAI, "track-ai-generated", false, "Track AI-generated content in posts (default: false for stdio, true for http)")
	fs.BoolVar(&cfg.showVersion, "version", false, "Print version and exit")
	fs.BoolVar(&cfg.showVersion, "v", false, "Shorthand for --version")

	if err := fs.Parse(expandShortFlags(fs, args)); err != nil {
		return cliConfig{}, err
	}

	fs.Visit(func(f *flag.Flag) {
		if f.Name == "track-ai-generated" {
			val := trackAI
			cfg.trackAIGenerated = &val
		}
	})

	return cfg, nil
}

// expandShortFlags preserves pflag/POSIX short-flag forms such as -sURL and -dl /path
// while leaving long flags, registered single-dash flag names, and option values untouched.
func expandShortFlags(fs *flag.FlagSet, args []string) []string {
	type boolFlag interface {
		IsBoolFlag() bool
	}
	isValueFlag := func(name string) bool {
		f := fs.Lookup(name)
		if f == nil {
			return false
		}
		if bf, ok := f.Value.(boolFlag); ok && bf.IsBoolFlag() {
			return false
		}
		return true
	}

	out := make([]string, 0, len(args))
	expectValue := false
	for i, arg := range args {
		if expectValue {
			out = append(out, arg)
			expectValue = false
			continue
		}
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			out = append(out, arg)
			continue
		}

		// Long flag (--foo or --foo=bar) or registered single-dash flag (-s, -server-url, etc.).
		trimmed := strings.TrimPrefix(arg, "--")
		if trimmed == arg {
			trimmed = arg[1:]
		}
		name, _, hasEq := strings.Cut(trimmed, "=")
		if arg[1] == '-' || len(arg) == 2 || fs.Lookup(name) != nil {
			out = append(out, arg)
			if !hasEq && isValueFlag(name) {
				expectValue = true
			}
			continue
		}

		var expanded []string
		valid := true
		needsNext := false
		for j := 1; j < len(arg); j++ {
			switch arg[j] {
			case 'd', 'v':
				expanded = append(expanded, "-"+arg[j:j+1])
			case 's', 't', 'l':
				expanded = append(expanded, "-"+arg[j:j+1])
				if j+1 < len(arg) {
					val := arg[j+1:]
					if val[0] == '=' {
						val = val[1:]
					}
					expanded = append(expanded, val)
				} else {
					needsNext = true
				}
				j = len(arg)
			default:
				valid = false
			}
			if !valid {
				break
			}
		}

		if valid && len(expanded) > 0 {
			out = append(out, expanded...)
			expectValue = needsNext
		} else {
			out = append(out, arg)
		}
	}
	return out
}

func main() {
	cfg, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(1)
	}
	if cfg.showVersion {
		fmt.Printf("mattermost-mcp-server version %s\n", version)
		return
	}

	if err := runServer(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runServer(cfg cliConfig) error {
	// Create logger with debug and file logging options
	// This automatically configures std log redirection
	logger, err := loggerlib.CreateLoggerWithOptions(cfg.debug, cfg.logFile)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Check for environment variables if flags not provided
	mmServerURL := cfg.mmServerURL
	if mmServerURL == "" {
		mmServerURL = os.Getenv("MM_SERVER_URL")
		if mmServerURL == "" {
			logger.Error("server URL is required (use --server-url or MM_SERVER_URL environment variable)")
			logger.Flush()
			return fmt.Errorf("server URL is required")
		}
	}

	mmInternalServerURL := cfg.mmInternalServerURL
	if mmInternalServerURL == "" {
		mmInternalServerURL = os.Getenv("MM_INTERNAL_SERVER_URL")
	}

	token := cfg.token
	if token == "" && cfg.transport == "stdio" {
		token = os.Getenv("MM_ACCESS_TOKEN")
		if token == "" {
			logger.Error("personal access token is required (use --token or MM_ACCESS_TOKEN environment variable)")
			logger.Flush()
			return fmt.Errorf("personal access token is required")
		}
	}

	// Validate transport type
	if cfg.transport != "stdio" && cfg.transport != "http" {
		logger.Error("invalid transport type", "transport", cfg.transport)
		logger.Flush()
		return fmt.Errorf("invalid transport type: %s (supported types: 'stdio', 'http')", cfg.transport)
	}

	logger.Debug("starting mattermost mcp server",
		"server_url", mmServerURL,
		"transport", cfg.transport,
	)

	if cfg.devMode {
		logger.Info("development mode enabled", "dev_mode", cfg.devMode)
	}

	// Create Mattermost MCP server based on transport type
	var mcpServer interface{ Serve() error }

	// Standalone stdio/http servers run outside the plugin and have no license
	// information; NewStdioServer and NewHTTPServer keep state-changing tools
	// available.
	switch cfg.transport {
	case "stdio":
		stdioConfig := mcpserver.StdioConfig{
			MMServerURL:         mmServerURL,
			MMInternalServerURL: mmInternalServerURL,
			DevMode:             cfg.devMode,
			TrackAIGenerated:    cfg.trackAIGenerated,
			PersonalAccessToken: token,
		}

		mcpServer, err = mcpserver.NewStdioServer(stdioConfig, logger, nil, nil)
	case "http":
		httpConfig := mcpserver.HTTPConfig{
			MMServerURL:         mmServerURL,
			MMInternalServerURL: mmInternalServerURL,
			DevMode:             cfg.devMode,
			TrackAIGenerated:    cfg.trackAIGenerated,
			HTTPPort:            cfg.httpPort,
			HTTPBindAddr:        cfg.httpBindAddr,
			SiteURL:             cfg.siteURL,
			Stateless:           cfg.stateless,
		}

		mcpServer, err = mcpserver.NewHTTPServer(httpConfig, logger)
	}
	if err != nil {
		logger.Error("failed to create MCP server", "error", err)
		logger.Flush()
		return fmt.Errorf("failed to create MCP server: %w", err)
	}

	if err := mcpServer.Serve(); err != nil {
		logger.Error("server error", "error", err)
		logger.Flush()
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}
