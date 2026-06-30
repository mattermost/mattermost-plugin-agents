// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// ListChannelBookmarksArgs represents arguments for the list_channel_bookmarks tool.
type ListChannelBookmarksArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel whose bookmarks to list,minLength=26,maxLength=26"`
}

// CreateChannelBookmarkArgs represents arguments for the create_channel_bookmark tool.
type CreateChannelBookmarkArgs struct {
	ChannelID   string `json:"channel_id" jsonschema:"The channel to add the bookmark to,minLength=26,maxLength=26"`
	DisplayName string `json:"display_name" jsonschema:"The bookmark display name,minLength=1"`
	LinkURL     string `json:"link_url,omitempty" jsonschema:"The bookmark link URL (for a link bookmark)"`
	FileID      string `json:"file_id,omitempty" jsonschema:"The file ID (for a file bookmark); provide link_url OR file_id,maxLength=26"`
	Emoji       string `json:"emoji,omitempty" jsonschema:"Optional emoji name for the bookmark"`
}

// UpdateChannelBookmarkArgs represents arguments for the update_channel_bookmark tool.
type UpdateChannelBookmarkArgs struct {
	ChannelID   string  `json:"channel_id" jsonschema:"The channel the bookmark belongs to,minLength=26,maxLength=26"`
	BookmarkID  string  `json:"bookmark_id" jsonschema:"The bookmark to update,minLength=26,maxLength=26"`
	DisplayName *string `json:"display_name,omitempty" jsonschema:"New display name (optional)"`
	LinkURL     *string `json:"link_url,omitempty" jsonschema:"New link URL (optional)"`
	Emoji       *string `json:"emoji,omitempty" jsonschema:"New emoji (optional)"`
}

// DeleteChannelBookmarkArgs represents arguments for the delete_channel_bookmark tool.
type DeleteChannelBookmarkArgs struct {
	ChannelID  string `json:"channel_id" jsonschema:"The channel the bookmark belongs to,minLength=26,maxLength=26"`
	BookmarkID string `json:"bookmark_id" jsonschema:"The bookmark to remove,minLength=26,maxLength=26"`
}

const (
	listChannelBookmarksDescription  = "List a channel's bookmarks (pinned links and files). Parameters: channel_id (required)."
	createChannelBookmarkDescription = "Add a link or file bookmark to a channel. Parameters: channel_id (required), display_name (required), link_url OR file_id, emoji (optional)."
	updateChannelBookmarkDescription = "Edit a channel bookmark (name, link, or emoji). Parameters: channel_id (required), bookmark_id (required), plus any of display_name, link_url, emoji."
	deleteChannelBookmarkDescription = "Remove a channel bookmark. Parameters: channel_id (required), bookmark_id (required)."
)

// getBookmarkTools returns the channel bookmark tools.
func (p *MattermostToolProvider) getBookmarkTools() []MCPTool {
	return []MCPTool{
		{Name: "list_channel_bookmarks", Description: listChannelBookmarksDescription, Schema: NewJSONSchemaForAccessMode[ListChannelBookmarksArgs](string(p.accessMode)), Resolver: typed("list_channel_bookmarks", p.toolListChannelBookmarks)},
		{Name: "create_channel_bookmark", Description: createChannelBookmarkDescription, Schema: NewJSONSchemaForAccessMode[CreateChannelBookmarkArgs](string(p.accessMode)), Resolver: typed("create_channel_bookmark", p.toolCreateChannelBookmark)},
		{Name: "update_channel_bookmark", Description: updateChannelBookmarkDescription, Schema: NewJSONSchemaForAccessMode[UpdateChannelBookmarkArgs](string(p.accessMode)), Resolver: typed("update_channel_bookmark", p.toolUpdateChannelBookmark)},
		{Name: "delete_channel_bookmark", Description: deleteChannelBookmarkDescription, Schema: NewJSONSchemaForAccessMode[DeleteChannelBookmarkArgs](string(p.accessMode)), Resolver: typed("delete_channel_bookmark", p.toolDeleteChannelBookmark)},
	}
}

// toolListChannelBookmarks implements the list_channel_bookmarks tool.
func (p *MattermostToolProvider) toolListChannelBookmarks(mcpContext *MCPToolContext, args ListChannelBookmarksArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}

	bookmarks, _, err := mcpContext.Client.ListChannelBookmarksForChannel(mcpContext.Ctx, args.ChannelID, 0)
	if err != nil {
		return "", fmt.Errorf("error fetching channel bookmarks: %w", err)
	}
	if len(bookmarks) == 0 {
		return "no bookmarks found in this channel", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d bookmark(s):\n\n", len(bookmarks)))
	for i, bookmark := range bookmarks {
		format.WriteBookmark(&result, format.BookmarkEntry{
			HeaderLabel: fmt.Sprintf("Bookmark %d", i+1),
			Bookmark:    bookmark,
		})
	}
	return result.String(), nil
}

// toolCreateChannelBookmark implements the create_channel_bookmark tool.
func (p *MattermostToolProvider) toolCreateChannelBookmark(mcpContext *MCPToolContext, args CreateChannelBookmarkArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if args.DisplayName == "" {
		return "", fmt.Errorf("display_name cannot be empty")
	}
	if (args.LinkURL == "") == (args.FileID == "") {
		return "", fmt.Errorf("provide exactly one of link_url or file_id")
	}
	if err := optionalID("file_id", args.FileID); err != nil {
		return "", err
	}

	bookmark := &model.ChannelBookmark{
		ChannelId:   args.ChannelID,
		DisplayName: args.DisplayName,
		Emoji:       args.Emoji,
	}
	if args.LinkURL != "" {
		bookmark.Type = model.ChannelBookmarkLink
		bookmark.LinkUrl = args.LinkURL
	} else {
		bookmark.Type = model.ChannelBookmarkFile
		bookmark.FileId = args.FileID
	}

	created, _, err := mcpContext.Client.CreateChannelBookmark(mcpContext.Ctx, bookmark)
	if err != nil {
		return "", fmt.Errorf("error creating channel bookmark: %w", err)
	}

	return fmt.Sprintf("Successfully created bookmark '%s' (ID: %s)", created.DisplayName, created.Id), nil
}

// toolUpdateChannelBookmark implements the update_channel_bookmark tool.
func (p *MattermostToolProvider) toolUpdateChannelBookmark(mcpContext *MCPToolContext, args UpdateChannelBookmarkArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if err := requireID("bookmark_id", args.BookmarkID); err != nil {
		return "", err
	}
	if args.DisplayName == nil && args.LinkURL == nil && args.Emoji == nil {
		return "", fmt.Errorf("provide at least one of display_name, link_url, emoji to update")
	}

	patch := &model.ChannelBookmarkPatch{
		DisplayName: args.DisplayName,
		LinkUrl:     args.LinkURL,
		Emoji:       args.Emoji,
	}
	resp, _, err := mcpContext.Client.UpdateChannelBookmark(mcpContext.Ctx, args.ChannelID, args.BookmarkID, patch)
	if err != nil {
		return "", fmt.Errorf("error updating channel bookmark: %w", err)
	}

	if resp != nil && resp.Updated != nil {
		return fmt.Sprintf("Successfully updated bookmark '%s' (ID: %s)", resp.Updated.DisplayName, resp.Updated.Id), nil
	}
	return fmt.Sprintf("Successfully updated bookmark %s", args.BookmarkID), nil
}

// toolDeleteChannelBookmark implements the delete_channel_bookmark tool.
func (p *MattermostToolProvider) toolDeleteChannelBookmark(mcpContext *MCPToolContext, args DeleteChannelBookmarkArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if err := requireID("bookmark_id", args.BookmarkID); err != nil {
		return "", err
	}

	if _, _, err := mcpContext.Client.DeleteChannelBookmark(mcpContext.Ctx, args.ChannelID, args.BookmarkID); err != nil {
		return "", fmt.Errorf("error deleting channel bookmark: %w", err)
	}

	return fmt.Sprintf("Successfully deleted bookmark %s", args.BookmarkID), nil
}
