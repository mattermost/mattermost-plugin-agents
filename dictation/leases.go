// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dictation

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// LeaseStore is shared storage every node sees, such as the plugin KV
// store. CompareAndSet writes value only if the key still holds old (nil
// meaning absent), and expires the key after ttl.
type LeaseStore interface {
	Get(key string) ([]byte, error)
	CompareAndSet(key string, old, value []byte, ttl time.Duration) (bool, error)
}

// errConcurrentLimit is returned when the user already holds the maximum
// number of active sessions.
var errConcurrentLimit = errors.New("too many active dictation sessions")

const leaseCASAttempts = 5

type lease struct {
	SessionID string `json:"id"`
	// ExpiresAt is set from this server's clock, never a provider's.
	ExpiresAt int64 `json:"exp"` // Unix milliseconds
}

// sessionLeases caps active sessions per user across nodes. A lease outlives
// its session by at most leaseTTL when the node that holds it stops without
// releasing it.
type sessionLeases struct {
	store    LeaseStore
	max      int
	leaseTTL time.Duration
	now      func() time.Time
}

func leaseKey(userID string) string {
	return "dictation_sessions_" + userID
}

func (l *sessionLeases) update(userID string, change func(live []lease) ([]lease, error)) error {
	key := leaseKey(userID)
	for range leaseCASAttempts {
		old, err := l.store.Get(key)
		if err != nil {
			return fmt.Errorf("failed to read dictation leases: %w", err)
		}
		var current []lease
		if len(old) > 0 {
			if err = json.Unmarshal(old, &current); err != nil {
				return fmt.Errorf("failed to decode dictation leases: %w", err)
			}
		}
		now := l.now().UnixMilli()
		live := slices.DeleteFunc(current, func(x lease) bool { return x.ExpiresAt <= now })
		next, err := change(live)
		if err != nil {
			return err
		}
		value, err := json.Marshal(next)
		if err != nil {
			return fmt.Errorf("failed to encode dictation leases: %w", err)
		}
		if len(old) == 0 {
			old = nil
		}
		ok, err := l.store.CompareAndSet(key, old, value, l.leaseTTL)
		if err != nil {
			return fmt.Errorf("failed to write dictation leases: %w", err)
		}
		if ok {
			return nil
		}
	}
	return errors.New("dictation leases kept changing; giving up")
}

func (l *sessionLeases) acquire(userID, sessionID string) error {
	expires := l.now().Add(l.leaseTTL).UnixMilli()
	return l.update(userID, func(live []lease) ([]lease, error) {
		if len(live) >= l.max {
			return nil, errConcurrentLimit
		}
		return append(live, lease{SessionID: sessionID, ExpiresAt: expires}), nil
	})
}

func (l *sessionLeases) release(userID, sessionID string) error {
	return l.update(userID, func(live []lease) ([]lease, error) {
		return slices.DeleteFunc(live, func(x lease) bool { return x.SessionID == sessionID }), nil
	})
}
