// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

// Query parameters the webapp opens from a project channel link: a pane tab,
// or an artifact from the channel's library.
const (
	ProjectPaneQueryParam      = "mm_ai_project"
	LibraryArtifactQueryParam  = "mm_ai_library_artifact"
	projectPaneTabBoard        = "board"
	projectPaneTabChecklist    = "checklist"
	createBookmarkToolName     = "create_channel_bookmark"
	boardBookmarkDisplayName   = "Board"
	runBookmarkDisplayName     = "Checklist"
	charterBookmarkDisplayName = "Charter"
)

// addProjectBookmark bookmarks a link to the project channel with query in
// the channel itself. It runs the turn's own create_channel_bookmark tool, so
// the bookmark is written on the turn's scoped session and write grant, like
// the agent's other bookmarks. A failure is only logged: what the bookmark
// points at is already recorded on the project.
func (s *Service) addProjectBookmark(ctx context.Context, llmCtx *llm.Context, project *store.Project, displayName, query string) {
	link, err := s.projectChannelLink(project.ChannelID, query)
	if err != nil {
		s.deps.Platform.LogError("Failed to build a project bookmark link", "channel_id", project.ChannelID, "error", err)
		return
	}
	if link == "" || llmCtx == nil || llmCtx.Tools == nil {
		return
	}
	lookup, ok := llmCtx.Tools.LookupTool(createBookmarkToolName, mcp.EmbeddedClientKey)
	if !ok {
		s.deps.Platform.LogError("Can't add a project bookmark: the bookmark tool isn't available to this turn", "channel_id", project.ChannelID)
		return
	}
	args, err := json.Marshal(map[string]string{"channel_id": project.ChannelID, "display_name": displayName, "link_url": link})
	if err != nil {
		s.deps.Platform.LogError("Failed to encode a project bookmark", "channel_id", project.ChannelID, "error", err)
		return
	}
	getArgs := func(v any) error { return json.Unmarshal(args, v) }
	if _, err := llmCtx.Tools.ResolveTool(ctx, lookup.RuntimeName, getArgs, llmCtx); err != nil {
		s.deps.Platform.LogError("Failed to add a project bookmark", "channel_id", project.ChannelID, "error", err)
	}
}

// projectChannelLink is the project channel's URL with query, by channel
// name: Mattermost drops the query when it redirects a channel ID URL. It is
// "" when no site URL is configured.
func (s *Service) projectChannelLink(channelID, query string) (string, error) {
	if s.deps.SiteURL == nil {
		return "", nil
	}
	siteURL := strings.TrimRight(strings.TrimSpace(s.deps.SiteURL()), "/")
	if siteURL == "" {
		return "", nil
	}
	channel, err := s.deps.Platform.GetChannel(channelID)
	if err != nil {
		return "", err
	}
	team, err := s.deps.Platform.GetTeam(channel.TeamId)
	if err != nil {
		return "", err
	}
	return siteURL + "/" + url.PathEscape(team.Name) + "/channels/" + url.PathEscape(channel.Name) + "?" + query, nil
}

func paneQuery(tab string) string {
	return url.Values{ProjectPaneQueryParam: {tab}}.Encode()
}

func artifactQuery(artifactID string) string {
	return url.Values{LibraryArtifactQueryParam: {artifactID}}.Encode()
}
