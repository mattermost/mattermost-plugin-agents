// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi_test

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestCheckFileChannelRead(t *testing.T) {
	const (
		userID     = "user-id"
		readableID = "readable-channel"
		deniedID   = "denied-channel"
	)

	tests := []struct {
		name      string
		userID    string
		channelID string
		wantErr   error
	}{
		{name: "readable channel", userID: userID, channelID: readableID},
		{name: "unreadable channel", userID: userID, channelID: deniedID, wantErr: mmapi.ErrFileChannelForbidden},
		{name: "file without a channel fails closed", userID: userID, wantErr: mmapi.ErrFileChannelForbidden},
		{name: "no user fails closed", channelID: readableID, wantErr: mmapi.ErrFileChannelForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mm := mocks.NewMockClient(t)
			mm.EXPECT().HasPermissionToChannel(userID, readableID, model.PermissionReadChannel).Return(true).Maybe()
			mm.EXPECT().HasPermissionToChannel(userID, deniedID, model.PermissionReadChannel).Return(false).Maybe()

			err := mmapi.CheckFileChannelRead(mm, tt.userID, &model.FileInfo{Id: "file-id", ChannelId: tt.channelID})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestGetReadableFileInfo(t *testing.T) {
	const (
		sessionID = "session-id"
		userID    = "user-id"
		fileID    = "file-id"
		channelID = "channel-id"
	)

	tests := []struct {
		name            string
		downloadAllowed bool
		channelReadable bool
		wantErr         error
	}{
		{name: "download allowed and channel readable", downloadAllowed: true, channelReadable: true},
		{name: "download denied by file policy", channelReadable: true, wantErr: mmapi.ErrFileActionForbidden},
		{name: "channel no longer readable", downloadAllowed: true, wantErr: mmapi.ErrFileChannelForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mm := mocks.NewMockClient(t)
			mm.EXPECT().HasPermissionToFileAction(sessionID, fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(tt.downloadAllowed)
			mm.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, ChannelId: channelID}, nil).Maybe()
			mm.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(tt.channelReadable).Maybe()

			info, err := mmapi.GetReadableFileInfo(mm, sessionID, userID, fileID)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, info)
				return
			}
			require.NoError(t, err)
			require.Equal(t, fileID, info.Id)
		})
	}
}
