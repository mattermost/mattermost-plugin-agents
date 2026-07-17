// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeUsageStatsStore is a hand-written UsageStatsStore for handler tests.
// countsByWindow is keyed by the trailing-window length in calendar days,
// derived from the `since` each call receives — this implicitly verifies the
// handler's window arithmetic (a wrong `since` reads a missing key → 0 → the
// shape assertion fails).
type fakeUsageStatsStore struct {
	countsByWindow map[int]int64 // e.g. {7: 10, 30: 42, 60: 50, 90: 60}
	perBot         []store.BotActiveUsers
	totals         store.UsageTotals
	daily          []store.DailyTokens
	err            error // when set, every method returns it

	sinces []time.Time // records every `since` received, for boundary assertions
}

func (f *fakeUsageStatsStore) recordSince(since time.Time) {
	f.sinces = append(f.sinces, since)
}

func (f *fakeUsageStatsStore) GetActiveUserCount(_ context.Context, since time.Time) (int64, error) {
	f.recordSince(since)
	if f.err != nil {
		return 0, f.err
	}
	days := int(utcDayStart(time.Now()).Sub(since).Hours()/24) + 1
	return f.countsByWindow[days], nil
}

func (f *fakeUsageStatsStore) GetActiveUserCountPerBot(_ context.Context, since time.Time) ([]store.BotActiveUsers, error) {
	f.recordSince(since)
	if f.err != nil {
		return nil, f.err
	}
	if f.perBot == nil {
		return []store.BotActiveUsers{}, nil
	}
	return f.perBot, nil
}

func (f *fakeUsageStatsStore) GetUsageTotals(_ context.Context, since time.Time) (store.UsageTotals, error) {
	f.recordSince(since)
	if f.err != nil {
		return store.UsageTotals{}, f.err
	}
	return f.totals, nil
}

func (f *fakeUsageStatsStore) GetDailyTokenTotals(_ context.Context, since time.Time) ([]store.DailyTokens, error) {
	f.recordSince(since)
	if f.err != nil {
		return nil, f.err
	}
	if f.daily == nil {
		return []store.DailyTokens{}, nil
	}
	return f.daily, nil
}

func TestHandleGetUsageStatsAuth(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		setUserHeader  bool
		isAdmin        bool
		expectedStatus int
	}{
		{
			name:           "no user header",
			setUserHeader:  false,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "non-admin",
			setUserHeader:  true,
			isAdmin:        false,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "admin",
			setUserHeader:  true,
			isAdmin:        true,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.mockAPI.On("LogError", mock.Anything).Maybe()
			if tc.setUserHeader {
				e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(tc.isAdmin)
			}

			req := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
			if tc.setUserHeader {
				req.Header.Set("Mattermost-User-Id", "userid")
			}
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			require.Equal(t, tc.expectedStatus, recorder.Code)
		})
	}
}

func TestHandleGetUsageStats(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		seed           func(e *TestEnvironment)
		expectedStatus int
		validate       func(t *testing.T, e *TestEnvironment, resp UsageStatsResponse, body []byte)
	}{
		{
			name: "happy path — full shape",
			seed: func(e *TestEnvironment) {
				e.bots.SetBotsForTesting([]*bots.Bot{bots.NewBot(
					llm.BotConfig{Name: "matty", DisplayName: "Matty"},
					llm.ServiceConfig{},
					&model.Bot{UserId: testBotUserID, Username: "matty", DisplayName: "Matty"},
					nil,
				)})
				today := utcDayStart(time.Now())
				e.usageStatsStore.countsByWindow = map[int]int64{30: 42, 7: 10, 60: 50, 90: 60}
				e.usageStatsStore.perBot = []store.BotActiveUsers{{BotID: testBotUserID, ActiveUsers: 12}}
				e.usageStatsStore.totals = store.UsageTotals{InputTokens: 120000, OutputTokens: 45000, Cost: 12.34}
				e.usageStatsStore.daily = []store.DailyTokens{
					{Day: today.AddDate(0, 0, -1), InputTokens: 100, OutputTokens: 50},
					{Day: today, InputTokens: 1200, OutputTokens: 300},
				}
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, e *TestEnvironment, resp UsageStatsResponse, _ []byte) {
				assert.Equal(t, int64(42), resp.MonthlyActiveUsers)
				assert.Equal(t, int64(10), resp.UniqueUsers7d)
				assert.Equal(t, int64(50), resp.UniqueUsers60d)
				assert.Equal(t, int64(60), resp.UniqueUsers90d)
				assert.Equal(t, []AgentActiveUsers{{
					BotID:       testBotUserID,
					DisplayName: "Matty",
					ActiveUsers: 12,
				}}, resp.ActiveUsersPerAgent)
				assert.Equal(t, TokenTotals{InputTokens: 120000, OutputTokens: 45000, TotalTokens: 165000}, resp.Tokens30d)
				assert.Equal(t, 12.34, resp.Cost30d)

				require.Len(t, resp.TokensPerDay30d, 30)
				for i := 1; i < len(resp.TokensPerDay30d); i++ {
					assert.True(t, resp.TokensPerDay30d[i].Day > resp.TokensPerDay30d[i-1].Day,
						"days must be strictly ascending: %s then %s", resp.TokensPerDay30d[i-1].Day, resp.TokensPerDay30d[i].Day)
				}
				// Last two entries carry seeded values; others are zero.
				for i, point := range resp.TokensPerDay30d {
					switch i {
					case 28:
						assert.Equal(t, DailyTokenCount{Day: point.Day, InputTokens: 100, OutputTokens: 50, TotalTokens: 150}, point)
					case 29:
						assert.Equal(t, DailyTokenCount{Day: point.Day, InputTokens: 1200, OutputTokens: 300, TotalTokens: 1500}, point)
					default:
						assert.Equal(t, int64(0), point.InputTokens)
						assert.Equal(t, int64(0), point.OutputTokens)
						assert.Equal(t, int64(0), point.TotalTokens)
					}
				}

				// Handler records every store `since` (7 calls across 4 windows).
				require.Len(t, e.usageStatsStore.sinces, 7)
				for _, since := range e.usageStatsStore.sinces {
					assert.True(t, since.Equal(utcDayStart(since)), "since must be midnight UTC: %v", since)
				}
				since30 := e.usageStatsStore.sinces[0]
				since7 := e.usageStatsStore.sinces[2]
				since60 := e.usageStatsStore.sinces[3]
				since90 := e.usageStatsStore.sinces[4]
				assert.Equal(t, 23*24*time.Hour, since7.Sub(since30))
				assert.Equal(t, 30*24*time.Hour, since30.Sub(since60))
				assert.Equal(t, 30*24*time.Hour, since60.Sub(since90))
			},
		},
		{
			name: "empty table — zero response",
			seed: func(e *TestEnvironment) {
				// Default empty fake from SetupTestEnvironment.
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, _ *TestEnvironment, resp UsageStatsResponse, body []byte) {
				assert.Equal(t, int64(0), resp.MonthlyActiveUsers)
				assert.Equal(t, int64(0), resp.UniqueUsers7d)
				assert.Equal(t, int64(0), resp.UniqueUsers60d)
				assert.Equal(t, int64(0), resp.UniqueUsers90d)
				assert.Equal(t, TokenTotals{}, resp.Tokens30d)
				assert.Equal(t, 0.0, resp.Cost30d)
				require.Len(t, resp.TokensPerDay30d, 30)
				for i, point := range resp.TokensPerDay30d {
					assert.Equal(t, int64(0), point.InputTokens)
					assert.Equal(t, int64(0), point.OutputTokens)
					assert.Equal(t, int64(0), point.TotalTokens)
					if i > 0 {
						assert.True(t, point.Day > resp.TokensPerDay30d[i-1].Day)
					}
				}
				assert.Contains(t, string(body), `"active_users_per_agent":[]`)
				assert.NotContains(t, string(body), `"active_users_per_agent":null`)
			},
		},
		{
			name: "per-agent order preserved",
			seed: func(e *TestEnvironment) {
				botA := "aaaa12345678901234567890ab"
				botB := "bbbb12345678901234567890ab"
				botC := "cccc12345678901234567890ab"
				e.usageStatsStore.perBot = []store.BotActiveUsers{
					{BotID: botA, ActiveUsers: 12},
					{BotID: botB, ActiveUsers: 5},
					{BotID: botC, ActiveUsers: 5},
				}
				e.mockAPI.On("GetUser", botA).Return(&model.User{Id: botA, Username: "a"}, nil)
				e.mockAPI.On("GetUser", botB).Return(&model.User{Id: botB, Username: "b"}, nil)
				e.mockAPI.On("GetUser", botC).Return(&model.User{Id: botC, Username: "c"}, nil)
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, e *TestEnvironment, resp UsageStatsResponse, _ []byte) {
				require.Len(t, resp.ActiveUsersPerAgent, 3)
				assert.Equal(t, e.usageStatsStore.perBot[0].BotID, resp.ActiveUsersPerAgent[0].BotID)
				assert.Equal(t, e.usageStatsStore.perBot[1].BotID, resp.ActiveUsersPerAgent[1].BotID)
				assert.Equal(t, e.usageStatsStore.perBot[2].BotID, resp.ActiveUsersPerAgent[2].BotID)
				assert.Equal(t, []int64{12, 5, 5}, []int64{
					resp.ActiveUsersPerAgent[0].ActiveUsers,
					resp.ActiveUsersPerAgent[1].ActiveUsers,
					resp.ActiveUsersPerAgent[2].ActiveUsers,
				})
			},
		},
		{
			name: "display-name fallbacks",
			seed: func(e *TestEnvironment) {
				e.bots.SetBotsForTesting([]*bots.Bot{bots.NewBot(
					llm.BotConfig{Name: "matty", DisplayName: "Matty"},
					llm.ServiceConfig{},
					&model.Bot{UserId: testBotUserID, Username: "matty", DisplayName: "Matty"},
					nil,
				)})
				e.usageStatsStore.perBot = []store.BotActiveUsers{
					{BotID: testBotUserID, ActiveUsers: 12},
					{BotID: testOtherUserID, ActiveUsers: 5},
					{BotID: testNonexistentBot, ActiveUsers: 3},
					{BotID: "", ActiveUsers: 1},
				}
				e.mockAPI.On("GetUser", testOtherUserID).Return(&model.User{
					Id:        testOtherUserID,
					Username:  "old-bot",
					FirstName: "Old Bot",
				}, nil)
				e.mockAPI.On("GetUser", testNonexistentBot).Return(nil, &model.AppError{})
			},
			expectedStatus: http.StatusOK,
			validate: func(t *testing.T, _ *TestEnvironment, resp UsageStatsResponse, _ []byte) {
				require.Len(t, resp.ActiveUsersPerAgent, 4)
				assert.Equal(t, "Matty", resp.ActiveUsersPerAgent[0].DisplayName)
				assert.Equal(t, "Old Bot", resp.ActiveUsersPerAgent[1].DisplayName)
				assert.Equal(t, testNonexistentBot, resp.ActiveUsersPerAgent[2].DisplayName)
				assert.Equal(t, unknownAgentDisplayName, resp.ActiveUsersPerAgent[3].DisplayName)
			},
		},
		{
			name: "store error → 500",
			seed: func(e *TestEnvironment) {
				e.usageStatsStore.err = errors.New("db gone")
			},
			expectedStatus: http.StatusInternalServerError,
			validate:       nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(true)
			e.mockAPI.On("LogError", mock.Anything).Maybe()
			if tc.seed != nil {
				tc.seed(e)
			}

			req := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
			req.Header.Set("Mattermost-User-Id", "userid")
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			require.Equal(t, tc.expectedStatus, recorder.Code)

			if tc.expectedStatus != http.StatusOK {
				return
			}

			body := recorder.Body.Bytes()
			var resp UsageStatsResponse
			require.NoError(t, json.Unmarshal(body, &resp))
			if tc.validate != nil {
				tc.validate(t, e, resp, body)
			}
		})
	}
}

func TestTrailingWindowStart(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		days int
		want time.Time
	}{
		{
			name: "30 day window matches §2.4 example",
			now:  time.Date(2026, 7, 17, 15, 4, 5, 0, time.UTC),
			days: 30,
			want: time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "7 day window",
			now:  time.Date(2026, 7, 17, 15, 4, 5, 0, time.UTC),
			days: 7,
			want: time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "60 day window",
			now:  time.Date(2026, 7, 17, 15, 4, 5, 0, time.UTC),
			days: 60,
			want: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "90 day window",
			now:  time.Date(2026, 7, 17, 15, 4, 5, 0, time.UTC),
			days: 90,
			want: time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "1 day window is today only",
			now:  time.Date(2026, 7, 17, 15, 4, 5, 0, time.UTC),
			days: 1,
			want: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "month and year boundary",
			now:  time.Date(2026, 1, 5, 0, 30, 0, 0, time.UTC),
			days: 30,
			want: time.Date(2025, 12, 7, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "non-UTC input normalized to UTC calendar day",
			now:  time.Date(2026, 7, 17, 20, 0, 0, 0, time.FixedZone("PDT", -7*3600)),
			days: 30,
			want: time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := trailingWindowStart(tc.now, tc.days)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestZeroFillDailyTokens(t *testing.T) {
	now := time.Date(2026, 7, 17, 15, 4, 5, 0, time.UTC)
	cest := time.FixedZone("CEST", 2*3600)

	tests := []struct {
		name  string
		rows  []store.DailyTokens
		check func(t *testing.T, got []DailyTokenCount)
	}{
		{
			name: "empty rows → 30 zeros ascending",
			rows: nil,
			check: func(t *testing.T, got []DailyTokenCount) {
				require.Len(t, got, 30)
				assert.Equal(t, "2026-06-18", got[0].Day)
				assert.Equal(t, "2026-07-17", got[29].Day)
				for i, point := range got {
					assert.Equal(t, int64(0), point.InputTokens)
					assert.Equal(t, int64(0), point.OutputTokens)
					assert.Equal(t, int64(0), point.TotalTokens)
					if i > 0 {
						assert.True(t, point.Day > got[i-1].Day)
					}
				}
			},
		},
		{
			name: "sparse rows land on correct keys",
			rows: []store.DailyTokens{
				{Day: time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC), InputTokens: 10, OutputTokens: 1},
				{Day: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), InputTokens: 20, OutputTokens: 2},
				{Day: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC), InputTokens: 30, OutputTokens: 3},
			},
			check: func(t *testing.T, got []DailyTokenCount) {
				require.Len(t, got, 30)
				byDay := make(map[string]DailyTokenCount, len(got))
				for _, point := range got {
					byDay[point.Day] = point
				}
				assert.Equal(t, DailyTokenCount{Day: "2026-06-18", InputTokens: 10, OutputTokens: 1, TotalTokens: 11}, byDay["2026-06-18"])
				assert.Equal(t, DailyTokenCount{Day: "2026-07-01", InputTokens: 20, OutputTokens: 2, TotalTokens: 22}, byDay["2026-07-01"])
				assert.Equal(t, DailyTokenCount{Day: "2026-07-17", InputTokens: 30, OutputTokens: 3, TotalTokens: 33}, byDay["2026-07-17"])
				assert.Equal(t, DailyTokenCount{Day: "2026-06-19", InputTokens: 0, OutputTokens: 0, TotalTokens: 0}, byDay["2026-06-19"])
			},
		},
		{
			name: "non-UTC location for midnight UTC still keys correctly",
			rows: []store.DailyTokens{
				// 2026-07-01T00:00:00Z == 2026-07-01T02:00:00+02:00
				{Day: time.Date(2026, 7, 1, 2, 0, 0, 0, cest), InputTokens: 7, OutputTokens: 8},
			},
			check: func(t *testing.T, got []DailyTokenCount) {
				require.Len(t, got, 30)
				found := false
				for _, point := range got {
					if point.Day == "2026-07-01" {
						found = true
						assert.Equal(t, int64(7), point.InputTokens)
						assert.Equal(t, int64(8), point.OutputTokens)
						assert.Equal(t, int64(15), point.TotalTokens)
					}
				}
				assert.True(t, found, "expected 2026-07-01 entry")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := zeroFillDailyTokens(now, 30, tc.rows)
			tc.check(t, got)
		})
	}
}
