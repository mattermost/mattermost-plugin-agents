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
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
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

	// Store retry policy and heartbeat cadence; overridable in tests to
	// avoid long sleeps.
	storeRetryAttempts  int
	storeRetryBaseDelay time.Duration
	heartbeatInterval   time.Duration

	// beforePersistModelInfo, if set, runs after the job is marked complete
	// and before model metadata is written. Tests use it to change live
	// config while the worker is still in persist.
	beforePersistModelInfo func()
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
		getSearch:           getSearch,
		configGetter:        configGetter,
		pluginAPI:           pluginAPI,
		bots:                bots,
		db:                  db,
		clusterMutex:        clusterMutex,
		storeRetryAttempts:  defaultStoreRetryAttempts,
		storeRetryBaseDelay: defaultStoreRetryBaseDelay,
		heartbeatInterval:   defaultHeartbeatInterval,
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

	// Skipped posts: catch-up (new) or repair (edits) after the build.
	if s.deferredBuildWriteGated() {
		s.pluginAPI.LogDebug("Skipping live post indexing while the vector index is being rebuilt", "post_id", post.Id)
		return nil
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

	// Safe to skip: repair's stale-row DELETE removes them after the build.
	if s.deferredBuildWriteGated() {
		s.pluginAPI.LogDebug("Skipping post deletion from the index while the vector index is being rebuilt", "post_id", postID)
		return nil
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

	// Safe to skip: retention re-runs on schedule.
	if s.deferredBuildWriteGated() {
		s.pluginAPI.LogDebug("Skipping embeddings data retention while the vector index is being rebuilt")
		return 0, nil
	}

	return search.DeleteOrphaned(ctx, nowTime, batchSize)
}

// StartReindexJob starts a post reindexing job
// If clearIndex is true, the existing index will be cleared before reindexing.
// If clearIndex is false, the job will resume from where it left off (if applicable).
func (s *Indexer) StartReindexJob(clearIndex bool) (JobStatus, error) {
	if s.getSearch == nil || s.getSearch() == nil {
		return JobStatus{}, fmt.Errorf("search functionality is not configured")
	}

	sess, err := s.beginExclusiveJob()
	if err != nil {
		return s.exclusiveJobConflict(err)
	}
	defer sess.Unlock()

	if !clearIndex && sess.hasExisting && sess.existing.isRebuild() {
		return JobStatus{}, ErrCannotResumeRebuild
	}

	cutoffTimestamp := time.Now().UnixMilli()

	newJobStatus := JobStatus{
		JobID:     model.NewId(),
		Status:    JobStatusRunning,
		StartedAt: time.Now(),
		Resumable: !clearIndex,
		NodeID:    s.getNodeID(),
		Operation: JobOperationReindex,
	}

	if !clearIndex && sess.hasExisting {
		newJobStatus.TotalRows = sess.existing.TotalRows
		newJobStatus.CutoffAt = sess.existing.CutoffAt
		newJobStatus.ProcessedRows = sess.existing.ProcessedRows
		newJobStatus.ModelInfo = sess.existing.ModelInfo
		newJobStatus.RetentionFloor = sess.existing.RetentionFloor
		newJobStatus.IndexRetentionDays = sess.existing.IndexRetentionDays
		if sess.existing.isCatchUp() {
			newJobStatus.Operation = JobOperationCatchUp
			if identErr := s.errIfCatchUpIncompatible(s.getModelInfoFromConfig()); identErr != nil {
				return JobStatus{}, identErr
			}
		}
	} else {
		snap := s.snapshotRetention(cutoffTimestamp)
		count, dbErr := s.countIndexablePosts(cutoffTimestamp, snap.floor, false)
		if dbErr != nil {
			s.pluginAPI.LogWarn("Failed to get post count for progress tracking", "error", dbErr)
			count = 0
		}
		newJobStatus.TotalRows = count
		newJobStatus.CutoffAt = cutoffTimestamp
		newJobStatus.ModelInfo = snap.model
		newJobStatus.RetentionFloor = snap.floor
		newJobStatus.IndexRetentionDays = snap.days
	}

	if err := sess.commit(s, newJobStatus); err != nil {
		return s.exclusiveJobConflict(err)
	}

	if clearIndex {
		if err := s.pluginAPI.KVDelete(IndexerCursorKey); err != nil {
			return JobStatus{}, fmt.Errorf("failed to clear reindex cursor: %w", err)
		}
	}

	if newJobStatus.isCatchUp() {
		returnStatus := newJobStatus
		go s.runCatchUpJob(&newJobStatus)
		return returnStatus, nil
	}

	deferRun, deferErr := s.resolveDeferredRebuild(clearIndex, newJobStatus.JobID)
	if deferErr != nil {
		failedStatus := newJobStatus
		failedStatus.Status = JobStatusFailed
		failedStatus.Error = deferErr.Error()
		failedStatus.CompletedAt = time.Now()
		if _, casErr := s.pluginAPI.KVCompareAndSet(ReindexJobKey, newJobStatus, failedStatus); casErr != nil {
			s.pluginAPI.LogError("Failed to record reindex job failure", "error", casErr)
		}
		return JobStatus{}, deferErr
	}

	returnStatus := newJobStatus
	go s.runReindexJob(&newJobStatus, clearIndex, deferRun)

	return returnStatus, nil
}

// isActiveJob reports whether a job is non-terminal and should block a new Start.
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

// CancelJob asks the worker to stop. It CASes Running -> CancelRequested;
// the worker writes the terminal Canceled state when it observes the request
// scoped to its own JobID. The split keeps cancel signaling JobID-keyed so a
// stale replica read can't poison a successor run.
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
		// Row changed between read and CAS: nothing to cancel.
		return JobStatus{}, fmt.Errorf("not running")
	}

	return newStatus, nil
}

// shouldIndexPost returns whether a post should be indexed based on consistent criteria
func (s *Indexer) shouldIndexPost(post *model.Post, channel *model.Channel) bool {
	return s.shouldIndexPostWithFloor(post, channel, s.retentionFloorNow())
}

func (s *Indexer) shouldIndexPostWithFloor(post *model.Post, channel *model.Channel, floor int64) bool {
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

	if floor > 0 && post.CreateAt < floor {
		return false
	}

	return true
}

// StartCatchUpJob indexes posts created since the last successful index
func (s *Indexer) StartCatchUpJob() (JobStatus, error) {
	if s.getSearch == nil || s.getSearch() == nil {
		return JobStatus{}, fmt.Errorf("search functionality is not configured")
	}

	lastIndexed := s.getLastIndexedTimestamp()
	if lastIndexed == 0 {
		return JobStatus{}, fmt.Errorf("no previous index found, run a full reindex first")
	}

	sess, err := s.beginExclusiveJob()
	if err != nil {
		return s.exclusiveJobConflict(err)
	}
	defer sess.Unlock()

	cutoffTimestamp := time.Now().UnixMilli()
	snap := s.snapshotRetention(cutoffTimestamp)
	if identErr := s.errIfCatchUpIncompatible(snap.model); identErr != nil {
		return JobStatus{}, identErr
	}

	count, err := s.countIndexablePosts(cutoffTimestamp, snap.floor, true)
	if err != nil {
		s.pluginAPI.LogWarn("Failed to get catch-up post count", "error", err)
	}

	newJobStatus := JobStatus{
		JobID:              model.NewId(),
		Status:             JobStatusRunning,
		StartedAt:          time.Now(),
		TotalRows:          count,
		Resumable:          true,
		NodeID:             s.getNodeID(),
		CutoffAt:           cutoffTimestamp,
		Operation:          JobOperationCatchUp,
		RetentionFloor:     snap.floor,
		IndexRetentionDays: snap.days,
	}

	if err := sess.commit(s, newJobStatus); err != nil {
		return s.exclusiveJobConflict(err)
	}

	s.saveCursor(Cursor{LastCreateAt: snap.floor, LastID: ""})

	returnStatus := newJobStatus
	go s.runCatchUpJob(&newJobStatus)

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
	floor := s.retentionFloorAt(result.CheckedAt.UnixMilli())

	// Surface deferred-index ownership (explains search unavailability).
	if state, stateErr := s.loadVectorIndexState(); stateErr == nil && state != nil {
		result.VectorIndexState = state
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
		if floor > 0 {
			query += " AND p.CreateAt >= ?"
			args = append(args, floor)
		}

		query = s.db.Rebind(query)
		err = s.db.GetContext(ctx, &result.DBPostCount, query, args...)
		if err != nil {
			result.Error = fmt.Sprintf("failed to count DB posts: %v", err)
			result.Status = "error"
			return result, err
		}
	} else {
		query := `
			SELECT COUNT(*) FROM Posts
			WHERE DeleteAt = 0 AND (Message != '' OR Props::text LIKE '%"attachments"%') AND Type = ''`
		var args []any
		if floor > 0 {
			query += " AND CreateAt >= $1"
			args = append(args, floor)
		}
		err := s.db.GetContext(ctx, &result.DBPostCount, query, args...)
		if err != nil {
			result.Error = fmt.Sprintf("failed to count DB posts: %v", err)
			result.Status = "error"
			return result, err
		}
	}

	// Count posts in index
	indexedCount, err := s.countIndexedPosts(ctx, floor)
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
	tolerance := max(int64(float64(result.DBPostCount)*0.01),
		// Minimum tolerance of 10 posts
		10)

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
func (s *Indexer) countIndexedPosts(ctx context.Context, floor int64) (int64, error) {
	query := `SELECT COUNT(DISTINCT post_id) FROM llm_posts_embeddings`
	var args []any
	if floor > 0 {
		query += ` WHERE created_at >= $1`
		args = append(args, floor)
	}
	var count int64
	err := s.db.GetContext(ctx, &count, query, args...)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Indexer) countIndexablePosts(cutoff, floor int64, skipExisting bool) (int64, error) {
	query := `SELECT COUNT(*) FROM Posts WHERE DeleteAt = 0 AND (Message != '' OR Props::text LIKE '%"attachments"%') AND Type = '' AND CreateAt <= $1`
	args := []any{cutoff}
	if skipExisting {
		query += ` AND NOT EXISTS (SELECT 1 FROM llm_posts_embeddings e WHERE e.post_id = Posts.Id)`
	}
	query, args = appendCreateAtFloor(query, args, floor, "CreateAt")
	var count int64
	err := s.db.Get(&count, query, args...)
	return count, err
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

// CheckModelCompatibility checks if current config matches the indexed model.
// Missing stored fields (including hnsw_m == 0, empty vector element type,
// and a missing index-retention window) are treated as compatible so upgrades
// do not nag. An HNSW m change needs an index rebuild but search stays
// compatible. A stored vector element type that differs from current is the
// same class as a dimension mismatch: search is incompatible and a Full
// Reindex is required. Widening the retention window needs Catch Up (search
// stays on); tightening is write-only, so already-indexed posts stay
// searchable and no Catch Up is required.
func (s *Indexer) CheckModelCompatibility(current ModelInfo) ModelCompatibility {
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
		StoredProviderType:       storedInfo.ProviderType,
		StoredDimensions:         storedInfo.Dimensions,
		StoredModelName:          storedInfo.ModelName,
		StoredHNSWM:              storedInfo.HNSWM,
		StoredVectorElementType:  storedInfo.VectorElementType,
		StoredIndexRetentionDays: storedInfo.IndexRetentionDays,
	}

	if storedInfo.ProviderType != "" && current.ProviderType != "" && storedInfo.ProviderType != current.ProviderType {
		result.Compatible = false
		result.NeedsReindex = true
		result.Reason = fmt.Sprintf("provider changed: stored=%s, current=%s", storedInfo.ProviderType, current.ProviderType)
		return result
	}

	if storedInfo.Dimensions != current.Dimensions {
		result.Compatible = false
		result.NeedsReindex = true
		result.Reason = fmt.Sprintf("dimension mismatch: stored=%d, current=%d", storedInfo.Dimensions, current.Dimensions)
		return result
	}

	currentElementType := embeddings.NormalizeVectorElementType(current.VectorElementType)
	if storedInfo.VectorElementType != "" && storedInfo.VectorElementType != currentElementType {
		result.Compatible = false
		result.NeedsReindex = true
		result.Reason = fmt.Sprintf("vector element type changed: stored=%s, current=%s", storedInfo.VectorElementType, currentElementType)
		return result
	}

	if storedInfo.ModelName != current.ModelName && current.ModelName != "" {
		result.Compatible = false
		result.NeedsReindex = true
		result.Reason = fmt.Sprintf("model changed: stored=%s, current=%s", storedInfo.ModelName, current.ModelName)
		return result
	}

	result.Compatible = true
	result.NeedsReindex = false
	if storedInfo.HNSWM != 0 && storedInfo.HNSWM != current.HNSWM {
		result.NeedsReindex = true
		result.Reason = fmt.Sprintf("hnsw m changed: stored=%d, current=%d", storedInfo.HNSWM, current.HNSWM)
	}
	applyRetentionCompatibility(&result, storedInfo.IndexRetentionDays, retentionDaysValue(current.IndexRetentionDays))
	return result
}

func applyRetentionCompatibility(result *ModelCompatibility, storedDays *int, currentDays int) {
	if storedDays == nil {
		return
	}
	stored := *storedDays
	switch {
	case retentionWindowWider(currentDays, stored):
		result.NeedsCatchUp = true
		if result.Reason == "" {
			result.Reason = fmt.Sprintf("index retention increased: stored=%d, current=%d", stored, currentDays)
		}
	case retentionWindowNarrower(currentDays, stored):
		if result.Reason == "" {
			result.Reason = "Lowering this does not remove already-indexed posts; they stay searchable. The new window applies to live indexing and the next Full Reindex or Catch Up. Run Full Reindex to drop history and reduce RAM."
		}
	}
}

// StaleJobThreshold is the duration after which a running job is considered stale
const StaleJobThreshold = 10 * time.Minute

// isJobStale reports whether a non-terminal job's heartbeat is older than
// StaleJobThreshold. Both Running and CancelRequested are non-terminal: a
// worker that died mid-cancel must still be reclaimable.
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

// MarkOrphanedJobAsFailed reclaims any non-terminal reindex job whose
// heartbeat is older than StaleJobThreshold, on any node. Keying on
// staleness (not the original NodeID) lets containerized deploys —
// where the hostname changes on restart — and clustered deploys — where
// the original node may be gone — recover after a crash. Resumable=true
// preserves the cursor so the admin can resume from where the wedged
// run left off.
func (s *Indexer) MarkOrphanedJobAsFailed() error {
	var jobStatus JobStatus
	err := s.pluginAPI.KVGet(ReindexJobKey, &jobStatus)
	if err != nil {
		if mmapi.IsKVNotFound(err) {
			return nil
		}
		return err
	}

	if !s.isJobStale(&jobStatus) {
		return nil
	}

	newStatus := jobStatus
	newStatus.Status = JobStatusFailed
	newStatus.Resumable = !jobStatus.isRebuild()
	newStatus.Error = fmt.Sprintf("Job orphaned: heartbeat older than %s on node %q",
		StaleJobThreshold, jobStatus.NodeID)
	newStatus.CompletedAt = time.Now()
	newStatus.Phase = "" // phase is live-only; do not leave building_index on a terminal row

	s.pluginAPI.LogWarn("Reclaiming stale reindex job",
		"job_id", jobStatus.JobID,
		"previous_status", jobStatus.Status,
		"node_id", jobStatus.NodeID,
		"processed_rows", jobStatus.ProcessedRows)

	// CAS so a fresh run that has already claimed the row on another
	// node is not clobbered. We don't care if the CAS loses — that just
	// means someone else already moved the row.
	if _, casErr := s.pluginAPI.KVCompareAndSet(ReindexJobKey, jobStatus, newStatus); casErr != nil {
		return casErr
	}
	return nil
}

// getModelInfoFromConfig builds ModelInfo from a single config read.
func (s *Indexer) getModelInfoFromConfig() *ModelInfo {
	return s.snapshotRetention(time.Now().UnixMilli()).model
}

func (s *Indexer) errIfCatchUpIncompatible(current *ModelInfo) error {
	if current == nil {
		return nil
	}
	compat := s.CheckModelCompatibility(*current)
	if !compat.Compatible {
		return fmt.Errorf("%w: %s", ErrCatchUpIncompatible, compat.Reason)
	}
	return nil
}
