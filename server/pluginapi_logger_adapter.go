// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// PluginAPILoggerAdapter adapts pluginapi.LogService to logger.Logger
// This allows the embedded MCP server to use the plugin's logging infrastructure
type PluginAPILoggerAdapter struct {
	log pluginapi.LogService
}

// NewPluginAPILoggerAdapter creates a new adapter
func NewPluginAPILoggerAdapter(log pluginapi.LogService) logger.Logger {
	return &PluginAPILoggerAdapter{log: log}
}

// Debug logs at debug level
func (a *PluginAPILoggerAdapter) Debug(msg string, keyValuePairs ...any) {
	a.log.Debug(msg, keyValuePairs...)
}

// Info logs at info level
func (a *PluginAPILoggerAdapter) Info(msg string, keyValuePairs ...any) {
	a.log.Info(msg, keyValuePairs...)
}

// Warn logs at warn level
func (a *PluginAPILoggerAdapter) Warn(msg string, keyValuePairs ...any) {
	a.log.Warn(msg, keyValuePairs...)
}

// Error logs at error level
func (a *PluginAPILoggerAdapter) Error(msg string, keyValuePairs ...any) {
	a.log.Error(msg, keyValuePairs...)
}

// Flush flushes any buffered logs (no-op for plugin API)
func (a *PluginAPILoggerAdapter) Flush() error {
	// Plugin API doesn't support explicit flush
	return nil
}
