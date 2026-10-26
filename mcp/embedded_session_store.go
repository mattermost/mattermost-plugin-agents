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
// The created session is tagged for MCP via Props
// The second return reports whether the session was granted anew: a fresh
// mint, or a lapsed session whose expiry was extended. Only silent reuse of a
// still-valid session returns false — audit logging keys off this.
func (m *ClientManager) ensureEmbeddedSessionID(userID string) (string, bool, error) {
	session, granted, err := m.ensureEmbeddedSession(userID)
	if err != nil {
		return "", false, err
	}
	return session.Id, granted, nil
}

// ensureEmbeddedSession is ensureEmbeddedSessionID returning the session
// itself, so callers can connect without reading it back (see
// EmbeddedServerClient.CreateClientForSession).
func (m *ClientManager) ensureEmbeddedSession(userID string) (*model.Session, bool, error) {
	user, err := m.pluginAPI.User.Get(userID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to fetch user for embedded session: %w", err)
	}
	if user.DeleteAt != 0 {
		return nil, false, fmt.Errorf("cannot create embedded session for deleted user")
	}
	reused, renewed, reuseErr := m.tryReuseEmbeddedSession(user.Id)
	if reuseErr != nil {
		return nil, false, reuseErr
	}
	if reused != nil {
		return reused, renewed, nil
	}

	session, err := m.createEmbeddedSession(user)
	if err != nil {
		return nil, false, err
	}
	return session, true, nil
}

// tryReuseEmbeddedSession returns the stored session when still usable. The
// second return reports whether a lapsed session's expiry had to be extended
// — re-enabling an expired credential is a new grant, not silent reuse.
func (m *ClientManager) tryReuseEmbeddedSession(userID string) (*model.Session, bool, error) {
	stored, err := m.loadEmbeddedSessionID(userID)
	if err != nil {
		m.log.Debug("Failed to load embedded session ID", "userID", userID, "error", err)
		return nil, false, nil
	}

	if stored == "" {
		return nil, false, nil
	}

	sess, getErr := m.pluginAPI.Session.Get(stored)
	if getErr != nil || sess == nil || sess.UserId != userID {
		m.log.Debug("Stored embedded session invalid or missing", "userID", userID, "error", getErr)
		if deleteErr := m.deleteEmbeddedSessionID(userID); deleteErr != nil {
			m.log.Debug("Failed to delete stale embedded session key", "userID", userID, "error", deleteErr)
		}
		return nil, false, nil
	}

	if !sess.IsExpired() {
		return sess, false, nil
	}

	newExpiry := time.Now().Add(m.sessionLengthDuration()).UnixMilli()
	if err := m.pluginAPI.Session.ExtendExpiry(stored, newExpiry); err == nil {
		m.log.Debug("Extended embedded session expiry", "userID", userID)
		return sess, true, nil
	}

	m.log.Debug("Failed to extend embedded session", "userID", userID)
	return nil, false, nil
}

func (m *ClientManager) createEmbeddedSession(user *model.User) (*model.Session, error) {
	sessionDuration := m.sessionLengthDuration()
	expiresAt := time.Now().Add(sessionDuration).UnixMilli()

	newSession := &model.Session{
		UserId:    user.Id,
		Props:     map[string]string{"isMCP": "true"},
		Roles:     user.GetRawRoles(),
		ExpiresAt: expiresAt,
	}

	if user.IsBot {
		newSession.AddProp(model.SessionPropIsBot, model.SessionPropIsBotValue)
	}

	if user.IsGuest() {
		newSession.AddProp(model.SessionPropIsGuest, "true")
	} else {
		newSession.AddProp(model.SessionPropIsGuest, "false")
	}

	created, err := m.pluginAPI.Session.Create(newSession)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedded session: %w", err)
	}

	if created == nil || created.Id == "" {
		return nil, fmt.Errorf("embedded session creation returned empty result")
	}

	if err := m.storeEmbeddedSessionID(user.Id, created.Id); err != nil {
		return nil, err
	}

	return created, nil
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
