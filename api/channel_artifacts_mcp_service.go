// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/model"
)

var errChannelArtifactsUnavailable = errors.New("channel artifacts are not available")

type mcpArtifactConfig interface {
	EnableChannelArtifacts() bool
}

// scopeChannels looks up what a scoped read needs to check a channel against
// its scope.
type scopeChannels interface {
	scopedsession.ServerSettings
	GetChannel(channelID string) (*model.Channel, error)
	GetTeam(teamID string) (*model.Team, error)
}

// MCPChannelArtifacts serves the channel artifact MCP tools in process, so
// plugin-hosted and embedded MCP servers reach the library without an HTTP
// callback through the site URL. The /channel-artifacts/mcp/* routes call the
// same methods for external MCP servers.
type MCPChannelArtifacts struct {
	library  channelartifacts.Library
	config   mcpArtifactConfig
	channels scopeChannels
}

var _ tools.ChannelArtifactsService = (*MCPChannelArtifacts)(nil)

// NewMCPChannelArtifacts returns the in-process channel artifact MCP service.
func NewMCPChannelArtifacts(library channelartifacts.Library, config Config, channels scopeChannels) *MCPChannelArtifacts {
	return &MCPChannelArtifacts{library: library, config: config, channels: channels}
}

func (a *API) mcpChannelArtifacts() *MCPChannelArtifacts {
	return &MCPChannelArtifacts{library: a.channelLibrary, config: a.config, channels: a.mmClient}
}

// checkScope refuses a read of channelID under a scoped session whose scope
// does not cover the channel. The library authorizes the session's user, the
// agent's bot, whose memberships can be wider than the scope.
func (m *MCPChannelArtifacts) checkScope(ctx context.Context, channelID string) error {
	scope, err := scopedsession.ScopeFromContext(ctx)
	if err != nil {
		return err
	}
	if scope == nil {
		return nil
	}
	if m.channels == nil {
		return channelartifacts.ErrForbidden
	}
	channel, err := m.channels.GetChannel(channelID)
	if err != nil {
		return channelartifacts.ErrForbidden
	}
	var team *model.Team
	if channel.TeamId != "" {
		if team, err = m.channels.GetTeam(channel.TeamId); err != nil {
			return err
		}
	}
	if !scopedsession.AllowsChannelContent(scope, channel, team, scopedsession.ServerLimits(m.channels)) {
		return channelartifacts.ErrForbidden
	}
	return nil
}

// Enabled reports whether the library is on.
func (m *MCPChannelArtifacts) Enabled() bool {
	return m.library != nil && m.config.EnableChannelArtifacts()
}

func mcpActor(ctx context.Context, userID string) channelartifacts.Actor {
	return channelartifacts.Actor{UserID: userID, SessionID: auth.SessionIDFromContext(ctx)}
}

// List returns the entries userID may read in channelID.
func (m *MCPChannelArtifacts) List(ctx context.Context, userID, channelID string) ([]*channelartifacts.Entry, error) {
	if !m.Enabled() {
		return nil, errChannelArtifactsUnavailable
	}
	if !model.IsValidId(channelID) {
		return nil, channelartifacts.ErrInvalidInput
	}
	if err := m.checkScope(ctx, channelID); err != nil {
		return nil, err
	}
	resp, err := m.library.List(ctx, mcpActor(ctx, userID), channelID)
	if err != nil {
		return nil, err
	}
	return resp.Entries, nil
}

// Get returns one entry userID may read. With content, the artifact is read
// from the source file under the caller's session file policy and windowed.
func (m *MCPChannelArtifacts) Get(ctx context.Context, userID, artifactID string, opts tools.ChannelArtifactReadOptions) (tools.ChannelArtifactContent, error) {
	if !m.Enabled() {
		return tools.ChannelArtifactContent{}, errChannelArtifactsUnavailable
	}
	if !model.IsValidId(artifactID) {
		return tools.ChannelArtifactContent{}, channelartifacts.ErrInvalidInput
	}
	actor := mcpActor(ctx, userID)
	if scope, scopeErr := scopedsession.ScopeFromContext(ctx); scopeErr != nil || scope != nil {
		entry, getErr := m.library.Get(ctx, actor, artifactID)
		if getErr != nil {
			return tools.ChannelArtifactContent{}, getErr
		}
		if err := m.checkScope(ctx, entry.ChannelID); err != nil {
			return tools.ChannelArtifactContent{}, err
		}
	}
	read, err := m.library.Read(ctx, actor, artifactID, channelartifacts.ReadOptions{
		Version:      opts.Version,
		MetadataOnly: !opts.IncludeContent,
	})
	if err != nil {
		return tools.ChannelArtifactContent{}, err
	}
	if !opts.IncludeContent {
		return tools.ChannelArtifactContent{Entry: read.Entry}, nil
	}
	out := tools.ChannelArtifactWindow(string(read.Body), opts.Offset, opts.Limit)
	out.Entry = read.Entry
	return out, nil
}
