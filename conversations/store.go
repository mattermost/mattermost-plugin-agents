// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
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

// DeleteConversationsForDeletedPost drops the text of the turn anchored to the
// given post and soft-deletes conversations associated with it. If the post is
// a root post, conversations keyed by that RootPostID are marked as deleted.
func (c *Conversations) DeleteConversationsForDeletedPost(post *model.Post) error {
	if post == nil || post.Id == "" {
		return nil
	}

	turn, err := c.turnAnchoredToPost(post.Id)
	if err != nil {
		return err
	}
	if turn != nil {
		if setErr := c.setTurnText(turn, ""); setErr != nil {
			return setErr
		}
	}

	if c.db == nil {
		return nil
	}
	now := model.GetMillis()
	_, err = c.db.ExecBuilder(c.db.Builder().
		Update("LLM_Conversations").
		Set("DeleteAt", now).
		Set("UpdatedAt", now).
		Where(sq.And{
			sq.Eq{"RootPostID": post.Id},
			sq.Eq{"DeleteAt": 0},
		}))
	return err
}

// UpdateTurnForEditedPost keeps the text of the user turn anchored to the
// given post in step with the post's message. Turns of other roles are
// written by the streaming layer from the same content as the post itself,
// so they are left as stored.
func (c *Conversations) UpdateTurnForEditedPost(post, previousPost *model.Post) error {
	if post == nil || post.Id == "" {
		return nil
	}
	if previousPost != nil && previousPost.Message == post.Message {
		return nil
	}

	turn, err := c.turnAnchoredToPost(post.Id)
	if err != nil {
		return err
	}
	if turn == nil || turn.Role != "user" {
		return nil
	}
	return c.setTurnText(turn, post.Message)
}

// turnAnchoredToPost returns the turn anchored to postID, or nil when there is
// none or turn storage is not configured.
func (c *Conversations) turnAnchoredToPost(postID string) (*store.Turn, error) {
	if c.convService == nil {
		return nil, nil
	}
	turn, err := c.convService.GetTurnByPostID(postID)
	if err != nil {
		return nil, fmt.Errorf("failed to get turn for post: %w", err)
	}
	return turn, nil
}

// setTurnText writes text as the whole text content of the turn, leaving its
// other content blocks as stored.
func (c *Conversations) setTurnText(turn *store.Turn, text string) error {
	blocks, err := conversation.UnmarshalBlocks(turn.Content)
	if err != nil {
		return fmt.Errorf("failed to unmarshal turn content: %w", err)
	}
	content, err := json.Marshal(conversation.WithTextContent(blocks, text))
	if err != nil {
		return fmt.Errorf("failed to marshal turn content: %w", err)
	}
	return c.convService.UpdateTurnContent(turn.ID, content)
}
