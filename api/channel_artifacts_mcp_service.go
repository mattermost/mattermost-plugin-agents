// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost/server/public/model"
)

var errChannelArtifactsUnavailable = errors.New("channel artifacts are not available")

type mcpArtifactConfig interface {
	EnableChannelArtifacts() bool
}

// MCPChannelArtifacts serves the channel artifact MCP tools in process, so
// plugin-hosted and embedded MCP servers reach the library without an HTTP
// callback through the site URL. The /channel-artifacts/mcp/* routes call the
// same methods for external MCP servers.
type MCPChannelArtifacts struct {
	library channelartifacts.Library
	config  mcpArtifactConfig
}

var _ tools.ChannelArtifactsService = (*MCPChannelArtifacts)(nil)

// NewMCPChannelArtifacts returns the in-process channel artifact MCP service.
func NewMCPChannelArtifacts(library channelartifacts.Library, config Config) *MCPChannelArtifacts {
	return &MCPChannelArtifacts{library: library, config: config}
}

func (a *API) mcpChannelArtifacts() *MCPChannelArtifacts {
	return &MCPChannelArtifacts{library: a.channelLibrary, config: a.config}
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
	read, err := m.library.Read(ctx, mcpActor(ctx, userID), artifactID, channelartifacts.ReadOptions{
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
