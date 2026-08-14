// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
)

func retentionDaysPtr(days int) *int {
	return &days
}

func retentionDaysValue(days *int) int {
	if days == nil {
		return 0
	}
	return *days
}

type retentionSnapshot struct {
	days  int
	floor int64
	model *ModelInfo
}

// snapshotRetention reads plugin config once and derives the retention
// window and model metadata from that same value.
func (s *Indexer) snapshotRetention(nowMillis int64) retentionSnapshot {
	if s.configGetter == nil {
		return retentionSnapshot{}
	}
	cfg := s.configGetter()
	days := cfg.GetIndexRetentionDays()
	return retentionSnapshot{
		days:  days,
		floor: cfg.IndexRetentionFloor(nowMillis),
		model: modelInfoFromConfig(cfg),
	}
}

func modelInfoFromConfig(cfg embeddings.EmbeddingSearchConfig) *ModelInfo {
	days := cfg.GetIndexRetentionDays()
	return &ModelInfo{
		ProviderType:       cfg.GetProviderType(),
		ModelName:          cfg.GetModelName(),
		Dimensions:         cfg.Dimensions,
		HNSWM:              cfg.GetHNSWM(),
		VectorElementType:  cfg.GetVectorElementType(),
		IndexRetentionDays: &days,
	}
}

func (s *Indexer) retentionFloorNow() int64 {
	return s.retentionFloorAt(time.Now().UnixMilli())
}

func (s *Indexer) retentionFloorAt(nowMillis int64) int64 {
	if s.configGetter == nil {
		return 0
	}
	cfg := s.configGetter()
	return cfg.IndexRetentionFloor(nowMillis)
}

func bumpCursorToFloor(cursor Cursor, floor int64) Cursor {
	if floor > 0 && cursor.LastCreateAt < floor {
		return Cursor{LastCreateAt: floor, LastID: ""}
	}
	return cursor
}

// retentionWindowWider reports whether currentDays covers more history than
// storedDays. 0 means unbounded (all posts).
func retentionWindowWider(currentDays, storedDays int) bool {
	if currentDays <= 0 {
		return storedDays > 0
	}
	if storedDays <= 0 {
		return false
	}
	return currentDays > storedDays
}

func retentionWindowNarrower(currentDays, storedDays int) bool {
	if storedDays <= 0 {
		return currentDays > 0
	}
	if currentDays <= 0 {
		return false
	}
	return currentDays < storedDays
}

func appendCreateAtFloor(query string, args []any, floor int64, column string) (string, []any) {
	if floor <= 0 {
		return query, args
	}
	n := len(args) + 1
	return query + fmt.Sprintf(" AND %s >= $%d", column, n), append(args, floor)
}

func indexablePostsFetchSQL(floor int64, skipExisting bool) string {
	q := postFetchBase
	if floor > 0 {
		q += " AND Posts.CreateAt >= $5"
	}
	if skipExisting {
		q += `
		AND NOT EXISTS (
			SELECT 1 FROM llm_posts_embeddings e WHERE e.post_id = Posts.Id
		)`
	}
	return q + postFetchTail
}

func editedPostsFetchSQL(floor int64) string {
	q := editedPostsFetchBase
	if floor > 0 {
		q += " AND Posts.CreateAt >= $5"
	}
	return q + editedPostsFetchTail
}
