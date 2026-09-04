// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"testing"

	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestLanguageModelLoggingDoesNotLogFileAttachmentData(t *testing.T) {
	const (
		sensitiveFileName = "private-attachment.txt"
		sensitiveContent  = "private attachment contents"
	)

	var entries []string
	mockAPI := &plugintest.API{}
	mockAPI.On(
		"LogInfo",
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).Run(func(args mock.Arguments) {
		entries = append(entries, fmt.Sprint(args...))
	}).Return()

	logger := pluginapi.NewClient(mockAPI, nil).Log
	wrapper := NewLanguageModelLogWrapper(logger, nil)
	wrapper.logInput(CompletionRequest{
		Posts: []Post{{
			Role:    PostRoleUser,
			Message: "Attached File Contents:\nFile Name: " + sensitiveFileName + "\nContent: " + sensitiveContent,
		}},
	})

	require.NotEmpty(t, entries)
	for _, entry := range entries {
		assert.NotContains(t, entry, sensitiveFileName, "attachment names must never be written to logs")
		assert.NotContains(t, entry, sensitiveContent, "attachment contents must never be written to logs")
	}
}
