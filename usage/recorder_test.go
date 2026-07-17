// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package usage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStore struct {
	mu     sync.Mutex
	deltas []store.DailyUsageDelta
	err    error
}

func (f *fakeStore) IncrementDailyUsage(_ context.Context, delta store.DailyUsageDelta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.deltas = append(f.deltas, delta)
	return nil
}

func (f *fakeStore) Deltas() []store.DailyUsageDelta {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]store.DailyUsageDelta, len(f.deltas))
	copy(result, f.deltas)
	return result
}

type fakeLogger struct {
	mu     sync.Mutex
	errors []string
}

func (f *fakeLogger) Error(message string, _ ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors = append(f.errors, message)
}

func (f *fakeLogger) Errors() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]string, len(f.errors))
	copy(result, f.errors)
	return result
}

func TestRecorderRecordTokenUsage(t *testing.T) {
	tests := []struct {
		name       string
		rec        llm.TokenUsageRecord
		storeErr   error
		nilLogger  bool
		wantDeltas int
		wantLogged bool
	}{
		{
			name: "maps all fields into the delta",
			rec: llm.TokenUsageRecord{
				UserID:    "u1",
				IsGuest:   true,
				IsBot:     false,
				BotUserID: "b1",
				Usage:     llm.TokenUsage{InputTokens: 7, OutputTokens: 11, Cost: 0.5},
			},
			wantDeltas: 1,
		},
		{
			name:       "zero usage skipped",
			rec:        llm.TokenUsageRecord{Usage: llm.TokenUsage{}},
			wantDeltas: 0,
		},
		{
			name: "cost-only usage recorded",
			rec: llm.TokenUsageRecord{
				UserID: "u1",
				Usage:  llm.TokenUsage{Cost: 0.02},
			},
			wantDeltas: 1,
		},
		{
			name: "tokens-only usage recorded",
			rec: llm.TokenUsageRecord{
				UserID: "u1",
				Usage:  llm.TokenUsage{InputTokens: 1},
			},
			wantDeltas: 1,
		},
		{
			name: "store error swallowed and logged",
			rec: llm.TokenUsageRecord{
				UserID:      "u1",
				BotUsername: "agent-1",
				Usage:       llm.TokenUsage{InputTokens: 1},
			},
			storeErr:   errors.New("boom"),
			wantDeltas: 0,
			wantLogged: true,
		},
		{
			name: "nil logger does not panic on store error",
			rec: llm.TokenUsageRecord{
				UserID: "u1",
				Usage:  llm.TokenUsage{InputTokens: 1},
			},
			storeErr:   errors.New("boom"),
			nilLogger:  true,
			wantDeltas: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{err: tc.storeErr}
			var log *fakeLogger
			var recorder *Recorder
			if tc.nilLogger {
				recorder = New(st, nil)
			} else {
				log = &fakeLogger{}
				recorder = New(st, log)
			}

			require.NotPanics(t, func() {
				recorder.RecordTokenUsage(context.Background(), tc.rec)
			})

			deltas := st.Deltas()
			require.Len(t, deltas, tc.wantDeltas)
			if tc.wantDeltas == 1 && tc.storeErr == nil {
				delta := deltas[0]
				assert.Equal(t, tc.rec.UserID, delta.UserID)
				assert.Equal(t, tc.rec.BotUserID, delta.BotID)
				assert.Equal(t, tc.rec.IsGuest, delta.IsGuest)
				assert.Equal(t, tc.rec.IsBot, delta.IsBot)
				assert.Equal(t, tc.rec.Usage.InputTokens, delta.InputTokens)
				assert.Equal(t, tc.rec.Usage.OutputTokens, delta.OutputTokens)
				assert.InDelta(t, tc.rec.Usage.Cost, delta.Cost, 1e-9)
				assert.Equal(t, time.Now().UTC().Format("2006-01-02"), delta.Day.UTC().Format("2006-01-02"))
			}

			if tc.wantLogged {
				require.NotNil(t, log)
				assert.Len(t, log.Errors(), 1)
			} else if log != nil {
				assert.Empty(t, log.Errors())
			}
		})
	}
}
