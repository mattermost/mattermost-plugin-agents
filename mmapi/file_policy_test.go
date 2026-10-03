// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi_test

import (
	"io"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithFilePolicyDeniesAdminReads(t *testing.T) {
	sessionID := model.NewId()
	fileID := model.NewId()

	inner := mocks.NewMockClient(t)
	inner.On(
		"HasPermissionToFileAction",
		sessionID,
		fileID,
		model.AccessControlPolicyActionDownloadFileAttachment,
	).Return(false)

	mm := mmapi.WithFilePolicy(inner, sessionID)

	_, err := mm.GetFileInfo(fileID)
	require.ErrorIs(t, err, mmapi.ErrFileActionForbidden)

	_, err = mm.GetFile(fileID)
	require.ErrorIs(t, err, mmapi.ErrFileActionForbidden)
}

func TestWithFilePolicyAllowsAdminReads(t *testing.T) {
	sessionID := model.NewId()
	fileID := model.NewId()
	info := &model.FileInfo{Id: fileID, Name: "notes.txt"}

	inner := mocks.NewMockClient(t)
	inner.On(
		"HasPermissionToFileAction",
		sessionID,
		fileID,
		model.AccessControlPolicyActionDownloadFileAttachment,
	).Return(true)
	inner.On("GetFileInfo", fileID).Return(info, nil)
	inner.On("GetFile", fileID).Return(io.NopCloser(strings.NewReader("hello")), nil)

	mm := mmapi.WithFilePolicy(inner, sessionID)

	got, err := mm.GetFileInfo(fileID)
	require.NoError(t, err)
	assert.Equal(t, info, got)

	reader, err := mm.GetFile(fileID)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(body))
}

func TestWithFilePolicyFailsClosedWithoutSession(t *testing.T) {
	fileID := model.NewId()
	inner := mocks.NewMockClient(t)
	inner.On(
		"HasPermissionToFileAction",
		"",
		fileID,
		model.AccessControlPolicyActionDownloadFileAttachment,
	).Return(true).Maybe()

	mm := mmapi.WithFilePolicy(inner, "")

	_, err := mm.GetFileInfo(fileID)
	require.ErrorIs(t, err, mmapi.ErrFileActionForbidden)
}
