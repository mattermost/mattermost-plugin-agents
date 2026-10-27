// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package notifications

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
)

// ViewingHeartbeat is how often the webapp reports a response that is still
// on screen; webapp/src/viewing.ts must use the same interval.
const ViewingHeartbeat = 10 * time.Second

// ViewingTTL bounds how long a viewing report lasts without a heartbeat. It
// covers two heartbeats, so one late report does not end viewing, while a
// closed tab or lost connection stops counting as viewing within one TTL.
const ViewingTTL = 25 * time.Second

const viewingKeyPrefix = "notif_viewing_"

// viewingUpdateAttempts bounds the compare-and-set retries when reports from
// several tabs of one user race.
const viewingUpdateAttempts = 5

func viewingKey(postID, userID string) string {
	return viewingKeyPrefix + postID + "_" + userID
}

// viewingLeases maps each browser tab that has a post on screen to when its
// report lapses, in Unix milliseconds.
type viewingLeases map[string]int64

// SetViewing records whether userID has postID on screen in a focused window
// of the browser tab tabID. Each tab holds its own lease, so a tab that stops
// viewing does not end viewing reported by another tab or device.
func (s *Service) SetViewing(postID, userID, tabID string, viewing bool) error {
	key := viewingKey(postID, userID)
	for range viewingUpdateAttempts {
		raw, leases, err := s.getViewingLeases(key)
		if err != nil {
			return err
		}
		if viewing {
			leases[tabID] = time.Now().Add(ViewingTTL).UnixMilli()
		} else {
			if _, ok := leases[tabID]; !ok {
				return nil
			}
			delete(leases, tabID)
		}

		var oldValue any
		if raw != nil {
			oldValue = json.RawMessage(raw)
		}
		var written bool
		if len(leases) == 0 {
			written, err = s.client.KVCompareAndSet(key, oldValue, nil)
		} else {
			written, err = s.client.KVCompareAndSetWithExpiry(key, oldValue, leases, ViewingTTL)
		}
		if err != nil {
			return err
		}
		if written {
			return nil
		}
	}
	return fmt.Errorf("viewing state for post %s kept changing during %d updates", postID, viewingUpdateAttempts)
}

// IsViewing reports whether userID has postID on screen in a focused window.
func (s *Service) IsViewing(postID, userID string) (bool, error) {
	_, leases, err := s.getViewingLeases(viewingKey(postID, userID))
	if err != nil {
		return false, err
	}
	return len(leases) > 0, nil
}

// getViewingLeases returns the stored value of key, nil when absent, and its
// leases that have not lapsed.
func (s *Service) getViewingLeases(key string) ([]byte, viewingLeases, error) {
	var raw []byte
	err := s.client.KVGet(key, &raw)
	if errors.Is(err, mmapi.ErrKVNotFound) {
		return nil, viewingLeases{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var stored viewingLeases
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, nil, fmt.Errorf("failed to decode viewing state: %w", err)
	}
	now := time.Now().UnixMilli()
	leases := viewingLeases{}
	for tab, expiresAt := range stored {
		if expiresAt > now {
			leases[tab] = expiresAt
		}
	}
	return raw, leases, nil
}

// RecipientIsViewing is IsViewing for notification paths: a failed lookup is
// logged and reported as not viewing, so the notification is still sent.
func (s *Service) RecipientIsViewing(postID, userID string) bool {
	if s == nil || postID == "" || userID == "" {
		return false
	}
	viewing, err := s.IsViewing(postID, userID)
	if err != nil {
		s.client.LogWarn("Failed to read agent post viewing state; notifying anyway", "post_id", postID, "user_id", userID, "error", err)
		return false
	}
	return viewing
}
