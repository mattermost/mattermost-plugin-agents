// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
)

// ErrFileChannelForbidden is returned when a user can no longer read the
// channel a stored file belongs to.
var ErrFileChannelForbidden = errors.New("user cannot read the channel the file belongs to")

// CheckFileChannelRead fails closed unless userID may read the channel
// fileInfo belongs to. Download policy is a separate, session-scoped check
// (see WithFilePolicy); both must pass before stored file content is reused.
func CheckFileChannelRead(mm Client, userID string, fileInfo *model.FileInfo) error {
	if userID == "" || fileInfo == nil || fileInfo.ChannelId == "" {
		return ErrFileChannelForbidden
	}
	if !mm.HasPermissionToChannel(userID, fileInfo.ChannelId, model.PermissionReadChannel) {
		return ErrFileChannelForbidden
	}
	return nil
}

// GetReadableFileInfo returns fileID's metadata only when sessionID may
// download the file and userID may still read its channel.
func GetReadableFileInfo(mm Client, sessionID, userID, fileID string) (*model.FileInfo, error) {
	fileInfo, err := WithFilePolicy(mm, sessionID).GetFileInfo(fileID)
	if err != nil {
		return nil, err
	}
	if err := CheckFileChannelRead(mm, userID, fileInfo); err != nil {
		return nil, fmt.Errorf("file %s: %w", fileID, err)
	}
	return fileInfo, nil
}
