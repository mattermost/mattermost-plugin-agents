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
	// Status is TurnStatusStreaming while an assistant response is being
	// generated, TurnStatusComplete once it finished (including a user stop),
	// and TurnStatusInterrupted when generation died with the node or plugin.
	Status string `json:"status" db:"status"`
	// UpdatedAt is the DB-clock time (ms) of the last write. For streaming
	// turns it doubles as the heartbeat used to detect abandoned streams.
	UpdatedAt int64 `json:"updated_at" db:"updatedat"`
}

// Turn status values stored in LLM_Turns.Status.
const (
	TurnStatusStreaming   = "streaming"
	TurnStatusComplete    = "complete"
	TurnStatusInterrupted = "interrupted"
)

// dbNowMillis is the database clock in milliseconds. Heartbeats and staleness
// checks both use the DB clock so skew between HA nodes does not matter.
const dbNowMillis = "(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT"

var turnColumns = []string{
	"ID", "ConversationID", "PostID", "Role", "Content",
	"TokensIn", "TokensOut", "Sequence", "CreatedAt", "Status", "UpdatedAt",
}

func turnStatusOrDefault(status string) string {
	if status == "" {
		return TurnStatusComplete
	}
	return status
}

// CreateTurn inserts a new turn row.
// The caller must set ID, ConversationID, Role, Content, Sequence, and CreatedAt before calling.
func (s *Store) CreateTurn(turn *Turn) error {
	return s.execBuilder(s.builder.Insert("LLM_Turns").
		Columns(turnColumns...).
		Values(turn.ID, turn.ConversationID, turn.PostID, turn.Role, string(turn.Content),
			turn.TokensIn, turn.TokensOut, turn.Sequence, turn.CreatedAt,
			turnStatusOrDefault(turn.Status), turn.CreatedAt),
		"create turn")
}

// GetTurnsForConversation retrieves all turns for a conversation ordered by Sequence ascending.
// Returns an empty slice (not nil) if no turns exist.
func (s *Store) GetTurnsForConversation(conversationID string) ([]Turn, error) {
	var turns []Turn
	if err := s.selectBuilder(&turns, s.builder.
		Select(turnColumns...).
		From("LLM_Turns").
		Where(sq.Eq{"ConversationID": conversationID}).
		OrderBy("Sequence ASC"),
		"get turns for conversation"); err != nil {
		return nil, err
	}
	if turns == nil {
		turns = []Turn{}
	}
	return turns, nil
}

// UpdateTurnContent replaces the Content JSONB column for a specific turn.
func (s *Store) UpdateTurnContent(id string, content json.RawMessage) error {
	return s.execBuilder(s.builder.
		Update("LLM_Turns").
		Set("Content", string(content)).
		Where(sq.Eq{"ID": id}),
		"update turn content")
}

// GetMaxSequenceForConversation returns the maximum sequence number for turns in the
// given conversation, or 0 if no turns exist.
func (s *Store) GetMaxSequenceForConversation(conversationID string) (int, error) {
	var maxSeq int
	if err := s.getBuilder(&maxSeq, s.builder.
		Select("COALESCE(MAX(Sequence), 0)").
		From("LLM_Turns").
		Where(sq.Eq{"ConversationID": conversationID}),
		"get max sequence"); err != nil {
		return 0, err
	}
	return maxSeq, nil
}

// GetTurnByPostID retrieves a turn by its PostID.
// Returns nil, nil if no turn with the given PostID exists.
func (s *Store) GetTurnByPostID(postID string) (*Turn, error) {
	var turn Turn
	if err := s.getBuilder(&turn, s.builder.
		Select(turnColumns...).
		From("LLM_Turns").
		Where(sq.Eq{"PostID": postID}),
		"get turn by post ID"); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
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
INSERT INTO LLM_Turns (ID, ConversationID, PostID, Role, Content, TokensIn, TokensOut, Sequence, CreatedAt, Status, UpdatedAt)
VALUES ($1, $2, $3, $4, $5, $6, $7,
        COALESCE((SELECT MAX(Sequence) FROM LLM_Turns WHERE ConversationID = $2), 0) + 1,
        $8, $9, ` + dbNowMillis + `)
RETURNING Sequence, UpdatedAt`

	var lastErr error
	for range maxAutoSequenceRetries {
		var seq int
		var updatedAt int64
		lastErr = s.db.QueryRow(query,
			turn.ID, turn.ConversationID, turn.PostID, turn.Role,
			string(turn.Content), turn.TokensIn, turn.TokensOut, turn.CreatedAt,
			turnStatusOrDefault(turn.Status),
		).Scan(&seq, &updatedAt)
		if lastErr == nil {
			turn.Sequence = seq
			turn.Status = turnStatusOrDefault(turn.Status)
			turn.UpdatedAt = updatedAt
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

// UpdateTurnPostID sets or clears the PostID column for a turn. The webapp's
// anchor lookup expects at most one assistant turn per post_id.
func (s *Store) UpdateTurnPostID(id string, postID *string) error {
	return s.execBuilder(s.builder.
		Update("LLM_Turns").
		Set("PostID", postID).
		Where(sq.Eq{"ID": id}),
		"update turn post id")
}

// DeleteResponseTurns removes the post's anchor turn and any assistant or
// tool_result turns between it and the originating user turn. Callers must
// build any completion request before calling this.
func (s *Store) DeleteResponseTurns(conversationID, postID string) error {
	const query = `
DELETE FROM LLM_Turns
WHERE ConversationID = $1
  AND Sequence > COALESCE((
    SELECT MAX(Sequence) FROM LLM_Turns
    WHERE ConversationID = $1
      AND Role = 'user'
      AND Sequence < (
        SELECT Sequence FROM LLM_Turns
        WHERE ConversationID = $1 AND Role = 'assistant' AND PostID = $2
        LIMIT 1
      )
  ), 0)
  AND Sequence <= (
    SELECT Sequence FROM LLM_Turns
    WHERE ConversationID = $1 AND Role = 'assistant' AND PostID = $2
    LIMIT 1
  )`
	if _, err := s.db.Exec(query, conversationID, postID); err != nil {
		return fmt.Errorf("failed to delete response turns: %w", err)
	}
	return nil
}

// UpdateTurnTokens updates the TokensIn and TokensOut fields on a turn.
func (s *Store) UpdateTurnTokens(id string, tokensIn, tokensOut int64) error {
	return s.execBuilder(s.builder.
		Update("LLM_Turns").
		Set("TokensIn", tokensIn).
		Set("TokensOut", tokensOut).
		Where(sq.Eq{"ID": id}),
		"update turn tokens")
}

// HeartbeatStreamingTurn records the partial content of an in-progress turn
// and bumps UpdatedAt to the DB clock. It is a no-op once the turn has left
// the streaming state (e.g. a sweep already marked it interrupted).
func (s *Store) HeartbeatStreamingTurn(id string, content json.RawMessage) error {
	return s.execBuilder(s.builder.
		Update("LLM_Turns").
		Set("Content", string(content)).
		Set("UpdatedAt", sq.Expr(dbNowMillis)).
		Where(sq.Eq{"ID": id, "Status": TurnStatusStreaming}),
		"heartbeat streaming turn")
}

// FinalizeStreamingTurn writes the final content, token usage and status of
// a turn created in the streaming state. If other turns (e.g. tool rounds
// persisted by the tool runner) were appended to the conversation after the
// turn reserved its sequence at stream start, the turn is moved to the end so
// it stays the last turn of its run. It is a no-op once a recovery sweep has
// claimed the turn, so a stalled stream that resumes cannot overwrite the
// sweep's interrupted verdict.
func (s *Store) FinalizeStreamingTurn(id string, content json.RawMessage, tokensIn, tokensOut int64, status string) error {
	const query = `
UPDATE LLM_Turns AS t
SET Content = $2,
    TokensIn = $3,
    TokensOut = $4,
    Status = $5,
    UpdatedAt = ` + dbNowMillis + `,
    Sequence = CASE
        WHEN t.Sequence < (SELECT MAX(o.Sequence) FROM LLM_Turns o WHERE o.ConversationID = t.ConversationID)
        THEN (SELECT MAX(o.Sequence) FROM LLM_Turns o WHERE o.ConversationID = t.ConversationID) + 1
        ELSE t.Sequence
    END
WHERE t.ID = $1
  AND t.Status = $6`

	var lastErr error
	for range maxAutoSequenceRetries {
		_, lastErr = s.db.Exec(query, id, string(content), tokensIn, tokensOut, status, TurnStatusStreaming)
		if lastErr == nil {
			return nil
		}
		if !isUniqueViolation(lastErr) {
			return fmt.Errorf("failed to finalize streaming turn: %w", lastErr)
		}
	}
	return fmt.Errorf("failed to finalize streaming turn after %d retries: %w", maxAutoSequenceRetries, lastErr)
}

// GetStaleStreamingTurns returns up to limit turns still in the streaming
// state whose last heartbeat is older than staleAfterMillis by the DB clock.
func (s *Store) GetStaleStreamingTurns(staleAfterMillis int64, limit uint64) ([]Turn, error) {
	var turns []Turn
	if err := s.selectBuilder(&turns, s.builder.
		Select(turnColumns...).
		From("LLM_Turns").
		Where(sq.Eq{"Status": TurnStatusStreaming}).
		Where(sq.Expr("UpdatedAt < "+dbNowMillis+" - ?", staleAfterMillis)).
		OrderBy("UpdatedAt ASC").
		Limit(limit),
		"get stale streaming turns"); err != nil {
		return nil, err
	}
	if turns == nil {
		turns = []Turn{}
	}
	return turns, nil
}

// ClaimStaleStreamingTurn atomically moves a stale streaming turn to the
// interrupted state. It returns true only for the single caller whose update
// won, so concurrent sweeps on several HA nodes never both repair the same
// post. A turn that heartbeated since it was read is not claimed.
func (s *Store) ClaimStaleStreamingTurn(id string, staleAfterMillis int64) (bool, error) {
	// Like FinalizeStreamingTurn, move the turn after any tool-round turns
	// persisted while it streamed so it stays the last turn of its response.
	query := `
UPDATE LLM_Turns AS t
SET Status = $2,
    UpdatedAt = ` + dbNowMillis + `,
    Sequence = CASE
        WHEN t.Sequence < (SELECT MAX(o.Sequence) FROM LLM_Turns o WHERE o.ConversationID = t.ConversationID)
        THEN (SELECT MAX(o.Sequence) FROM LLM_Turns o WHERE o.ConversationID = t.ConversationID) + 1
        ELSE t.Sequence
    END
WHERE t.ID = $1
  AND t.Status = $3
  AND t.UpdatedAt < ` + dbNowMillis + ` - $4`

	var lastErr error
	for range maxAutoSequenceRetries {
		res, err := s.db.Exec(query, id, TurnStatusInterrupted, TurnStatusStreaming, staleAfterMillis)
		if err != nil {
			lastErr = err
			if isUniqueViolation(err) {
				continue
			}
			return false, fmt.Errorf("failed to claim stale streaming turn: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("failed to read claimed rows: %w", err)
		}
		return n == 1, nil
	}
	return false, fmt.Errorf("failed to claim stale streaming turn after %d retries: %w", maxAutoSequenceRetries, lastErr)
}

// ReleaseStreamingTurnClaim returns a claimed turn to the streaming state
// without refreshing its heartbeat, so a later sweep can retry the repair.
func (s *Store) ReleaseStreamingTurnClaim(id string) error {
	query := `UPDATE LLM_Turns SET Status = $2 WHERE ID = $1 AND Status = $3`
	if _, err := s.db.Exec(query, id, TurnStatusStreaming, TurnStatusInterrupted); err != nil {
		return fmt.Errorf("failed to release streaming turn claim: %w", err)
	}
	return nil
}
