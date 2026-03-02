// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"sync/atomic"

	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// TokenUsageSinks stores shared sink state for token usage logging.
// Wrappers can read these atomically so config toggles do not require
// rebuilding bot wrappers.
type TokenUsageSinks struct {
	pluginLogger TokenUsagePluginLogger

	loggingEnabled atomic.Bool
	pluginEnabled  atomic.Bool
	fileEnabled    atomic.Bool
	fileLogger     atomic.Pointer[mlog.Logger]
}

// NewTokenUsageSinks creates a sink controller with the provided plugin logger.
func NewTokenUsageSinks(pluginLogger TokenUsagePluginLogger) *TokenUsageSinks {
	return &TokenUsageSinks{
		pluginLogger: pluginLogger,
	}
}

func (s *TokenUsageSinks) SetLoggingEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.loggingEnabled.Store(enabled)
}

func (s *TokenUsageSinks) SetPluginEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.pluginEnabled.Store(enabled)
}

func (s *TokenUsageSinks) SetFileEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.fileEnabled.Store(enabled)
}

func (s *TokenUsageSinks) SetFileLogger(logger *mlog.Logger) {
	if s == nil {
		return
	}
	prev := s.fileLogger.Swap(logger)
	if prev != nil {
		_ = prev.Shutdown()
	}
}

func (s *TokenUsageSinks) LoggingEnabled() bool {
	if s == nil {
		return false
	}
	return s.loggingEnabled.Load()
}

func (s *TokenUsageSinks) PluginLogger() TokenUsagePluginLogger {
	if s == nil {
		return nil
	}
	if !s.loggingEnabled.Load() || !s.pluginEnabled.Load() {
		return nil
	}
	return s.pluginLogger
}

func (s *TokenUsageSinks) FileLogger() *mlog.Logger {
	if s == nil {
		return nil
	}
	if !s.loggingEnabled.Load() || !s.fileEnabled.Load() {
		return nil
	}
	return s.fileLogger.Load()
}
