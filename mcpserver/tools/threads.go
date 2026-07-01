// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetThreadsArgs represents arguments for the get_threads tool.
type GetThreadsArgs struct {
	TeamID     string `json:"team_id" jsonschema:"The team whose threads inbox to read,minLength=26,maxLength=26"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"Only return unread threads (default: false)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Number of threads to return (default: 30, max: 100),minimum=1,maximum=100"`
}

// GetMentionsArgs represents arguments for the get_mentions tool.
type GetMentionsArgs struct {
	TeamID string `json:"team_id,omitempty" jsonschema:"Optional team ID to limit the search scope,maxLength=26"`
}

// GetUnreadCountsArgs represents arguments for the get_unread_counts tool.
type GetUnreadCountsArgs struct {
	TeamID string `json:"team_id,omitempty" jsonschema:"Optional team ID; omit for unread counts across all teams,maxLength=26"`
}

// GetChannelUnreadArgs represents arguments for the get_channel_unread tool.
type GetChannelUnreadArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel to read unread counts for,minLength=26,maxLength=26"`
}

// GetPostsAroundUnreadArgs represents arguments for the get_posts_around_unread tool.
type GetPostsAroundUnreadArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel to read around the last-read line,minLength=26,maxLength=26"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Posts to fetch before and after the unread line (default: 20, max: 100),minimum=1,maximum=100"`
}

// MarkChannelReadArgs represents arguments for the mark_channel_read tool.
type MarkChannelReadArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel to mark viewed/read,minLength=26,maxLength=26"`
}

// MarkChannelsViewedArgs represents arguments for the mark_channels_viewed tool.
type MarkChannelsViewedArgs struct {
	ChannelIDs []string `json:"channel_ids" jsonschema:"The channels to mark viewed (bulk)"`
}

// MarkPostUnreadArgs represents arguments for the mark_post_unread tool.
type MarkPostUnreadArgs struct {
	PostID string `json:"post_id" jsonschema:"Mark the channel unread starting at this post,minLength=26,maxLength=26"`
}

// SetThreadFollowArgs represents arguments for the set_thread_follow tool.
type SetThreadFollowArgs struct {
	TeamID   string `json:"team_id" jsonschema:"The team the thread belongs to,minLength=26,maxLength=26"`
	ThreadID string `json:"thread_id" jsonschema:"The root post ID of the thread,minLength=26,maxLength=26"`
	Follow   bool   `json:"follow" jsonschema:"true to follow the thread, false to unfollow"`
}

const (
	getThreadsDescription           = "Get your collated threads inbox (followed and unread threads, CRT / collapsed reply threads) for a team. Parameters: team_id (required), unread_only (optional), limit. Returns each thread's root post, reply count, and unread counts."
	getMentionsDescription          = "Get recent posts containing unread at-mentions of you (Recent Mentions). Parameters: team_id (optional). Returns posts that mention your username or notification keywords."
	getUnreadCountsDescription      = "Get your unread message, mention, and thread counts across teams, or for one team. Parameters: team_id (optional)."
	getChannelUnreadDescription     = "Get your unread message and mention counts for one channel. Parameters: channel_id (required)."
	getPostsAroundUnreadDescription = "Get the posts surrounding your last-read line in a channel. Parameters: channel_id (required), limit. Returns posts before and after the unread marker."
	markChannelReadDescription      = "Mark a channel viewed/read to clear its unread badge. Parameters: channel_id (required)."
	markChannelsViewedDescription   = "Mark one or more channels viewed/read (bulk). Parameters: channel_ids (required list)."
	markPostUnreadDescription       = "Mark a channel unread starting at a specific post. Parameters: post_id (required)."
	setThreadFollowDescription      = "Follow or unfollow a thread. Parameters: team_id (required), thread_id (root post ID, required), follow (true/false)."
)

// getThreadTools returns the threads, mentions, and unread-state tools.
func (p *MattermostToolProvider) getThreadTools() []MCPTool {
	return []MCPTool{
		{Name: "get_threads", Description: getThreadsDescription, Schema: NewJSONSchemaForAccessMode[GetThreadsArgs](string(p.accessMode)), Resolver: typed("get_threads", p.toolGetThreads)},
		{Name: "get_mentions", Description: getMentionsDescription, Schema: NewJSONSchemaForAccessMode[GetMentionsArgs](string(p.accessMode)), Resolver: typed("get_mentions", p.toolGetMentions)},
		{Name: "get_unread_counts", Description: getUnreadCountsDescription, Schema: NewJSONSchemaForAccessMode[GetUnreadCountsArgs](string(p.accessMode)), Resolver: typed("get_unread_counts", p.toolGetUnreadCounts)},
		{Name: "get_channel_unread", Description: getChannelUnreadDescription, Schema: NewJSONSchemaForAccessMode[GetChannelUnreadArgs](string(p.accessMode)), Resolver: typed("get_channel_unread", p.toolGetChannelUnread)},
		{Name: "get_posts_around_unread", Description: getPostsAroundUnreadDescription, Schema: NewJSONSchemaForAccessMode[GetPostsAroundUnreadArgs](string(p.accessMode)), Resolver: typed("get_posts_around_unread", p.toolGetPostsAroundUnread)},
		{Name: "mark_channel_read", Description: markChannelReadDescription, Schema: NewJSONSchemaForAccessMode[MarkChannelReadArgs](string(p.accessMode)), Resolver: typed("mark_channel_read", p.toolMarkChannelRead)},
		{Name: "mark_channels_viewed", Description: markChannelsViewedDescription, Schema: NewJSONSchemaForAccessMode[MarkChannelsViewedArgs](string(p.accessMode)), Resolver: typed("mark_channels_viewed", p.toolMarkChannelsViewed)},
		{Name: "mark_post_unread", Description: markPostUnreadDescription, Schema: NewJSONSchemaForAccessMode[MarkPostUnreadArgs](string(p.accessMode)), Resolver: typed("mark_post_unread", p.toolMarkPostUnread)},
		{Name: "set_thread_follow", Description: setThreadFollowDescription, Schema: NewJSONSchemaForAccessMode[SetThreadFollowArgs](string(p.accessMode)), Resolver: typed("set_thread_follow", p.toolSetThreadFollow)},
	}
}

// toolGetThreads implements the get_threads tool.
func (p *MattermostToolProvider) toolGetThreads(mcpContext *MCPToolContext, args GetThreadsArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if args.Limit <= 0 {
		args.Limit = 30
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	opts := model.GetUserThreadsOpts{
		PageSize: uint64(args.Limit),
		Extended: true,
		Unread:   args.UnreadOnly,
	}
	threads, _, err := mcpContext.Client.GetUserThreads(mcpContext.Ctx, userID, args.TeamID, opts)
	if err != nil {
		return "", fmt.Errorf("error fetching threads: %w", err)
	}

	if threads == nil || len(threads.Threads) == 0 {
		return "no threads found", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Threads inbox: %d total, %d unread threads, %d unread mentions\n\n",
		threads.Total, threads.TotalUnreadThreads, threads.TotalUnreadMentions))
	for i, tr := range threads.Threads {
		username := ""
		if tr.Post != nil {
			username = p.usernameFor(mcpContext.Ctx, mcpContext.Client, tr.Post.UserId)
		}
		format.WriteThreadSummary(&result, format.ThreadSummaryEntry{
			HeaderLabel: fmt.Sprintf("Thread %d", i+1),
			Thread:      tr,
			Username:    username,
		})
	}
	return result.String(), nil
}

// toolGetMentions implements the get_mentions tool. There is no dedicated mentions
// endpoint, so this searches for posts containing the user's mention keywords.
func (p *MattermostToolProvider) toolGetMentions(mcpContext *MCPToolContext, args GetMentionsArgs) (string, error) {
	if err := optionalID("team_id", args.TeamID); err != nil {
		return "", err
	}

	user, _, err := mcpContext.Client.GetMe(mcpContext.Ctx, "")
	if err != nil {
		return "", fmt.Errorf("error getting current user: %w", err)
	}

	keywords := mentionKeywords(user)
	if len(keywords) == 0 {
		return "no mention keywords configured for your account", nil
	}

	// OR-search across all mention keywords.
	postList, _, err := mcpContext.Client.SearchPosts(mcpContext.Ctx, args.TeamID, strings.Join(keywords, " "), true)
	if err != nil {
		return "", fmt.Errorf("error searching mentions: %w", err)
	}

	return p.formatPostListChrono(mcpContext, postList, "posts mentioning you"), nil
}

// mentionKeywords builds the set of terms that mention a user: their @username plus
// any configured notification keywords.
func mentionKeywords(user *model.User) []string {
	seen := make(map[string]bool)
	var keywords []string
	add := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" || seen[term] {
			return
		}
		seen[term] = true
		keywords = append(keywords, term)
	}

	add("@" + user.Username)
	if user.NotifyProps != nil {
		for _, kw := range strings.Split(user.NotifyProps["mention_keys"], ",") {
			add(kw)
		}
	}
	return keywords
}

// toolGetUnreadCounts implements the get_unread_counts tool.
func (p *MattermostToolProvider) toolGetUnreadCounts(mcpContext *MCPToolContext, args GetUnreadCountsArgs) (string, error) {
	if err := optionalID("team_id", args.TeamID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	var result strings.Builder
	if args.TeamID != "" {
		unread, _, uErr := client.GetTeamUnread(ctx, args.TeamID, userID)
		if uErr != nil {
			return "", fmt.Errorf("error fetching team unread counts: %w", uErr)
		}
		format.WriteTeamUnread(&result, unread)
		return result.String(), nil
	}

	unreads, _, uErr := client.GetTeamsUnreadForUser(ctx, userID, "", true)
	if uErr != nil {
		return "", fmt.Errorf("error fetching unread counts: %w", uErr)
	}
	if len(unreads) == 0 {
		return "no unread messages across your teams", nil
	}
	result.WriteString(fmt.Sprintf("Unread counts across %d team(s):\n", len(unreads)))
	for _, unread := range unreads {
		format.WriteTeamUnread(&result, unread)
	}
	return result.String(), nil
}

// toolGetChannelUnread implements the get_channel_unread tool.
func (p *MattermostToolProvider) toolGetChannelUnread(mcpContext *MCPToolContext, args GetChannelUnreadArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	unread, _, err := mcpContext.Client.GetChannelUnread(mcpContext.Ctx, args.ChannelID, userID)
	if err != nil {
		return "", fmt.Errorf("error fetching channel unread counts: %w", err)
	}

	var result strings.Builder
	format.WriteChannelUnread(&result, unread)
	return result.String(), nil
}

// toolGetPostsAroundUnread implements the get_posts_around_unread tool.
func (p *MattermostToolProvider) toolGetPostsAroundUnread(mcpContext *MCPToolContext, args GetPostsAroundUnreadArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if args.Limit <= 0 {
		args.Limit = 20
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	postList, _, err := mcpContext.Client.GetPostsAroundLastUnread(mcpContext.Ctx, userID, args.ChannelID, args.Limit, args.Limit, false)
	if err != nil {
		return "", fmt.Errorf("error fetching posts around unread: %w", err)
	}

	return p.formatPostListChrono(mcpContext, postList, "posts around your last-read line"), nil
}

// toolMarkChannelRead implements the mark_channel_read tool.
func (p *MattermostToolProvider) toolMarkChannelRead(mcpContext *MCPToolContext, args MarkChannelReadArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	if _, _, err := mcpContext.Client.ViewChannel(mcpContext.Ctx, userID, &model.ChannelView{ChannelId: args.ChannelID}); err != nil {
		return "", fmt.Errorf("error marking channel read: %w", err)
	}

	return fmt.Sprintf("Successfully marked channel %s as read", args.ChannelID), nil
}

// toolMarkChannelsViewed implements the mark_channels_viewed tool.
func (p *MattermostToolProvider) toolMarkChannelsViewed(mcpContext *MCPToolContext, args MarkChannelsViewedArgs) (string, error) {
	if len(args.ChannelIDs) == 0 {
		return "", fmt.Errorf("channel_ids cannot be empty")
	}
	for _, id := range args.ChannelIDs {
		if err := requireID("channel_ids", id); err != nil {
			return "", err
		}
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	for _, id := range args.ChannelIDs {
		if _, _, err := mcpContext.Client.ViewChannel(mcpContext.Ctx, userID, &model.ChannelView{ChannelId: id}); err != nil {
			return "", fmt.Errorf("error marking channel %s viewed: %w", id, err)
		}
	}

	return fmt.Sprintf("Successfully marked %d channel(s) viewed", len(args.ChannelIDs)), nil
}

// toolMarkPostUnread implements the mark_post_unread tool.
func (p *MattermostToolProvider) toolMarkPostUnread(mcpContext *MCPToolContext, args MarkPostUnreadArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	if _, err := mcpContext.Client.SetPostUnread(mcpContext.Ctx, userID, args.PostID, true); err != nil {
		return "", fmt.Errorf("error marking post unread: %w", err)
	}

	return fmt.Sprintf("Successfully marked channel unread starting at post %s", args.PostID), nil
}

// toolSetThreadFollow implements the set_thread_follow tool.
func (p *MattermostToolProvider) toolSetThreadFollow(mcpContext *MCPToolContext, args SetThreadFollowArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if err := requireID("thread_id", args.ThreadID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	if _, err := mcpContext.Client.UpdateThreadFollowForUser(mcpContext.Ctx, userID, args.TeamID, args.ThreadID, args.Follow); err != nil {
		return "", fmt.Errorf("error updating thread follow state: %w", err)
	}

	verb := "unfollowed"
	if args.Follow {
		verb = "followed"
	}
	return fmt.Sprintf("Successfully %s thread %s", verb, args.ThreadID), nil
}
