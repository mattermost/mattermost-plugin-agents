// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const embeddedSessionKeyPrefix = "mcp_embedded_session_id"

func buildEmbeddedSessionKey(userID string) string {
	return fmt.Sprintf("%s_%s", embeddedSessionKeyPrefix, userID)
}

// loadEmbeddedSessionID retrieves the stored embedded session ID for a user from KV
// Returns empty string if none is stored
func (m *ClientManager) loadEmbeddedSessionID(userID string) (string, error) {
	key := buildEmbeddedSessionKey(userID)
	var stored []byte
	if err := m.pluginAPI.KV.Get(key, &stored); err != nil {
		return "", fmt.Errorf("failed to retrieve embedded session from KV: %w", err)
	}
	return string(stored), nil
}

// storeEmbeddedSessionID stores the embedded session ID for a user in KV
func (m *ClientManager) storeEmbeddedSessionID(userID, sessionID string) error {
	key := buildEmbeddedSessionKey(userID)
	if _, err := m.pluginAPI.KV.Set(key, []byte(sessionID)); err != nil {
		return fmt.Errorf("failed to store embedded session in KV: %w", err)
	}
	return nil
}

func (m *ClientManager) deleteEmbeddedSessionID(userID string) error {
	return m.pluginAPI.KV.Delete(buildEmbeddedSessionKey(userID))
}

// ensureEmbeddedSessionID ensures there is a valid embedded session for the user
// It loads from KV, validates via Session.Get, and if missing/invalid, creates a new one
// The created session is tagged for MCP via DeviceId and Props
func (m *ClientManager) ensureEmbeddedSessionID(userID string) (string, error) {
	if sessionID, err := m.tryReuseEmbeddedSession(userID); err != nil {
		return "", err
	} else if sessionID != "" {
		return sessionID, nil
	}

	return m.createEmbeddedSession(userID)
}

func (m *ClientManager) tryReuseEmbeddedSession(userID string) (string, error) {
	stored, err := m.loadEmbeddedSessionID(userID)
	if err != nil {
		m.log.Debug("Failed to load embedded session ID", "userID", userID, "error", err)
		return "", nil
	}

	if stored == "" {
		return "", nil
	}

	sess, getErr := m.pluginAPI.Session.Get(stored)
	if getErr != nil || sess == nil {
		m.log.Debug("Stored embedded session invalid or missing", "userID", userID, "error", getErr)
		if deleteErr := m.deleteEmbeddedSessionID(userID); deleteErr != nil {
			m.log.Debug("Failed to delete stale embedded session key", "userID", userID, "error", deleteErr)
		}
		return "", nil
	}

	const renewalWindow = 24 * time.Hour
	renewalDeadline := time.Now().Add(renewalWindow).UnixMilli()
	if sess.ExpiresAt == 0 || sess.ExpiresAt > renewalDeadline {
		return stored, nil
	}

	newExpiry := time.Now().Add(m.sessionLengthDuration()).UnixMilli()
	if err := m.pluginAPI.Session.ExtendExpiry(stored, newExpiry); err == nil {
		m.log.Debug("Extended embedded session expiry", "userID", userID)
		return stored, nil
	}

	m.log.Debug("Failed to extend embedded session", "userID", userID)
	return "", nil
}

func (m *ClientManager) createEmbeddedSession(userID string) (string, error) {
	user, err := m.pluginAPI.User.Get(userID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch user for embedded session: %w", err)
	}

	sessionDuration := m.sessionLengthDuration()
	expiresAt := time.Now().Add(sessionDuration).UnixMilli()
	roles := ""
	if user != nil {
		roles = user.GetRawRoles()
	}

	newSession := &model.Session{
		UserId:    userID,
		Props:     map[string]string{"isMCP": "true"},
		Roles:     roles,
		ExpiresAt: expiresAt,
	}
	created, err := m.pluginAPI.Session.Create(newSession)
	if err != nil {
		return "", fmt.Errorf("failed to create embedded session: %w", err)
	}

	if created == nil || created.Id == "" {
		return "", fmt.Errorf("embedded session creation returned empty result")
	}

	if err := m.storeEmbeddedSessionID(userID, created.Id); err != nil {
		return "", err
	}

	return created.Id, nil
}

func (m *ClientManager) sessionLengthDuration() time.Duration {
	const defaultDuration = 30 * 24 * time.Hour

	config := m.pluginAPI.Configuration.GetConfig()
	if config == nil {
		return defaultDuration
	}

	if hoursPtr := config.ServiceSettings.SessionLengthWebInHours; hoursPtr != nil && *hoursPtr > 0 {
		return time.Duration(*hoursPtr) * time.Hour
	}

	if daysPtr := config.ServiceSettings.SessionLengthWebInDays; daysPtr != nil && *daysPtr > 0 {
		return time.Duration(*daysPtr) * 24 * time.Hour
	}

	return defaultDuration
}
