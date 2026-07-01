// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// testLogger is a no-op logger for tests. Errors are surfaced via t.Logf.
type testLogger struct {
	t *testing.T
}

func (l *testLogger) Debug(msg string, keyValuePairs ...any) {}
func (l *testLogger) Info(msg string, keyValuePairs ...any)  {}
func (l *testLogger) Warn(msg string, keyValuePairs ...any)  {}
func (l *testLogger) Error(msg string, keyValuePairs ...any) {
	l.t.Logf("ERROR: %s %v", msg, keyValuePairs)
}
func (l *testLogger) Flush() error { return nil }

// newTestProvider builds a provider wired to a test logger and the given server URL.
func newTestProvider(t *testing.T, serverURL string) *MattermostToolProvider {
	t.Helper()
	return &MattermostToolProvider{
		logger:      &testLogger{t: t},
		mmServerURL: serverURL,
	}
}

// newTestClient returns a Client4 pointed at serverURL with a dummy token set.
func newTestClient(serverURL string) *model.Client4 {
	client := model.NewAPIv4Client(serverURL)
	client.SetToken("test-token")
	return client
}
