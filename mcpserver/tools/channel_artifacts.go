// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
)

const (
	// DefaultChannelArtifactContentLimit is how many characters of an
	// artifact's HTML get_channel_artifact returns when the caller names no limit.
	DefaultChannelArtifactContentLimit = 8000
	// MaxChannelArtifactContentLimit is the most characters one call returns.
	MaxChannelArtifactContentLimit = 20000
)

// ChannelArtifactReadOptions selects what Get returns.
type ChannelArtifactReadOptions struct {
	IncludeContent bool
	// Offset is the character (rune) offset of the first character returned.
	Offset int
	// Limit is the most characters returned; 0 means the default.
	Limit int
	// Version is the version to read; 0 means the current version.
	Version int
}

// ChannelArtifactContent is metadata plus an optional window of the HTML for the LLM.
type ChannelArtifactContent struct {
	Entry           *channelartifacts.Entry `json:"entry"`
	ContentIncluded bool                    `json:"content_included"`
	Content         string                  `json:"content,omitempty"`
	Offset          int                     `json:"offset,omitempty"`
	TotalRunes      int                     `json:"total_runes,omitempty"`
	Returned        int                     `json:"returned,omitempty"`
	HasMore         bool                    `json:"has_more,omitempty"`
	NextOffset      int                     `json:"next_offset,omitempty"`
}

// ChannelArtifactWindow returns the characters of text from offset, at most
// limit of them, with the bookkeeping a caller needs to read the rest.
func ChannelArtifactWindow(text string, offset, limit int) ChannelArtifactContent {
	if limit <= 0 {
		limit = DefaultChannelArtifactContentLimit
	}
	limit = min(limit, MaxChannelArtifactContentLimit)
	offset = max(offset, 0)
	total := utf8.RuneCountInString(text)
	out := ChannelArtifactContent{ContentIncluded: true, TotalRunes: total, Offset: offset}
	if offset >= total {
		return out
	}
	runes := []rune(text)
	end := min(offset+limit, total)
	out.Content = string(runes[offset:end])
	out.Returned = end - offset
	if end < total {
		out.HasMore = true
		out.NextOffset = end
	}
	return out
}

// ChannelArtifactsService lists and reads channel library artifacts on behalf of
// a user. The plugin passes an in-process implementation to the servers it
// hosts; HTTPChannelArtifactsService reaches the same checks through plugin
// HTTP callbacks for external MCP servers.
type ChannelArtifactsService interface {
	// Enabled reports whether the library is on. The tools are hidden when it is not.
	Enabled() bool
	List(ctx context.Context, userID, channelID string) ([]*channelartifacts.Entry, error)
	Get(ctx context.Context, userID, artifactID string, opts ChannelArtifactReadOptions) (ChannelArtifactContent, error)
}

// ListChannelArtifactsArgs represents arguments for list_channel_artifacts.
type ListChannelArtifactsArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel whose saved artifacts to list,minLength=26,maxLength=26"`
}

// GetChannelArtifactArgs represents arguments for get_channel_artifact.
type GetChannelArtifactArgs struct {
	ArtifactID     string `json:"artifact_id" jsonschema:"The channel artifact ID from list_channel_artifacts,minLength=26,maxLength=26"`
	IncludeContent *bool  `json:"include_content,omitempty" jsonschema:"When true (default), include the artifact source (HTML or Markdown) as text"`
	Offset         int    `json:"offset,omitempty" jsonschema:"Character offset to start reading the content from (default 0),minimum=0"`
	ContentLimit   int    `json:"content_limit,omitempty" jsonschema:"Maximum content characters to return (default 8000, maximum 20000),minimum=0,maximum=20000"`
	Version        int    `json:"version,omitempty" jsonschema:"Version to read (default: the current version),minimum=0"`
}

const (
	listChannelArtifactsDescription = "List the interactive HTML apps (dashboards, calculators, reports, small tools) and Markdown documents (charters, summaries, notes) saved to a channel's Channel artifacts panel. " +
		"Use when the user refers to a saved, pinned or shared artifact or app in a channel, or before opening one. " +
		"Requires channel_id; use get_channel_info or search_channels to find it from a channel name. " +
		"Returns id, title, version, source agent, whether the source message and file still exist, and last update time."
	getChannelArtifactDescription = "Open one saved channel artifact by id (from list_channel_artifacts). " +
		"Returns metadata and, by default, the artifact's source (HTML or Markdown) as text so you can answer questions about it or revise it. " +
		"The content is read from the original message's file, so it is unavailable once that message or file is deleted. " +
		"Parameters: artifact_id (required), include_content (default true), offset (character offset, default 0), " +
		"content_limit (characters, default 8000, maximum 20000), version (default: current). " +
		"When has_more is true, call again with offset set to next_offset to read the rest."
)

// getChannelArtifactTools returns the channel artifact read tools. The
// provider hides them when the library is turned off.
func (p *MattermostToolProvider) getChannelArtifactTools() []MCPTool {
	return []MCPTool{
		mcpReadTool(p, channelartifacts.ListToolName, listChannelArtifactsDescription, p.toolListChannelArtifacts),
		mcpReadTool(p, channelartifacts.GetToolName, getChannelArtifactDescription, p.toolGetChannelArtifact),
	}
}

// channelArtifactToolNames are the tools the library switch hides.
var channelArtifactToolNames = map[string]struct{}{
	channelartifacts.ListToolName: {},
	channelartifacts.GetToolName:  {},
}

func (p *MattermostToolProvider) toolListChannelArtifacts(mcpContext *MCPToolContext, args ListChannelArtifactsArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if p.channelArtifactsService == nil {
		return "", errors.New("channel artifacts are not available")
	}

	entries, err := p.channelArtifactsService.List(mcpContext.Ctx, mcpContext.UserID, args.ChannelID)
	if err != nil {
		if errors.Is(err, channelartifacts.ErrNotFound) {
			return "", errors.New("channel not found or you do not have permission to read its artifacts")
		}
		return "", fmt.Errorf("error listing channel artifacts: %w", err)
	}
	if len(entries) == 0 {
		return "no saved artifacts found in this channel", nil
	}
	return format.ChannelArtifactList(entries), nil
}

func (p *MattermostToolProvider) toolGetChannelArtifact(mcpContext *MCPToolContext, args GetChannelArtifactArgs) (string, error) {
	if err := requireID("artifact_id", args.ArtifactID); err != nil {
		return "", err
	}
	if p.channelArtifactsService == nil {
		return "", errors.New("channel artifacts are not available")
	}

	opts := ChannelArtifactReadOptions{
		IncludeContent: args.IncludeContent == nil || *args.IncludeContent,
		Offset:         args.Offset,
		Limit:          args.ContentLimit,
		Version:        args.Version,
	}
	view, err := p.channelArtifactsService.Get(mcpContext.Ctx, mcpContext.UserID, args.ArtifactID, opts)
	switch {
	case errors.Is(err, channelartifacts.ErrNotFound):
		return "", errors.New("channel artifact not found or you do not have permission to read it; use list_channel_artifacts with the channel_id first")
	case errors.Is(err, channelartifacts.ErrUnavailable):
		return "", errors.New("the artifact's source message or file was deleted, so its content is no longer available")
	case err != nil:
		return "", fmt.Errorf("error reading channel artifact %s: %w", args.ArtifactID, err)
	case view.Entry == nil:
		return "", errors.New("channel artifact not found or you do not have permission to read it")
	}

	var b strings.Builder
	b.WriteString("Saved channel artifact (interactive HTML app or Markdown document, not a post file attachment):\n\n")
	format.WriteChannelArtifact(&b, format.ChannelArtifactEntry{Entry: view.Entry})

	if view.ContentIncluded {
		fmt.Fprintf(&b, "HTML content (characters %d-%d of %d)", view.Offset, view.Offset+view.Returned, view.TotalRunes)
		if view.HasMore {
			fmt.Fprintf(&b, "; more remains: call again with offset=%d", view.NextOffset)
		}
		b.WriteString(":\n\n")
		b.WriteString(view.Content)
	}
	return b.String(), nil
}

// channelArtifactsToolsMiddleware omits the channel artifact tools from
// tools/list and answers a tools/call of one with a tool error while the
// library is off. enabled is evaluated per request, so a configuration change
// applies without rebuilding the server.
func channelArtifactsToolsMiddleware(enabled func() bool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				if _, gated := channelArtifactToolNames[mcpCallToolName(req)]; gated && !enabled() {
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: "channel artifacts are not available"}},
						IsError: true,
					}, nil
				}
				return next(ctx, method, req)
			}
			result, err := next(ctx, method, req)
			if err != nil || method != "tools/list" || enabled() {
				return result, err
			}
			listResult, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, nil
			}
			listResult.Tools = slices.DeleteFunc(listResult.Tools, func(tool *mcp.Tool) bool {
				_, gated := channelArtifactToolNames[tool.Name]
				return gated
			})
			return listResult, nil
		}
	}
}
