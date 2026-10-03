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

	// JobPhaseBuildingIndex is set while FinalizeBulkIndex (CREATE INDEX) runs.
	JobPhaseBuildingIndex = "building_index"

	// KV store keys
	ReindexJobKey         = "reindex_job_status"
	IndexerCursorKey      = "indexer_cursor"
	IndexerModelKey       = "indexer_model_info"
	IndexerLastIndexedKey = "indexer_last_indexed_ts"

	// JobOperationReindex embeds posts. JobOperationRebuildVectorIndex
	// drops and recreates HNSW without re-embedding. JobOperationCatchUp
	// fills holes (NOT EXISTS) without re-embedding rows already stored.
	JobOperationReindex            = "reindex"
	JobOperationRebuildVectorIndex = "rebuild_vector_index"
	JobOperationCatchUp            = "catch_up"
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

	// Set only by the edited-posts repair query (keyset on UpdateAt).
	UpdateAt int64 `db:"updateat"`
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
	CompletedAt   time.Time `json:"completed_at"`
	ProcessedRows int64     `json:"processed_rows"`
	TotalRows     int64     `json:"total_rows"`
	Resumable     bool      `json:"resumable"`
	ErrorCount    int       `json:"error_count"`
	NodeID        string    `json:"node_id,omitempty"`
	CutoffAt      int64     `json:"cutoff_at,omitempty"`
	LastUpdatedAt time.Time `json:"last_updated_at"`
	IsStale       bool      `json:"is_stale"`
	// Phase is a short-lived UI hint (e.g. JobPhaseBuildingIndex); empty otherwise.
	Phase string `json:"phase,omitempty"`
	// Operation is JobOperationReindex, JobOperationRebuildVectorIndex, or JobOperationCatchUp.
	Operation string `json:"operation,omitempty"`
	// ModelInfo is captured at job start and written to IndexerModelKey on
	// successful completion so a resumed run unlocks compatibility for the
	// model it actually indexed with (not whatever is configured at finish).
	ModelInfo *ModelInfo `json:"model_info,omitempty"`
	// RetentionFloor is the inclusive CreateAt lower bound snapshotted at
	// job start. Mid-job config changes do not alter this run; abort and
	// start a new job if the window should change.
	RetentionFloor int64 `json:"retention_floor,omitempty"`
	// IndexRetentionDays is the retention window this job actually covered.
	// Catch-up success writes this value onto stored ModelInfo without
	// rewriting provider, model, or dimensions.
	IndexRetentionDays int `json:"index_retention_days,omitempty"`
}

func (js *JobStatus) isRebuild() bool {
	return js != nil && js.Operation == JobOperationRebuildVectorIndex
}

// isUnfinishedFullReindex reports a failed or canceled full reindex leftover
// (Resumable=false). Pre-rebuild-PR rows have an empty Operation.
func (js *JobStatus) isUnfinishedFullReindex() bool {
	if js == nil || js.Resumable {
		return false
	}
	if js.Operation != "" && js.Operation != JobOperationReindex {
		return false
	}
	return js.Status == JobStatusFailed || js.Status == JobStatusCanceled
}

func (js *JobStatus) isCatchUp() bool {
	return js != nil && js.Operation == JobOperationCatchUp
}

// fail marks the job failed with the given error message.
func (js *JobStatus) fail(errMsg string) {
	js.Status = JobStatusFailed
	js.Error = errMsg
	js.CompletedAt = time.Now()
}

// Cursor stores the cursor position for resumable indexing
type Cursor struct {
	LastCreateAt int64  `json:"last_create_at"`
	LastID       string `json:"last_id"`
}

// ModelInfo stores the model configuration used when indexing
type ModelInfo struct {
	ProviderType       string `json:"provider_type"`
	ModelName          string `json:"model_name"`
	Dimensions         int    `json:"dimensions"`
	HNSWM              int    `json:"hnsw_m,omitempty"`
	VectorElementType  string `json:"vector_element_type,omitempty"`
	IndexRetentionDays *int   `json:"index_retention_days,omitempty"`
	IndexedAt          int64  `json:"indexed_at"`
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
	ModelCompatible          bool   `json:"model_compatible"`
	ModelNeedsReindex        bool   `json:"model_needs_reindex"`
	ModelCompatReason        string `json:"model_compat_reason,omitempty"`
	StoredProviderType       string `json:"stored_provider_type,omitempty"`
	StoredDimensions         int    `json:"stored_dimensions,omitempty"`
	StoredModelName          string `json:"stored_model_name,omitempty"`
	StoredHNSWM              int    `json:"stored_hnsw_m,omitempty"`
	StoredVectorElementType  string `json:"stored_vector_element_type,omitempty"`
	StoredIndexRetentionDays *int   `json:"stored_index_retention_days,omitempty"`
	NeedsCatchUp             bool   `json:"needs_catch_up,omitempty"`

	// Present while a deferred reindex owns the ANN index lifecycle.
	VectorIndexState *VectorIndexState `json:"vector_index_state,omitempty"`
}

// ModelCompatibility represents the result of checking model compatibility
type ModelCompatibility struct {
	Compatible               bool   `json:"compatible"`
	NeedsReindex             bool   `json:"needs_reindex"`
	Reason                   string `json:"reason,omitempty"`
	StoredProviderType       string `json:"stored_provider_type,omitempty"`
	StoredDimensions         int    `json:"stored_dimensions,omitempty"`
	StoredModelName          string `json:"stored_model_name,omitempty"`
	StoredHNSWM              int    `json:"stored_hnsw_m,omitempty"`
	StoredVectorElementType  string `json:"stored_vector_element_type,omitempty"`
	StoredIndexRetentionDays *int   `json:"stored_index_retention_days,omitempty"`
	NeedsCatchUp             bool   `json:"needs_catch_up,omitempty"`
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

	// Repair fetch: gated-window edits, keyset on (UpdateAt, Id). Store
	// overwrites in-place (delete+insert in one txn) — no pre-delete.
	// indexableContentSQL mirrors shouldIndexPost with jsonb (not LIKE), so
	// Props={"attachments":[]} is non-indexable; fetch and stale delete must
	// partition the same set. Expression is never NULL (safe to negate).
	indexableContentSQL = `(COALESCE(Posts.Message, '') != '' OR CASE
		WHEN jsonb_typeof(NULLIF(Posts.Props::text, '')::jsonb -> 'attachments') = 'array'
		THEN jsonb_array_length(NULLIF(Posts.Props::text, '')::jsonb -> 'attachments') > 0
		ELSE FALSE END)`

	editedPostsFetchBase = `SELECT
		Posts.Id as id,
		Posts.Message as message,
		Posts.Props as props,
		Posts.UserId as userid,
		Posts.ChannelId as channelid,
		Posts.CreateAt as createat,
		Posts.UpdateAt as updateat,
		Channels.TeamId as teamid,
		Channels.Name as channelname,
		Channels.Type as channeltype
	FROM Posts
	LEFT JOIN Channels ON Posts.ChannelId = Channels.Id
	WHERE Posts.DeleteAt = 0
		AND ` + indexableContentSQL + `
		AND Posts.Type = ''
		AND (Posts.UpdateAt, Posts.Id) > ($1, $2)
		AND Posts.UpdateAt <= $3`

	editedPostsFetchTail = `
	ORDER BY Posts.UpdateAt ASC, Posts.Id ASC
	LIMIT $4`

	// Exact complement of the repair fetch in the same UpdateAt window.
	staleEditedEmbeddingsDeleteQuery = `DELETE FROM llm_posts_embeddings e
	USING Posts
	WHERE e.post_id = Posts.Id
		AND Posts.UpdateAt >= $1
		AND Posts.UpdateAt <= $2
		AND (Posts.DeleteAt != 0
			OR Posts.Type != ''
			OR NOT ` + indexableContentSQL + `)`
)

// postFetcher builds a fetchFunc paging the given query up to cutoff.
// When floor > 0 the query must include $5 as Posts.CreateAt >= $5.
func (s *Indexer) postFetcher(query string, cutoff, floor int64) fetchFunc {
	return func(ctx context.Context, cursor Cursor, limit int) ([]PostRecord, error) {
		var posts []PostRecord
		args := []any{cursor.LastCreateAt, cursor.LastID, cutoff, limit}
		if floor > 0 {
			args = append(args, floor)
		}
		err := s.db.SelectContext(ctx, &posts, query, args...)
		return posts, err
	}
}

type indexJobSpec struct {
	panicLog              string
	completeLog           string
	clearIndex            bool
	runMainPass           bool
	skipExisting          bool
	saveLastIndexed       bool
	deleteCursorOnSuccess bool
	persistHNSWMOnly      bool
}

func (s *Indexer) runCatchUpJob(jobStatus *JobStatus) {
	s.runIndexJob(context.Background(), jobStatus, nil, indexJobSpec{
		panicLog:              "Catch-up job panicked",
		completeLog:           "Catch-up completed",
		runMainPass:           true,
		skipExisting:          true,
		saveLastIndexed:       true,
		deleteCursorOnSuccess: true,
	})
}

func (s *Indexer) runReindexJob(jobStatus *JobStatus, clearIndex bool, deferRun *deferredRun) {
	s.runIndexJob(context.Background(), jobStatus, deferRun, indexJobSpec{
		panicLog:              "Reindex job panicked",
		completeLog:           "Reindexing completed",
		clearIndex:            clearIndex,
		runMainPass:           true,
		saveLastIndexed:       true,
		deleteCursorOnSuccess: true,
	})
}

// runIndexJob is the shared exclusive-job worker: optional embed pass, then
// deferred HNSW build/repair/catch-up/completion. Rebuild jobs omit the
// embed pass and persist only HNSW m.
func (s *Indexer) runIndexJob(ctx context.Context, jobStatus *JobStatus, deferRun *deferredRun, spec indexJobSpec) {
	// ownedState is this run's last CAS-written claim (ownership proof).
	var bulk embeddings.BulkIndexer
	var ownedState VectorIndexState
	deferPending := false  // index dropped; leave claim for resume on exit
	repairPending := false // gated-window edits still need re-indexing

	defer func() {
		if r := recover(); r != nil {
			s.pluginAPI.LogError(spec.panicLog, "panic", r)
			errMsg := fmt.Sprintf("Job panicked: %v", r)
			if deferPending {
				errMsg = appendDroppedIndexNote(errMsg)
			} else if repairPending {
				errMsg = appendPendingRepairNote(errMsg)
			}
			s.failJob(jobStatus, errMsg)
		}
	}()

	// Snapshot once; a mid-job config change can alter getSearch().
	var search embeddings.EmbeddingSearch
	if s.getSearch != nil {
		search = s.getSearch()
	}
	if search == nil {
		errMsg := "Search not configured"
		if deferRun != nil {
			if abandonErr := s.abandonUndroppedClaim(deferRun); abandonErr != nil {
				s.pluginAPI.LogError("Failed to release vector index claim on early exit", "error", abandonErr)
				errMsg = fmt.Sprintf("%s; additionally failed to release the vector index claim: %s", errMsg, abandonErr)
			}
		}
		s.failJob(jobStatus, errMsg)
		return
	}

	if deferRun != nil {
		ownedState = deferRun.state
		// Rebuild always drops; reindex resumes repair without dropping.
		if spec.runMainPass && ownedState.Phase == VectorIndexPhaseRepairing {
			repairPending = true
		} else {
			bulk = bulkIndexerFor(search)
			if bulk == nil {
				errMsg := "Vector store no longer supports deferred indexing; start a new reindex"
				if !spec.runMainPass {
					errMsg = errVectorStoreNoBulkIndex.Error()
				}
				if abandonErr := s.abandonUndroppedClaim(deferRun); abandonErr != nil {
					s.pluginAPI.LogError("Failed to release vector index claim", "error", abandonErr)
					errMsg = fmt.Sprintf("%s (additionally failed to release the vector index claim: %s)", errMsg, abandonErr)
				}
				s.failJob(jobStatus, errMsg)
				return
			}
			if ok, casErr := s.casVectorIndexState(&ownedState, &ownedState); casErr != nil || !ok {
				s.pluginAPI.LogError("Deferred index claim is no longer current; aborting before DROP",
					"job_id", jobStatus.JobID, "error", casErr)
				s.failJob(jobStatus, "Deferred index claim lost before bulk load began")
				return
			}
			deferPending = true
			if err := bulk.PrepareBulkIndex(ctx); err != nil {
				s.failJob(jobStatus, appendDroppedIndexNote(fmt.Sprintf("Failed to drop vector index: %s", err)))
				return
			}
		}
	}

	watermark := Cursor{}
	if spec.runMainPass {
		if spec.clearIndex {
			if err := search.Clear(ctx); err != nil {
				errMsg := fmt.Sprintf("Failed to clear search index: %s", err)
				if deferPending {
					errMsg = appendDroppedIndexNote(errMsg)
				}
				s.failJob(jobStatus, errMsg)
				return
			}
		}

		cursor := bumpCursorToFloor(s.loadCursor(), jobStatus.RetentionFloor)
		workers, batchSize := s.reindexSettings()
		mainFetch := s.postFetcher(indexablePostsFetchSQL(jobStatus.RetentionFloor, spec.skipExisting), jobStatus.CutoffAt, jobStatus.RetentionFloor)
		var err error
		_, watermark, err = s.runIndexPass(ctx, jobStatus, search, mainFetch, cursor, passOptions{workers: workers, batchSize: batchSize})
		if errors.Is(err, errCancelRequested) {
			if deferPending {
				jobStatus.Error = appendDroppedIndexNote(jobStatus.Error)
			} else if repairPending {
				jobStatus.Error = appendPendingRepairNote(jobStatus.Error)
			}
			s.acknowledgeCancel(jobStatus)
			return
		}
		if err != nil {
			errMsg := fmt.Sprintf("Failed to index posts: %s", err)
			if deferPending {
				errMsg = appendDroppedIndexNote(errMsg)
			} else if repairPending {
				errMsg = appendPendingRepairNote(errMsg)
			}
			s.handleJobError(jobStatus, errMsg, watermark.LastCreateAt, watermark.LastID)
			return
		}
	}

	if deferPending {
		newState, buildErr := s.finalizeDeferredIndex(ctx, jobStatus, bulk, ownedState)
		ownedState = newState
		if buildErr != nil {
			deferPending = false
			s.handleJobError(jobStatus, appendDroppedIndexNote(fmt.Sprintf("Failed to rebuild vector index: %s", buildErr)), watermark.LastCreateAt, watermark.LastID)
			return
		}
		deferPending = false
		repairPending = true
		if canceled, cancelErr := s.isCancelRequested(jobStatus.JobID); cancelErr == nil && canceled {
			jobStatus.Error = appendPendingRepairNote(jobStatus.Error)
			s.acknowledgeCancel(jobStatus)
			return
		}
	}

	if repairPending {
		if repairErr := s.reindexEditedPosts(ctx, jobStatus, search, ownedState.BuildStartedAt); repairErr != nil {
			if errors.Is(repairErr, errCancelRequested) {
				jobStatus.Error = appendPendingRepairNote(jobStatus.Error)
				s.acknowledgeCancel(jobStatus)
				return
			}
			s.handleJobError(jobStatus, fmt.Sprintf("Failed to re-index posts edited during the index build: %s", repairErr), watermark.LastCreateAt, watermark.LastID)
			return
		}
		if clearErr := s.clearVectorIndexState(ownedState); clearErr != nil {
			s.handleJobError(jobStatus, fmt.Sprintf("Repair completed but the vector index state could not be cleared: %s", clearErr), watermark.LastCreateAt, watermark.LastID)
			return
		}
		repairPending = false
	}

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

	if !s.finishJob(jobStatus) {
		return
	}

	if spec.deleteCursorOnSuccess {
		_ = s.pluginAPI.KVDelete(IndexerCursorKey)
	}
	if spec.saveLastIndexed {
		s.saveLastIndexedTimestamp(time.Now().UnixMilli())
	}
	if s.beforePersistModelInfo != nil {
		s.beforePersistModelInfo()
	}
	s.persistModelInfoAfterJob(jobStatus, spec)

	s.pluginAPI.LogWarn(spec.completeLog, "processed_posts", jobStatus.ProcessedRows)
}

// filterAndCreateDocsWithFloor filters posts and creates PostDocuments,
// skipping posts with CreateAt below the retention floor (0 means no floor).
func (s *Indexer) filterAndCreateDocsWithFloor(posts []PostRecord, floor int64) []embeddings.PostDocument {
	docs := make([]embeddings.PostDocument, 0, len(posts))
	for _, post := range posts {
		modelPost := &model.Post{
			Id:        post.ID,
			ChannelId: post.ChannelID,
			UserId:    post.UserID,
			Message:   post.Message,
			Type:      model.PostTypeDefault,
			DeleteAt:  0,
			CreateAt:  post.CreateAt,
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

		if !s.shouldIndexPostWithFloor(modelPost, channel, floor) {
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

// failJob marks the job failed and persists it.
func (s *Indexer) failJob(jobStatus *JobStatus, errMsg string) {
	jobStatus.fail(errMsg)
	s.saveJobStatus(jobStatus)
}

// handleJobError handles a job error by saving cursor and updating status
func (s *Indexer) handleJobError(jobStatus *JobStatus, errMsg string, lastCreateAt int64, lastID string) {
	jobStatus.fail(errMsg)
	jobStatus.ErrorCount++

	// Rebuild jobs are not cursor-resumable; leaving IndexerCursorKey would
	// let "Resume Reindex" enter the embed workflow.
	if !jobStatus.isRebuild() {
		s.saveCursor(Cursor{LastCreateAt: lastCreateAt, LastID: lastID})
	}
	s.saveJobStatus(jobStatus)
}

func (s *Indexer) persistModelInfoAfterJob(jobStatus *JobStatus, spec indexJobSpec) {
	if spec.persistHNSWMOnly {
		s.saveHNSWMAfterRebuild(jobStatus)
		return
	}
	if jobStatus.isCatchUp() {
		s.persistProvenIndexRetentionDays(jobStatus.IndexRetentionDays)
		return
	}
	if jobStatus.ModelInfo != nil {
		if err := s.SaveModelInfo(*jobStatus.ModelInfo); err != nil {
			s.pluginAPI.LogError("Failed to save model info after reindex", "error", err)
		}
	}
}

func (s *Indexer) persistProvenIndexRetentionDays(days int) {
	stored, err := s.GetModelInfo()
	if err != nil && !mmapi.IsKVNotFound(err) {
		s.pluginAPI.LogError("Failed to read model info after catch-up", "error", err)
		return
	}
	if err != nil {
		stored = ModelInfo{}
	}
	stored.IndexRetentionDays = new(days)
	if saveErr := s.SaveModelInfo(stored); saveErr != nil {
		s.pluginAPI.LogError("Failed to save index retention days after catch-up", "error", saveErr)
	}
}

func (s *Indexer) saveHNSWMAfterRebuild(jobStatus *JobStatus) {
	stored, err := s.GetModelInfo()
	if err != nil && !mmapi.IsKVNotFound(err) {
		s.pluginAPI.LogError("Failed to read model info after vector index rebuild", "error", err)
		return
	}
	if err != nil {
		stored = ModelInfo{}
	}

	currentM := 0
	if jobStatus.ModelInfo != nil {
		currentM = jobStatus.ModelInfo.HNSWM
	}

	// Never copy the job's provider/model/dimensions onto stored metadata.
	// Missing or blank identity stays blank; rebuild is not a re-embed.
	stored.HNSWM = currentM
	if saveErr := s.SaveModelInfo(stored); saveErr != nil {
		s.pluginAPI.LogError("Failed to save HNSW m after vector index rebuild", "error", saveErr)
	}
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

	var oldValue any
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

		var oldValue any
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
	floor := jobStatus.RetentionFloor
	catchUpFetch := s.postFetcher(indexablePostsFetchSQL(floor, true), catchUpCutoff, floor)
	startCursor := Cursor{LastCreateAt: jobStatus.CutoffAt, LastID: ""}
	return s.runIndexPass(ctx, jobStatus, search, catchUpFetch, startCursor, passOptions{workers: 1, batchSize: batchSize})
}

// reindexEditedPosts re-embeds posts edited while live indexing was gated.
// Catch-up's NOT EXISTS never touches already-indexed rows. Uses UpdateAt
// (not EditAt) so props/attachment changes are covered. Sequential on
// (UpdateAt, Id) — must not checkpoint into IndexerCursorKey (would poison
// main-pass resume). Store overwrites in-place; cancel mid-repair is safe.
func (s *Indexer) reindexEditedPosts(ctx context.Context, jobStatus *JobStatus, search embeddings.EmbeddingSearch, since int64) error {
	if since <= 0 {
		return nil
	}

	// Bound the window; later edits go through live indexing (gate is off).
	upperBound := time.Now().UnixMilli()

	// Delete stale rows for posts edited into a non-indexable form (complement
	// of the repair fetch). Cancel mid-repair leaves removals done.
	if _, err := s.db.ExecContext(ctx, staleEditedEmbeddingsDeleteQuery, since, upperBound); err != nil {
		return fmt.Errorf("failed to delete stale embeddings for posts edited into a non-indexable form: %w", err)
	}

	_, batchSize := s.reindexSettings()
	floor := jobStatus.RetentionFloor
	lastUpdateAt, lastID := since, ""
	for {
		// Heartbeat only (never cursor); also polls cancel.
		if s.heartbeatTick(jobStatus) {
			return errCancelRequested
		}
		var posts []PostRecord
		args := []any{lastUpdateAt, lastID, upperBound, batchSize}
		if floor > 0 {
			args = append(args, floor)
		}
		if err := s.db.SelectContext(ctx, &posts, editedPostsFetchSQL(floor), args...); err != nil {
			return fmt.Errorf("failed to fetch edited posts: %w", err)
		}
		if len(posts) == 0 {
			return nil
		}
		if err := s.safeStoreBatch(ctx, search, posts, floor); err != nil {
			return err
		}
		last := posts[len(posts)-1]
		lastUpdateAt, lastID = last.UpdateAt, last.ID
	}
}
