// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"encoding/json"
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
		Values(turn.ID, turn.ConversationID, turn.PostID, turn.Role, turn.Content,
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
		Set("Content", content).
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
