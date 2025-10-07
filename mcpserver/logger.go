// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

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
