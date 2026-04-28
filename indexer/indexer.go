// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

type Indexer struct {
	getSearch    func() embeddings.EmbeddingSearch
	configGetter func() embeddings.EmbeddingSearchConfig
	pluginAPI    mmapi.Client
	bots         *bots.MMBots
	db           *sqlx.DB
	clusterMutex cluster.MutexPluginAPI
}

func New(
	getSearch func() embeddings.EmbeddingSearch,
	configGetter func() embeddings.EmbeddingSearchConfig,
	pluginAPI mmapi.Client,
	bots *bots.MMBots,
	db *sqlx.DB,
	clusterMutex cluster.MutexPluginAPI,
) *Indexer {
	return &Indexer{
		getSearch:    getSearch,
		configGetter: configGetter,
		pluginAPI:    pluginAPI,
		bots:         bots,
		db:           db,
		clusterMutex: clusterMutex,
	}
}

// IndexPost indexes a post if it meets the criteria
func (s *Indexer) IndexPost(ctx context.Context, post *model.Post, channel *model.Channel) error {
	if !s.shouldIndexPost(post, channel) {
		return nil
	}

	if s.getSearch == nil {
		return nil // Search not configured
	}
	search := s.getSearch()
	if search == nil {
		return nil // Search not configured
	}

	// Create document
	doc := embeddings.PostDocument{
		PostID:    post.Id,
		CreateAt:  post.CreateAt,
		TeamID:    channel.TeamId,
		ChannelID: post.ChannelId,
		UserID:    post.UserId,
		Content:   format.PostBody(post),
	}

	// Store the document
	return search.Store(ctx, []embeddings.PostDocument{doc})
}

// DeletePost deletes a post from the index
func (s *Indexer) DeletePost(ctx context.Context, postID string) error {
	if s.getSearch == nil {
		return nil // Search not configured
	}
	search := s.getSearch()
	if search == nil {
		return nil // Search not configured
	}

	return search.Delete(ctx, []string{postID})
}

// RunDataRetention deletes orphaned embeddings as part of data retention cleanup.
func (s *Indexer) RunDataRetention(ctx context.Context, nowTime, batchSize int64) (int64, error) {
	if s.getSearch == nil {
		return 0, nil
	}
	search := s.getSearch()
	if search == nil {
		return 0, nil
	}

	return search.DeleteOrphaned(ctx, nowTime, batchSize)
}

// StartReindexJob starts a post reindexing job
// If clearIndex is true, the existing index will be cleared before reindexing.
// If clearIndex is false, the job will resume from where it left off (if applicable).
func (s *Indexer) StartReindexJob(clearIndex bool) (JobStatus, error) {
	// Check if search is initialized
	if s.getSearch == nil || s.getSearch() == nil {
		return JobStatus{}, fmt.Errorf("search functionality is not configured")
	}

	// Optimistic check before acquiring mutex
	var jobStatus JobStatus
	err := s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil && !mmapi.IsKVNotFound(err) {
		return JobStatus{}, fmt.Errorf("failed to check job status: %w", err)
	}
	if isActiveJob(&jobStatus) && !s.isJobStale(&jobStatus) {
		return jobStatus, fmt.Errorf("job already running")
	}

	// Acquire cluster mutex for job start
	mtx, err := cluster.NewMutex(s.clusterMutex, "ai_reindex_job")
	if err != nil {
		return JobStatus{}, fmt.Errorf("failed to create mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	// Re-check after acquiring lock (double-checked locking pattern). Reset
	// jobStatus on not-found so a populated optimistic-read snapshot doesn't
	// leak into the resume carry-over below.
	err = s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil && !mmapi.IsKVNotFound(err) {
		return JobStatus{}, fmt.Errorf("failed to check job status: %w", err)
	}
	hasExisting := err == nil
	if !hasExisting {
		jobStatus = JobStatus{}
	}
	if hasExisting && isActiveJob(&jobStatus) && !s.isJobStale(&jobStatus) {
		return jobStatus, fmt.Errorf("job already running")
	}

	// Capture cutoff timestamp BEFORE counting posts to prevent race gap
	// Posts created after this point will be caught by the catch-up pass
	cutoffTimestamp := time.Now().UnixMilli()

	// Get an estimate of total posts for progress tracking
	var count int64
	dbErr := s.db.Get(&count, `SELECT COUNT(*) FROM Posts WHERE DeleteAt = 0 AND (Message != '' OR Props::text LIKE '%"attachments"%') AND Type = '' AND CreateAt <= $1`, cutoffTimestamp)
	if dbErr != nil {
		s.pluginAPI.LogWarn("Failed to get post count for progress tracking", "error", dbErr)
		count = 0 // Continue with zero estimate
	}

	// Create initial job status with a fresh JobID so cancel checks and CAS
	// transitions for this run cannot be confused with a previous run.
	newJobStatus := JobStatus{
		JobID:     model.NewId(),
		Status:    JobStatusRunning,
		StartedAt: time.Now(),
		Resumable: !clearIndex,
		NodeID:    s.getNodeID(),
	}

	// When resuming, preserve CutoffAt, TotalRows, and ProcessedRows from the previous job
	// so the UI shows accurate progress and catch-up covers posts from original start time
	if !clearIndex && hasExisting {
		newJobStatus.TotalRows = jobStatus.TotalRows
		newJobStatus.CutoffAt = jobStatus.CutoffAt
		newJobStatus.ProcessedRows = jobStatus.ProcessedRows
	} else {
		// Fresh start - calculate new values
		newJobStatus.TotalRows = count
		newJobStatus.CutoffAt = cutoffTimestamp
	}

	// CAS-write the initial job status. The predicate is the row we observed
	// above (or "no row" when the key was absent). This routes through the
	// master and is rejected if the row changed underneath us, even when our
	// initial KVGet was served by a stale replica.
	var oldValue interface{}
	if hasExisting {
		oldValue = jobStatus
	}
	ok, err := s.pluginAPI.KVCompareAndSet(ReindexJobKey, oldValue, newJobStatus)
	if err != nil {
		return JobStatus{}, fmt.Errorf("failed to save job status: %w", err)
	}
	if !ok {
		return JobStatus{}, fmt.Errorf("job already running")
	}

	// Clear cursor if doing a fresh reindex
	if clearIndex {
		if err := s.pluginAPI.KVDelete(IndexerCursorKey); err != nil {
			return JobStatus{}, fmt.Errorf("failed to clear reindex cursor: %w", err)
		}
	}

	// Snapshot status for return value before the background job mutates newJobStatus.
	returnStatus := newJobStatus
	// Start the reindexing job in background
	go s.runReindexJob(&newJobStatus, clearIndex)

	return returnStatus, nil
}

// isActiveJob reports whether a job row represents a run that should block a
// new Start: either still running, or with a cancel pending acknowledgment
// from its worker. Both states are non-terminal and a Start would race with
// the existing run.
func isActiveJob(s *JobStatus) bool {
	return s.Status == JobStatusRunning || s.Status == JobStatusCancelRequested
}

// getNodeID returns a unique identifier for this node
func (s *Indexer) getNodeID() string {
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	return hostname
}

// GetJobStatus gets the status of the reindex job
func (s *Indexer) GetJobStatus() (JobStatus, error) {
	var jobStatus JobStatus
	err := s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil {
		return JobStatus{}, err
	}
	jobStatus.IsStale = s.isJobStale(&jobStatus)
	return jobStatus, nil
}

// CancelJob requests cancellation of the running reindex job. The transition
// is Running -> CancelRequested via CAS; the actual terminal Canceled state
// is written by the worker after it observes the request scoped to its own
// JobID. This split lets cancel signaling survive replica-lag KVGet races
// without poisoning a freshly-started successor run.
func (s *Indexer) CancelJob() (JobStatus, error) {
	// Acquire cluster mutex
	mtx, err := cluster.NewMutex(s.clusterMutex, "ai_reindex_job")
	if err != nil {
		return JobStatus{}, fmt.Errorf("failed to create mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	var jobStatus JobStatus
	err = s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil {
		return JobStatus{}, err
	}

	if jobStatus.Status != JobStatusRunning {
		return JobStatus{}, fmt.Errorf("not running")
	}

	newStatus := jobStatus
	newStatus.Status = JobStatusCancelRequested

	ok, casErr := s.pluginAPI.KVCompareAndSet(ReindexJobKey, jobStatus, newStatus)
	if casErr != nil {
		return JobStatus{}, fmt.Errorf("failed to save job status: %w", casErr)
	}
	if !ok {
		// The row changed between our read and CAS — the worker has already
		// completed, failed, or transitioned the row, so there is no live
		// run to cancel.
		return JobStatus{}, fmt.Errorf("not running")
	}

	return newStatus, nil
}

// shouldIndexPost returns whether a post should be indexed based on consistent criteria
func (s *Indexer) shouldIndexPost(post *model.Post, channel *model.Channel) bool {
	// Skip posts that don't have content (message or attachments)
	if post.Message == "" && len(post.Attachments()) == 0 {
		return false
	}

	// Skip posts from bots
	if s.bots.IsAnyBot(post.UserId) {
		return false
	}

	// Skip non-regular posts
	if post.Type != model.PostTypeDefault {
		return false
	}

	// Skip deleted posts
	if post.DeleteAt != 0 {
		return false
	}

	// Skip posts in DM channels with the bots
	if channel != nil && s.bots.GetBotForDMChannel(channel) != nil {
		return false
	}

	return true
}

// StartCatchUpJob indexes posts created since the last successful index
func (s *Indexer) StartCatchUpJob() (JobStatus, error) {
	if s.getSearch == nil || s.getSearch() == nil {
		return JobStatus{}, fmt.Errorf("search functionality is not configured")
	}

	// Get last indexed timestamp
	lastIndexed := s.getLastIndexedTimestamp()
	if lastIndexed == 0 {
		return JobStatus{}, fmt.Errorf("no previous index found, run a full reindex first")
	}

	// Acquire cluster mutex
	mtx, err := cluster.NewMutex(s.clusterMutex, "ai_reindex_job")
	if err != nil {
		return JobStatus{}, fmt.Errorf("failed to create mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	// Check if job is already running (allow restart if stale). Reset
	// jobStatus on not-found so we never act on a populated zero-row that
	// looks like an empty status struct.
	var jobStatus JobStatus
	err = s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil && !mmapi.IsKVNotFound(err) {
		return JobStatus{}, fmt.Errorf("failed to check job status: %w", err)
	}
	hasExisting := err == nil
	if !hasExisting {
		jobStatus = JobStatus{}
	}
	if hasExisting && isActiveJob(&jobStatus) && !s.isJobStale(&jobStatus) {
		return jobStatus, fmt.Errorf("job already running")
	}

	// Capture cutoff timestamp for catch-up
	cutoffTimestamp := time.Now().UnixMilli()

	// Count posts to catch up
	var count int64
	err = s.db.Get(&count, `
		SELECT COUNT(*) FROM Posts
		WHERE DeleteAt = 0 AND (Message != '' OR Props::text LIKE '%"attachments"%') AND Type = ''
		AND CreateAt > $1`, lastIndexed)
	if err != nil {
		s.pluginAPI.LogWarn("Failed to get catch-up post count", "error", err)
	}

	newJobStatus := JobStatus{
		JobID:     model.NewId(),
		Status:    JobStatusRunning,
		StartedAt: time.Now(),
		TotalRows: count,
		Resumable: true,
		NodeID:    s.getNodeID(),
		CutoffAt:  cutoffTimestamp,
	}

	var oldValue interface{}
	if hasExisting {
		oldValue = jobStatus
	}
	ok, err := s.pluginAPI.KVCompareAndSet(ReindexJobKey, oldValue, newJobStatus)
	if err != nil {
		return JobStatus{}, fmt.Errorf("failed to save job status: %w", err)
	}
	if !ok {
		return JobStatus{}, fmt.Errorf("job already running")
	}

	// Set cursor to start from last indexed timestamp
	s.saveCursor(Cursor{LastCreateAt: lastIndexed, LastID: ""})

	// Snapshot status for return value before the background job mutates newJobStatus.
	returnStatus := newJobStatus
	// Start catch-up job (reuses runReindexJob with clearIndex=false)
	go s.runReindexJob(&newJobStatus, false)

	return returnStatus, nil
}

// CheckIndexHealth compares database posts with indexed posts
func (s *Indexer) CheckIndexHealth(ctx context.Context) (HealthCheckResult, error) {
	if s.getSearch == nil || s.getSearch() == nil {
		return HealthCheckResult{}, fmt.Errorf("search functionality is not configured")
	}

	result := HealthCheckResult{
		CheckedAt: time.Now(),
	}

	// Get bot user IDs to exclude from count (matching shouldIndexPost behavior)
	var botUserIDs []string
	if s.bots != nil {
		botUserIDs = s.bots.GetAllBotUserIDs()
	}

	// Count posts in database, excluding bot posts and posts in bot DM channels
	// This matches the filtering in shouldIndexPost which skips:
	// - Posts from bots (UserId in botUserIDs)
	// - Posts in DM channels with bots (channel Type='D' and Name contains bot ID)
	if len(botUserIDs) > 0 {
		// Build exclusion for bot DM channels using parameterized LIKE conditions
		// DM channel names contain both user IDs separated by "__"
		query, args, err := sqlx.In(`
			SELECT COUNT(*) FROM Posts p
			JOIN Channels c ON p.ChannelId = c.Id
			WHERE p.DeleteAt = 0 AND (p.Message != '' OR p.Props::text LIKE '%"attachments"%') AND p.Type = ''
			AND p.UserId NOT IN (?)`, botUserIDs)
		if err != nil {
			result.Error = fmt.Sprintf("failed to build query: %v", err)
			result.Status = "error"
			return result, err
		}

		var likeConditions []string
		for _, botID := range botUserIDs {
			likeConditions = append(likeConditions, "c.Name LIKE ?")
			args = append(args, "%"+botID+"%")
		}
		query += " AND NOT (c.Type = 'D' AND (" + strings.Join(likeConditions, " OR ") + "))"

		query = s.db.Rebind(query)
		err = s.db.GetContext(ctx, &result.DBPostCount, query, args...)
		if err != nil {
			result.Error = fmt.Sprintf("failed to count DB posts: %v", err)
			result.Status = "error"
			return result, err
		}
	} else {
		err := s.db.GetContext(ctx, &result.DBPostCount, `
			SELECT COUNT(*) FROM Posts
			WHERE DeleteAt = 0 AND (Message != '' OR Props::text LIKE '%"attachments"%') AND Type = ''`)
		if err != nil {
			result.Error = fmt.Sprintf("failed to count DB posts: %v", err)
			result.Status = "error"
			return result, err
		}
	}

	// Count posts in index
	indexedCount, err := s.countIndexedPosts(ctx)
	if err != nil {
		result.Error = fmt.Sprintf("failed to count indexed posts: %v", err)
		result.Status = "error"
		return result, err
	}
	result.IndexedPostCount = indexedCount

	// Calculate differences
	if result.DBPostCount > result.IndexedPostCount {
		result.MissingPosts = result.DBPostCount - result.IndexedPostCount
	}

	// Determine status based on 1% tolerance
	tolerance := int64(float64(result.DBPostCount) * 0.01)
	if tolerance < 10 {
		tolerance = 10 // Minimum tolerance of 10 posts
	}

	switch {
	case result.MissingPosts > tolerance:
		result.Status = "needs_reindex"
	case result.MissingPosts > 0:
		result.Status = "mismatch"
	default:
		result.Status = "healthy"
	}

	return result, nil
}

// countIndexedPosts counts unique posts in the vector store
func (s *Indexer) countIndexedPosts(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.GetContext(ctx, &count, `
		SELECT COUNT(DISTINCT post_id) FROM llm_posts_embeddings`)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// SaveModelInfo stores the current model configuration
func (s *Indexer) SaveModelInfo(info ModelInfo) error {
	info.IndexedAt = time.Now().UnixMilli()
	return s.pluginAPI.KVSet(IndexerModelKey, info)
}

// GetModelInfo retrieves the stored model configuration
func (s *Indexer) GetModelInfo() (ModelInfo, error) {
	var info ModelInfo
	err := s.pluginAPI.KVGet(IndexerModelKey, &info)
	return info, err
}

// CheckModelCompatibility checks if current config matches the indexed model
func (s *Indexer) CheckModelCompatibility(currentProviderType string, currentDimensions int, currentModelName string) ModelCompatibility {
	storedInfo, err := s.GetModelInfo()
	if err != nil || (storedInfo.Dimensions == 0 && storedInfo.ModelName == "") {
		// No stored info means this is a fresh install or no previous index
		return ModelCompatibility{
			Compatible:   true,
			NeedsReindex: false,
		}
	}

	// Always include stored values so frontend can do client-side comparison
	result := ModelCompatibility{
		StoredProviderType: storedInfo.ProviderType,
		StoredDimensions:   storedInfo.Dimensions,
		StoredModelName:    storedInfo.ModelName,
	}

	if storedInfo.ProviderType != "" && currentProviderType != "" && storedInfo.ProviderType != currentProviderType {
		result.Compatible = false
		result.NeedsReindex = true
		result.Reason = fmt.Sprintf("provider changed: stored=%s, current=%s", storedInfo.ProviderType, currentProviderType)
		return result
	}

	if storedInfo.Dimensions != currentDimensions {
		result.Compatible = false
		result.NeedsReindex = true
		result.Reason = fmt.Sprintf("dimension mismatch: stored=%d, current=%d", storedInfo.Dimensions, currentDimensions)
		return result
	}

	if storedInfo.ModelName != currentModelName && currentModelName != "" {
		result.Compatible = false
		result.NeedsReindex = true
		result.Reason = fmt.Sprintf("model changed: stored=%s, current=%s", storedInfo.ModelName, currentModelName)
		return result
	}

	result.Compatible = true
	result.NeedsReindex = false
	return result
}

// StaleJobThreshold is the duration after which a running job is considered stale
const StaleJobThreshold = 10 * time.Minute

// isJobStale checks if a non-terminal job's heartbeat is beyond the stale
// threshold. Both running and cancel_requested are non-terminal: a worker
// that died after Cancel was requested but before it could record the
// terminal canceled state must be reclaimable, otherwise every future Start
// would be rejected by isActiveJob.
func (s *Indexer) isJobStale(jobStatus *JobStatus) bool {
	if jobStatus.Status != JobStatusRunning && jobStatus.Status != JobStatusCancelRequested {
		return false
	}

	lastUpdate := jobStatus.LastUpdatedAt
	if lastUpdate.IsZero() {
		lastUpdate = jobStatus.StartedAt
	}

	return time.Since(lastUpdate) > StaleJobThreshold
}

// MarkOrphanedJobAsFailed marks any non-terminal job on this node as failed.
// This should be called on plugin startup to handle cases where the
// plugin/server crashed while a job was running, including the brief window
// between CancelJob writing cancel_requested and the worker recording the
// terminal canceled state. Only affects jobs that were owned by THIS node.
func (s *Indexer) MarkOrphanedJobAsFailed() error {
	var jobStatus JobStatus
	err := s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil {
		if mmapi.IsKVNotFound(err) {
			return nil // No job exists, nothing to do
		}
		return err
	}

	// Only mark non-terminal jobs (running or pending cancel) as failed.
	if jobStatus.Status != JobStatusRunning && jobStatus.Status != JobStatusCancelRequested {
		return nil
	}

	// Only mark as failed if job was running on THIS node
	currentNodeID := s.getNodeID()
	if jobStatus.NodeID != currentNodeID {
		return nil // Job is running on a different node, don't interfere
	}

	// Mark as failed - the plugin restarted while this job was running.
	// CAS predicates the write on the row we observed, so a stale replica
	// read cannot clobber a fresh run that another node started in the
	// meantime: the CAS will simply fail and we treat the row as
	// already-reclaimed.
	newStatus := jobStatus
	newStatus.Status = JobStatusFailed
	newStatus.Error = fmt.Sprintf("Job orphaned: plugin restarted on node %s while job was running", currentNodeID)
	newStatus.CompletedAt = time.Now()

	s.pluginAPI.LogWarn("Marking orphaned reindex job as failed",
		"node_id", currentNodeID,
		"processed_rows", jobStatus.ProcessedRows)

	ok, casErr := s.pluginAPI.KVCompareAndSet(ReindexJobKey, jobStatus, newStatus)
	if casErr != nil {
		return casErr
	}
	if !ok {
		// Row changed concurrently — a newer run already owns it.
		return nil
	}
	return nil
}

// getModelInfoFromConfig builds ModelInfo from the current configuration
func (s *Indexer) getModelInfoFromConfig() *ModelInfo {
	if s.configGetter == nil {
		return nil
	}

	cfg := s.configGetter()
	return &ModelInfo{
		ProviderType: cfg.GetProviderType(),
		ModelName:    cfg.GetModelName(),
		Dimensions:   cfg.Dimensions,
	}
}
