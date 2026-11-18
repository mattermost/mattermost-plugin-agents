// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	defaultMCPArtifactContentLimit = 8000
	maxMCPArtifactContentLimit     = 20000
)

var errChannelArtifactsUnavailable = errors.New("channel artifacts are not available")

// mcpStatusError carries the HTTP status the callback routes answer with and
// the message the MCP tool shows when the same check runs in process.
type mcpStatusError struct {
	status  int
	message string
}

func (e *mcpStatusError) Error() string { return e.message }

type mcpArtifactConfig interface {
	EnableChannelArtifacts() bool
	DisableHumanChannelArtifacts() bool
}

// MCPChannelArtifacts serves the channel artifact MCP tools in process, so
// plugin-hosted and embedded MCP servers reach the library without an HTTP
// callback through the site URL. The /channel-artifacts/mcp/* routes call the
// same methods for external MCP servers.
type MCPChannelArtifacts struct {
	svc    *channelartifacts.Service
	mm     mmapi.Client
	config mcpArtifactConfig
	bots   *bots.MMBots
}

var _ tools.ChannelArtifactsService = (*MCPChannelArtifacts)(nil)

// NewMCPChannelArtifacts returns the in-process channel artifact MCP service.
func NewMCPChannelArtifacts(svc *channelartifacts.Service, mm mmapi.Client, config Config, botRegistry *bots.MMBots) *MCPChannelArtifacts {
	return &MCPChannelArtifacts{svc: svc, mm: mm, config: config, bots: botRegistry}
}

func (a *API) mcpChannelArtifacts() *MCPChannelArtifacts {
	return &MCPChannelArtifacts{svc: a.channelArtifacts, mm: a.mmClient, config: a.config, bots: a.bots}
}

func (m *MCPChannelArtifacts) available() bool {
	return m.svc != nil && m.config.EnableChannelArtifacts()
}

// List returns the entries userID may read in channelID.
func (m *MCPChannelArtifacts) List(_ context.Context, userID, channelID string) ([]*channelartifacts.Entry, error) {
	if !m.available() {
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

// Get returns one entry userID may read, with its HTML truncated to contentLimit runes.
func (m *MCPChannelArtifacts) Get(_ context.Context, userID, artifactID string, includeContent bool, contentLimit int) (tools.ChannelArtifactContent, error) {
	if !m.available() {
		return tools.ChannelArtifactContent{}, errChannelArtifactsUnavailable
	}
	if !model.IsValidId(artifactID) {
		return tools.ChannelArtifactContent{}, channelartifacts.ErrInvalidInput
	}
	entry, err := m.svc.GetReadable(userID, artifactID)
	if err != nil {
		return tools.ChannelArtifactContent{}, err
	}
	out := tools.ChannelArtifactContent{Entry: entry}
	if !includeContent {
		return out, nil
	}
	body, err := m.svc.ReadContent(entry)
	if err != nil {
		return tools.ChannelArtifactContent{}, err
	}
	out.Content, out.TotalRunes, out.Returned, out.HasMore = truncateUTF8(string(body), contentLimit)
	out.ContentIncluded = true
	return out, nil
}

// CanSave reports whether userID may save the post attachment into the library.
// The file policy comes from the session on ctx.
func (m *MCPChannelArtifacts) CanSave(ctx context.Context, userID, postID, fileID string) error {
	if !m.available() || m.mm == nil {
		return errChannelArtifactsUnavailable
	}
	if !model.IsValidId(postID) || !model.IsValidId(fileID) {
		return &mcpStatusError{http.StatusBadRequest, "a valid post_id and file_id are required"}
	}
	client := mmapi.WithFilePolicy(m.mm, auth.SessionIDFromContext(ctx))
	info, err := client.GetFileInfo(fileID)
	if err != nil || info == nil {
		return &mcpStatusError{http.StatusNotFound, "not found"}
	}
	post, err := client.GetPost(postID)
	if err != nil || post == nil {
		return &mcpStatusError{http.StatusNotFound, "not found"}
	}
	if !client.HasPermissionToChannel(userID, post.ChannelId, model.PermissionReadChannel) {
		return &mcpStatusError{http.StatusNotFound, "not found"}
	}
	if !isArtifactExtension(info) {
		return &mcpStatusError{http.StatusNotFound, "file is not an HTML artifact"}
	}
	if info.PostId == "" || info.PostId != postID || !slices.Contains(post.FileIds, fileID) {
		return &mcpStatusError{http.StatusNotFound, "file is not attached to the post"}
	}
	if m.config.DisableHumanChannelArtifacts() && (m.bots == nil || !m.bots.IsAnyBot(post.UserId)) {
		return &mcpStatusError{http.StatusForbidden, "channel artifacts only accept files posted by agents"}
	}
	if info.Size > artifactRenderLimit(m.mm.GetConfig()) {
		return &mcpStatusError{http.StatusRequestEntityTooLarge, "artifact too large"}
	}
	return nil
}

func truncateUTF8(s string, limit int) (text string, total, returned int, hasMore bool) {
	total = utf8.RuneCountInString(s)
	if limit <= 0 {
		limit = defaultMCPArtifactContentLimit
	}
	if limit > maxMCPArtifactContentLimit {
		limit = maxMCPArtifactContentLimit
	}
	if total <= limit {
		return s, total, total, false
	}
	runes := []rune(s)
	return string(runes[:limit]), total, limit, true
}
