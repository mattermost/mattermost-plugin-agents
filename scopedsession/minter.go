// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scopedsession

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// RunTTL bounds one run's scoped session. Runs revoke their session when they
// end; the expiry is the backstop for a run that never does.
const RunTTL = 30 * time.Minute

// Minter creates and revokes scoped sessions on the Mattermost server.
type Minter interface {
	// CreateScopedSession creates a session for userID that can only read what
	// scope allows, expiring at expiresAt (unix ms).
	CreateScopedSession(userID string, scope *model.SessionScope, expiresAt int64) (*model.Session, error)
	RevokeSession(sessionID string) error
}

// SessionAPI is the plugin API surface PluginAPIMinter needs.
type SessionAPI interface {
	CreateScopedSession(userID string, scope *model.SessionScope, expiresAt int64) (*model.Session, *model.AppError)
	RevokeSession(sessionID string) *model.AppError
}

// PluginAPIMinter mints through the server plugin API. Only use it once Probe
// has confirmed the server can create scoped sessions.
type PluginAPIMinter struct {
	api SessionAPI
}

// NewPluginAPIMinter returns a Minter backed by the plugin API.
func NewPluginAPIMinter(api SessionAPI) *PluginAPIMinter {
	return &PluginAPIMinter{api: api}
}

func (m *PluginAPIMinter) CreateScopedSession(userID string, scope *model.SessionScope, expiresAt int64) (*model.Session, error) {
	session, appErr := m.api.CreateScopedSession(userID, scope, expiresAt)
	if appErr != nil {
		return nil, fmt.Errorf("server refused to create scoped session: %w", appErr)
	}
	if session == nil {
		return nil, errors.New("server returned no scoped session")
	}
	return session, nil
}

func (m *PluginAPIMinter) RevokeSession(sessionID string) error {
	if appErr := m.api.RevokeSession(sessionID); appErr != nil {
		return fmt.Errorf("failed to revoke scoped session: %w", appErr)
	}
	return nil
}

// ErrServerUnsupported is returned by Probe when the server has no
// CreateScopedSession.
var ErrServerUnsupported = errors.New("this Mattermost server cannot create scoped sessions")

// Probe reports whether api's server can create scoped sessions, without
// creating one. It asks for a session with no scope, which a server that has
// CreateScopedSession refuses as a bad request before reading anything. A
// server without the method answers with neither a session nor an error,
// because the plugin RPC client only logs a call to a missing method. Probe
// returns nil only for that bad request: ErrServerUnsupported for a missing
// method, and any other error when the answer was neither.
func Probe(api SessionAPI) error {
	session, appErr := api.CreateScopedSession("", nil, 0)
	switch {
	case session != nil:
		err := errors.New("server created a scoped session without a scope")
		if session.Id != "" {
			if revokeErr := api.RevokeSession(session.Id); revokeErr != nil {
				err = errors.Join(err, revokeErr)
			}
		}
		return err
	case appErr == nil:
		return ErrServerUnsupported
	case appErr.StatusCode != http.StatusBadRequest:
		return fmt.Errorf("scoped session probe failed: %w", appErr)
	}
	return nil
}
