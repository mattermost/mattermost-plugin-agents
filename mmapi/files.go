// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi

import (
	"fmt"

	sq "github.com/Masterminds/squirrel"
)

// LinkFilesToPost points unattached FileInfo rows at a post so the files
// render as the post's attachments. Only rows whose PostId is empty are
// touched, which makes linking idempotent. Returns the IDs actually linked.
func (db *DBClient) LinkFilesToPost(fileIDs []string, postID, channelID string) ([]string, error) {
	var linked []string
	for _, fileID := range fileIDs {
		if fileID == "" {
			continue
		}
		result, err := db.ExecBuilder(db.Builder().
			Update("FileInfo").
			Set("PostId", postID).
			Set("ChannelId", channelID).
			Where(sq.And{
				sq.Eq{"Id": fileID},
				sq.Eq{"PostId": ""},
			}))
		if err != nil {
			return linked, fmt.Errorf("unable to link file %s to post %s: %w", fileID, postID, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return linked, fmt.Errorf("unable to get rows affected linking file %s to post %s: %w", fileID, postID, err)
		}
		if rows > 0 {
			linked = append(linked, fileID)
		}
	}
	return linked, nil
}

// UnlinkFilesFromPost clears FileInfo.PostId for all files currently
// attached to postID (used when a regenerated response replaces its
// attachments).
func (db *DBClient) UnlinkFilesFromPost(postID string) error {
	if _, err := db.ExecBuilder(db.Builder().
		Update("FileInfo").
		Set("PostId", "").
		Where(sq.Eq{"PostId": postID})); err != nil {
		return fmt.Errorf("unable to unlink files from post %s: %w", postID, err)
	}
	return nil
}
