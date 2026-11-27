// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
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
	svc    *channelartifacts.Service
	mm     mmapi.Client
	config mcpArtifactConfig
}

var _ tools.ChannelArtifactsService = (*MCPChannelArtifacts)(nil)

// NewMCPChannelArtifacts returns the in-process channel artifact MCP service.
func NewMCPChannelArtifacts(svc *channelartifacts.Service, mm mmapi.Client, config Config) *MCPChannelArtifacts {
	return &MCPChannelArtifacts{svc: svc, mm: mm, config: config}
}

func (a *API) mcpChannelArtifacts() *MCPChannelArtifacts {
	return &MCPChannelArtifacts{svc: a.channelArtifacts, mm: a.mmClient, config: a.config}
}

// Enabled reports whether the library is on.
func (m *MCPChannelArtifacts) Enabled() bool {
	return m.svc != nil && m.config.EnableChannelArtifacts()
}

// List returns the entries userID may read in channelID.
func (m *MCPChannelArtifacts) List(_ context.Context, userID, channelID string) ([]*channelartifacts.Entry, error) {
	if !m.Enabled() {
		return nil, errChannelArtifactsUnavailable
	}
	if !model.IsValidId(channelID) {
		return nil, channelartifacts.ErrInvalidInput
	}
	resp, err := m.svc.List(userID, channelID)
	if err != nil {
		return nil, err
	}
	return resp.Entries, nil
}

// Get returns one entry userID may read. With content, the HTML is read from
// the source file under the caller's session file policy and windowed.
func (m *MCPChannelArtifacts) Get(ctx context.Context, userID, artifactID string, opts tools.ChannelArtifactReadOptions) (tools.ChannelArtifactContent, error) {
	if !m.Enabled() {
		return tools.ChannelArtifactContent{}, errChannelArtifactsUnavailable
	}
	if !model.IsValidId(artifactID) {
		return tools.ChannelArtifactContent{}, channelartifacts.ErrInvalidInput
	}
	entry, err := m.svc.GetReadable(userID, artifactID)
	if err != nil {
		return tools.ChannelArtifactContent{}, err
	}
	viewed := entry
	if opts.Version > 0 && opts.Version != entry.Version {
		rec, recErr := m.svc.VersionOf(entry, opts.Version)
		if recErr != nil {
			return tools.ChannelArtifactContent{}, recErr
		}
		viewed = entryAtVersion(entry, rec)
	}
	if !opts.IncludeContent {
		return tools.ChannelArtifactContent{Entry: viewed}, nil
	}
	body, err := m.svc.ReadContent(auth.SessionIDFromContext(ctx), entry, opts.Version, artifactRenderLimit(m.mm.GetConfig()))
	if err != nil {
		return tools.ChannelArtifactContent{}, err
	}
	out := tools.ChannelArtifactWindow(string(body), opts.Offset, opts.Limit)
	out.Entry = viewed
	return out, nil
}

// entryAtVersion describes the entry as it was at one version, so the metadata
// returned beside a version's HTML (number, hash, size, source) is that
// version's, not the current one's.
func entryAtVersion(entry *channelartifacts.Entry, rec *channelartifacts.VersionRecord) *channelartifacts.Entry {
	at := *entry
	at.Version = rec.Version
	at.SHA256 = rec.SHA256
	at.Size = rec.Size
	at.SourcePostID = rec.SourcePostID
	at.SourceFileID = rec.SourceFileID
	at.SourceBotUserID = rec.SourceBotUserID
	at.SourceAgentDisplayName = rec.SourceAgentDisplayName
	at.Available = rec.Available
	return &at
}
