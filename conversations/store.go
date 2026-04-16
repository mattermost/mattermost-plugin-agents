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
		Where(sq.Eq{"RootPostID": threadID}).
		Where(sq.Eq{"DeleteAt": 0}))
	return err
}

// DeleteConversationsForDeletedPost soft-deletes conversations associated with the given post.
// If the post is a root post, conversations keyed by that RootPostID are marked as deleted.
func (c *Conversations) DeleteConversationsForDeletedPost(post *model.Post) error {
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
	var threads []AIThread
	// Join on the root post's channel (not the conversation's ChannelID)
	// because channel analysis conversations store the analyzed channel as
	// their ChannelID, but the result post lives in the DM channel.
	if err := c.db.DoQuery(&threads, c.db.Builder().
		Select(
			"c.ID",
			"COALESCE(p.Message, '') as Message",
			"c.Title",
			"COALESCE(p.ChannelId, '') as ChannelID",
			"c.BotID",
			"COALESCE(c.RootPostID, '') as RootPostID",
			"(SELECT COUNT(*) FROM Posts WHERE Posts.RootId = c.RootPostID AND Posts.DeleteAt = 0) AS ReplyCount",
			"c.UpdatedAt as UpdateAt",
		).
		From("LLM_Conversations c").
		Join("Posts p ON p.Id = c.RootPostID AND p.DeleteAt = 0").
		Where(sq.Eq{"p.ChannelId": dmChannelIDs}).
		Where(sq.Eq{"c.DeleteAt": 0}).
		OrderBy("c.UpdatedAt DESC").
		Limit(60),
	); err != nil {
		return nil, fmt.Errorf("failed to get AI threads: %w", err)
	}

	return threads, nil
}
