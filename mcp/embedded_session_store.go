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

// ensureEmbeddedSessionID ensures there is a valid embedded session for the user
// It loads from KV, validates via Session.Get, and if missing/invalid, creates a new one
// The created session is tagged for MCP via DeviceId and Props
func (m *ClientManager) ensureEmbeddedSessionID(userID string) (string, error) {
	// Try existing stored session id
	if stored, err := m.loadEmbeddedSessionID(userID); err == nil && stored != "" {
		if sess, getErr := m.pluginAPI.Session.Get(stored); getErr == nil && sess != nil {
			// Proactively extend if expiring within 24h; ExpiresAt is in milliseconds
			const renewalWindow = 24 * time.Hour
			nowPlusWindowMs := time.Now().Add(renewalWindow).UnixMilli()
			if sess.ExpiresAt == 0 || sess.ExpiresAt > nowPlusWindowMs {
				return stored, nil
			}
			// Extend expiry to align with configured session length
			sessionDuration := m.sessionLengthDuration()
			newExpiryMs := time.Now().Add(sessionDuration).UnixMilli()
			if err = m.pluginAPI.Session.ExtendExpiry(stored, newExpiryMs); err == nil {
				m.log.Debug("Extended embedded session expiry", "userID", userID, "new_expires_at", newExpiryMs)
				return stored, nil
			}
			m.log.Debug("Failed to extend embedded session; creating new", "userID", userID, "expires_at", sess.ExpiresAt)
		} else {
			m.log.Debug("Stored embedded session invalid or missing; creating new", "userID", userID, "sessionID", stored, "error", getErr)
			if deleteErr := m.pluginAPI.KV.Delete(buildEmbeddedSessionKey(userID)); deleteErr != nil {
				m.log.Debug("Failed to delete stale embedded session key", "userID", userID, "error", deleteErr)
			}
		}
	} else if err != nil {
		m.log.Debug("Failed to load embedded session ID; will create new", "userID", userID, "error", err)
	}

	// Create a new dedicated session for embedded MCP usage
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
		DeviceId:  "mcp-embedded",
		Props:     map[string]string{"mcp": "true"},
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

	// Ensure the stored session expiry matches current configuration if the server ignored our suggestion.
	if created.ExpiresAt == 0 || created.ExpiresAt < expiresAt {
		if extendErr := m.pluginAPI.Session.ExtendExpiry(created.Id, expiresAt); extendErr != nil {
			m.log.Debug("Failed to align embedded session expiry with configuration", "userID", userID, "sessionID", created.Id, "error", extendErr)
		} else {
			created.ExpiresAt = expiresAt
		}
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
