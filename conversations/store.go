// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/mattermost/mattermost/server/public/model"
)

// SaveTitleAsync saves a title asynchronously
func (c *Conversations) SaveTitleAsync(threadID, title string) {
	go func() {
		if err := c.SaveTitle(threadID, title); err != nil {
			c.mmClient.LogError("failed to save title: " + err.Error())
		}
	}()
}

// SaveTitle saves a title for a conversation identified by its root post ID.
// It looks up the conversation by RootPostID and updates the title via
// the LLM_Conversations table.
func (c *Conversations) SaveTitle(threadID, title string) error {
	if c.db == nil {
		return nil // Skip database operations when db is not available
	}
	// Update any conversation whose RootPostID matches the given thread ID.
	_, err := c.db.ExecBuilder(c.db.Builder().
		Update("LLM_Conversations").
		Set("Title", title).
		Set("UpdatedAt", model.GetMillis()).
		Where(sq.Eq{"RootPostID": threadID}))
	return err
}

// DeletePostMetaForDeletedPost soft-deletes conversations associated with the given post.
// If the post is a root post, conversations keyed by that RootPostID are marked as deleted.
func (c *Conversations) DeletePostMetaForDeletedPost(post *model.Post) error {
	if c.db == nil || post == nil || post.Id == "" {
		return nil
	}
	now := model.GetMillis()
	_, err := c.db.ExecBuilder(c.db.Builder().
		Update("LLM_Conversations").
		Set("DeleteAt", now).
		Set("UpdatedAt", now).
		Where(sq.And{
			sq.Eq{"RootPostID": post.Id},
			sq.Eq{"DeleteAt": 0},
		}))
	return err
}

func (c *Conversations) getAIThreads(dmChannelIDs []string) ([]AIThread, error) {
	var dbPosts []AIThread
	if err := c.db.DoQuery(&dbPosts, c.db.Builder().
		Select(
			"p.Id",
			"p.Message",
			"p.ChannelID",
			"COALESCE(t.Title, '') as Title",
			"(SELECT COUNT(*) FROM Posts WHERE Posts.RootId = p.Id AND DeleteAt = 0) AS ReplyCount",
			"p.UpdateAt",
		).
		From("Posts as p").
		Where(sq.Eq{"ChannelID": dmChannelIDs}).
		Where(sq.Eq{"RootId": ""}).
		Where(sq.Eq{"DeleteAt": 0}).
		LeftJoin("LLM_Conversations as t ON t.RootPostID = p.Id").
		OrderBy("CreateAt DESC").
		Limit(60).
		Offset(0),
	); err != nil {
		return nil, fmt.Errorf("failed to get posts for bot DM: %w", err)
	}

	return dbPosts, nil
}
