// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	defaultChannelArtifactContentLimit = 8000
)

// ChannelArtifactContent is metadata plus optional truncated HTML for the LLM.
type ChannelArtifactContent struct {
	Entry           *channelartifacts.Entry
	ContentIncluded bool
	Content         string
	TotalRunes      int
	Returned        int
	HasMore         bool
}

// ChannelArtifactsService lists and reads channel library artifacts on behalf of
// a user. HTTPChannelArtifactsService implements it via plugin HTTP callbacks so
// both embedded and external MCP servers share one permission path.
type ChannelArtifactsService interface {
	List(ctx context.Context, userID, channelID string) ([]*channelartifacts.Entry, error)
	Get(ctx context.Context, userID, artifactID string, includeContent bool, contentLimit int) (ChannelArtifactContent, error)
	CanSave(ctx context.Context, userID, postID, fileID string) error
}

// ListChannelArtifactsArgs represents arguments for list_channel_artifacts.
type ListChannelArtifactsArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel whose library artifacts to list,minLength=26,maxLength=26"`
}

// GetChannelArtifactArgs represents arguments for get_channel_artifact.
type GetChannelArtifactArgs struct {
	ArtifactID     string `json:"artifact_id" jsonschema:"The channel library artifact ID,minLength=26,maxLength=26"`
	IncludeContent *bool  `json:"include_content,omitempty" jsonschema:"When true (default), include truncated HTML text for LLM context"`
	ContentLimit   int    `json:"content_limit,omitempty" jsonschema:"Maximum HTML characters to return (default 8000)"`
}

// ProposeChannelArtifactUpdateArgs represents arguments for propose_channel_artifact_update.
type ProposeChannelArtifactUpdateArgs struct {
	ChannelID  string `json:"channel_id" jsonschema:"The channel that owns the library artifact,minLength=26,maxLength=26"`
	ArtifactID string `json:"artifact_id" jsonschema:"The library artifact to propose updating,minLength=26,maxLength=26"`
	PostID     string `json:"post_id" jsonschema:"The post that holds the new HTML file,minLength=26,maxLength=26"`
	FileID     string `json:"file_id" jsonschema:"The new HTML file ID on that post,minLength=26,maxLength=26"`
}

const (
	listChannelArtifactsDescription         = "List saved channel library artifacts (HTML entries in the channel artifact library, not ordinary post file attachments). Parameters: channel_id (required). Returns id, title, version, sha prefix, source agent, and update time for each entry."
	getChannelArtifactDescription           = "Open one channel library artifact by id. Returns metadata and, by default, truncated HTML text for LLM context (not a Mattermost file attachment). Parameters: artifact_id (required), include_content (optional, default true), content_limit (optional, default 8000)."
	proposeChannelArtifactUpdateDescription = "Propose updating a channel library artifact with a new HTML file already posted in the channel. Posts a message asking a channel admin to open the file and use Save → update that library entry. Does not apply the update itself. Parameters: channel_id, artifact_id, post_id, and file_id (all required)."
)

// getChannelArtifactTools returns channel library artifact tools.
func (p *MattermostToolProvider) getChannelArtifactTools() []MCPTool {
	return []MCPTool{
		mcpReadTool(p, "list_channel_artifacts", listChannelArtifactsDescription, p.toolListChannelArtifacts),
		mcpReadTool(p, "get_channel_artifact", getChannelArtifactDescription, p.toolGetChannelArtifact),
		mcpTool(p, "propose_channel_artifact_update", proposeChannelArtifactUpdateDescription, p.toolProposeChannelArtifactUpdate),
	}
}

func (p *MattermostToolProvider) toolListChannelArtifacts(mcpContext *MCPToolContext, args ListChannelArtifactsArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if p.channelArtifactsService == nil {
		return "", fmt.Errorf("channel artifacts are not available")
	}

	entries, err := p.channelArtifactsService.List(mcpContext.Ctx, mcpContext.UserID, args.ChannelID)
	if err != nil {
		if errors.Is(err, channelartifacts.ErrNotFound) {
			return "", fmt.Errorf("channel not found or you do not have permission to read its artifacts")
		}
		return "", fmt.Errorf("error listing channel artifacts: %w", err)
	}
	if len(entries) == 0 {
		return "no channel library artifacts found in this channel", nil
	}
	return format.ChannelArtifactList(entries), nil
}

func (p *MattermostToolProvider) toolGetChannelArtifact(mcpContext *MCPToolContext, args GetChannelArtifactArgs) (string, error) {
	if err := requireID("artifact_id", args.ArtifactID); err != nil {
		return "", err
	}
	if p.channelArtifactsService == nil {
		return "", fmt.Errorf("channel artifacts are not available")
	}

	includeContent := true
	if args.IncludeContent != nil {
		includeContent = *args.IncludeContent
	}
	limit := args.ContentLimit
	if limit <= 0 {
		limit = defaultChannelArtifactContentLimit
	}

	view, err := p.channelArtifactsService.Get(mcpContext.Ctx, mcpContext.UserID, args.ArtifactID, includeContent, limit)
	if err != nil {
		if errors.Is(err, channelartifacts.ErrNotFound) {
			return "", fmt.Errorf("channel artifact not found or you do not have permission to read it; use list_channel_artifacts with the channel_id first")
		}
		return "", fmt.Errorf("error reading channel artifact %s: %w", args.ArtifactID, err)
	}
	if view.Entry == nil {
		return "", fmt.Errorf("channel artifact not found or you do not have permission to read it; use list_channel_artifacts with the channel_id first")
	}

	var b strings.Builder
	b.WriteString("Channel library artifact (saved HTML library entry, not a post file attachment):\n\n")
	format.WriteChannelArtifact(&b, format.ChannelArtifactEntry{Entry: view.Entry})

	if view.ContentIncluded {
		fmt.Fprintf(&b, "HTML content (characters 0-%d of %d)", view.Returned, view.TotalRunes)
		if view.HasMore {
			b.WriteString("; truncated for LLM context — ask a channel admin to open the artifact in the UI for the full document")
		}
		b.WriteString(":\n\n")
		b.WriteString(view.Content)
	}
	return b.String(), nil
}

func (p *MattermostToolProvider) toolProposeChannelArtifactUpdate(mcpContext *MCPToolContext, args ProposeChannelArtifactUpdateArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if err := requireID("artifact_id", args.ArtifactID); err != nil {
		return "", err
	}
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}
	if err := requireID("file_id", args.FileID); err != nil {
		return "", err
	}
	if p.channelArtifactsService == nil {
		return "", fmt.Errorf("channel artifacts are not available")
	}

	view, err := p.channelArtifactsService.Get(mcpContext.Ctx, mcpContext.UserID, args.ArtifactID, false, 0)
	if err != nil {
		if errors.Is(err, channelartifacts.ErrNotFound) {
			return "", fmt.Errorf("channel artifact not found or you do not have permission to read it")
		}
		return "", fmt.Errorf("error loading channel artifact: %w", err)
	}
	if view.Entry == nil || view.Entry.ChannelID != args.ChannelID {
		return "", fmt.Errorf("channel artifact %s is not in channel %s", args.ArtifactID, args.ChannelID)
	}

	post, _, err := mcpContext.Client.GetPost(mcpContext.Ctx, args.PostID, "")
	if err != nil || post == nil {
		return "", fmt.Errorf("error loading post %s: could not find post or you lack access", args.PostID)
	}
	if post.ChannelId != args.ChannelID {
		return "", fmt.Errorf("post %s is not in channel %s", args.PostID, args.ChannelID)
	}
	if !slices.Contains(post.FileIds, args.FileID) {
		return "", fmt.Errorf("file %s is not attached to post %s", args.FileID, args.PostID)
	}

	fileInfo, _, err := mcpContext.Client.GetFileInfo(mcpContext.Ctx, args.FileID)
	if err != nil || fileInfo == nil {
		return "", fmt.Errorf("error loading file %s: could not find file or you lack access", args.FileID)
	}
	if !isHTMLArtifactFile(fileInfo) {
		return "", fmt.Errorf("file %s is not an HTML artifact", args.FileID)
	}
	if saveErr := p.channelArtifactsService.CanSave(mcpContext.Ctx, mcpContext.UserID, args.PostID, args.FileID); saveErr != nil {
		return "", fmt.Errorf("this file cannot be saved to the channel library: %w", saveErr)
	}

	title := view.Entry.Title
	if title == "" {
		title = view.Entry.ID
	}
	fileLabel := fileInfo.Name
	if fileLabel == "" {
		fileLabel = args.FileID
	}

	message := fmt.Sprintf(
		"Proposed update to channel artifact **%s** (`%s`). A channel admin can open the new HTML file **%s** (`%s`) on post `%s` and choose Save → update that library entry.",
		literalChannelLabel(title), args.ArtifactID, literalChannelLabel(fileLabel), args.FileID, args.PostID,
	)

	postOut := &model.Post{
		ChannelId: args.ChannelID,
		Message:   message,
	}
	p.stampAIGenerated(postOut, mcpContext, mcpContext.UserID)

	created, _, err := mcpContext.Client.CreatePost(mcpContext.Ctx, postOut)
	if err != nil {
		return "", fmt.Errorf("error posting artifact update proposal: %w", err)
	}

	return fmt.Sprintf("Posted proposal in channel %s (post %s) for artifact %s (%s).", args.ChannelID, created.Id, args.ArtifactID, title), nil
}

func isHTMLArtifactFile(info *model.FileInfo) bool {
	if info == nil {
		return false
	}
	ext := strings.ToLower(strings.TrimPrefix(info.Extension, "."))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(info.Name), "."))
	}
	return ext == "html" || ext == "htm"
}

// literalChannelLabel keeps a title or file name from becoming a mention or
// markdown when it is interpolated into the proposal post.
func literalChannelLabel(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', '*', '_', '`', '[', ']', '~', '>':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '@':
			b.WriteRune('@')
			b.WriteRune('\u200b')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
