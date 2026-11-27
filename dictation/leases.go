// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dictation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
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

var (
	// errConcurrentLimit is returned when the user already holds the maximum
	// number of active sessions.
	errConcurrentLimit = errors.New("too many active dictation sessions")
	errLeaseNotFound   = errors.New("no such dictation session")
)

const leaseCASAttempts = 5

// lease records one of a user's sessions in shared storage, so any node can
// end it. An ended lease stays until it expires so that ending it again is
// recognized, but it no longer counts against the cap.
type lease struct {
	SessionID string `json:"id"`
	CallID    string `json:"call,omitempty"`
	ServiceID string `json:"svc,omitempty"`
	Ended     bool   `json:"ended,omitempty"`
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
	for attempt := range leaseCASAttempts {
		if attempt > 0 {
			// Spread out writers racing on the same user's leases.
			time.Sleep(time.Duration(5+rand.IntN(20)) * time.Millisecond) //nolint:gosec // retry jitter, not security-sensitive
		}
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
		active := 0
		for _, x := range live {
			if !x.Ended {
				active++
			}
		}
		if active >= l.max {
			return nil, errConcurrentLimit
		}
		return append(live, lease{SessionID: sessionID, ExpiresAt: expires}), nil
	})
}

// setCall records the provider call behind sessionID so any node can hang
// it up. It fails if the session was ended meanwhile.
func (l *sessionLeases) setCall(userID, sessionID, callID, serviceID string) error {
	return l.update(userID, func(live []lease) ([]lease, error) {
		i := slices.IndexFunc(live, func(x lease) bool { return x.SessionID == sessionID })
		if i < 0 || live[i].Ended {
			return nil, errLeaseNotFound
		}
		live[i].CallID = callID
		live[i].ServiceID = serviceID
		return live, nil
	})
}

// get returns userID's lease for sessionID, or errLeaseNotFound.
func (l *sessionLeases) get(userID, sessionID string) (lease, error) {
	raw, err := l.store.Get(leaseKey(userID))
	if err != nil {
		return lease{}, fmt.Errorf("failed to read dictation leases: %w", err)
	}
	var current []lease
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &current); err != nil {
			return lease{}, fmt.Errorf("failed to decode dictation leases: %w", err)
		}
	}
	now := l.now().UnixMilli()
	for _, x := range current {
		if x.SessionID == sessionID && x.ExpiresAt > now {
			return x, nil
		}
	}
	return lease{}, errLeaseNotFound
}

// end marks sessionID ended and returns its lease as it was before, or
// errLeaseNotFound when userID has no such session.
func (l *sessionLeases) end(userID, sessionID string) (lease, error) {
	var before lease
	err := l.update(userID, func(live []lease) ([]lease, error) {
		i := slices.IndexFunc(live, func(x lease) bool { return x.SessionID == sessionID })
		if i < 0 {
			return nil, errLeaseNotFound
		}
		before = live[i]
		live[i].Ended = true
		return live, nil
	})
	return before, err
}

// release marks sessionID ended, if it is still recorded.
func (l *sessionLeases) release(userID, sessionID string) error {
	_, err := l.end(userID, sessionID)
	if errors.Is(err, errLeaseNotFound) {
		return nil
	}
	return err
}
