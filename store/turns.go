// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
)

// Turn represents a single turn in a conversation stored in LLM_Turns.
type Turn struct {
	ID             string          `json:"id"              db:"id"`
	ConversationID string          `json:"conversation_id" db:"conversationid"`
	PostID         *string         `json:"post_id"         db:"postid"`
	Role           string          `json:"role"            db:"role"`
	Content        json.RawMessage `json:"content"         db:"content"`
	TokensIn       int64           `json:"tokens_in"       db:"tokensin"`
	TokensOut      int64           `json:"tokens_out"      db:"tokensout"`
	Sequence       int             `json:"sequence"        db:"sequence"`
	CreatedAt      int64           `json:"created_at"      db:"createdat"`
}

var turnColumns = []string{
	"ID", "ConversationID", "PostID", "Role", "Content",
	"TokensIn", "TokensOut", "Sequence", "CreatedAt",
}

// CreateTurn inserts a new turn row.
// The caller must set ID, ConversationID, Role, Content, Sequence, and CreatedAt before calling.
func (s *Store) CreateTurn(turn *Turn) error {
	query, args, err := s.builder.Insert("LLM_Turns").
		Columns(turnColumns...).
		Values(turn.ID, turn.ConversationID, turn.PostID, turn.Role, string(turn.Content),
			turn.TokensIn, turn.TokensOut, turn.Sequence, turn.CreatedAt).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build create turn query: %w", err)
	}
	_, err = s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to create turn: %w", err)
	}
	return nil
}

// GetTurnsForConversation retrieves all turns for a conversation ordered by Sequence ascending.
// Returns an empty slice (not nil) if no turns exist.
func (s *Store) GetTurnsForConversation(conversationID string) ([]Turn, error) {
	query, args, err := s.builder.
		Select(turnColumns...).
		From("LLM_Turns").
		Where(sq.Eq{"ConversationID": conversationID}).
		OrderBy("Sequence ASC").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build get turns query: %w", err)
	}
	var turns []Turn
	if err := s.db.Select(&turns, query, args...); err != nil {
		return nil, fmt.Errorf("failed to get turns for conversation: %w", err)
	}
	if turns == nil {
		turns = []Turn{}
	}
	return turns, nil
}

// UpdateTurnContent replaces the Content JSONB column for a specific turn.
func (s *Store) UpdateTurnContent(id string, content json.RawMessage) error {
	query, args, err := s.builder.
		Update("LLM_Turns").
		Set("Content", string(content)).
		Where(sq.Eq{"ID": id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build update turn content query: %w", err)
	}
	_, err = s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update turn content: %w", err)
	}
	return nil
}

// GetMaxSequenceForConversation returns the maximum sequence number for turns in the
// given conversation, or 0 if no turns exist.
func (s *Store) GetMaxSequenceForConversation(conversationID string) (int, error) {
	query, args, err := s.builder.
		Select("COALESCE(MAX(Sequence), 0)").
		From("LLM_Turns").
		Where(sq.Eq{"ConversationID": conversationID}).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to build max sequence query: %w", err)
	}
	var maxSeq int
	if err := s.db.Get(&maxSeq, query, args...); err != nil {
		return 0, fmt.Errorf("failed to get max sequence: %w", err)
	}
	return maxSeq, nil
}

// GetTurnByPostID retrieves a turn by its PostID.
// Returns nil, nil if no turn with the given PostID exists.
func (s *Store) GetTurnByPostID(postID string) (*Turn, error) {
	query, args, err := s.builder.
		Select(turnColumns...).
		From("LLM_Turns").
		Where(sq.Eq{"PostID": postID}).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build get turn by post ID query: %w", err)
	}
	var turn Turn
	if err := s.db.Get(&turn, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get turn by post ID: %w", err)
	}
	return &turn, nil
}

// maxAutoSequenceRetries is the number of times CreateTurnAutoSequence will
// retry on a unique-constraint violation for (ConversationID, Sequence).
const maxAutoSequenceRetries = 3

// CreateTurnAutoSequence inserts a new turn, atomically computing the next
// Sequence value via a subquery. On success the assigned sequence is written
// back into turn.Sequence.
//
// Under PostgreSQL READ COMMITTED, two concurrent inserts for the same
// conversation can read the same MAX(Sequence) before either commits.
// The UNIQUE index on (ConversationID, Sequence) catches this, and the
// method retries (up to 3 times) so the second writer succeeds.
func (s *Store) CreateTurnAutoSequence(turn *Turn) error {
	const query = `
INSERT INTO LLM_Turns (ID, ConversationID, PostID, Role, Content, TokensIn, TokensOut, Sequence, CreatedAt)
VALUES ($1, $2, $3, $4, $5, $6, $7,
        COALESCE((SELECT MAX(Sequence) FROM LLM_Turns WHERE ConversationID = $2), 0) + 1,
        $8)
RETURNING Sequence`

	var lastErr error
	for attempt := 0; attempt < maxAutoSequenceRetries; attempt++ {
		var seq int
		lastErr = s.db.QueryRow(query,
			turn.ID, turn.ConversationID, turn.PostID, turn.Role,
			string(turn.Content), turn.TokensIn, turn.TokensOut, turn.CreatedAt,
		).Scan(&seq)
		if lastErr == nil {
			turn.Sequence = seq
			return nil
		}
		if !isUniqueViolation(lastErr) {
			return fmt.Errorf("failed to create turn: %w", lastErr)
		}
		// Unique violation on (ConversationID, Sequence): retry so the
		// subquery picks up the newly committed row.
	}
	return fmt.Errorf("failed to create turn after %d retries: %w", maxAutoSequenceRetries, lastErr)
}

// UpdateTurnTokens updates the TokensIn and TokensOut fields on a turn.
func (s *Store) UpdateTurnTokens(id string, tokensIn, tokensOut int64) error {
	query, args, err := s.builder.
		Update("LLM_Turns").
		Set("TokensIn", tokensIn).
		Set("TokensOut", tokensOut).
		Where(sq.Eq{"ID": id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build update turn tokens query: %w", err)
	}
	_, err = s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update turn tokens: %w", err)
	}
	return nil
}
