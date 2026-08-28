// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/stretchr/testify/require"
)

type nopWakeLog struct{}

func (nopWakeLog) LogDebug(string, ...interface{}) {}
func (nopWakeLog) LogError(string, ...interface{}) {}

type memoryWakeStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryWakeStore() *memoryWakeStore {
	return &memoryWakeStore{data: make(map[string][]byte)}
}

func (m *memoryWakeStore) Get(key string, value any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.data[key]
	if !ok {
		return mmapi.ErrKVNotFound
	}
	return json.Unmarshal(raw, value)
}

func (m *memoryWakeStore) Set(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = make(map[string][]byte)
	}
	m.data[key] = raw
	return nil
}

func (m *memoryWakeStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *memoryWakeStore) ListPrefix(prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.data))
	for key := range m.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

type scheduledWake struct {
	delay time.Duration
	fn    func()
}

func TestWakeSchedulerScheduleSweepRearms(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	overdue := 10 * time.Second
	store := newMemoryWakeStore()

	tests := []struct {
		name      string
		wait      time.Duration
		wantDelay time.Duration
	}{
		{name: "future wait re-arms remaining duration", wait: 5 * time.Minute, wantDelay: 5 * time.Minute},
		{name: "already-due wait re-arms with overdue delay", wait: -time.Minute, wantDelay: overdue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var first []scheduledWake
			scheduler := NewWakeScheduler(store, nopWakeLog{}, WakeSchedulerOptions{
				Now:          func() time.Time { return now },
				OverdueDelay: overdue,
				AfterFunc: func(d time.Duration, f func()) {
					first = append(first, scheduledWake{delay: d, fn: f})
				},
			})

			err := scheduler.Schedule(context.Background(), WakeRecord{
				ConversationID: "conv-id",
				BotID:          "bot-id",
				UserID:         "user-id",
				ChannelID:      "channel-id",
				PostID:         "post-id",
				IsDM:           true,
				Reason:         "poll agent",
			}, tt.wait)
			require.NoError(t, err)
			require.Len(t, first, 1)
			require.Equal(t, tt.wantDelay, first[0].delay)

			keys, listErr := store.ListPrefix(wakeKVKeyPrefix)
			require.NoError(t, listErr)
			require.Len(t, keys, 1)

			var persisted WakeRecord
			require.NoError(t, store.Get(keys[0], &persisted))
			require.Equal(t, "poll agent", persisted.Reason)
			require.Equal(t, "conv-id", persisted.ConversationID)
			require.NotEmpty(t, persisted.ID)

			var rearmed []scheduledWake
			restored := NewWakeScheduler(store, nopWakeLog{}, WakeSchedulerOptions{
				Now:          func() time.Time { return now },
				OverdueDelay: overdue,
				AfterFunc: func(d time.Duration, f func()) {
					rearmed = append(rearmed, scheduledWake{delay: d, fn: f})
				},
			})
			require.NoError(t, restored.Sweep())
			require.Len(t, rearmed, 1)
			require.Equal(t, tt.wantDelay, rearmed[0].delay)

			require.NoError(t, store.Delete(keys[0]))
		})
	}
}

func TestWakeSchedulerFireDeletesRecordAndInvokesHandler(t *testing.T) {
	store := newMemoryWakeStore()
	var got WakeRecord
	var scheduled []scheduledWake

	scheduler := NewWakeScheduler(store, nopWakeLog{}, WakeSchedulerOptions{
		AfterFunc: func(d time.Duration, f func()) {
			scheduled = append(scheduled, scheduledWake{delay: d, fn: f})
		},
	})
	scheduler.SetOnWake(func(_ context.Context, rec WakeRecord) error {
		got = rec
		return nil
	})

	require.NoError(t, scheduler.Schedule(context.Background(), WakeRecord{
		ConversationID: "conv-id",
		Reason:         "poll agent",
	}, time.Minute))
	require.Len(t, scheduled, 1)

	scheduled[0].fn()

	keys, err := store.ListPrefix(wakeKVKeyPrefix)
	require.NoError(t, err)
	require.Empty(t, keys)
	require.Equal(t, "conv-id", got.ConversationID)
	require.Equal(t, "poll agent", got.Reason)
	require.NotEmpty(t, got.ID)

	// A second fire is a no-op once the KV entry is gone (HA dedupe).
	got = WakeRecord{}
	scheduled[0].fn()
	require.Empty(t, got.ID)
}
