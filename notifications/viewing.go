// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package notifications

import (
	"errors"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
)

// ViewingTTL bounds how long a viewing report lasts without a heartbeat. The
// webapp reports every ten seconds while a response is on screen, so a
// closed tab or lost connection stops counting as viewing within one TTL.
const ViewingTTL = 25 * time.Second

const viewingKeyPrefix = "notif_viewing_"

func viewingKey(postID, userID string) string {
	return viewingKeyPrefix + postID + "_" + userID
}

// SetViewing records whether userID has postID on screen in a focused window.
func (s *Service) SetViewing(postID, userID string, viewing bool) error {
	key := viewingKey(postID, userID)
	if !viewing {
		return s.client.KVDelete(key)
	}
	return s.client.KVSetWithExpiry(key, true, ViewingTTL)
}

// IsViewing reports whether userID has postID on screen in a focused window.
func (s *Service) IsViewing(postID, userID string) (bool, error) {
	var viewing bool
	err := s.client.KVGet(viewingKey(postID, userID), &viewing)
	if errors.Is(err, mmapi.ErrKVNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return viewing, nil
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
