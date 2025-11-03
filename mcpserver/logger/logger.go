// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package logger

import (
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// Logger is a minimal logging interface that can be satisfied by both
// standalone (mlog) and embedded (pluginapi) logging implementations
type Logger interface {
	Debug(msg string, keyValuePairs ...any)
	Info(msg string, keyValuePairs ...any)
	Warn(msg string, keyValuePairs ...any)
	Error(msg string, keyValuePairs ...any)
	Flush() error
}

// standaloneLoggerAdapter adapts mlog.Logger to our minimal Logger interface
type standaloneLoggerAdapter struct {
	logger *mlog.Logger
}

// NewStandaloneLogger creates a Logger that wraps mlog.Logger
func NewStandaloneLogger(logger *mlog.Logger) Logger {
	return &standaloneLoggerAdapter{logger: logger}
}

func (a *standaloneLoggerAdapter) Debug(msg string, keyValuePairs ...any) {
	fields := keyValuePairsToFields(keyValuePairs)
	a.logger.Debug(msg, fields...)
}

func (a *standaloneLoggerAdapter) Info(msg string, keyValuePairs ...any) {
	fields := keyValuePairsToFields(keyValuePairs)
	a.logger.Info(msg, fields...)
}

func (a *standaloneLoggerAdapter) Warn(msg string, keyValuePairs ...any) {
	fields := keyValuePairsToFields(keyValuePairs)
	a.logger.Warn(msg, fields...)
}

func (a *standaloneLoggerAdapter) Error(msg string, keyValuePairs ...any) {
	fields := keyValuePairsToFields(keyValuePairs)
	a.logger.Error(msg, fields...)
}

func (a *standaloneLoggerAdapter) Flush() error {
	return a.logger.Flush()
}

// keyValuePairsToFields converts key-value pairs to mlog.Field slices
func keyValuePairsToFields(keyValuePairs []any) []mlog.Field {
	fields := make([]mlog.Field, 0, len(keyValuePairs)/2)
	for i := 0; i < len(keyValuePairs)-1; i += 2 {
		key, ok := keyValuePairs[i].(string)
		if !ok {
			continue
		}
		fields = append(fields, mlog.Any(key, keyValuePairs[i+1]))
	}
	return fields
}

// CreateDefaultLogger creates a logger with sensible defaults for the MCP server
func CreateDefaultLogger() (Logger, error) {
	// Use the same configuration helper for consistency
	mlogger, err := CreateMlogLoggerWithOptions(false, "") // No debug, no file logging
	if err != nil {
		return nil, err
	}
	return NewStandaloneLogger(mlogger), nil
}

// CreateLoggerWithOptions creates a logger with debug and file logging options
// This function sets up a fully configured logger and enables std log redirection
// Returns the simplified Logger interface
func CreateLoggerWithOptions(enableDebug bool, logFile string) (Logger, error) {
	mlogger, err := CreateMlogLoggerWithOptions(enableDebug, logFile)
	if err != nil {
		return nil, err
	}
	return NewStandaloneLogger(mlogger), nil
}

// CreateMlogLoggerWithOptions creates an mlog.Logger with debug and file logging options
// This function sets up a fully configured logger and enables std log redirection
func CreateMlogLoggerWithOptions(enableDebug bool, logFile string) (*mlog.Logger, error) {
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
	logger.RedirectStdLog(mlog.LvlStdLog)

	return logger, nil
}
