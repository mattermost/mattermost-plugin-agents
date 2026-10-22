// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scopedsession

import (
	"errors"
	"fmt"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/mattermost/mattermost/server/public/model"
)

// RunTTL bounds one run's scoped session. Runs revoke their session when they
// end; the expiry is the backstop for a run that never does.
const RunTTL = 30 * time.Minute

// MinServerVersion is the first Mattermost release with CreateScopedSession.
const MinServerVersion = "12.2.0"

// ErrUnsupported is returned when the server cannot mint scoped sessions.
var ErrUnsupported = errors.New("this Mattermost server cannot create scoped sessions")

// Minter creates and revokes scoped sessions on the Mattermost server.
type Minter interface {
	// CreateScopedSession creates a session for userID that can only read what
	// scope allows, expiring at expiresAt (unix ms).
	CreateScopedSession(userID string, scope *Scope, expiresAt int64) (*model.Session, error)
	RevokeSession(sessionID string) error
}

// SessionRevoker is the plugin API surface PluginAPIMinter needs.
type SessionRevoker interface {
	RevokeSession(sessionID string) *model.AppError
}

// PluginAPIMinter mints through the server plugin API. The server API that
// creates scoped sessions does not exist in any released server yet, so
// CreateScopedSession fails closed with ErrUnsupported.
type PluginAPIMinter struct {
	api SessionRevoker
}

// NewPluginAPIMinter returns a Minter backed by the plugin API.
func NewPluginAPIMinter(api SessionRevoker) *PluginAPIMinter {
	return &PluginAPIMinter{api: api}
}

func (m *PluginAPIMinter) CreateScopedSession(string, *Scope, int64) (*model.Session, error) {
	return nil, ErrUnsupported
}

func (m *PluginAPIMinter) RevokeSession(sessionID string) error {
	if appErr := m.api.RevokeSession(sessionID); appErr != nil {
		return fmt.Errorf("failed to revoke scoped session: %w", appErr)
	}
	return nil
}

// ServerSupports reports whether a server at version can mint scoped
// sessions. An unparseable version fails closed.
func ServerSupports(version string) bool {
	current, err := semver.NewVersion(version)
	if err != nil {
		return false
	}
	return !current.LessThan(semver.MustParse(MinServerVersion))
}
