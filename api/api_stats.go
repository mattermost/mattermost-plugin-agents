// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// UsageStatsStore is the subset of *store.Store used by the stats endpoint.
type UsageStatsStore interface {
	GetActiveUserCount(ctx context.Context, since time.Time) (int64, error)
	GetActiveUserCountPerBot(ctx context.Context, since time.Time) ([]store.BotActiveUsers, error)
	GetUsageTotals(ctx context.Context, since time.Time) (store.UsageTotals, error)
	GetDailyTokenTotals(ctx context.Context, since time.Time) ([]store.DailyTokens, error)
}

// UsageStatsResponse is the body of GET /admin/stats.
type UsageStatsResponse struct {
	MonthlyActiveUsers  int64              `json:"monthly_active_users"`
	ActiveUsersPerAgent []AgentActiveUsers `json:"active_users_per_agent"`
	UniqueUsers7d       int64              `json:"unique_users_7d"`
	UniqueUsers60d      int64              `json:"unique_users_60d"`
	UniqueUsers90d      int64              `json:"unique_users_90d"`
	Tokens30d           TokenTotals        `json:"tokens_30d"`
	Cost30d             float64            `json:"cost_30d"`
	TokensPerDay30d     []DailyTokenCount  `json:"tokens_per_day_30d"`
}

// AgentActiveUsers is one per-agent MAU entry, display name resolved at request time.
type AgentActiveUsers struct {
	BotID       string `json:"bot_id"`
	DisplayName string `json:"display_name"`
	ActiveUsers int64  `json:"active_users"`
}

// TokenTotals is an input/output/total token triple.
type TokenTotals struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

// DailyTokenCount is one point of the zero-filled tokens-per-day series. Day is "YYYY-MM-DD" (UTC).
type DailyTokenCount struct {
	Day          string `json:"day"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

// unknownAgentDisplayName labels usage rows recorded with an empty BotID.
// English-only server-side constant per master plan §2.4: the plugin's Go i18n
// bundle is hand-curated and this admin endpoint has no request-locale plumbing;
// do NOT add it to i18n/en.json in this phase.
const unknownAgentDisplayName = "Unknown agent"

const usageStatsWindowDaysMAU = 30 // also used for tokens/cost/series windows

// utcDayStart returns midnight UTC of now's UTC calendar date.
func utcDayStart(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// trailingWindowStart returns the inclusive lower bound (midnight UTC) of a
// trailing window covering `days` calendar dates ending today (inclusive).
func trailingWindowStart(now time.Time, days int) time.Time {
	return utcDayStart(now).AddDate(0, 0, -(days - 1))
}

// zeroFillDailyTokens expands sparse per-day rows into exactly `days` points,
// ascending by day, ending at now's UTC date. Days absent from rows get zeros.
func zeroFillDailyTokens(now time.Time, days int, rows []store.DailyTokens) []DailyTokenCount {
	byDay := make(map[string]store.DailyTokens, len(rows))
	for _, row := range rows {
		byDay[row.Day.UTC().Format(time.DateOnly)] = row
	}

	out := make([]DailyTokenCount, 0, days)
	d := trailingWindowStart(now, days)
	for i := 0; i < days; i++ {
		key := d.Format(time.DateOnly)
		r := byDay[key]
		out = append(out, DailyTokenCount{
			Day:          key,
			InputTokens:  r.InputTokens,
			OutputTokens: r.OutputTokens,
			TotalTokens:  r.InputTokens + r.OutputTokens,
		})
		d = d.AddDate(0, 0, 1)
	}
	return out
}

// resolveAgentDisplayName resolves a bot user ID to a human-readable agent name.
// Fallback order: live bot registry, Mattermost user record (bots that were
// deleted from plugin config still have a user), raw bot ID.
func (a *API) resolveAgentDisplayName(botID string) string {
	if botID == "" {
		return unknownAgentDisplayName
	}
	if bot := a.bots.GetBotByID(botID); bot != nil {
		if mmBot := bot.GetMMBot(); mmBot != nil {
			if mmBot.DisplayName != "" {
				return mmBot.DisplayName
			}
			if mmBot.Username != "" {
				return mmBot.Username
			}
		}
	}
	if user, err := a.pluginAPI.User.Get(botID); err == nil {
		if name := user.GetDisplayName(model.ShowFullName); name != "" {
			return name
		}
	}
	return botID
}

// handleGetUsageStats returns aggregated agent usage statistics for the
// System Console Site Statistics page.
// GET /admin/stats
func (a *API) handleGetUsageStats(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now()

	since30 := trailingWindowStart(now, usageStatsWindowDaysMAU)

	mau, err := a.usageStatsStore.GetActiveUserCount(ctx, since30)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get monthly active users: %w", err))
		return
	}

	perBot, err := a.usageStatsStore.GetActiveUserCountPerBot(ctx, since30)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get active users per agent: %w", err))
		return
	}

	users7, err := a.usageStatsStore.GetActiveUserCount(ctx, trailingWindowStart(now, 7))
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get 7 day active users: %w", err))
		return
	}

	users60, err := a.usageStatsStore.GetActiveUserCount(ctx, trailingWindowStart(now, 60))
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get 60 day active users: %w", err))
		return
	}

	users90, err := a.usageStatsStore.GetActiveUserCount(ctx, trailingWindowStart(now, 90))
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get 90 day active users: %w", err))
		return
	}

	totals, err := a.usageStatsStore.GetUsageTotals(ctx, since30)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get usage totals: %w", err))
		return
	}

	daily, err := a.usageStatsStore.GetDailyTokenTotals(ctx, since30)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get daily token totals: %w", err))
		return
	}

	agents := make([]AgentActiveUsers, 0, len(perBot))
	for _, entry := range perBot {
		agents = append(agents, AgentActiveUsers{
			BotID:       entry.BotID,
			DisplayName: a.resolveAgentDisplayName(entry.BotID),
			ActiveUsers: entry.ActiveUsers,
		})
	}

	c.JSON(http.StatusOK, UsageStatsResponse{
		MonthlyActiveUsers:  mau,
		ActiveUsersPerAgent: agents,
		UniqueUsers7d:       users7,
		UniqueUsers60d:      users60,
		UniqueUsers90d:      users90,
		Tokens30d: TokenTotals{
			InputTokens:  totals.InputTokens,
			OutputTokens: totals.OutputTokens,
			TotalTokens:  totals.InputTokens + totals.OutputTokens,
		},
		Cost30d:         totals.Cost,
		TokensPerDay30d: zeroFillDailyTokens(now, usageStatsWindowDaysMAU, daily),
	})
}
