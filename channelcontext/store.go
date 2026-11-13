// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelcontext

import (
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
)

// Instructions is one channel's agent instructions. A channel with no row has
// no instructions.
type Instructions struct {
	ChannelID    string `db:"channelid"`
	Instructions string `db:"instructions"`
	UpdatedBy    string `db:"updatedby"`
	UpdateAt     int64  `db:"updateat"`
}

// Pin records one post pinned to a channel's agent context.
type Pin struct {
	ChannelID string `db:"channelid"`
	PostID    string `db:"postid"`
	PinnedBy  string `db:"pinnedby"`
	PinnedAt  int64  `db:"pinnedat"`
}

// Store provides access to the Agents_ChannelInstructions and
// Agents_ChannelContextPosts tables. It persists rows exactly as given;
// validation and timestamps are the Service's job.
type Store struct {
	db *mmapi.DBClient
}

// NewStore creates a new channel context store.
func NewStore(db *mmapi.DBClient) *Store {
	return &Store{db: db}
}

// GetInstructions returns the channel's instructions, or (nil, nil) when the
// channel has none.
func (s *Store) GetInstructions(channelID string) (*Instructions, error) {
	var rows []Instructions
	if err := s.db.DoQuery(&rows, s.db.Builder().
		Select("ChannelID", "Instructions", "UpdatedBy", "UpdateAt").
		From("Agents_ChannelInstructions").
		Where(sq.Eq{"ChannelID": channelID}),
	); err != nil {
		return nil, fmt.Errorf("failed to get channel instructions: %w", err)
	}

	if len(rows) == 0 {
		return nil, nil
	}

	return &rows[0], nil
}

// SetInstructions upserts the instructions for instructions.ChannelID.
func (s *Store) SetInstructions(instructions Instructions) error {
	_, err := s.db.ExecBuilder(s.db.Builder().
		Insert("Agents_ChannelInstructions").
		Columns("ChannelID", "Instructions", "UpdatedBy", "UpdateAt").
		Values(instructions.ChannelID, instructions.Instructions, instructions.UpdatedBy, instructions.UpdateAt).
		Suffix("ON CONFLICT (ChannelID) DO UPDATE SET Instructions = EXCLUDED.Instructions, UpdatedBy = EXCLUDED.UpdatedBy, UpdateAt = EXCLUDED.UpdateAt"))
	if err != nil {
		return fmt.Errorf("failed to set channel instructions: %w", err)
	}

	return nil
}

// DeleteInstructions removes the channel's instructions. Deleting a channel
// with no row is a no-op, not an error.
func (s *Store) DeleteInstructions(channelID string) error {
	_, err := s.db.ExecBuilder(s.db.Builder().
		Delete("Agents_ChannelInstructions").
		Where(sq.Eq{"ChannelID": channelID}))
	if err != nil {
		return fmt.Errorf("failed to delete channel instructions: %w", err)
	}

	return nil
}

// ListPins returns the channel's pins, oldest first.
func (s *Store) ListPins(channelID string) ([]Pin, error) {
	var pins []Pin
	if err := s.db.DoQuery(&pins, s.db.Builder().
		Select("ChannelID", "PostID", "PinnedBy", "PinnedAt").
		From("Agents_ChannelContextPosts").
		Where(sq.Eq{"ChannelID": channelID}).
		OrderBy("PinnedAt ASC", "PostID ASC"),
	); err != nil {
		return nil, fmt.Errorf("failed to list channel context pins: %w", err)
	}

	return pins, nil
}

// AddPin inserts pin unless the post is already pinned or the channel already
// has limit pins. added is false when nothing was inserted, for either reason.
func (s *Store) AddPin(pin Pin, limit int) (added bool, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return false, fmt.Errorf("failed to begin channel context pin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Concurrent pins in one channel must not each count below the limit and
	// all insert, so serialize them on a per-channel transaction lock.
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock(hashtext('Agents_ChannelContextPosts'), hashtext($1))", pin.ChannelID); err != nil {
		return false, fmt.Errorf("failed to lock channel context pins: %w", err)
	}

	var count int
	if err = tx.Get(&count, "SELECT COUNT(*) FROM Agents_ChannelContextPosts WHERE ChannelID = $1", pin.ChannelID); err != nil {
		return false, fmt.Errorf("failed to count channel context pins: %w", err)
	}
	if count >= limit {
		return false, nil
	}

	result, err := tx.Exec(
		"INSERT INTO Agents_ChannelContextPosts (ChannelID, PostID, PinnedBy, PinnedAt) VALUES ($1, $2, $3, $4) ON CONFLICT (ChannelID, PostID) DO NOTHING",
		pin.ChannelID, pin.PostID, pin.PinnedBy, pin.PinnedAt,
	)
	if err != nil {
		return false, fmt.Errorf("failed to add channel context pin: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read channel context pin result: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit channel context pin: %w", err)
	}

	return rows > 0, nil
}

// HasPin reports whether the post is pinned to the channel's agent context.
func (s *Store) HasPin(channelID, postID string) (bool, error) {
	var pins []Pin
	if err := s.db.DoQuery(&pins, s.db.Builder().
		Select("ChannelID", "PostID", "PinnedBy", "PinnedAt").
		From("Agents_ChannelContextPosts").
		Where(sq.Eq{"ChannelID": channelID, "PostID": postID}),
	); err != nil {
		return false, fmt.Errorf("failed to look up channel context pin: %w", err)
	}

	return len(pins) > 0, nil
}

// RemovePin unpins the post. Removing a pin that does not exist is a no-op,
// not an error.
func (s *Store) RemovePin(channelID, postID string) error {
	_, err := s.db.ExecBuilder(s.db.Builder().
		Delete("Agents_ChannelContextPosts").
		Where(sq.Eq{"ChannelID": channelID, "PostID": postID}))
	if err != nil {
		return fmt.Errorf("failed to remove channel context pin: %w", err)
	}

	return nil
}
