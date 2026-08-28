// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	wakeKVKeyPrefix     = "wakeup_"
	wakeListPageSize    = 100
	defaultOverdueDelay = 10 * time.Second
)

// WakeRecord is the KV payload for a scheduled wait_for_async_work resume.
type WakeRecord struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	BotID          string `json:"bot_id"`
	UserID         string `json:"user_id"`
	ChannelID      string `json:"channel_id"`
	PostID         string `json:"post_id"`
	IsDM           bool   `json:"is_dm"`
	Reason         string `json:"reason"`
	FireAt         int64  `json:"fire_at"`
}

// WakeHandler resumes a conversation when a wait fires.
type WakeHandler func(ctx context.Context, rec WakeRecord) error

// WakeStore is the KV subset used to persist wait records.
type WakeStore interface {
	Get(key string, value any) error
	Set(key string, value any) error
	Delete(key string) error
	ListPrefix(prefix string) ([]string, error)
}

type wakeLogger interface {
	LogDebug(msg string, keyValuePairs ...interface{})
	LogError(msg string, keyValuePairs ...interface{})
}

// WakeSchedulerOptions configures timer behavior for tests.
type WakeSchedulerOptions struct {
	AfterFunc    func(d time.Duration, f func())
	Now          func() time.Time
	OverdueDelay time.Duration
}

// WakeScheduler persists wait records and arms in-memory timers.
type WakeScheduler struct {
	store        WakeStore
	log          wakeLogger
	onWake       WakeHandler
	afterFunc    func(d time.Duration, f func())
	now          func() time.Time
	overdueDelay time.Duration
}

// NewWakeScheduler creates a scheduler. SetOnWake must be called before waits fire.
func NewWakeScheduler(store WakeStore, log wakeLogger, opts WakeSchedulerOptions) *WakeScheduler {
	afterFunc := opts.AfterFunc
	if afterFunc == nil {
		afterFunc = func(d time.Duration, f func()) {
			time.AfterFunc(d, f)
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	overdue := opts.OverdueDelay
	if overdue <= 0 {
		overdue = defaultOverdueDelay
	}
	return &WakeScheduler{
		store:        store,
		log:          log,
		afterFunc:    afterFunc,
		now:          now,
		overdueDelay: overdue,
	}
}

// SetOnWake installs the resume callback invoked when a wait fires.
func (s *WakeScheduler) SetOnWake(handler WakeHandler) {
	if s == nil {
		return
	}
	s.onWake = handler
}

// Schedule writes a KV record and arms a timer. It returns immediately.
func (s *WakeScheduler) Schedule(ctx context.Context, rec WakeRecord, wait time.Duration) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("wait scheduler is not configured")
	}
	if wait < 0 {
		wait = 0
	}

	rec.ID = model.NewId()
	rec.FireAt = s.now().Add(wait).UnixMilli()
	key := wakeKVKey(rec.ID)
	if err := s.store.Set(key, rec); err != nil {
		return fmt.Errorf("failed to persist wait: %w", err)
	}

	wakeCtx := context.WithoutCancel(ctx)
	s.arm(wakeCtx, rec.ID, rec.FireAt)
	return nil
}

// Sweep re-arms timers for every persisted wait. Past fireAt values are
// scheduled after a short delay instead of being dropped.
func (s *WakeScheduler) Sweep() error {
	if s == nil || s.store == nil {
		return nil
	}

	keys, err := s.store.ListPrefix(wakeKVKeyPrefix)
	if err != nil {
		return fmt.Errorf("failed to list wait records: %w", err)
	}

	ctx := context.Background()
	for _, key := range keys {
		var rec WakeRecord
		if getErr := s.store.Get(key, &rec); getErr != nil {
			if mmapi.IsKVNotFound(getErr) {
				continue
			}
			s.logError("wait_for_async_work: failed to load wait record on sweep", "error", getErr, "key", key)
			continue
		}
		if rec.ID == "" {
			continue
		}
		s.arm(ctx, rec.ID, rec.FireAt)
	}
	return nil
}

func (s *WakeScheduler) arm(ctx context.Context, id string, fireAt int64) {
	idCopy := id
	s.afterFunc(s.delayUntil(fireAt), func() {
		s.fire(ctx, idCopy)
	})
}

func (s *WakeScheduler) delayUntil(fireAt int64) time.Duration {
	fireAtTime := time.UnixMilli(fireAt)
	now := s.now()
	if !fireAtTime.After(now) {
		return s.overdueDelay
	}
	return fireAtTime.Sub(now)
}

func (s *WakeScheduler) fire(ctx context.Context, id string) {
	if s == nil || s.store == nil {
		return
	}

	key := wakeKVKey(id)
	var rec WakeRecord
	err := s.store.Get(key, &rec)
	if mmapi.IsKVNotFound(err) {
		return
	}
	if err != nil {
		s.logError("wait_for_async_work: failed to load wait record", "error", err, "id", id)
		return
	}

	if delErr := s.store.Delete(key); delErr != nil {
		s.logError("wait_for_async_work: failed to delete wait record", "error", delErr, "id", id)
	}

	if s.onWake == nil {
		return
	}

	ctx, span := telemetry.Tracer().Start(ctx, "wait_for_async_work wake",
		trace.WithAttributes(
			telemetry.UserID.String(rec.UserID),
			telemetry.ChannelID.String(rec.ChannelID),
			telemetry.PostID.String(rec.PostID),
			telemetry.AgentID.String(rec.BotID),
		),
	)
	defer span.End()

	if wakeErr := s.onWake(ctx, rec); wakeErr != nil {
		span.RecordError(wakeErr)
		span.SetStatus(codes.Error, "wake failed")
		s.logError("wait_for_async_work: wake failed", "error", wakeErr, "id", id)
	}
}

func (s *WakeScheduler) logError(msg string, keyValuePairs ...interface{}) {
	if s == nil || s.log == nil {
		return
	}
	s.log.LogError(msg, keyValuePairs...)
}

func wakeKVKey(id string) string {
	return wakeKVKeyPrefix + id
}

type keyLister interface {
	ListKeys(page, count int, options ...pluginapi.ListKeysOption) ([]string, error)
}

type pluginWakeStore struct {
	client mmapi.Client
	lister keyLister
}

// NewPluginWakeStore persists wait records via the plugin KV store.
func NewPluginWakeStore(client mmapi.Client, lister keyLister) WakeStore {
	return &pluginWakeStore{client: client, lister: lister}
}

func (s *pluginWakeStore) Get(key string, value any) error {
	return s.client.KVGet(key, value)
}

func (s *pluginWakeStore) Set(key string, value any) error {
	return s.client.KVSet(key, value)
}

func (s *pluginWakeStore) Delete(key string) error {
	return s.client.KVDelete(key)
}

func (s *pluginWakeStore) ListPrefix(prefix string) ([]string, error) {
	if s.lister == nil {
		return nil, fmt.Errorf("kv list is not configured")
	}

	var all []string
	for page := 0; ; page++ {
		keys, err := s.lister.ListKeys(page, wakeListPageSize, pluginapi.WithPrefix(prefix))
		if err != nil {
			return nil, err
		}
		all = append(all, keys...)
		if len(keys) < wakeListPageSize {
			return all, nil
		}
	}
}
