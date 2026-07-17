// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// stubEmbeddingsRetention records the passthrough args and returns canned results.
type stubEmbeddingsRetention struct {
	count            int64
	err              error
	calls            int
	gotNow, gotBatch int64
}

func (s *stubEmbeddingsRetention) RunDataRetention(_ context.Context, nowTime, batchSize int64) (int64, error) {
	s.calls++
	s.gotNow, s.gotBatch = nowTime, batchSize
	return s.count, s.err
}

type usageDeleteResult struct {
	n   int64
	err error
}

// stubUsageDeleter returns one scripted result per call and records every cutoff/batchSize.
type stubUsageDeleter struct {
	results []usageDeleteResult
	cutoffs []time.Time
	batches []int64
}

func (s *stubUsageDeleter) DeleteUsageBefore(_ context.Context, cutoff time.Time, batchSize int64) (int64, error) {
	s.cutoffs = append(s.cutoffs, cutoff)
	s.batches = append(s.batches, batchSize)
	i := len(s.cutoffs) - 1
	if i >= len(s.results) {
		return 0, nil // script exhausted → done
	}
	return s.results[i].n, s.results[i].err
}

// nopLogger satisfies retentionLogger.
type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

func TestRunDataRetentionHelper(t *testing.T) {
	// 2026-07-17T10:30:00Z — mid-day so day-truncation is observable.
	nowMillis := time.Date(2026, 7, 17, 10, 30, 0, 0, time.UTC).UnixMilli()
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	settingsEnabled := func(hours int) *model.DataRetentionSettings {
		return &model.DataRetentionSettings{
			EnableMessageDeletion: model.NewPointer(true),
			MessageRetentionHours: model.NewPointer(hours),
		}
	}
	errEmb, errUsage := errors.New("emb boom"), errors.New("usage boom")

	tests := []struct {
		name           string
		embeddings     *stubEmbeddingsRetention
		usage          *stubUsageDeleter
		settings       *model.DataRetentionSettings
		nowTime        int64
		batchSize      int64
		wantCount      int64
		wantErrs       []error
		wantUsageCalls int
		wantCutoff     *time.Time
	}{
		{
			name:       "disabled flag → no delete",
			embeddings: &stubEmbeddingsRetention{count: 5},
			usage:      &stubUsageDeleter{},
			settings: &model.DataRetentionSettings{
				EnableMessageDeletion: model.NewPointer(false),
			},
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      5,
			wantUsageCalls: 0,
		},
		{
			name:           "nil EnableMessageDeletion → no delete",
			embeddings:     &stubEmbeddingsRetention{count: 5},
			usage:          &stubUsageDeleter{},
			settings:       &model.DataRetentionSettings{},
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      5,
			wantUsageCalls: 0,
		},
		{
			name:           "nil settings → no delete, embeddings unaffected",
			embeddings:     &stubEmbeddingsRetention{count: 3},
			usage:          &stubUsageDeleter{},
			settings:       nil,
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      3,
			wantUsageCalls: 0,
		},
		{
			name:       "enabled, hours cutoff math",
			embeddings: &stubEmbeddingsRetention{count: 1},
			usage: &stubUsageDeleter{
				results: []usageDeleteResult{{n: 2}},
			},
			settings:       settingsEnabled(48),
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      3,
			wantUsageCalls: 1,
			wantCutoff:     ptrTime(day(2026, time.July, 15)),
		},
		{
			name:       "enabled, deprecated days fallback",
			embeddings: &stubEmbeddingsRetention{count: 0},
			usage: &stubUsageDeleter{
				results: []usageDeleteResult{{n: 1}},
			},
			settings: &model.DataRetentionSettings{
				EnableMessageDeletion: model.NewPointer(true),
				MessageRetentionDays:  model.NewPointer(30),
			},
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      1,
			wantUsageCalls: 1,
			wantCutoff:     ptrTime(day(2026, time.June, 17)),
		},
		{
			name:       "batching loop, multiple batches",
			embeddings: &stubEmbeddingsRetention{count: 0},
			usage: &stubUsageDeleter{
				results: []usageDeleteResult{{n: 3000}, {n: 3000}, {n: 120}},
			},
			settings:       settingsEnabled(24),
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      6120,
			wantUsageCalls: 3,
			wantCutoff:     ptrTime(day(2026, time.July, 16)),
		},
		{
			name:       "loop bound respected",
			embeddings: &stubEmbeddingsRetention{count: 0},
			usage: &stubUsageDeleter{
				results: fullBatchResults(maxUsageRetentionBatches+50, 3000),
			},
			settings:       settingsEnabled(24),
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      int64(maxUsageRetentionBatches) * 3000,
			wantUsageCalls: maxUsageRetentionBatches,
			wantCutoff:     ptrTime(day(2026, time.July, 16)),
		},
		{
			name:       "degenerate batchSize 0 terminates",
			embeddings: &stubEmbeddingsRetention{count: 4},
			usage: &stubUsageDeleter{
				results: []usageDeleteResult{{n: 0}},
			},
			settings:       settingsEnabled(24),
			nowTime:        nowMillis,
			batchSize:      0,
			wantCount:      4,
			wantUsageCalls: 1,
			wantCutoff:     ptrTime(day(2026, time.July, 16)),
		},
		{
			name: "embeddings error does not skip usage",
			embeddings: &stubEmbeddingsRetention{
				err: errEmb,
			},
			usage: &stubUsageDeleter{
				results: []usageDeleteResult{{n: 4}},
			},
			settings:       settingsEnabled(24),
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      4,
			wantErrs:       []error{errEmb},
			wantUsageCalls: 1,
			wantCutoff:     ptrTime(day(2026, time.July, 16)),
		},
		{
			name:       "usage error does not clobber embeddings result",
			embeddings: &stubEmbeddingsRetention{count: 7},
			usage: &stubUsageDeleter{
				results: []usageDeleteResult{{err: errUsage}},
			},
			settings:       settingsEnabled(24),
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      7,
			wantErrs:       []error{errUsage},
			wantUsageCalls: 1,
			wantCutoff:     ptrTime(day(2026, time.July, 16)),
		},
		{
			name: "both fail → joined error carries both",
			embeddings: &stubEmbeddingsRetention{
				err: errEmb,
			},
			usage: &stubUsageDeleter{
				results: []usageDeleteResult{{err: errUsage}},
			},
			settings:       settingsEnabled(24),
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      0,
			wantErrs:       []error{errEmb, errUsage},
			wantUsageCalls: 1,
			wantCutoff:     ptrTime(day(2026, time.July, 16)),
		},
		{
			name:       "mid-loop usage error keeps partial count",
			embeddings: &stubEmbeddingsRetention{count: 1},
			usage: &stubUsageDeleter{
				results: []usageDeleteResult{{n: 3000}, {err: errUsage}},
			},
			settings:       settingsEnabled(24),
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      3001,
			wantErrs:       []error{errUsage},
			wantUsageCalls: 2,
			wantCutoff:     ptrTime(day(2026, time.July, 16)),
		},
		{
			name:       "nil embeddings runner, usage still runs",
			embeddings: nil,
			usage: &stubUsageDeleter{
				results: []usageDeleteResult{{n: 2}},
			},
			settings:       settingsEnabled(24),
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      2,
			wantUsageCalls: 1,
			wantCutoff:     ptrTime(day(2026, time.July, 16)),
		},
		{
			name:           "nil usage store, embeddings still runs",
			embeddings:     &stubEmbeddingsRetention{count: 9},
			usage:          nil,
			settings:       settingsEnabled(24),
			nowTime:        nowMillis,
			batchSize:      3000,
			wantCount:      9,
			wantUsageCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var embeddings embeddingsRetentionRunner
			if tt.embeddings != nil {
				embeddings = tt.embeddings
			}
			var usage usageRetentionStore
			if tt.usage != nil {
				usage = tt.usage
			}

			count, err := runDataRetention(context.Background(), embeddings, usage, tt.settings,
				nopLogger{}, tt.nowTime, tt.batchSize)

			require.Equal(t, tt.wantCount, count)
			if len(tt.wantErrs) == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				for _, want := range tt.wantErrs {
					require.ErrorIs(t, err, want)
				}
			}

			if tt.embeddings != nil {
				require.Equal(t, 1, tt.embeddings.calls)
				require.Equal(t, tt.nowTime, tt.embeddings.gotNow)
				require.Equal(t, tt.batchSize, tt.embeddings.gotBatch)
			}

			if tt.usage == nil {
				return
			}
			require.Len(t, tt.usage.cutoffs, tt.wantUsageCalls)
			if tt.wantCutoff != nil {
				for i, cutoff := range tt.usage.cutoffs {
					require.True(t, cutoff.Equal(*tt.wantCutoff), "call %d cutoff = %v, want %v", i, cutoff, *tt.wantCutoff)
					require.Equal(t, tt.batchSize, tt.usage.batches[i])
				}
			}
		})
	}
}

func TestPluginRunDataRetentionHook(t *testing.T) {
	nowMillis := time.Date(2026, 7, 17, 10, 30, 0, 0, time.UTC).UnixMilli()

	cfgWith := func(enabled bool) *model.Config {
		cfg := &model.Config{}
		cfg.DataRetentionSettings.EnableMessageDeletion = model.NewPointer(enabled)
		return cfg
	}

	tests := []struct {
		name string
		cfg  *model.Config
	}{
		{name: "disabled in server config → (0, nil)", cfg: cfgWith(false)},
		{name: "nil config → (0, nil)", cfg: nil},
		{name: "enabled with nil store → (0, nil)", cfg: cfgWith(true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("GetConfig").Return(tt.cfg)
			mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Maybe()
			mockAPI.On("LogDebug", mock.Anything).Maybe()

			p := &Plugin{pluginAPI: pluginapi.NewClient(mockAPI, nil)}
			p.SetAPI(mockAPI)

			count, err := p.RunDataRetention(nowMillis, 3000)
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func fullBatchResults(n int, batchSize int64) []usageDeleteResult {
	out := make([]usageDeleteResult, n)
	for i := range out {
		out[i].n = batchSize
	}
	return out
}
