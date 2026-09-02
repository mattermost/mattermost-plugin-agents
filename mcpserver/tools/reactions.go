// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetPostReactionsArgs represents arguments for the get_post_reactions tool.
type GetPostReactionsArgs struct {
	PostID string `json:"post_id" jsonschema:"The ID of the post,minLength=26,maxLength=26"`
}

// ListCustomEmojiArgs represents arguments for the list_custom_emoji tool.
type ListCustomEmojiArgs struct {
	Page    int `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage int `json:"per_page,omitempty" jsonschema:"Number of emoji per page (default: 50, max: 200),minimum=1,maximum=200"`
}

// SearchCustomEmojiArgs represents arguments for the search_custom_emoji tool.
type SearchCustomEmojiArgs struct {
	Term string `json:"term" jsonschema:"Search term to match against custom emoji names,minLength=1,maxLength=64"`
}

// AddReactionArgs represents arguments for the add_reaction tool.
type AddReactionArgs struct {
	PostID    string `json:"post_id" jsonschema:"The ID of the post to react to,minLength=26,maxLength=26"`
	EmojiName string `json:"emoji_name" jsonschema:"The emoji name without colons (e.g. thumbsup),minLength=1"`
}

// RemoveReactionArgs represents arguments for the remove_reaction tool.
type RemoveReactionArgs struct {
	PostID    string `json:"post_id" jsonschema:"The ID of the post to remove your reaction from,minLength=26,maxLength=26"`
	EmojiName string `json:"emoji_name" jsonschema:"The emoji name without colons (e.g. thumbsup),minLength=1"`
}

const (
	getPostReactionsDescription  = "List the emoji reactions on a post and who reacted. Parameters: post_id (required). Returns each emoji with its count and reacting usernames."
	listCustomEmojiDescription   = "List the server's custom emoji. Parameters: page, per_page. Returns each emoji's name and ID."
	searchCustomEmojiDescription = "Search custom emoji by name. Parameters: term (required). Returns matching custom emoji."
	addReactionDescription       = "Add an emoji reaction to a post. Parameters: post_id (required), emoji_name (required, no colons)."
	removeReactionDescription    = "Remove your emoji reaction from a post. Parameters: post_id (required), emoji_name (required, no colons)."
)

// getReactionTools returns the reaction and custom-emoji tools.
func (p *MattermostToolProvider) getReactionTools() []MCPTool {
	return []MCPTool{
		mcpTool(p, "get_post_reactions", getPostReactionsDescription, p.toolGetPostReactions),
		mcpTool(p, "list_custom_emoji", listCustomEmojiDescription, p.toolListCustomEmoji),
		mcpTool(p, "search_custom_emoji", searchCustomEmojiDescription, p.toolSearchCustomEmoji),
		mcpTool(p, "add_reaction", addReactionDescription, p.toolAddReaction),
		mcpTool(p, "remove_reaction", removeReactionDescription, p.toolRemoveReaction),
	}
}

// toolGetPostReactions implements the get_post_reactions tool.
func (p *MattermostToolProvider) toolGetPostReactions(mcpContext *MCPToolContext, args GetPostReactionsArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	reactions, _, err := mcpContext.Client.GetReactions(mcpContext.Ctx, args.PostID)
	if err != nil {
		return "", fmt.Errorf("error fetching reactions: %w", err)
	}

	usernames := p.reactionUsernames(mcpContext, reactions)

	var result strings.Builder
	format.WriteReactions(&result, args.PostID, reactions, usernames)
	return result.String(), nil
}

// toolListCustomEmoji implements the list_custom_emoji tool.
func (p *MattermostToolProvider) toolListCustomEmoji(mcpContext *MCPToolContext, args ListCustomEmojiArgs) (string, error) {
	if args.PerPage <= 0 {
		args.PerPage = 50
	}
	if args.PerPage > 200 {
		args.PerPage = 200
	}
	if args.Page < 0 {
		args.Page = 0
	}

	emojis, _, err := mcpContext.Client.GetEmojiList(mcpContext.Ctx, args.Page, args.PerPage)
	if err != nil {
		return "", fmt.Errorf("error fetching custom emoji: %w", err)
	}

	return p.formatEmojiList(mcpContext, emojis), nil
}

// toolSearchCustomEmoji implements the search_custom_emoji tool.
func (p *MattermostToolProvider) toolSearchCustomEmoji(mcpContext *MCPToolContext, args SearchCustomEmojiArgs) (string, error) {
	if args.Term == "" {
		return "", fmt.Errorf("term cannot be empty")
	}

	emojis, _, err := mcpContext.Client.SearchEmoji(mcpContext.Ctx, &model.EmojiSearch{Term: args.Term})
	if err != nil {
		return "", fmt.Errorf("error searching custom emoji: %w", err)
	}

	return p.formatEmojiList(mcpContext, emojis), nil
}

// toolAddReaction implements the add_reaction tool.
func (p *MattermostToolProvider) toolAddReaction(mcpContext *MCPToolContext, args AddReactionArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}
	if args.EmojiName == "" {
		return "", fmt.Errorf("emoji_name cannot be empty")
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	reaction := &model.Reaction{
		UserId:    userID,
		PostId:    args.PostID,
		EmojiName: strings.Trim(args.EmojiName, ":"),
	}
	if _, _, err := mcpContext.Client.SaveReaction(mcpContext.Ctx, reaction); err != nil {
		return "", fmt.Errorf("error adding reaction: %w", err)
	}

	return fmt.Sprintf("Successfully added :%s: reaction to post %s", reaction.EmojiName, args.PostID), nil
}

// toolRemoveReaction implements the remove_reaction tool.
func (p *MattermostToolProvider) toolRemoveReaction(mcpContext *MCPToolContext, args RemoveReactionArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}
	if args.EmojiName == "" {
		return "", fmt.Errorf("emoji_name cannot be empty")
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	reaction := &model.Reaction{
		UserId:    userID,
		PostId:    args.PostID,
		EmojiName: strings.Trim(args.EmojiName, ":"),
	}
	if _, err := mcpContext.Client.DeleteReaction(mcpContext.Ctx, reaction); err != nil {
		return "", fmt.Errorf("error removing reaction: %w", err)
	}

	return fmt.Sprintf("Successfully removed :%s: reaction from post %s", reaction.EmojiName, args.PostID), nil
}

// reactionUsernames resolves the usernames of all users who reacted, in one batch.
func (p *MattermostToolProvider) reactionUsernames(mcpContext *MCPToolContext, reactions []*model.Reaction) map[string]string {
	usernames := make(map[string]string)
	ids := make([]string, 0)
	for _, r := range reactions {
		if _, ok := usernames[r.UserId]; !ok {
			usernames[r.UserId] = ""
			ids = append(ids, r.UserId)
		}
	}
	if len(ids) == 0 {
		return usernames
	}
	users, _, err := mcpContext.Client.GetUsersByIds(mcpContext.Ctx, ids)
	if err != nil {
		p.logger.Warn("failed to fetch reaction users", "error", err)
		return usernames
	}
	for _, user := range users {
		usernames[user.Id] = user.Username
	}
	return usernames
}

// formatEmojiList renders custom emoji with their creators.
func (p *MattermostToolProvider) formatEmojiList(mcpContext *MCPToolContext, emojis []*model.Emoji) string {
	if len(emojis) == 0 {
		return "no custom emoji found"
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Found %d custom emoji:\n\n", len(emojis))
	for i, emoji := range emojis {
		format.WriteEmoji(&result, format.EmojiEntry{
			HeaderLabel: fmt.Sprintf("Emoji %d", i+1),
			Emoji:       emoji,
			CreatorName: p.usernameFor(mcpContext.Ctx, mcpContext.Client, emoji.CreatorId),
		})
	}
	return result.String()
}
