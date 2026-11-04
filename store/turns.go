// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"
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
	// RunPostID is the response post whose run wrote this turn. Tool-round
	// turns carry it so a run's rows can be told from another run's in the
	// same conversation; it is nil for turns that belong to no run.
	RunPostID *string `json:"-" db:"runpostid"`
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
	"TokensIn", "TokensOut", "Sequence", "CreatedAt", "Status", "UpdatedAt", "RunPostID",
}

func turnStatusOrDefault(status string) string {
	if status == "" {
		return TurnStatusComplete
	}
	return status
}

// bumpConversationActivity advances the conversation's UpdatedAt (its last
// activity, which orders the conversation list) to the new turn's CreatedAt in
// the same statement as the insert; GREATEST keeps it from moving backwards.
// Arguments: the turn's CreatedAt, then its ConversationID.
const bumpConversationActivity = "WITH bumped_conversation AS (UPDATE LLM_Conversations SET UpdatedAt = GREATEST(UpdatedAt, ?) WHERE ID = ?)"

// insertTurn inserts a turn and bumps its conversation's activity. sequence and
// updatedAt are the turn's values or expressions for them.
func (s *Store) insertTurn(turn *Turn, sequence, updatedAt any) sq.InsertBuilder {
	return s.builder.Insert("LLM_Turns").
		Prefix(bumpConversationActivity, turn.CreatedAt, turn.ConversationID).
		Columns(turnColumns...).
		Values(turn.ID, turn.ConversationID, turn.PostID, turn.Role, string(turn.Content),
			turn.TokensIn, turn.TokensOut, sequence, turn.CreatedAt,
			turnStatusOrDefault(turn.Status), updatedAt, turn.RunPostID)
}

// CreateTurn inserts a new turn row and bumps its conversation's activity.
// The caller must set ID, ConversationID, Role, Content, Sequence, and CreatedAt before calling.
func (s *Store) CreateTurn(turn *Turn) error {
	return s.execBuilder(s.insertTurn(turn, turn.Sequence, turn.CreatedAt), "create turn")
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

// CompareAndSwapTurnContent replaces a turn's Content only while it still
// equals expected (compared as JSONB), and reports whether it did. A missing
// turn reports false.
func (s *Store) CompareAndSwapTurnContent(id string, expected, content json.RawMessage) (bool, error) {
	query, args, err := s.builder.
		Update("LLM_Turns").
		Set("Content", string(content)).
		Where(sq.Eq{"ID": id}).
		Where(sq.Expr("Content = ?::jsonb", string(expected))).
		ToSql()
	if err != nil {
		return false, fmt.Errorf("failed to build compare-and-swap turn content query: %w", err)
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return false, fmt.Errorf("failed to compare-and-swap turn content: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read compare-and-swap turn content result: %w", err)
	}
	return rows == 1, nil
}

// CompareAndSwapTurnContentAndStatus replaces a turn's Content and Status only
// while both still equal the expected values (Content compared as JSONB),
// bumps UpdatedAt to the DB clock, and reports whether it did. A missing turn
// reports false.
func (s *Store) CompareAndSwapTurnContentAndStatus(id string, expectedContent json.RawMessage, expectedStatus string, content json.RawMessage, status string) (bool, error) {
	return s.execConditional(s.builder.
		Update("LLM_Turns").
		Set("Content", string(content)).
		Set("Status", status).
		Set("UpdatedAt", sq.Expr(dbNowMillis)).
		Where(sq.Eq{"ID": id, "Status": expectedStatus}).
		Where(sq.Expr("Content = ?::jsonb", string(expectedContent))),
		"compare-and-swap turn content and status")
}

// TouchStreamingTurn bumps a streaming turn's UpdatedAt to the DB clock
// without changing its content, and reports whether the turn is still
// streaming (false once e.g. a recovery sweep marked it interrupted).
func (s *Store) TouchStreamingTurn(id string) (bool, error) {
	return s.execConditional(s.builder.
		Update("LLM_Turns").
		Set("UpdatedAt", sq.Expr(dbNowMillis)).
		Where(sq.Eq{"ID": id, "Status": TurnStatusStreaming}),
		"touch streaming turn")
}

// TransitionTurnStatus moves a turn from status from to status to, bumping
// UpdatedAt to the DB clock, and reports whether it did. Content is unchanged.
func (s *Store) TransitionTurnStatus(id, from, to string) (bool, error) {
	return s.execConditional(s.builder.
		Update("LLM_Turns").
		Set("Status", to).
		Set("UpdatedAt", sq.Expr(dbNowMillis)).
		Where(sq.Eq{"ID": id, "Status": from}),
		"transition turn status")
}

// execConditional runs a single-row conditional update and reports whether
// it matched.
func (s *Store) execConditional(builder sq.UpdateBuilder, op string) (bool, error) {
	query, args, err := builder.ToSql()
	if err != nil {
		return false, fmt.Errorf("failed to build %s query: %w", op, err)
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return false, fmt.Errorf("failed to %s: %w", op, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read %s result: %w", op, err)
	}
	return rows == 1, nil
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
// Sequence value via a subquery, and records it as the conversation's latest
// activity (see bumpConversationActivity). On success the assigned sequence is
// written back into turn.Sequence.
//
// Under PostgreSQL READ COMMITTED, two concurrent inserts for the same
// conversation can read the same MAX(Sequence) before either commits.
// The UNIQUE index on (ConversationID, Sequence) catches this, and the
// method retries (up to 3 times) so the second writer succeeds.
func (s *Store) CreateTurnAutoSequence(turn *Turn) error {
	nextSequence := sq.Expr("COALESCE((SELECT MAX(Sequence) FROM LLM_Turns WHERE ConversationID = ?), 0) + 1", turn.ConversationID)
	query, args, err := s.insertTurn(turn, nextSequence, sq.Expr(dbNowMillis)).Suffix("RETURNING Sequence, UpdatedAt").ToSql()
	if err != nil {
		return fmt.Errorf("failed to build create turn query: %w", err)
	}

	var lastErr error
	for range maxAutoSequenceRetries {
		var seq int
		var updatedAt int64
		lastErr = s.db.QueryRow(query, args...).Scan(&seq, &updatedAt)
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

// turnSpanRow is a turn read to decide which run it belongs to.
type turnSpanRow struct {
	ID        string  `db:"id"`
	Sequence  int     `db:"sequence"`
	Role      string  `db:"role"`
	PostID    *string `db:"postid"`
	RunPostID *string `db:"runpostid"`
}

// runSpanEnd returns how many of the turns after a streaming anchor the
// anchor must move past to end its run: up to the last turn written by its
// run (RunPostID), within the window that ends at the next user turn. Other
// runs' turns inside that window, including their anchors, are skipped, so the
// run's own later rounds still come before the anchor.
func runSpanEnd(later []turnSpanRow, postID *string) int {
	if postID == nil {
		return 0
	}
	end := 0
	for i, turn := range later {
		if turn.Role == "user" {
			break
		}
		if turn.RunPostID != nil && *turn.RunPostID == *postID {
			end = i + 1
		}
	}
	return end
}

// DeleteResponseTurns removes the post's anchor turn and every turn its run
// wrote (RunPostID), including rounds written after a later user turn arrived
// while the run was still executing tools. Turns of other runs are kept. Turns
// written before runs were recorded have no RunPostID and are treated as this
// response's when they sit between its anchor and the originating user turn.
// Callers must build any completion request before calling this.
func (s *Store) DeleteResponseTurns(conversationID, postID string) (err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin delete response turns: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var span []turnSpanRow
	if err = tx.Select(&span, `
SELECT ID, Sequence, Role, PostID, RunPostID FROM LLM_Turns
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
  )
ORDER BY Sequence ASC`, conversationID, postID); err != nil {
		return fmt.Errorf("failed to read response turns: %w", err)
	}

	var ids []string
	for _, turn := range span {
		if turn.Role == "user" {
			continue
		}
		switch {
		case turn.RunPostID != nil:
			if *turn.RunPostID == postID {
				ids = append(ids, turn.ID)
			}
		case turn.PostID == nil || *turn.PostID == postID:
			ids = append(ids, turn.ID)
		}
	}
	if len(ids) > 0 {
		if _, err = tx.Exec(`DELETE FROM LLM_Turns WHERE ConversationID = $1 AND ID = ANY($2)`, conversationID, pq.Array(ids)); err != nil {
			return fmt.Errorf("failed to delete response turns: %w", err)
		}
	}
	if _, err = tx.Exec(`DELETE FROM LLM_Turns WHERE ConversationID = $1 AND RunPostID = $2`, conversationID, postID); err != nil {
		return fmt.Errorf("failed to delete run turns: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit delete response turns: %w", err)
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
// and bumps UpdatedAt to the DB clock. It reports false, writing nothing, once
// the turn has left the streaming state (e.g. a sweep marked it interrupted).
func (s *Store) HeartbeatStreamingTurn(id string, content json.RawMessage) (bool, error) {
	query, args, err := s.builder.
		Update("LLM_Turns").
		Set("Content", string(content)).
		Set("UpdatedAt", sq.Expr(dbNowMillis)).
		Where(sq.Eq{"ID": id, "Status": TurnStatusStreaming}).
		ToSql()
	if err != nil {
		return false, fmt.Errorf("failed to build heartbeat streaming turn query: %w", err)
	}
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return false, fmt.Errorf("failed to heartbeat streaming turn: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read heartbeat rows: %w", err)
	}
	return n == 1, nil
}

// FinalizeStreamingTurn writes the final content, token usage and status of
// a turn created in the streaming state, moving it to the end of its run (see
// settleStreamingTurn). It is a no-op once a recovery sweep has claimed the
// turn, so a stalled stream that resumes cannot overwrite the sweep's
// interrupted verdict, and then reports false.
func (s *Store) FinalizeStreamingTurn(id string, content json.RawMessage, tokensIn, tokensOut int64, status string) (bool, error) {
	settled, err := s.settleStreamingTurn(id, 0, `
UPDATE LLM_Turns
SET Content = $2, TokensIn = $3, TokensOut = $4, Status = $5, UpdatedAt = `+dbNowMillis+`
WHERE ID = $1`, id, string(content), tokensIn, tokensOut, status)
	if err != nil {
		return false, fmt.Errorf("failed to finalize streaming turn: %w", err)
	}
	return settled, nil
}

// settleStreamingTurn runs update on turn id if it is still streaming (and,
// when staleAfterMillis > 0, its heartbeat is older than that by the DB
// clock), and reports whether it did. The turn reserved its sequence at stream
// start, so it is first moved after the tool rounds its run persisted since,
// but never past a later user turn: a follow-up posted meanwhile keeps its
// place in history.
func (s *Store) settleStreamingTurn(id string, staleAfterMillis int64, update string, args ...any) (settled bool, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil || !settled {
			_ = tx.Rollback()
		}
	}()

	// Lock the conversation before the turn, the same order a turn insert
	// takes them. One settle at a time per conversation: each reorders
	// sequences, including other streaming turns, and parks its turn at
	// MIN(Sequence)-1.
	var conversationID string
	if err = tx.Get(&conversationID, `SELECT ConversationID FROM LLM_Turns WHERE ID = $1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read streaming turn: %w", err)
	}
	if _, err = tx.Exec(`SELECT 1 FROM LLM_Conversations WHERE ID = $1 FOR UPDATE`, conversationID); err != nil {
		return false, fmt.Errorf("failed to lock conversation: %w", err)
	}

	var anchor struct {
		ConversationID string  `db:"conversationid"`
		Sequence       int     `db:"sequence"`
		PostID         *string `db:"postid"`
	}
	lockQuery := `SELECT ConversationID, Sequence, PostID FROM LLM_Turns WHERE ID = $1 AND Status = $2`
	lockArgs := []any{id, TurnStatusStreaming}
	if staleAfterMillis > 0 {
		lockQuery += ` AND UpdatedAt < ` + dbNowMillis + ` - $3`
		lockArgs = append(lockArgs, staleAfterMillis)
	}
	if err = tx.Get(&anchor, lockQuery+` FOR UPDATE`, lockArgs...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to lock streaming turn: %w", err)
	}

	var later []turnSpanRow
	if err = tx.Select(&later, `SELECT ID, Sequence, Role, PostID, RunPostID FROM LLM_Turns WHERE ConversationID = $1 AND Sequence > $2 ORDER BY Sequence ASC`,
		anchor.ConversationID, anchor.Sequence); err != nil {
		return false, fmt.Errorf("failed to read later turns: %w", err)
	}
	runEnd := runSpanEnd(later, anchor.PostID)

	if runEnd > 0 {
		// Rotate: park the anchor, shift each run turn into the slot freed
		// before it, then drop the anchor into the last freed slot.
		if _, err = tx.Exec(`UPDATE LLM_Turns SET Sequence = (SELECT MIN(Sequence) FROM LLM_Turns WHERE ConversationID = $1) - 1 WHERE ID = $2`,
			anchor.ConversationID, id); err != nil {
			return false, fmt.Errorf("failed to park streaming turn: %w", err)
		}
		free := anchor.Sequence
		for _, turn := range later[:runEnd] {
			if _, err = tx.Exec(`UPDATE LLM_Turns SET Sequence = $1 WHERE ID = $2`, free, turn.ID); err != nil {
				return false, fmt.Errorf("failed to reorder run turn: %w", err)
			}
			free = turn.Sequence
		}
		if _, err = tx.Exec(`UPDATE LLM_Turns SET Sequence = $1 WHERE ID = $2`, free, id); err != nil {
			return false, fmt.Errorf("failed to move streaming turn: %w", err)
		}
	}

	if _, err = tx.Exec(update, args...); err != nil {
		return false, fmt.Errorf("failed to update streaming turn: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit: %w", err)
	}
	return true, nil
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

// HasLiveStreamingTurn reports whether postID has a streaming turn whose last
// heartbeat is newer than staleAfterMillis by the DB clock, i.e. a stream that
// is still running on some node.
func (s *Store) HasLiveStreamingTurn(postID string, staleAfterMillis int64) (bool, error) {
	var live bool
	if err := s.db.Get(&live, `SELECT EXISTS (
  SELECT 1 FROM LLM_Turns
  WHERE PostID = $1 AND Status = $2 AND UpdatedAt >= `+dbNowMillis+` - $3
)`, postID, TurnStatusStreaming, staleAfterMillis); err != nil {
		return false, fmt.Errorf("failed to check live streaming turn: %w", err)
	}
	return live, nil
}

// ClaimStaleStreamingTurn atomically moves a stale streaming turn to the
// interrupted state, at the end of its run (see settleStreamingTurn). It
// returns true only for the single caller whose update won, so concurrent
// sweeps on several HA nodes never both repair the same post. A turn that
// heartbeated since it was read is not claimed.
func (s *Store) ClaimStaleStreamingTurn(id string, staleAfterMillis int64) (bool, error) {
	claimed, err := s.settleStreamingTurn(id, staleAfterMillis,
		`UPDATE LLM_Turns SET Status = $2, UpdatedAt = `+dbNowMillis+` WHERE ID = $1`, id, TurnStatusInterrupted)
	if err != nil {
		return false, fmt.Errorf("failed to claim stale streaming turn: %w", err)
	}
	return claimed, nil
}

// ReleaseStreamingTurnClaim returns a claimed turn to the streaming state with
// a stale heartbeat (the claim refreshed UpdatedAt), so a later sweep or
// regenerate can retry the repair at once instead of seeing a live stream.
func (s *Store) ReleaseStreamingTurnClaim(id string) error {
	query := `UPDATE LLM_Turns SET Status = $2, UpdatedAt = 0 WHERE ID = $1 AND Status = $3`
	if _, err := s.db.Exec(query, id, TurnStatusStreaming, TurnStatusInterrupted); err != nil {
		return fmt.Errorf("failed to release streaming turn claim: %w", err)
	}
	return nil
}
