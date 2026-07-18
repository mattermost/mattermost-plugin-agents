// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// day returns a UTC calendar date.
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// seedUsage inserts one delta, failing the test on error.
func seedUsage(t *testing.T, s *Store, delta DailyUsageDelta) {
	t.Helper()
	require.NoError(t, s.IncrementDailyUsage(context.Background(), delta))
}

type usageRow struct {
	UserID       string  `db:"userid"`
	BotID        string  `db:"botid"`
	IsGuest      bool    `db:"isguest"`
	IsBot        bool    `db:"isbot"`
	InputTokens  int64   `db:"inputtokens"`
	OutputTokens int64   `db:"outputtokens"`
	Cost         float64 `db:"cost"`
}

func readUsageRows(t *testing.T, s *Store) []usageRow {
	t.Helper()
	var rows []usageRow
	err := s.db.Select(&rows, `
SELECT UserID AS userid, BotID AS botid, IsGuest AS isguest, IsBot AS isbot,
       InputTokens AS inputtokens, OutputTokens AS outputtokens, Cost AS cost
FROM LLM_Usage_Daily
ORDER BY UserID, BotID`)
	require.NoError(t, err)
	if rows == nil {
		rows = []usageRow{}
	}
	return rows
}

func TestIncrementDailyUsage(t *testing.T) {
	tests := []struct {
		name     string
		deltas   []DailyUsageDelta
		wantRows []usageRow
	}{
		{
			name: "insert creates row",
			deltas: []DailyUsageDelta{{
				Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1",
				InputTokens: 10, OutputTokens: 5, Cost: 0.01,
			}},
			wantRows: []usageRow{{
				UserID: "u1", BotID: "b1", InputTokens: 10, OutputTokens: 5, Cost: 0.01,
			}},
		},
		{
			name: "second delta same cell increments additively",
			deltas: []DailyUsageDelta{
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", InputTokens: 10, OutputTokens: 5, Cost: 0.01},
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", InputTokens: 2, OutputTokens: 3, Cost: 0.02},
			},
			wantRows: []usageRow{{
				UserID: "u1", BotID: "b1", InputTokens: 12, OutputTokens: 8, Cost: 0.03,
			}},
		},
		{
			name: "snapshot flags: last write wins",
			deltas: []DailyUsageDelta{
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", IsGuest: false, IsBot: false, InputTokens: 1},
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", IsGuest: true, IsBot: true, InputTokens: 1},
			},
			wantRows: []usageRow{{
				UserID: "u1", BotID: "b1", IsGuest: true, IsBot: true, InputTokens: 2,
			}},
		},
		{
			name: "different bot same user same day → separate rows",
			deltas: []DailyUsageDelta{
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", InputTokens: 1},
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b2", InputTokens: 2},
			},
			wantRows: []usageRow{
				{UserID: "u1", BotID: "b1", InputTokens: 1},
				{UserID: "u1", BotID: "b2", InputTokens: 2},
			},
		},
		{
			name: "time-of-day truncated to date",
			deltas: []DailyUsageDelta{
				{Day: time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC), UserID: "u1", BotID: "b1", InputTokens: 1},
				{Day: time.Date(2026, time.July, 10, 23, 59, 0, 0, time.UTC), UserID: "u1", BotID: "b1", InputTokens: 2},
			},
			wantRows: []usageRow{{
				UserID: "u1", BotID: "b1", InputTokens: 3,
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())

			for _, d := range tc.deltas {
				seedUsage(t, s, d)
			}

			got := readUsageRows(t, s)
			require.Len(t, got, len(tc.wantRows))
			for i, want := range tc.wantRows {
				assert.Equal(t, want.UserID, got[i].UserID)
				assert.Equal(t, want.BotID, got[i].BotID)
				assert.Equal(t, want.IsGuest, got[i].IsGuest)
				assert.Equal(t, want.IsBot, got[i].IsBot)
				assert.Equal(t, want.InputTokens, got[i].InputTokens)
				assert.Equal(t, want.OutputTokens, got[i].OutputTokens)
				assert.InDelta(t, want.Cost, got[i].Cost, 1e-9)
			}
		})
	}
}

func TestGetActiveUserCount(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())

	seedUsage(t, s, DailyUsageDelta{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1"})
	seedUsage(t, s, DailyUsageDelta{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b2"})
	seedUsage(t, s, DailyUsageDelta{Day: day(2026, time.July, 9), UserID: "u2", BotID: "b1"})
	seedUsage(t, s, DailyUsageDelta{Day: day(2026, time.July, 10), UserID: "u3", BotID: "b1", IsGuest: true})
	seedUsage(t, s, DailyUsageDelta{Day: day(2026, time.July, 10), UserID: "u4", BotID: "b1", IsBot: true})
	seedUsage(t, s, DailyUsageDelta{Day: day(2026, time.July, 10), UserID: "", BotID: "b1", IsBot: true})
	seedUsage(t, s, DailyUsageDelta{Day: day(2026, time.July, 8), UserID: "u5", BotID: "b1"})

	tests := []struct {
		name  string
		since time.Time
		want  int64
	}{
		{name: "window includes rows exactly at since (inclusive lower edge)", since: day(2026, time.July, 9), want: 2},
		{name: "older rows excluded (exclusive below since)", since: day(2026, time.July, 10), want: 1},
		{name: "guests, bots, empty user IDs never counted", since: day(2026, time.July, 8), want: 3},
		{name: "empty window", since: day(2026, time.July, 11), want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.GetActiveUserCount(context.Background(), tc.since)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestGetActiveUserCountPerBot(t *testing.T) {
	tests := []struct {
		name  string
		seed  []DailyUsageDelta
		since time.Time
		want  []BotActiveUsers
	}{
		{
			name: "groups per bot with distinct users",
			seed: []DailyUsageDelta{
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1"},
				{Day: day(2026, time.July, 10), UserID: "u2", BotID: "b1"},
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b2"},
			},
			since: day(2026, time.July, 10),
			want: []BotActiveUsers{
				{BotID: "b1", ActiveUsers: 2},
				{BotID: "b2", ActiveUsers: 1},
			},
		},
		{
			name: "ordered by ActiveUsers DESC then BotID ASC",
			seed: []DailyUsageDelta{
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1"},
				{Day: day(2026, time.July, 10), UserID: "u2", BotID: "b2"},
				{Day: day(2026, time.July, 10), UserID: "u3", BotID: "a3"},
				{Day: day(2026, time.July, 10), UserID: "u4", BotID: "a3"},
				{Day: day(2026, time.July, 10), UserID: "u5", BotID: "a3"},
			},
			since: day(2026, time.July, 10),
			want: []BotActiveUsers{
				{BotID: "a3", ActiveUsers: 3},
				{BotID: "b1", ActiveUsers: 1},
				{BotID: "b2", ActiveUsers: 1},
			},
		},
		{
			name: "guest/bot/empty-user rows excluded per bot",
			seed: []DailyUsageDelta{
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1"},
				{Day: day(2026, time.July, 10), UserID: "g1", BotID: "b-guest-only", IsGuest: true},
				{Day: day(2026, time.July, 10), UserID: "bot1", BotID: "b-bot-only", IsBot: true},
				{Day: day(2026, time.July, 10), UserID: "", BotID: "b-empty-only", IsBot: true},
			},
			since: day(2026, time.July, 10),
			want: []BotActiveUsers{
				{BotID: "b1", ActiveUsers: 1},
			},
		},
		{
			name: "empty BotID rows excluded from per-agent breakdown",
			seed: []DailyUsageDelta{
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1"},
				{Day: day(2026, time.July, 10), UserID: "u2", BotID: ""},
				{Day: day(2026, time.July, 10), UserID: "u3", BotID: ""},
			},
			since: day(2026, time.July, 10),
			want: []BotActiveUsers{
				{BotID: "b1", ActiveUsers: 1},
			},
		},
		{
			name:  "empty table returns empty non-nil slice",
			seed:  nil,
			since: day(2026, time.July, 10),
			want:  []BotActiveUsers{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())
			for _, d := range tc.seed {
				seedUsage(t, s, d)
			}

			got, err := s.GetActiveUserCountPerBot(context.Background(), tc.since)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestGetUsageTotals(t *testing.T) {
	tests := []struct {
		name  string
		seed  []DailyUsageDelta
		since time.Time
		want  UsageTotals
	}{
		{
			name: "sums all rows including guests and bot-originated",
			seed: []DailyUsageDelta{
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", InputTokens: 10, OutputTokens: 5, Cost: 0.1},
				{Day: day(2026, time.July, 10), UserID: "g1", BotID: "b1", IsGuest: true, InputTokens: 20, OutputTokens: 10, Cost: 0.2},
				{Day: day(2026, time.July, 10), UserID: "bot1", BotID: "b1", IsBot: true, InputTokens: 30, OutputTokens: 15, Cost: 0.3},
				{Day: day(2026, time.July, 10), UserID: "", BotID: "b1", IsBot: true, InputTokens: 40, OutputTokens: 20, Cost: 0.4},
			},
			since: day(2026, time.July, 10),
			want:  UsageTotals{InputTokens: 100, OutputTokens: 50, Cost: 1.0},
		},
		{
			name: "window boundary inclusive at since",
			seed: []DailyUsageDelta{
				{Day: day(2026, time.July, 9), UserID: "u1", BotID: "b1", InputTokens: 100, OutputTokens: 50, Cost: 1.0},
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", InputTokens: 7, OutputTokens: 3, Cost: 0.05},
			},
			since: day(2026, time.July, 10),
			want:  UsageTotals{InputTokens: 7, OutputTokens: 3, Cost: 0.05},
		},
		{
			name:  "empty table returns zeros",
			seed:  nil,
			since: day(2026, time.July, 10),
			want:  UsageTotals{},
		},
		{
			name: "cost summed as float",
			seed: []DailyUsageDelta{
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", Cost: 0.1},
				{Day: day(2026, time.July, 10), UserID: "u2", BotID: "b1", Cost: 0.2},
			},
			since: day(2026, time.July, 10),
			want:  UsageTotals{Cost: 0.3},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())
			for _, d := range tc.seed {
				seedUsage(t, s, d)
			}

			got, err := s.GetUsageTotals(context.Background(), tc.since)
			require.NoError(t, err)
			assert.Equal(t, tc.want.InputTokens, got.InputTokens)
			assert.Equal(t, tc.want.OutputTokens, got.OutputTokens)
			assert.InDelta(t, tc.want.Cost, got.Cost, 1e-9)
		})
	}
}

func TestGetDailyTokenTotals(t *testing.T) {
	tests := []struct {
		name     string
		seed     []DailyUsageDelta
		since    time.Time
		wantLen  int
		wantDays []string
		wantIn   []int64
		wantOut  []int64
	}{
		{
			name: "sums per day across users and bots, ordered ascending",
			seed: []DailyUsageDelta{
				{Day: day(2026, time.July, 8), UserID: "u1", BotID: "b1", InputTokens: 10, OutputTokens: 5},
				{Day: day(2026, time.July, 8), UserID: "u2", BotID: "b2", InputTokens: 20, OutputTokens: 10},
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", InputTokens: 1, OutputTokens: 2},
				{Day: day(2026, time.July, 10), UserID: "u2", BotID: "b2", InputTokens: 3, OutputTokens: 4},
			},
			since:    day(2026, time.July, 8),
			wantLen:  2,
			wantDays: []string{"2026-07-08", "2026-07-10"},
			wantIn:   []int64{30, 4},
			wantOut:  []int64{15, 6},
		},
		{
			name: "sparse: absent days are omitted",
			seed: []DailyUsageDelta{
				{Day: day(2026, time.July, 8), UserID: "u1", BotID: "b1", InputTokens: 1},
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", InputTokens: 1},
			},
			since:    day(2026, time.July, 8),
			wantLen:  2,
			wantDays: []string{"2026-07-08", "2026-07-10"},
			wantIn:   []int64{1, 1},
			wantOut:  []int64{0, 0},
		},
		{
			name: "window boundary inclusive",
			seed: []DailyUsageDelta{
				{Day: day(2026, time.July, 9), UserID: "u1", BotID: "b1", InputTokens: 100},
				{Day: day(2026, time.July, 10), UserID: "u1", BotID: "b1", InputTokens: 7},
			},
			since:    day(2026, time.July, 10),
			wantLen:  1,
			wantDays: []string{"2026-07-10"},
			wantIn:   []int64{7},
			wantOut:  []int64{0},
		},
		{
			name:     "empty table returns empty non-nil slice",
			seed:     nil,
			since:    day(2026, time.July, 10),
			wantLen:  0,
			wantDays: nil,
			wantIn:   nil,
			wantOut:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())
			for _, d := range tc.seed {
				seedUsage(t, s, d)
			}

			got, err := s.GetDailyTokenTotals(context.Background(), tc.since)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Len(t, got, tc.wantLen)
			for i := range got {
				// Contract: Day is a YYYY-MM-DD string (RPC-safe), not time.Time.
				_, parseErr := time.Parse(time.DateOnly, got[i].Day)
				require.NoError(t, parseErr, "Day must be YYYY-MM-DD, got %q", got[i].Day)
				assert.Equal(t, tc.wantDays[i], got[i].Day)
				assert.Equal(t, tc.wantIn[i], got[i].InputTokens)
				assert.Equal(t, tc.wantOut[i], got[i].OutputTokens)
			}
		})
	}
}

func TestDeleteUsageBefore(t *testing.T) {
	t.Run("deletes only rows strictly older than cutoff (exclusive at cutoff)", func(t *testing.T) {
		s := setupTestStore(t)
		require.NoError(t, s.RunMigrations())

		seedUsage(t, s, DailyUsageDelta{Day: day(2026, time.July, 7), UserID: "u1", BotID: "b1"})
		seedUsage(t, s, DailyUsageDelta{Day: day(2026, time.July, 8), UserID: "u1", BotID: "b1"})
		seedUsage(t, s, DailyUsageDelta{Day: day(2026, time.July, 9), UserID: "u1", BotID: "b1"})

		deleted, err := s.DeleteUsageBefore(context.Background(), day(2026, time.July, 8), 100)
		require.NoError(t, err)
		assert.Equal(t, int64(1), deleted)

		rows := readUsageRows(t, s)
		require.Len(t, rows, 2)
	})

	t.Run("respects batch size and reports deleted count", func(t *testing.T) {
		s := setupTestStore(t)
		require.NoError(t, s.RunMigrations())

		for i := 0; i < 5; i++ {
			seedUsage(t, s, DailyUsageDelta{
				Day:    day(2026, time.July, 1+i),
				UserID: "u1",
				BotID:  "b1",
			})
		}

		cutoff := day(2026, time.July, 10)
		deleted, err := s.DeleteUsageBefore(context.Background(), cutoff, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(2), deleted)

		deleted, err = s.DeleteUsageBefore(context.Background(), cutoff, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(2), deleted)

		deleted, err = s.DeleteUsageBefore(context.Background(), cutoff, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(1), deleted)

		deleted, err = s.DeleteUsageBefore(context.Background(), cutoff, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(0), deleted)
	})

	t.Run("nothing to delete returns 0", func(t *testing.T) {
		s := setupTestStore(t)
		require.NoError(t, s.RunMigrations())

		deleted, err := s.DeleteUsageBefore(context.Background(), day(2026, time.July, 8), 100)
		require.NoError(t, err)
		assert.Equal(t, int64(0), deleted)
	})
}
