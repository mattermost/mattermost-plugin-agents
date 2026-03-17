// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/format"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	JobStatusRunning   = "running"
	JobStatusCompleted = "completed"
	JobStatusFailed    = "failed"
	JobStatusCanceled  = "canceled"

	defaultBatchSize        = 100
	maxRetries              = 3
	maxIncrementalRetries   = 2
	incrementalRetryBackoff = 500 * time.Millisecond

	// KV store keys
	ReindexJobKey         = "reindex_job_status"
	IndexerCursorKey      = "indexer_cursor"
	IndexerModelKey       = "indexer_model_info"
	IndexerLastIndexedKey = "indexer_last_indexed_ts"
)

// PostRecord represents a post record from the database
type PostRecord struct {
	ID       string `db:"id"`
	Message  string `db:"message"`
	Props    string `db:"props"`
	UserID   string `db:"userid"`
	CreateAt int64  `db:"createat"`
	TeamID   string `db:"teamid"`

	ChannelID   string `db:"channelid"`
	ChannelName string `db:"channelname"`
	ChannelType string `db:"channeltype"`
}

// JobStatus represents the status of a reindex job
type JobStatus struct {
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
	ProcessedRows int64     `json:"processed_rows"`
	TotalRows     int64     `json:"total_rows"`
	Resumable     bool      `json:"resumable"`
	ErrorCount    int       `json:"error_count"`
	NodeID        string    `json:"node_id,omitempty"`
	CutoffAt      int64     `json:"cutoff_at,omitempty"`
	LastUpdatedAt time.Time `json:"last_updated_at,omitempty"`
	IsStale       bool      `json:"is_stale"`
}

// Cursor stores the cursor position for resumable indexing
type Cursor struct {
	LastCreateAt int64  `json:"last_create_at"`
	LastID       string `json:"last_id"`
}

// ModelInfo stores the model configuration used when indexing
type ModelInfo struct {
	ProviderType string `json:"provider_type"`
	ModelName    string `json:"model_name"`
	Dimensions   int    `json:"dimensions"`
	IndexedAt    int64  `json:"indexed_at"`
}

// HealthCheckResult represents the result of an index health check
type HealthCheckResult struct {
	DBPostCount      int64     `json:"db_post_count"`
	IndexedPostCount int64     `json:"indexed_post_count"`
	MissingPosts     int64     `json:"missing_posts"`
	Status           string    `json:"status"` // "healthy", "needs_reindex", "mismatch"
	CheckedAt        time.Time `json:"checked_at"`
	Error            string    `json:"error,omitempty"`

	// Model compatibility fields
	ModelCompatible    bool   `json:"model_compatible"`
	ModelNeedsReindex  bool   `json:"model_needs_reindex"`
	ModelCompatReason  string `json:"model_compat_reason,omitempty"`
	StoredProviderType string `json:"stored_provider_type,omitempty"`
	StoredDimensions   int    `json:"stored_dimensions,omitempty"`
	StoredModelName    string `json:"stored_model_name,omitempty"`
}

// batchProcessor provides shared batch processing logic for reindex and catch-up passes
type batchProcessor struct {
	indexer        *Indexer
	jobStatus      *JobStatus
	search         embeddings.EmbeddingSearch
	processedCount int64
	lastSavedCount int64
}

// processBatch processes a batch of posts: filters, stores, updates progress and heartbeat
func (bp *batchProcessor) processBatch(ctx context.Context, posts []PostRecord) error {
	// Filter and create documents
	docs := bp.indexer.filterAndCreateDocs(posts)

	// Store with retry
	if len(docs) > 0 {
		if err := bp.indexer.storeWithRetry(ctx, docs, bp.search); err != nil {
			return err
		}
	}

	// Update progress
	bp.processedCount += int64(len(posts))
	bp.jobStatus.ProcessedRows = bp.processedCount

	// Update heartbeat
	bp.jobStatus.LastUpdatedAt = time.Now()

	// Save checkpoint every 500 posts
	if bp.processedCount >= bp.lastSavedCount+500 {
		bp.indexer.saveJobStatus(bp.jobStatus)
		bp.lastSavedCount = bp.processedCount
	}

	return nil
}

// ModelCompatibility represents the result of checking model compatibility
type ModelCompatibility struct {
	Compatible         bool   `json:"compatible"`
	NeedsReindex       bool   `json:"needs_reindex"`
	Reason             string `json:"reason,omitempty"`
	StoredProviderType string `json:"stored_provider_type,omitempty"`
	StoredDimensions   int    `json:"stored_dimensions,omitempty"`
	StoredModelName    string `json:"stored_model_name,omitempty"`
}

// runReindexJob runs the reindexing process
func (s *Indexer) runReindexJob(jobStatus *JobStatus, clearIndex bool) { //nolint:gocognit
	defer func() {
		if r := recover(); r != nil {
			s.pluginAPI.LogError("Reindex job panicked", "panic", r)
			jobStatus.Status = JobStatusFailed
			jobStatus.Error = fmt.Sprintf("Job panicked: %v", r)
			jobStatus.CompletedAt = time.Now()
			s.saveJobStatus(jobStatus)
		}
	}()

	// Snapshot search at job start for consistency throughout the entire job
	if s.getSearch == nil {
		jobStatus.Status = JobStatusFailed
		jobStatus.Error = "Search not configured"
		jobStatus.CompletedAt = time.Now()
		s.saveJobStatus(jobStatus)
		return
	}
	search := s.getSearch()
	if search == nil {
		jobStatus.Status = JobStatusFailed
		jobStatus.Error = "Search not configured"
		jobStatus.CompletedAt = time.Now()
		s.saveJobStatus(jobStatus)
		return
	}

	ctx := context.Background()

	// Only clear the index if explicitly requested (full reindex)
	if clearIndex {
		if err := search.Clear(ctx); err != nil {
			jobStatus.Status = JobStatusFailed
			jobStatus.Error = fmt.Sprintf("Failed to clear search index: %s", err)
			jobStatus.CompletedAt = time.Now()
			s.saveJobStatus(jobStatus)
			return
		}
	}

	// Load cursor for resumable operation
	cursor := s.loadCursor()

	var posts []PostRecord
	lastCreateAt := cursor.LastCreateAt
	lastID := cursor.LastID
	processedCount := jobStatus.ProcessedRows // Resume from previous count if resuming
	lastSavedCount := processedCount

	for {
		// Check if the job was canceled
		var currentStatus JobStatus
		if err := s.pluginAPI.KVGet(ReindexJobKey, &currentStatus); err == nil {
			if currentStatus.Status == JobStatusCanceled {
				s.pluginAPI.LogWarn("Reindex job was canceled")
				return
			}
		}

		// Run a batch of indexing
		// Use cutoff timestamp to prevent race gap with posts created during reindexing
		query := `SELECT
			Posts.Id as id,
			Posts.Message as message,
			Posts.Props as props,
			Posts.UserId as userid,
			Posts.ChannelId as channelid,
			Posts.CreateAt as createat,
			Channels.TeamId as teamid,
			Channels.Name as channelname,
			Channels.Type as channeltype
		FROM Posts
		LEFT JOIN Channels ON Posts.ChannelId = Channels.Id
		WHERE Posts.DeleteAt = 0
			AND (Posts.Message != '' OR Posts.Props::text LIKE '%"attachments"%')
			AND Posts.Type = ''
			AND (Posts.CreateAt, Posts.Id) > ($1, $2)
			AND Posts.CreateAt <= $3
		ORDER BY Posts.CreateAt ASC, Posts.Id ASC
		LIMIT $4`

		err := s.db.Select(&posts, query, lastCreateAt, lastID, jobStatus.CutoffAt, defaultBatchSize)
		if err != nil {
			s.handleJobError(jobStatus, fmt.Sprintf("Failed to fetch posts: %s", err), lastCreateAt, lastID)
			return
		}

		if len(posts) == 0 {
			break
		}

		// Process batch and index posts
		docs := s.filterAndCreateDocs(posts)

		// Store the batch with retry logic
		if len(docs) > 0 {
			if err := s.storeWithRetry(ctx, docs, search); err != nil {
				s.handleJobError(jobStatus, fmt.Sprintf("Failed to store documents: %s", err), lastCreateAt, lastID)
				return
			}
		}

		// Update progress
		processedCount += int64(len(posts))
		jobStatus.ProcessedRows = processedCount

		// Update cursors for next batch
		lastPost := posts[len(posts)-1]
		lastCreateAt = lastPost.CreateAt
		lastID = lastPost.ID

		// Update heartbeat timestamp every batch
		jobStatus.LastUpdatedAt = time.Now()

		// Save cursor and progress every 500 additional processed records
		if processedCount >= lastSavedCount+500 {
			s.saveCursor(Cursor{LastCreateAt: lastCreateAt, LastID: lastID})
			s.saveJobStatus(jobStatus)
			s.pluginAPI.LogWarn("Reindexing progress",
				"processed", processedCount,
				"estimated_total", jobStatus.TotalRows)
			lastSavedCount = processedCount
		}
	}

	// Run catch-up pass to index posts created during the main reindex
	catchUpCount, catchUpCursor, catchUpErr := s.runCatchUpPass(ctx, jobStatus, search)
	if catchUpErr != nil {
		s.handleJobError(jobStatus, fmt.Sprintf("Catch-up pass failed: %s", catchUpErr), catchUpCursor.LastCreateAt, catchUpCursor.LastID)
		return
	}
	if catchUpCount > 0 {
		s.pluginAPI.LogWarn("Catch-up pass completed", "catch_up_posts", catchUpCount)
	}

	// Completed successfully
	jobStatus.Status = JobStatusCompleted
	jobStatus.CompletedAt = time.Now()
	s.saveJobStatus(jobStatus)

	// Clear the cursor on successful completion
	_ = s.pluginAPI.KVDelete(IndexerCursorKey)

	// Update last indexed timestamp to now (after catch-up pass)
	s.saveLastIndexedTimestamp(time.Now().UnixMilli())

	// Save model info after a successful full reindex
	if clearIndex {
		if modelInfo := s.getModelInfoFromConfig(); modelInfo != nil {
			if err := s.SaveModelInfo(*modelInfo); err != nil {
				s.pluginAPI.LogError("Failed to save model info after reindex", "error", err)
			}
		}
	}

	s.pluginAPI.LogWarn("Reindexing completed", "processed_posts", processedCount)
}

// filterAndCreateDocs filters posts and creates PostDocuments
func (s *Indexer) filterAndCreateDocs(posts []PostRecord) []embeddings.PostDocument {
	docs := make([]embeddings.PostDocument, 0, len(posts))
	for _, post := range posts {
		modelPost := &model.Post{
			Id:        post.ID,
			ChannelId: post.ChannelID,
			UserId:    post.UserID,
			Message:   post.Message,
			Type:      model.PostTypeDefault,
			DeleteAt:  0,
		}

		// Parse Props JSON to populate attachments
		if post.Props != "" {
			var props model.StringInterface
			if err := json.Unmarshal([]byte(post.Props), &props); err == nil {
				modelPost.SetProps(props)
			}
		}

		channel := &model.Channel{
			Id:     post.ChannelID,
			TeamId: post.TeamID,
			Name:   post.ChannelName,
			Type:   model.ChannelType(post.ChannelType),
		}

		if !s.shouldIndexPost(modelPost, channel) {
			continue
		}

		docs = append(docs, embeddings.PostDocument{
			PostID:    modelPost.Id,
			CreateAt:  post.CreateAt,
			TeamID:    post.TeamID,
			ChannelID: post.ChannelID,
			UserID:    post.UserID,
			Content:   format.PostBody(modelPost),
		})
	}
	return docs
}

// storeWithRetry stores documents with retry logic
func (s *Indexer) storeWithRetry(ctx context.Context, docs []embeddings.PostDocument, search embeddings.EmbeddingSearch) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		err := search.Store(ctx, docs)
		if err == nil {
			return nil
		}
		lastErr = err

		s.pluginAPI.LogWarn("Embedding store failed, retrying",
			"attempt", attempt+1,
			"max_retries", maxRetries,
			"error", err)

		// Exponential backoff
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}
	return lastErr
}

// handleJobError handles a job error by saving cursor and updating status
func (s *Indexer) handleJobError(jobStatus *JobStatus, errMsg string, lastCreateAt int64, lastID string) {
	jobStatus.Status = JobStatusFailed
	jobStatus.Error = errMsg
	jobStatus.CompletedAt = time.Now()
	jobStatus.ErrorCount++

	// Save cursor so job can be resumed
	s.saveCursor(Cursor{LastCreateAt: lastCreateAt, LastID: lastID})
	s.saveJobStatus(jobStatus)
}

// loadCursor loads the cursor from KV store
func (s *Indexer) loadCursor() Cursor {
	var cursor Cursor
	err := s.pluginAPI.KVGet(IndexerCursorKey, &cursor)
	if err != nil {
		return Cursor{LastCreateAt: 0, LastID: ""}
	}
	return cursor
}

// saveCursor saves the cursor to KV store
func (s *Indexer) saveCursor(cursor Cursor) {
	if err := s.pluginAPI.KVSet(IndexerCursorKey, cursor); err != nil {
		s.pluginAPI.LogError("Failed to save cursor", "error", err)
	}
}

// saveLastIndexedTimestamp saves the last indexed timestamp
func (s *Indexer) saveLastIndexedTimestamp(ts int64) {
	if err := s.pluginAPI.KVSet(IndexerLastIndexedKey, ts); err != nil {
		s.pluginAPI.LogError("Failed to save last indexed timestamp", "error", err)
	}
}

// getLastIndexedTimestamp retrieves the last indexed timestamp
func (s *Indexer) getLastIndexedTimestamp() int64 {
	var timestamp int64
	err := s.pluginAPI.KVGet(IndexerLastIndexedKey, &timestamp)
	if err != nil {
		return 0
	}
	return timestamp
}

// saveJobStatus saves the job status to KV store
func (s *Indexer) saveJobStatus(status *JobStatus) {
	if err := s.pluginAPI.KVSet(ReindexJobKey, status); err != nil {
		s.pluginAPI.LogError("Failed to save job status", "error", err)
	}
}

// runCatchUpPass indexes posts created after the cutoff timestamp during the main reindex.
// Returns the number of posts processed, the cursor position at time of return, and any error.
func (s *Indexer) runCatchUpPass(ctx context.Context, jobStatus *JobStatus, search embeddings.EmbeddingSearch) (int64, Cursor, error) {
	if jobStatus.CutoffAt == 0 {
		return 0, Cursor{}, nil
	}

	// Capture upper bound so new posts arriving during catch-up don't make the loop unbounded.
	// Posts created after this point will be picked up by the incremental indexer.
	catchUpCutoff := time.Now().UnixMilli()

	bp := &batchProcessor{
		indexer:        s,
		jobStatus:      jobStatus,
		search:         search,
		processedCount: jobStatus.ProcessedRows,
		lastSavedCount: jobStatus.ProcessedRows,
	}

	var posts []PostRecord
	var catchUpCount int64
	lastCreateAt := jobStatus.CutoffAt
	lastID := ""

	for {
		// Query posts created after the cutoff
		query := `SELECT
			Posts.Id as id,
			Posts.Message as message,
			Posts.Props as props,
			Posts.UserId as userid,
			Posts.ChannelId as channelid,
			Posts.CreateAt as createat,
			Channels.TeamId as teamid,
			Channels.Name as channelname,
			Channels.Type as channeltype
		FROM Posts
		LEFT JOIN Channels ON Posts.ChannelId = Channels.Id
		WHERE Posts.DeleteAt = 0
			AND (Posts.Message != '' OR Posts.Props::text LIKE '%"attachments"%')
			AND Posts.Type = ''
			AND (Posts.CreateAt, Posts.Id) > ($1, $2)
			AND Posts.CreateAt <= $3
		ORDER BY Posts.CreateAt ASC, Posts.Id ASC
		LIMIT $4`

		err := s.db.SelectContext(ctx, &posts, query, lastCreateAt, lastID, catchUpCutoff, defaultBatchSize)
		if err != nil {
			return catchUpCount, Cursor{LastCreateAt: lastCreateAt, LastID: lastID}, fmt.Errorf("failed to fetch catch-up posts: %w", err)
		}

		if len(posts) == 0 {
			break
		}

		// Process batch (filters, stores, updates heartbeat and saves progress)
		if err := bp.processBatch(ctx, posts); err != nil {
			return catchUpCount, Cursor{LastCreateAt: lastCreateAt, LastID: lastID}, fmt.Errorf("failed to store catch-up documents: %w", err)
		}

		catchUpCount += int64(len(posts))

		// Update cursor for next batch
		lastPost := posts[len(posts)-1]
		lastCreateAt = lastPost.CreateAt
		lastID = lastPost.ID
	}

	return catchUpCount, Cursor{LastCreateAt: lastCreateAt, LastID: lastID}, nil
}
