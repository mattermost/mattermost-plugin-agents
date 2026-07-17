// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package usage

import (
	"context"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

// Logger is the subset of pluginapi.LogService the recorder needs.
type Logger interface {
	Error(message string, keyValuePairs ...any)
}

// Store is the subset of *store.Store the recorder needs.
type Store interface {
	IncrementDailyUsage(ctx context.Context, delta store.DailyUsageDelta) error
}

// Recorder persists aggregated token usage into LLM_Usage_Daily. It implements
// llm.TokenUsageRecorder and never propagates errors to the stream path.
type Recorder struct {
	store Store
	log   Logger
}

var _ llm.TokenUsageRecorder = (*Recorder)(nil)

// New creates a Recorder backed by the plugin store.
func New(st Store, log Logger) *Recorder {
	return &Recorder{store: st, log: log}
}

// RecordTokenUsage maps the record to a DailyUsageDelta for today's UTC date
// and upserts it. Failures are logged at error level and dropped. Records with
// zero input AND zero output AND zero cost are skipped.
func (r *Recorder) RecordTokenUsage(ctx context.Context, rec llm.TokenUsageRecord) {
	if rec.Usage.InputTokens == 0 && rec.Usage.OutputTokens == 0 && rec.Usage.Cost == 0 {
		return
	}

	delta := store.DailyUsageDelta{
		Day:          time.Now().UTC(),
		UserID:       rec.UserID,
		BotID:        rec.BotUserID,
		IsGuest:      rec.IsGuest,
		IsBot:        rec.IsBot,
		InputTokens:  rec.Usage.InputTokens,
		OutputTokens: rec.Usage.OutputTokens,
		Cost:         rec.Usage.Cost,
	}
	if err := r.store.IncrementDailyUsage(ctx, delta); err != nil && r.log != nil {
		r.log.Error("Failed to record token usage", "error", err, "agent_username", rec.BotUsername)
	}
}
