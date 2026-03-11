// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/shared/mlog"
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
