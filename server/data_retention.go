// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"errors"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// embeddingsRetentionRunner is the subset of *indexer.Indexer used by data retention.
type embeddingsRetentionRunner interface {
	RunDataRetention(ctx context.Context, nowTime, batchSize int64) (int64, error)
}

// usageRetentionStore is the subset of *store.Store used by data retention.
type usageRetentionStore interface {
	DeleteUsageBefore(ctx context.Context, cutoff time.Time, batchSize int64) (int64, error)
}

// retentionLogger is the subset of logging used by data retention.
// Satisfied by *pluginLogger (server/main.go), which is nil-service-safe.
type retentionLogger interface {
	Debug(message string, keyValuePairs ...any)
	Info(message string, keyValuePairs ...any)
	Error(message string, keyValuePairs ...any)
}

// maxUsageRetentionBatches bounds the internal delete loop. LLM_Usage_Daily grows by at most
// (active users x agents) rows per day, so steady-state nightly deletion removes roughly one
// day's worth of rows — almost always a single partial batch. The bound only matters on the
// first run after retention is enabled (a backlog of many days): 100 batches x 3000 rows
// clears 300k rows in one night, and the nightly job re-runs, so any remainder drains on
// subsequent runs. The bound guarantees termination and keeps one plugin from monopolizing
// the shared retention job.
const maxUsageRetentionBatches = 100

func (p *Plugin) RunDataRetention(nowTime, batchSize int64) (int64, error) {
	// Typed-nil guard: assigning a nil *indexer.Indexer straight into the
	// interface would make it non-nil and panic on the nil receiver.
	var embeddings embeddingsRetentionRunner
	if p.indexerService != nil {
		embeddings = p.indexerService
	}
	var usage usageRetentionStore
	if p.store != nil {
		usage = p.store
	}
	var settings *model.DataRetentionSettings
	if cfg := p.pluginAPI.Configuration.GetConfig(); cfg != nil {
		settings = &cfg.DataRetentionSettings
	}

	// context.Background(): accepted exception, the hook has no ctx (master plan Correction 6).
	return runDataRetention(context.Background(), embeddings, usage, settings,
		&pluginLogger{service: &p.pluginAPI.Log}, nowTime, batchSize)
}

// runDataRetention performs both retention cleanups. Each cleanup runs regardless of whether
// the other fails; the results are a summed deleted count and an errors.Join of the failures.
func runDataRetention(
	ctx context.Context,
	embeddings embeddingsRetentionRunner,
	usage usageRetentionStore,
	settings *model.DataRetentionSettings,
	log retentionLogger,
	nowTime, batchSize int64,
) (int64, error) {
	var total int64
	var embErr, usageErr error

	// 1. Embeddings cleanup (behavior identical to the pre-phase-5 hook, minus early return).
	if embeddings != nil {
		var count int64
		count, embErr = embeddings.RunDataRetention(ctx, nowTime, batchSize)
		if embErr != nil {
			log.Error("Failed to run data retention for embeddings", "error", embErr)
		} else if count > 0 {
			log.Info("Data retention cleaned up orphaned embeddings", "deleted", count)
		}
		total += count
	}

	// 2. Usage-aggregate cleanup.
	switch {
	case usage == nil:
		// store not initialized; nothing to do
	case settings == nil || settings.EnableMessageDeletion == nil || !*settings.EnableMessageDeletion:
		log.Debug("Skipping usage aggregate retention: message deletion is disabled")
	default:
		cutoffDay := usageRetentionCutoffDay(nowTime, settings.GetMessageRetentionHours())
		var deleted int64
		deleted, usageErr = deleteUsageBatched(ctx, usage, cutoffDay, batchSize)
		if usageErr != nil {
			log.Error("Failed to run data retention for usage aggregates", "error", usageErr)
		}
		if deleted > 0 {
			log.Info("Data retention cleaned up usage aggregates", "deleted", deleted, "cutoff_day", cutoffDay.Format("2006-01-02"))
		}
		total += deleted
	}

	return total, errors.Join(embErr, usageErr)
}

// usageRetentionCutoffDay converts the job's wall-clock unix-millis "now" and the retention
// window in hours into a UTC calendar day. Rows with Day strictly BEFORE this day are purged;
// rows ON this day are retained, because the retention instant falls partway through it and a
// per-day aggregate row cannot be split. The row is deleted by a later nightly run once the
// retention instant passes beyond that calendar day.
func usageRetentionCutoffDay(nowTime int64, retentionHours int) time.Time {
	cutoffInstant := time.UnixMilli(nowTime).UTC().Add(-time.Duration(retentionHours) * time.Hour)
	return time.Date(cutoffInstant.Year(), cutoffInstant.Month(), cutoffInstant.Day(), 0, 0, 0, 0, time.UTC)
}

// deleteUsageBatched deletes rows older than cutoffDay in batches of batchSize, looping until
// a partial batch signals completion or maxUsageRetentionBatches is reached. Returns rows
// deleted so far even when it returns an error.
func deleteUsageBatched(ctx context.Context, usage usageRetentionStore, cutoffDay time.Time, batchSize int64) (int64, error) {
	var total int64
	for i := 0; i < maxUsageRetentionBatches; i++ {
		deleted, err := usage.DeleteUsageBefore(ctx, cutoffDay, batchSize)
		total += deleted
		if err != nil {
			return total, err
		}
		if deleted < batchSize || deleted == 0 {
			return total, nil
		}
	}
	return total, nil
}
