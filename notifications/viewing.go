// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package notifications

import (
	"errors"
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

func viewingKey(postID, userID string) string {
	return viewingKeyPrefix + postID + "_" + userID
}

// SetViewing records whether userID has postID on screen in a focused window
// of the browser tab tabID. Each report replaces the previous one; a report
// that the tab stopped viewing clears the state only if that tab made the
// latest report, so another tab, or a late report from the same tab, still
// counts as viewing.
func (s *Service) SetViewing(postID, userID, tabID string, viewing bool) error {
	key := viewingKey(postID, userID)
	if viewing {
		return s.client.KVSetWithExpiry(key, tabID, ViewingTTL)
	}
	_, err := s.client.KVCompareAndSet(key, tabID, nil)
	return err
}

// IsViewing reports whether userID has postID on screen in a focused window.
func (s *Service) IsViewing(postID, userID string) (bool, error) {
	var tab []byte
	err := s.client.KVGet(viewingKey(postID, userID), &tab)
	if errors.Is(err, mmapi.ErrKVNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(tab) > 0, nil
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
