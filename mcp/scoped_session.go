// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/model"
)

// ErrScopedSessionsUnavailable is returned when this plugin instance has no
// way to mint scoped sessions.
var ErrScopedSessionsUnavailable = errors.New("scoped sessions are not available")

// ScopedSessionRequest describes one channel-triggered run of an agent whose
// tools read through a scoped session for its bot.
type ScopedSessionRequest struct {
	BotUserID string
	// RequesterID is the human the run acts for; it is recorded on the
	// session for attribution and never grants access.
	RequesterID string
	// ChannelID is the channel the run was triggered in.
	ChannelID string
	// ExtraChannelIDs are admin-picked channels the agent may also read.
	ExtraChannelIDs []string
}

// ScopedRun is one run's scoped session and the embedded MCP connection that
// uses it. It is never pooled, cached, or written to KV: Close revokes the
// session and closes the connection.
type ScopedRun struct {
	session  *model.Session
	scope    *scopedsession.Scope
	clients  *UserClients
	manager  *ClientManager
	minter   scopedsession.Minter
	registry *scopedsession.Registry
	closeErr error
	once     sync.Once
}

// Session returns the run's scoped session.
func (r *ScopedRun) Session() *model.Session {
	if r == nil {
		return nil
	}
	return r.session
}

// Scope returns the scope the run's session was minted with.
func (r *ScopedRun) Scope() *scopedsession.Scope {
	if r == nil {
		return nil
	}
	return r.scope
}

// BotUserID returns the bot the run's session belongs to.
func (r *ScopedRun) BotUserID() string {
	if r == nil || r.session == nil {
		return ""
	}
	return r.session.UserId
}

// Close revokes the session and closes the run's MCP connection. It is safe
// to call more than once and from any goroutine.
func (r *ScopedRun) Close() error {
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		r.registry.Revoke(r.session.Id)
		r.manager.forgetScopedRun(r)
		r.clients.Close()
		if err := r.minter.RevokeSession(r.session.Id); err != nil {
			r.manager.log.Warn("Failed to revoke scoped session; it expires on its own",
				"botUserID", r.session.UserId, "error", err)
			r.closeErr = err
		}
	})
	return r.closeErr
}

// SetScopedSessions configures how scoped sessions are minted and where
// plugin services look up their scope. Both are required to mint.
func (m *ClientManager) SetScopedSessions(minter scopedsession.Minter, registry *scopedsession.Registry) {
	m.scopedMu.Lock()
	defer m.scopedMu.Unlock()
	m.scopedMinter = minter
	m.scopedRegistry = registry
}

// MintScopedSession creates a scoped session for req's bot. The session can
// read req's channel, its extra channels, and every public channel on an open
// team, and nothing else. The caller owns the run and must Close it when the
// run ends.
func (m *ClientManager) MintScopedSession(req ScopedSessionRequest) (*ScopedRun, error) {
	for name, id := range map[string]string{"bot user": req.BotUserID, "requester": req.RequesterID, "channel": req.ChannelID} {
		if !model.IsValidId(id) {
			return nil, fmt.Errorf("scoped session %s ID is invalid", name)
		}
	}

	m.scopedMu.Lock()
	minter, registry := m.scopedMinter, m.scopedRegistry
	m.scopedMu.Unlock()
	if minter == nil || registry == nil {
		return nil, ErrScopedSessionsUnavailable
	}

	channelIDs := []string{req.ChannelID}
	for _, id := range req.ExtraChannelIDs {
		if model.IsValidId(id) && !slices.Contains(channelIDs, id) {
			channelIDs = append(channelIDs, id)
		}
	}
	scope := &scopedsession.Scope{
		Permissions: scopedsession.ReadPermissions(),
		Resources: scopedsession.Resources{
			ChannelIDs:             channelIDs,
			OpenTeamPublicChannels: true,
		},
		OnBehalfOfUserID: req.RequesterID,
	}

	session, err := minter.CreateScopedSession(req.BotUserID, scope, time.Now().Add(scopedsession.RunTTL).UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("failed to create scoped session: %w", err)
	}
	if session == nil || session.Id == "" || session.UserId != req.BotUserID {
		if session != nil && session.Id != "" {
			if revokeErr := minter.RevokeSession(session.Id); revokeErr != nil {
				m.log.Warn("Failed to revoke malformed scoped session", "error", revokeErr)
			}
		}
		return nil, errors.New("scoped session creation returned an unusable session")
	}

	registry.Register(session, scope)
	clients := newLocalClients(req.BotUserID, m.log, m.httpClient, m.toolsCache)
	clients.admission = m.admission
	run := &ScopedRun{session: session, scope: scope, clients: clients, manager: m, minter: minter, registry: registry}

	m.scopedMu.Lock()
	closed := m.scopedClosed
	if !closed {
		if m.scopedRuns == nil {
			m.scopedRuns = make(map[*ScopedRun]struct{})
		}
		m.scopedRuns[run] = struct{}{}
	}
	m.scopedMu.Unlock()
	if closed {
		_ = run.Close()
		return nil, ErrScopedSessionsUnavailable
	}
	return run, nil
}

func (m *ClientManager) forgetScopedRun(run *ScopedRun) {
	m.scopedMu.Lock()
	defer m.scopedMu.Unlock()
	delete(m.scopedRuns, run)
}

// RevokeScopedSessionsForBot ends every live scoped run of botUserID on this
// node, for when the agent's scoped mode is turned off or its bot goes away.
func (m *ClientManager) RevokeScopedSessionsForBot(botUserID string) {
	for _, run := range m.liveScopedRuns() {
		if run.BotUserID() == botUserID {
			_ = run.Close()
		}
	}
}

func (m *ClientManager) closeAllScopedRuns() {
	for _, run := range m.liveScopedRuns() {
		_ = run.Close()
	}
}

func (m *ClientManager) liveScopedRuns() []*ScopedRun {
	m.scopedMu.Lock()
	defer m.scopedMu.Unlock()
	runs := make([]*ScopedRun, 0, len(m.scopedRuns))
	for run := range m.scopedRuns {
		runs = append(runs, run)
	}
	return runs
}

// ScopedCatalogAllows reports whether an MCP tool may be in a scoped run's
// catalog: read-only embedded Mattermost tools and remote tools reached with
// service-account credentials. Other plugins' MCP servers are excluded, since
// they cannot enforce the scope. Built-in tools are not MCP tools and are
// never allowed here.
func ScopedCatalogAllows(tool llm.Tool) bool {
	if tool.ServerOrigin == "" {
		return false
	}
	switch ServerKind(tool.ServerOrigin) {
	case ServerKindEmbedded:
		return tool.ReadOnly
	case ServerKindPlugin:
		return false
	default:
		return true
	}
}
