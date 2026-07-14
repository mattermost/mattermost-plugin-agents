// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	JobStatusRunning         = "running"
	JobStatusCancelRequested = "cancel_requested"
	JobStatusCompleted       = "completed"
	JobStatusFailed          = "failed"
	JobStatusCanceled        = "canceled"

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
	// JobID uniquely identifies a single run. Cancel checks and CAS
	// transitions are scoped to this ID so a stale read for a previous run
	// cannot affect the current one.
	JobID         string    `json:"job_id,omitempty"`
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

	// VectorIndexState is set while a deferred reindex owns the ANN index
	// lifecycle (index dropped or being rebuilt).
	VectorIndexState *VectorIndexState `json:"vector_index_state,omitempty"`
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

// Keyset pagination of indexable posts, bounded above by a cutoff timestamp
// to prevent a race gap with posts created during reindexing.
const (
	postFetchBase = `SELECT
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
		AND Posts.CreateAt <= $3`

	postFetchTail = `
	ORDER BY Posts.CreateAt ASC, Posts.Id ASC
	LIMIT $4`

	reindexFetchQuery = postFetchBase + postFetchTail

	// The catch-up pass skips posts the live hook has already indexed.
	catchUpFetchQuery = postFetchBase + `
		AND NOT EXISTS (
			SELECT 1 FROM llm_posts_embeddings e WHERE e.post_id = Posts.Id
		)` + postFetchTail

	// The edited-posts repair pass re-embeds posts updated while live
	// indexing was gated during the deferred index build; their stale rows
	// are deleted first, so the NOT EXISTS anti-join picks them up.
	editedPostsFetchQuery = postFetchBase + `
		AND Posts.UpdateAt >= $4
		AND NOT EXISTS (
			SELECT 1 FROM llm_posts_embeddings e WHERE e.post_id = Posts.Id
		)
	ORDER BY Posts.CreateAt ASC, Posts.Id ASC
	LIMIT $5`
)

// postFetcher builds a fetchFunc paging the given query up to cutoff.
func (s *Indexer) postFetcher(query string, cutoff int64) fetchFunc {
	return func(ctx context.Context, cursor Cursor, limit int) ([]PostRecord, error) {
		var posts []PostRecord
		err := s.db.SelectContext(ctx, &posts, query, cursor.LastCreateAt, cursor.LastID, cutoff, limit)
		return posts, err
	}
}

// runReindexJob runs the reindexing process. When deferRebuild is true, the
// ANN index was claimed for an index-after-load run: it is dropped before
// the bulk load and rebuilt once afterwards, and it must be restored on
// EVERY terminal transition (success, failure, cancel, panic).
// adoptedState reports that the claim was inherited from a previous run, so
// the index may already be genuinely dropped even before this run touches it.
func (s *Indexer) runReindexJob(jobStatus *JobStatus, clearIndex bool, deferRebuild bool, adoptedState bool) {
	// Shared with the panic-recovery defer so the index is restored even
	// when the job dies mid-flight.
	var bulk embeddings.BulkIndexer
	deferPending := false

	defer func() {
		if r := recover(); r != nil {
			s.pluginAPI.LogError("Reindex job panicked", "panic", r)
			errMsg := fmt.Sprintf("Job panicked: %v", r)
			if deferPending && bulk != nil {
				errMsg = s.restoreDeferredIndex(context.Background(), jobStatus, bulk, errMsg)
			}
			jobStatus.Status = JobStatusFailed
			jobStatus.Error = errMsg
			jobStatus.CompletedAt = time.Now()
			s.saveJobStatus(jobStatus)
		}
	}()

	// Snapshot search at job start for consistency throughout the entire job
	if s.getSearch == nil || s.getSearch() == nil {
		if deferRebuild {
			// The index was not dropped by this run; release a fresh claim
			// so search is not gated forever (an adopted claim is kept).
			s.abandonUndroppedClaim(jobStatus.JobID, adoptedState)
		}
		jobStatus.Status = JobStatusFailed
		jobStatus.Error = "Search not configured"
		jobStatus.CompletedAt = time.Now()
		s.saveJobStatus(jobStatus)
		return
	}
	search := s.getSearch()

	ctx := context.Background()

	if deferRebuild {
		bulk = bulkIndexerFor(search)
		if bulk == nil {
			// The search snapshot lost bulk index support between job start
			// and now (e.g. a concurrent config change). A fresh claim can
			// be cleared (the index was never dropped); an adopted claim
			// must stay so search remains gated over the missing index.
			s.pluginAPI.LogWarn("Vector store no longer supports deferred indexing; maintaining the index during reindex")
			s.abandonUndroppedClaim(jobStatus.JobID, adoptedState)
			deferRebuild = false
		}
	}

	if deferRebuild {
		// Fence the DDL on current ownership: a stale-reclaimed worker must
		// not drop the index under a successor run's claim.
		if s.ownsVectorIndexState(jobStatus.JobID) == nil {
			s.pluginAPI.LogWarn("Vector index state is no longer owned by this run; maintaining the index during reindex",
				"job_id", jobStatus.JobID)
			deferRebuild = false
		}
	}

	if deferRebuild {
		deferPending = true
		// Drop the ANN index before the bulk load. On a resume this also
		// clears a leftover invalid index from an interrupted build.
		if err := bulk.PrepareBulkIndex(ctx); err != nil {
			errMsg := s.restoreDeferredIndex(ctx, jobStatus, bulk, fmt.Sprintf("Failed to drop vector index: %s", err))
			deferPending = false
			jobStatus.Status = JobStatusFailed
			jobStatus.Error = errMsg
			jobStatus.CompletedAt = time.Now()
			s.saveJobStatus(jobStatus)
			return
		}
	}

	// Only clear the index if explicitly requested (full reindex)
	if clearIndex {
		if err := search.Clear(ctx); err != nil {
			errMsg := fmt.Sprintf("Failed to clear search index: %s", err)
			if deferPending {
				errMsg = s.restoreDeferredIndex(ctx, jobStatus, bulk, errMsg)
				deferPending = false
			}
			jobStatus.Status = JobStatusFailed
			jobStatus.Error = errMsg
			jobStatus.CompletedAt = time.Now()
			s.saveJobStatus(jobStatus)
			return
		}
	}

	// Load cursor for resumable operation
	cursor := s.loadCursor()

	workers, batchSize := s.reindexSettings()
	mainFetch := s.postFetcher(reindexFetchQuery, jobStatus.CutoffAt)
	_, watermark, err := s.runIndexPass(ctx, jobStatus, search, mainFetch, cursor, passOptions{workers: workers, batchSize: batchSize})
	if errors.Is(err, errCancelRequested) {
		if deferPending {
			// Restore the index before the cancel terminalizes the job. A
			// rebuild failure must surface on the canceled status, not just
			// in the server log.
			jobStatus.Error = s.restoreDeferredIndex(ctx, jobStatus, bulk, jobStatus.Error)
			deferPending = false
		}
		s.acknowledgeCancel(jobStatus)
		return
	}
	if err != nil {
		errMsg := fmt.Sprintf("Failed to index posts: %s", err)
		if deferPending {
			errMsg = s.restoreDeferredIndex(ctx, jobStatus, bulk, errMsg)
			deferPending = false
		}
		s.handleJobError(jobStatus, errMsg, watermark.LastCreateAt, watermark.LastID)
		return
	}

	// Build the index BEFORE the catch-up pass on purpose: live-post
	// indexing is gated while the build runs (writes would block on plain
	// CREATE INDEX), and the catch-up pass afterwards sweeps everything
	// missed during both the main pass and the build window because it
	// scans from the original cutoff with NOT EXISTS.
	if deferPending {
		buildStartedAt, buildErr := s.finalizeDeferredIndex(ctx, jobStatus, bulk)
		if buildErr != nil {
			// The phase state stays in place (reverted to dropped) so the
			// condition is visible via the health check; a resume rebuilds
			// the index at the end.
			deferPending = false
			s.handleJobError(jobStatus, fmt.Sprintf("Failed to rebuild vector index: %s", buildErr), watermark.LastCreateAt, watermark.LastID)
			return
		}
		deferPending = false
		// A cancel requested during the build could not interrupt the DDL;
		// acknowledge it now that the index is present again.
		if canceled, cancelErr := s.isCancelRequested(jobStatus.JobID); cancelErr == nil && canceled {
			s.acknowledgeCancel(jobStatus)
			return
		}
		// Posts EDITED while live indexing was gated kept their stale
		// pre-edit embeddings (catch-up's NOT EXISTS only repairs posts
		// with no rows at all). Drop and re-embed them before catch-up.
		if repairErr := s.reindexEditedPosts(ctx, jobStatus, search, buildStartedAt); repairErr != nil {
			if errors.Is(repairErr, errCancelRequested) {
				s.acknowledgeCancel(jobStatus)
				return
			}
			s.handleJobError(jobStatus, fmt.Sprintf("Failed to re-index posts edited during the index build: %s", repairErr), watermark.LastCreateAt, watermark.LastID)
			return
		}
	}

	// Run catch-up pass to index posts created during the main reindex
	catchUpCount, catchUpCursor, catchUpErr := s.runCatchUpPass(ctx, jobStatus, search)
	if errors.Is(catchUpErr, errCancelRequested) {
		s.acknowledgeCancel(jobStatus)
		return
	}
	if catchUpErr != nil {
		s.handleJobError(jobStatus, fmt.Sprintf("Catch-up pass failed: %s", catchUpErr), catchUpCursor.LastCreateAt, catchUpCursor.LastID)
		return
	}
	if catchUpCount > 0 {
		s.pluginAPI.LogWarn("Catch-up pass completed", "catch_up_posts", catchUpCount)
	}

	// Resolve the terminal state; a cancel that raced with completion wins,
	// in which case the cursor and model info are left for a resume.
	if !s.finishJob(jobStatus) {
		return
	}

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

	s.pluginAPI.LogWarn("Reindexing completed", "processed_posts", jobStatus.ProcessedRows)
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

// saveJobStatus persists the worker's view of the job, gated on JobID match
// so a worker whose row has been claimed by a newer run does not clobber it.
// A status with no JobID falls back to an unconditional set.
//
// The cancel-survival guard closes a race in which the admin's
// CancelJob CAS lands before this worker's KVGet: without it, the
// worker would read cancel_requested and then CAS back to running,
// silently erasing the cancel. The dropped write leaves the KV row
// as cancel_requested for the pass fetcher's next cancel poll to
// observe.
func (s *Indexer) saveJobStatus(status *JobStatus) {
	if status.JobID == "" {
		if err := s.pluginAPI.KVSet(ReindexJobKey, status); err != nil {
			s.pluginAPI.LogError("Failed to save job status", "error", err)
		}
		return
	}

	var current JobStatus
	err := s.pluginAPI.KVGet(ReindexJobKey, &current)
	if err != nil && !mmapi.IsKVNotFound(err) {
		s.pluginAPI.LogError("Failed to read job status before save", "error", err)
		return
	}

	if err == nil && current.JobID != "" && current.JobID != status.JobID {
		s.pluginAPI.LogWarn("Reindex worker superseded by a newer run, dropping status write",
			"worker_job_id", status.JobID,
			"current_job_id", current.JobID)
		return
	}

	if err == nil && current.JobID == status.JobID &&
		current.Status == JobStatusCancelRequested && status.Status == JobStatusRunning {
		status.Status = JobStatusCancelRequested
		return
	}

	var oldValue interface{}
	if err == nil {
		oldValue = current
	}
	ok, casErr := s.pluginAPI.KVCompareAndSet(ReindexJobKey, oldValue, *status)
	if casErr != nil {
		s.pluginAPI.LogError("Failed to save job status", "error", casErr)
		return
	}
	if !ok {
		s.pluginAPI.LogWarn("Reindex job status write lost a CAS race; will retry on next iteration",
			"worker_job_id", status.JobID)
	}
}

// finishJob resolves a finished worker's terminal state: running -> completed
// normally, but a cancel that landed after the worker's last poll wins and
// lands in canceled instead. CAS races (e.g. with a concurrent CancelJob) are
// retried until a terminal state is persisted or the run is superseded.
// Returns true only when completion won, so the caller runs completion side
// effects (cursor cleanup, model info) exclusively for a real completion.
func (s *Indexer) finishJob(jobStatus *JobStatus) bool {
	// Without a JobID there is no CAS protocol to arbitrate; mirror
	// saveJobStatus's unconditional-set fallback.
	if jobStatus.JobID == "" {
		jobStatus.Status = JobStatusCompleted
		jobStatus.CompletedAt = time.Now()
		s.saveJobStatus(jobStatus)
		return true
	}

	const maxAttempts = 5
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var current JobStatus
		err := s.pluginAPI.KVGet(ReindexJobKey, &current)
		if err != nil && !mmapi.IsKVNotFound(err) {
			s.pluginAPI.LogError("Failed to read job status before completion", "error", err)
			return false
		}
		if err == nil && current.JobID != "" && current.JobID != jobStatus.JobID {
			s.pluginAPI.LogWarn("Reindex worker superseded by a newer run, dropping completion",
				"worker_job_id", jobStatus.JobID,
				"current_job_id", current.JobID)
			return false
		}

		newStatus := *jobStatus
		completed := false
		switch {
		case err != nil || current.Status == JobStatusRunning:
			newStatus.Status = JobStatusCompleted
			completed = true
		case current.Status == JobStatusCancelRequested:
			newStatus.Status = JobStatusCanceled
		default:
			// Another actor already recorded a terminal state (e.g. orphan
			// reclaim marked the row failed); keep it and skip completion
			// side effects so the row stays monotonic and resumable.
			s.pluginAPI.LogWarn("Reindex job already in a terminal state at completion, preserving it",
				"job_id", jobStatus.JobID,
				"status", current.Status)
			return false
		}
		newStatus.CompletedAt = time.Now()

		var oldValue interface{}
		if err == nil {
			oldValue = current
		}
		ok, casErr := s.pluginAPI.KVCompareAndSet(ReindexJobKey, oldValue, newStatus)
		if casErr != nil {
			s.pluginAPI.LogError("Failed to save terminal job status", "error", casErr)
			return false
		}
		if ok {
			jobStatus.Status = newStatus.Status
			jobStatus.CompletedAt = newStatus.CompletedAt
			if !completed {
				s.pluginAPI.LogWarn("Reindex job was canceled at completion")
			}
			return completed
		}
		// Lost a CAS race (e.g. an admin cancel landed between the read and
		// the write, possibly against a lagging replica read); back off so
		// the row converges before re-reading.
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt) * finishRetryBaseDelay)
		}
	}
	// Leave the row as-is; stale-job detection reclaims it as resumable.
	s.pluginAPI.LogError("Failed to persist terminal reindex job status after retries",
		"job_id", jobStatus.JobID)
	return false
}

// runCatchUpPass indexes posts created after the cutoff timestamp during the main reindex.
// Returns the number of posts processed, the watermark cursor at time of return, and any error.
func (s *Indexer) runCatchUpPass(ctx context.Context, jobStatus *JobStatus, search embeddings.EmbeddingSearch) (int64, Cursor, error) {
	if jobStatus.CutoffAt == 0 {
		return 0, Cursor{}, nil
	}

	// Capture upper bound so new posts arriving during catch-up don't make the loop unbounded.
	// Posts created after this point will be picked up by the incremental indexer.
	catchUpCutoff := time.Now().UnixMilli()

	// Run sequentially: with a single worker nothing is ever stored ahead of
	// the watermark, so the NOT EXISTS filter can't skip-and-undercount posts
	// a failed run had stored past its checkpoint. Catch-up covers only the
	// reindex window, so main-pass concurrency isn't needed here.
	_, batchSize := s.reindexSettings()
	catchUpFetch := s.postFetcher(catchUpFetchQuery, catchUpCutoff)
	startCursor := Cursor{LastCreateAt: jobStatus.CutoffAt, LastID: ""}
	return s.runIndexPass(ctx, jobStatus, search, catchUpFetch, startCursor, passOptions{workers: 1, batchSize: batchSize})
}

// reindexEditedPosts repairs posts edited while live indexing was gated
// during the deferred index build: an edited already-indexed post keeps its
// stale pre-edit embedding, which the catch-up pass's NOT EXISTS anti-join
// never touches. Stale rows for posts updated since the gate engaged are
// deleted first, then a sequential pass re-embeds those posts (the NOT
// EXISTS filter picks up exactly the deleted ones). Filtering on
// Posts.UpdateAt rather than EditAt is deliberate: props/attachment updates
// (which change the embedded content) bump UpdateAt without setting EditAt.
// The over-match (e.g. reaction-flag updates) is bounded by the build window
// and only costs re-embedding.
func (s *Indexer) reindexEditedPosts(ctx context.Context, jobStatus *JobStatus, search embeddings.EmbeddingSearch, since int64) error {
	if since <= 0 {
		return nil
	}

	// Deleting first also covers posts edited into a non-indexable form
	// (e.g. message cleared): those must not keep stale embeddings even
	// though the re-embed pass filters them out.
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM llm_posts_embeddings e
		USING Posts p
		WHERE e.post_id = p.Id AND p.UpdateAt >= $1`, since); err != nil {
		return fmt.Errorf("failed to delete stale embeddings for edited posts: %w", err)
	}

	// Bound the sweep so posts still being edited don't extend it; anything
	// after this point flows through normal live indexing (the gate is off).
	upperBound := time.Now().UnixMilli()
	_, batchSize := s.reindexSettings()
	fetch := func(fetchCtx context.Context, cursor Cursor, limit int) ([]PostRecord, error) {
		var posts []PostRecord
		err := s.db.SelectContext(fetchCtx, &posts, editedPostsFetchQuery,
			cursor.LastCreateAt, cursor.LastID, upperBound, since, limit)
		return posts, err
	}
	_, _, err := s.runIndexPass(ctx, jobStatus, search, fetch, Cursor{}, passOptions{workers: 1, batchSize: batchSize})
	return err
}
