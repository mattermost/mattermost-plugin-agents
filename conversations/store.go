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

// SaveTitle saves a title for a thread
func (c *Conversations) SaveTitle(threadID, title string) error {
	if c.db == nil {
		return nil // Skip database operations when db is not available
	}
	_, err := c.db.ExecBuilder(c.db.Builder().Insert("LLM_PostMeta").
		Columns("RootPostID", "Title").
		Values(threadID, title).
		Suffix("ON CONFLICT (RootPostID) DO UPDATE SET Title = ?", title))
	return err
}

// DeletePostMetaForDeletedPost removes the stored thread title when the given post is deleted.
// Rows are keyed by root post ID only; deleting a reply is a no-op at the database layer.
func (c *Conversations) DeletePostMetaForDeletedPost(post *model.Post) error {
	if c.db == nil || post == nil || post.Id == "" {
		return nil
	}
	_, err := c.db.ExecBuilder(c.db.Builder().Delete("LLM_PostMeta").Where(sq.Eq{"RootPostID": post.Id}))
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
		LeftJoin("LLM_PostMeta as t ON t.RootPostID = p.Id").
		OrderBy("CreateAt DESC").
		Limit(60).
		Offset(0),
	); err != nil {
		return nil, fmt.Errorf("failed to get posts for bot DM: %w", err)
	}

	return dbPosts, nil
}
