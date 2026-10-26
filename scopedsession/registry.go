// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scopedsession

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost/server/public/model"
)

// ErrRevoked is returned for a scoped session this node already revoked.
var ErrRevoked = errors.New("scoped session has been revoked")

// SessionGetter reads a session by ID from the server.
type SessionGetter func(sessionID string) (*model.Session, error)

// Registry resolves the scope of the session a plugin-side read runs under.
//
// Scoped sessions minted on this node are answered from memory: the plugin
// API reads sessions from a DB replica, which can miss a just-minted one.
// Any other session is read from the server, so a scope minted elsewhere is
// still honored, and a failed lookup is an error, never "unscoped".
type Registry struct {
	getSession SessionGetter

	mu       sync.Mutex
	sessions map[string]registered
}

type registered struct {
	scope     *model.SessionScope
	expiresAt int64
	revoked   bool
}

// NewRegistry creates a registry that reads unknown sessions with getSession.
func NewRegistry(getSession SessionGetter) *Registry {
	return &Registry{getSession: getSession, sessions: make(map[string]registered)}
}

// Register records a session this node minted or created with its scope; a
// nil scope records an unscoped session.
func (r *Registry) Register(session *model.Session, scope *model.SessionScope) {
	if r == nil || session == nil || session.Id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(model.GetMillis())
	r.sessions[session.Id] = registered{scope: scope, expiresAt: session.ExpiresAt}
}

// Revoke marks a registered session revoked until it would have expired, so
// a read still in flight when its run ends is denied rather than treated as
// unscoped.
func (r *Registry) Revoke(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.sessions[sessionID]; ok {
		entry.revoked = true
		r.sessions[sessionID] = entry
	}
}

func (r *Registry) sweepLocked(now int64) {
	for id, entry := range r.sessions {
		if entry.expiresAt > 0 && entry.expiresAt < now {
			delete(r.sessions, id)
		}
	}
}

// ErrNoRegistry is returned when a read under a session has no registry to
// resolve that session's scope.
var ErrNoRegistry = errors.New("no scoped session registry configured")

// ScopeForSession returns the session's scope, or nil for an unscoped
// session. An empty sessionID is unscoped: scoped sessions always reach
// plugin services with their session ID. A nil registry cannot tell, so it
// fails every read under a session.
func (r *Registry) ScopeForSession(sessionID string) (*model.SessionScope, error) {
	if sessionID == "" {
		return nil, nil
	}
	if r == nil {
		return nil, ErrNoRegistry
	}

	r.mu.Lock()
	entry, ok := r.sessions[sessionID]
	r.mu.Unlock()
	if ok {
		if entry.revoked {
			return nil, ErrRevoked
		}
		return entry.scope, nil
	}

	if r.getSession == nil {
		return nil, fmt.Errorf("cannot resolve session scope: no session lookup configured")
	}
	session, err := r.getSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve session scope: %w", err)
	}
	if session == nil {
		return nil, fmt.Errorf("failed to resolve session scope: session not found")
	}
	scope, scoped := session.Scope()
	if !scoped {
		return nil, nil
	}
	return scope, nil
}

// ScopeFromContext is ScopeForSession for the session carried by ctx.
func (r *Registry) ScopeFromContext(ctx context.Context) (*model.SessionScope, error) {
	return r.ScopeForSession(auth.SessionIDFromContext(ctx))
}
