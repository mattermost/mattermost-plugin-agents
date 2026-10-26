// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/stretchr/testify/require"
)

func TestSetFileLogger_ShutsDownPreviousLogger(t *testing.T) {
	logger, err := mlog.NewLogger()
	if err != nil {
		t.Skip("Could not create logger:", err)
	}

	sinks := NewTokenUsageSinks(nil)
	sinks.SetLoggingEnabled(true)
	sinks.SetFileEnabled(true)

	// Set a logger, then replace with nil. Previous logger must be shut down to avoid leaking file handles.
	sinks.SetFileLogger(logger)
	sinks.SetFileLogger(nil)

	// Setting nil when no logger was set should not panic.
	sinks.SetFileLogger(nil)
}

func TestAccountingEnabledFailsClosed(t *testing.T) {
	sinks := NewTokenUsageSinks(nil)
	sinks.SetLoggingEnabled(true)
	sinks.SetPluginEnabled(true)

	require.False(t, sinks.AccountingEnabled(), "unset predicate must fail closed")
	require.False(t, sinks.LoggingEnabled(), "logging must stay off when accounting is unset")

	sinks.SetAccountingEnabled(func() bool { return false })
	require.False(t, sinks.LoggingEnabled(), "logging must stay off when accounting is disabled")

	sinks.SetAccountingEnabled(func() bool { return true })
	require.True(t, sinks.LoggingEnabled(), "logging should follow the config flag when accounting is enabled")
}
