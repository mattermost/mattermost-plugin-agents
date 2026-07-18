// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"context"
	"fmt"
	"time"
)

// DailyUsageDelta is one recorded usage increment attributed to a (day, user, bot) cell.
type DailyUsageDelta struct {
	Day          time.Time // UTC calendar date; store truncates to date
	UserID       string    // "" when no human requesting user
	BotID        string    // agent bot user ID; "" when unknown
	IsGuest      bool
	IsBot        bool
	InputTokens  int64
	OutputTokens int64
	Cost         float64
}

// dayString converts a wall-clock time to the UTC calendar date string used
// for the LLM_Usage_Daily.Day DATE column.
func dayString(t time.Time) string {
	return t.UTC().Format(time.DateOnly)
}

// IncrementDailyUsage upserts one aggregate row. Token and cost increments are
// additive; IsGuest/IsBot are overwritten with the latest snapshot (last write
// wins within a day).
func (s *Store) IncrementDailyUsage(ctx context.Context, delta DailyUsageDelta) error {
	const query = `
INSERT INTO LLM_Usage_Daily (Day, UserID, BotID, IsGuest, IsBot, InputTokens, OutputTokens, Cost)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (Day, UserID, BotID) DO UPDATE SET
    InputTokens  = LLM_Usage_Daily.InputTokens  + EXCLUDED.InputTokens,
    OutputTokens = LLM_Usage_Daily.OutputTokens + EXCLUDED.OutputTokens,
    Cost         = LLM_Usage_Daily.Cost         + EXCLUDED.Cost,
    IsGuest      = EXCLUDED.IsGuest,
    IsBot        = EXCLUDED.IsBot`
	_, err := s.db.ExecContext(ctx, query,
		dayString(delta.Day), delta.UserID, delta.BotID, delta.IsGuest, delta.IsBot,
		delta.InputTokens, delta.OutputTokens, delta.Cost)
	if err != nil {
		return fmt.Errorf("failed to increment daily usage: %w", err)
	}
	return nil
}

// GetActiveUserCount returns COUNT(DISTINCT UserID) for Day >= since,
// filtered IsGuest = FALSE AND IsBot = FALSE AND UserID <> "".
func (s *Store) GetActiveUserCount(ctx context.Context, since time.Time) (int64, error) {
	const query = `
SELECT COUNT(DISTINCT UserID)
FROM LLM_Usage_Daily
WHERE Day >= $1 AND IsGuest = FALSE AND IsBot = FALSE AND UserID <> ''`
	var count int64
	if err := s.db.GetContext(ctx, &count, query, dayString(since)); err != nil {
		return 0, fmt.Errorf("failed to get active user count: %w", err)
	}
	return count, nil
}

// BotActiveUsers is one per-agent MAU entry (display name resolved at the API layer, not stored).
type BotActiveUsers struct {
	BotID       string `db:"botid"`
	ActiveUsers int64  `db:"activeusers"`
}

// GetActiveUserCountPerBot returns per-bot distinct active users for Day >= since
// (same human-user filter as GetActiveUserCount), ordered by ActiveUsers DESC, BotID ASC.
// Empty BotID rows are intentionally included so the API/webapp can surface them as
// an "Unknown agent" slice rather than dropping unattributed usage.
func (s *Store) GetActiveUserCountPerBot(ctx context.Context, since time.Time) ([]BotActiveUsers, error) {
	const query = `
SELECT BotID AS botid, COUNT(DISTINCT UserID) AS activeusers
FROM LLM_Usage_Daily
WHERE Day >= $1 AND IsGuest = FALSE AND IsBot = FALSE AND UserID <> ''
GROUP BY BotID
ORDER BY activeusers DESC, botid ASC`
	var rows []BotActiveUsers
	if err := s.db.SelectContext(ctx, &rows, query, dayString(since)); err != nil {
		return nil, fmt.Errorf("failed to get active user count per bot: %w", err)
	}
	if rows == nil {
		rows = []BotActiveUsers{}
	}
	return rows, nil
}

// UsageTotals aggregates ALL rows (including guests and bot-originated usage).
type UsageTotals struct {
	InputTokens  int64   `db:"inputtokens"`
	OutputTokens int64   `db:"outputtokens"`
	Cost         float64 `db:"cost"`
}

// GetUsageTotals returns COALESCE'd sums for Day >= since.
func (s *Store) GetUsageTotals(ctx context.Context, since time.Time) (UsageTotals, error) {
	const query = `
SELECT COALESCE(SUM(InputTokens), 0)  AS inputtokens,
       COALESCE(SUM(OutputTokens), 0) AS outputtokens,
       COALESCE(SUM(Cost), 0)         AS cost
FROM LLM_Usage_Daily
WHERE Day >= $1`
	var totals UsageTotals
	if err := s.db.GetContext(ctx, &totals, query, dayString(since)); err != nil {
		return UsageTotals{}, fmt.Errorf("failed to get usage totals: %w", err)
	}
	return totals, nil
}

// DailyTokens is one point of the tokens-per-day series (all rows, incl. guests/bots).
// Day is "YYYY-MM-DD" (UTC). Selected as text so the Mattermost plugin DB RPC can
// encode the value (native DATE/time.Time is not gob-registered across the boundary).
type DailyTokens struct {
	Day          string `db:"day"`
	InputTokens  int64  `db:"inputtokens"`
	OutputTokens int64  `db:"outputtokens"`
}

// GetDailyTokenTotals returns SUM(...) GROUP BY Day for Day >= since, ORDER BY Day ASC.
// Days without rows are absent; the API layer zero-fills the window.
func (s *Store) GetDailyTokenTotals(ctx context.Context, since time.Time) ([]DailyTokens, error) {
	const query = `
SELECT to_char(Day, 'YYYY-MM-DD') AS day, SUM(InputTokens) AS inputtokens, SUM(OutputTokens) AS outputtokens
FROM LLM_Usage_Daily
WHERE Day >= $1
GROUP BY Day
ORDER BY Day ASC`
	var rows []DailyTokens
	if err := s.db.SelectContext(ctx, &rows, query, dayString(since)); err != nil {
		return nil, fmt.Errorf("failed to get daily token totals: %w", err)
	}
	if rows == nil {
		rows = []DailyTokens{}
	}
	return rows, nil
}

// DeleteUsageBefore deletes up to batchSize rows with Day < cutoff (date comparison, UTC)
// and returns the number of rows deleted.
func (s *Store) DeleteUsageBefore(ctx context.Context, cutoff time.Time, batchSize int64) (int64, error) {
	const query = `
DELETE FROM LLM_Usage_Daily
WHERE (Day, UserID, BotID) IN (
    SELECT Day, UserID, BotID FROM LLM_Usage_Daily WHERE Day < $1 LIMIT $2
)`
	result, err := s.db.ExecContext(ctx, query, dayString(cutoff), batchSize)
	if err != nil {
		return 0, fmt.Errorf("failed to delete usage rows: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count deleted usage rows: %w", err)
	}
	return deleted, nil
}
